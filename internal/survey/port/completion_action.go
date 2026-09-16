package port

import (
	"context"
	"encoding/json"
	"net/netip"
	"net/url"
	"strings"
)

// PublicCompletionTargetResolver is composition-owned. Its input is only the
// Survey-owned opaque navigation reference; an implementation must enforce the
// allowlist and return a public HTTPS destination. It must never expose or
// reuse an outbound completion endpoint, its credentials, or its parameters.
type PublicCompletionTargetResolver interface {
	ResolvePublicCompletionTarget(context.Context, string) (url string, found bool, err error)
}

type CompletionEndpointManager interface {
	ReadSurveyCompletionEndpointWithin(context.Context, ID, string) (string, error)
	SaveSurveyCompletionEndpointWithin(context.Context, ID, string, string, json.RawMessage) (string, error)
}

// SafePublicCompletionURL accepts same-origin paths and public HTTPS URLs used
// by the browser after a successful submission. Numeric-looking hostnames are
// rejected before the browser can apply WHATWG's legacy IPv4 normalization
// (for example 2130706433, 127.1, octal, or hexadecimal loopback forms).
func SafePublicCompletionURL(raw string) bool {
	if raw == "" || len(raw) > 2048 || strings.ContainsAny(raw, "\\\r\n\t") {
		return false
	}
	if strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "//") {
		return true
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" || parsed.Port() != "" && parsed.Port() != "443" {
		return false
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || legacyIPv4Hostname(host) {
		return false
	}
	if ip, parseErr := netip.ParseAddr(host); parseErr == nil {
		return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified())
	}
	return true
}

func legacyIPv4Hostname(host string) bool {
	parts := strings.Split(host, ".")
	if len(parts) == 0 || len(parts) > 4 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		if strings.HasPrefix(part, "0x") {
			if len(part) == 2 || strings.IndexFunc(part[2:], func(r rune) bool {
				return !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f')
			}) >= 0 {
				return false
			}
			continue
		}
		if strings.IndexFunc(part, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
			return false
		}
	}
	return true
}
