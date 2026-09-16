package port

import "testing"

func TestSafePublicCompletionURLRejectsLegacyIPv4Forms(t *testing.T) {
	for _, raw := range []string{
		"https://2130706433/complete",
		"https://127.1/complete",
		"https://0177.0.0.1/complete",
		"https://0x7f000001/complete",
		"https://0300.0250.0001.0001/complete",
	} {
		if SafePublicCompletionURL(raw) {
			t.Fatalf("legacy IPv4 completion URL accepted: %q", raw)
		}
	}
}

func TestSafePublicCompletionURLAcceptsSupportedDestinations(t *testing.T) {
	for _, raw := range []string{"/same-origin/complete", "https://example.com/complete", "https://subdomain.127.example/complete"} {
		if !SafePublicCompletionURL(raw) {
			t.Fatalf("supported completion URL rejected: %q", raw)
		}
	}
}
