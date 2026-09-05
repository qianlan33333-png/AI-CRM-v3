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

const frozenServicePeriodPublicRendererSHA256 = "1f145da6e4559ce6956d92ba499f2e332cc57a27537fab499c6039ed9132050e"

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
	css = strings.ReplaceAll(css, "{lead_qr_modal_styles()}", `.lead-qr-modal[hidden]{display:none}.lead-qr-modal{position:fixed;inset:0;z-index:30;background:rgba(0,0,0,.55);padding:24px}.lead-qr-modal-card{margin:auto;max-width:320px;background:#fff;border-radius:12px;padding:16px}.lead-qr-modal img{display:block;width:100%}`)
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
	payload, _ := json.Marshal(map[string]any{"ok": true, "available": state.Available, "entitlement": entitlement, "cta_text": state.CTA, "checkout_url": state.Product.PaymentPath})
	script = strings.ReplaceAll(script, "{state_json}", strings.ReplaceAll(string(payload), "</", "<\\/"))
	script = strings.ReplaceAll(script, "{duration_days}", fmt.Sprintf("%d", state.Product.ServicePeriodDurationDays))
	script = strings.ReplaceAll(script, "{price_yuan}", fmt.Sprintf("%.2f", float64(state.Product.PriceMinor)/100))
	return template.JS(script)
}
