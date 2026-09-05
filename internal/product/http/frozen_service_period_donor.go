package http

import _ "embed"

// frozenServicePeriodPublicRenderer is the immutable donor renderer from
// frozen Commerce service-period renderer source. The Go Host owns
// trusted-session lookup and routes but preserves its public DOM/state contract
// in service_period_template.go; tests pin this source against its digest.
//
//go:embed donor/service_period_public.py
var frozenServicePeriodPublicRenderer string

const frozenServicePeriodPublicRendererSHA256 = "1f145da6e4559ce6956d92ba499f2e332cc57a27537fab499c6039ed9132050e"
