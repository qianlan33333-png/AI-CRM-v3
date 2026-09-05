package http

import (
	"fmt"
	"html"
	"io"
	"strconv"
	"strings"
	"time"
)

// servicePeriodPublicState is the small Host-owned fact set substituted into
// the frozen renderer. Product supplies already-public product facts; Payment
// supplies the trusted session and Order supplies an entitlement projection.
// No donor business code is executed.
type servicePeriodPublicState struct {
	Available                              bool
	LeadQRURL, LeadQRTitle, LeadQRSubtitle string
	Product                                publicProduct
	Status                                 string
	CTA                                    string
	EndAt                                  time.Time
	RemainingDays                          int32
}

// renderServicePeriodPublicPage adapts the exact HTML f-string returned by
// the frozen donor's render_service_period_public_page. Keeping the source
// body as the rendering template preserves its DOM, styles and state script;
// this function substitutes only the V3 Host facts that the Python code used
// to calculate before rendering.
func renderServicePeriodPublicPage(w io.Writer, state servicePeriodPublicState) error {
	page := frozenServicePeriodPageBody()
	if page == "" {
		return fmt.Errorf("frozen service-period renderer body unavailable")
	}

	status := state.Status
	if status == "" {
		status = "none"
	}
	price := fmt.Sprintf("%.2f", float64(state.Product.PriceMinor)/100)
	title := html.EscapeString(state.Product.Name)
	cta := html.EscapeString(state.CTA)
	if cta == "" {
		cta = "立即报名"
	}

	var tagText, heroText, barMeta, card string
	tagHidden, heroHidden, wecomHidden := " hidden", " hidden", " hidden"
	switch {
	case !state.Available || status == "unavailable":
		status = "unavailable"
		tagText, heroText, barMeta = "未上架", "该周期商品暂未开放", "暂未开放"
		tagHidden, heroHidden = "", ""
		if state.CTA == "" {
			cta = "暂未开放"
		}
		card = servicePeriodUnavailableCard(price, state.Product.ServicePeriodDurationDays)
	case status == "active":
		tagText, barMeta = "服务中", fmt.Sprintf("剩余 %d 天", state.RemainingDays)
		tagHidden = ""
		pct := 0
		if state.Product.ServicePeriodDurationDays > 0 {
			pct = int((int64(state.RemainingDays)*100 + int64(state.Product.ServicePeriodDurationDays)/2) / int64(state.Product.ServicePeriodDurationDays))
		}
		if pct > 100 {
			pct = 100
		}
		card = servicePeriodActiveCard(state.RemainingDays, pct, state.EndAt, price, state.Product.ServicePeriodDurationDays)
		if state.LeadQRURL != "" {
			wecomHidden = ""
		}
	case status == "expired", status == "refunded":
		status, tagText, heroText = "expired", "已过期", "服务期已结束"
		tagHidden, heroHidden = "", ""
		barMeta = fmt.Sprintf("¥%s / %d 天", price, state.Product.ServicePeriodDurationDays)
		card = servicePeriodExpiredCard(state.EndAt, price, state.Product.ServicePeriodDurationDays)
	default:
		status, barMeta = "none", fmt.Sprintf("¥%s / %d 天", price, state.Product.ServicePeriodDurationDays)
		card = servicePeriodNoneCard(price, state.Product.ServicePeriodDurationDays)
	}

	// These replacement keys are the dynamic Python f-string expressions in
	// the donor source. Values generated here are escaped or are Host-built
	// route/markup fragments. The final brace normalization reproduces Python
	// f-string escaping for CSS and JavaScript.
	replacements := map[string]string{
		"{title}":                             title,
		"{escape(status)}":                    html.EscapeString(status),
		"{tag_hidden}":                        tagHidden,
		"{escape(tag_text)}":                  html.EscapeString(tagText),
		"{hero_text_hidden}":                  heroHidden,
		"{escape(hero_text)}":                 html.EscapeString(heroText),
		"{card_html}":                         card,
		"{wecom_action_hidden}":               wecomHidden,
		"{media}":                             servicePeriodDetailMedia(state.Product.Images),
		"{escape(bar_meta)}":                  html.EscapeString(barMeta),
		"{cta_text}":                          cta,
		"{render_lead_qr_modal()}":            servicePeriodLeadQRModal(),
		"{lead_qr_modal_controller_script()}": servicePeriodLeadQRController(),
		"{lead_qr_modal_styles()}":            servicePeriodLeadQRStyles(),
		"{state_json}":                        servicePeriodStateJSON(state, status),
		"{duration_days}":                     strconv.FormatInt(int64(state.Product.ServicePeriodDurationDays), 10),
		"{price_yuan}":                        price,
		"{product_context_fragment_bootstrap_script()}": "",
	}
	for source, value := range replacements {
		page = strings.ReplaceAll(page, source, value)
	}
	page = strings.ReplaceAll(page, "{{", "{")
	page = strings.ReplaceAll(page, "}}", "}")
	_, err := io.WriteString(w, page)
	return err
}

func frozenServicePeriodPageBody() string {
	const start = "return f\"\"\"<!doctype html>"
	from := strings.Index(frozenServicePeriodPublicRenderer, start)
	if from < 0 {
		return ""
	}
	from += len("return f\"\"\"")
	to := strings.Index(frozenServicePeriodPublicRenderer[from:], "\"\"\"\n\n\ndef render_service_period_pay_page")
	if to < 0 {
		return ""
	}
	return frozenServicePeriodPublicRenderer[from : from+to]
}

func servicePeriodNoneCard(price string, duration int32) string {
	return fmt.Sprintf("\n      <div class=\"service-period-price\"><small>¥</small>%s</div>\n      <div class=\"service-period-line\"><span class=\"service-period-muted\">有效期</span><strong>%d 天</strong></div>", price, duration)
}

func servicePeriodUnavailableCard(price string, duration int32) string {
	return servicePeriodNoneCard(price, duration) + "\n      <p class=\"service-period-tip\">该周期商品尚未上架，暂不可购买。</p>"
}

func servicePeriodActiveCard(days int32, percent int, end time.Time, price string, duration int32) string {
	return fmt.Sprintf("\n      <div class=\"service-period-muted\">剩余有效期</div>\n      <div class=\"service-period-state-big\">%d 天</div>\n      <div class=\"service-period-progress\" aria-label=\"剩余服务期\"><span style=\"width:%d%%\"></span></div>\n      <div class=\"service-period-line\"><span class=\"service-period-muted\">到期日</span><strong>%s</strong></div>\n      <div class=\"service-period-line\"><span class=\"service-period-muted\">续费价格 / 有效期</span><strong>¥%s / %d 天</strong></div>", days, percent, servicePeriodEndDate(end), price, duration)
}

func servicePeriodExpiredCard(end time.Time, price string, duration int32) string {
	return fmt.Sprintf("\n      <div class=\"service-period-state-big\">已过期</div>\n      <div class=\"service-period-line\"><span class=\"service-period-muted\">上次到期日</span><strong>%s</strong></div>\n      <div class=\"service-period-line\"><span class=\"service-period-muted\">重新开通价格 / 有效期</span><strong>¥%s / %d 天</strong></div>", servicePeriodEndDate(end), price, duration)
}

func servicePeriodEndDate(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.In(time.FixedZone("Asia/Shanghai", 8*60*60)).Format("2006-01-02")
}

func servicePeriodDetailMedia(images []string) string {
	if len(images) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("    <section class=\"detail-media\" aria-label=\"商品详情图\">\n")
	for index, image := range images {
		if image == "" {
			continue
		}
		loading, priority := "lazy", ""
		if index == 0 {
			loading, priority = "eager", " fetchpriority=\"high\""
		}
		fmt.Fprintf(&b, "      <img class=\"slice-img\" src=\"%s\" loading=\"%s\" decoding=\"async\"%s alt=\"\">\n", html.EscapeString(image), loading, priority)
	}
	b.WriteString("    </section>")
	return b.String()
}
