package http

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"html/template"
	"strings"
	"time"
)

// frozenServicePeriodPublicRenderer is the immutable donor renderer from
// frozen Commerce service-period renderer source. The Go Host owns
// trusted-session lookup and routes but preserves its public DOM/state contract
// in service_period_template.go; tests pin this source against its digest.
//
//go:embed donor/service_period_public.py
var frozenServicePeriodPublicRenderer string

//go:embed donor/public_product_service.py
var frozenPublicProductService string

const frozenServicePeriodPublicRendererSHA256 = "1f145da6e4559ce6956d92ba499f2e332cc57a27537fab499c6039ed9132050e"
const frozenPublicProductServiceSHA256 = "8fc6cb21cd49531b0c14ca25a955ad1fdfe90ca1f8c9a7d138038002b05ff875"

// servicePeriodDonorStyles extracts the old renderer's own style block at
// runtime.  Host-only QR wiring is substituted where the Python renderer
// called its sibling helper; all remaining selectors come directly from the
// immutable donor source.
func servicePeriodDonorStyles() template.CSS {
	const start, end = "  <style>", "  </style>"
	from := strings.Index(frozenServicePeriodPublicRenderer, start)
	if from < 0 {
		return ""
	}
	from += len(start)
	to := strings.Index(frozenServicePeriodPublicRenderer[from:], end)
	if to < 0 {
		return ""
	}
	css := frozenServicePeriodPublicRenderer[from : from+to]
	css = strings.ReplaceAll(css, "{{", "{")
	css = strings.ReplaceAll(css, "}}", "}")
	css = strings.ReplaceAll(css, "{lead_qr_modal_styles()}", frozenTripleQuoted("def lead_qr_modal_styles", frozenPublicProductService))
	return template.CSS(css)
}

// servicePeriodDonorScript uses the donor's state renderer verbatim after
// substituting only Host-owned public facts.  The donor's QR controller stays
// inert unless a later Product-owned QR projection supplies a trusted URL.
func servicePeriodDonorScript(state servicePeriodPublicState) template.JS {
	const start, end = "  <script>\n    (function ()", "  </script>"
	from := strings.Index(frozenServicePeriodPublicRenderer, start)
	if from < 0 {
		return ""
	}
	from += len("  <script>\n")
	to := strings.Index(frozenServicePeriodPublicRenderer[from:], end)
	if to < 0 {
		return ""
	}
	script := frozenServicePeriodPublicRenderer[from : from+to]
	script = strings.ReplaceAll(script, "{{", "{")
	script = strings.ReplaceAll(script, "}}", "}")
	entitlement := map[string]any{"status": state.Status}
	if !state.EndAt.IsZero() {
		entitlement["end_at"] = state.EndAt.UTC().Format(time.RFC3339)
	}
	if state.RemainingDays > 0 {
		entitlement["remaining_days"] = state.RemainingDays
	}
	payload, _ := json.Marshal(map[string]any{"ok": true, "available": state.Available, "entitlement": entitlement, "lead_qr": map[string]any{"qr_url": state.LeadQRURL, "title": state.LeadQRTitle, "subtitle": state.LeadQRSubtitle}, "cta_text": state.CTA, "checkout_url": state.Product.PaymentPath})
	script = strings.ReplaceAll(script, "{state_json}", strings.ReplaceAll(string(payload), "</", "<\\/"))
	script = strings.ReplaceAll(script, "{duration_days}", fmt.Sprintf("%d", state.Product.ServicePeriodDurationDays))
	script = strings.ReplaceAll(script, "{price_yuan}", fmt.Sprintf("%.2f", float64(state.Product.PriceMinor)/100))
	return template.JS(script)
}

func frozenTripleQuoted(marker, source string) string {
	at := strings.Index(source, marker)
	if at < 0 {
		return ""
	}
	begin := strings.Index(source[at:], `return """`)
	if begin < 0 {
		return ""
	}
	begin += at + len(`return """`)
	end := strings.Index(source[begin:], `"""`)
	if end < 0 {
		return ""
	}
	return source[begin : begin+end]
}
func servicePeriodLeadQRModal() template.HTML {
	return template.HTML(frozenTripleQuoted("def render_lead_qr_modal", frozenPublicProductService))
}
func servicePeriodLeadQRController() template.HTML {
	return template.HTML(frozenTripleQuoted("def lead_qr_modal_controller_script", frozenPublicProductService))
}
