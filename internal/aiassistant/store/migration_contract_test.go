package store

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMigrationKeepsIdentityAndProviderDataOutsideOwnedTables(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test")
	}
	payload, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations", "0031_ai_assistant_review.sql"))
	if err != nil {
		t.Fatal(err)
	}
	schema := strings.ToLower(string(payload))
	for _, forbidden := range []string{"external_userid", "openid", "unionid", "phone", "mobile", "sender_userid", "provider_payload"} {
		if strings.Contains(schema, forbidden) {
			t.Fatalf("AI Assistant migration contains forbidden identity/provider field %q", forbidden)
		}
	}
	for _, required := range []string{"customer_id bigint not null", "staff_id bigint not null", "foreign key (current_content_version_id, id)"} {
		if !strings.Contains(schema, required) {
			t.Fatalf("AI Assistant migration missing invariant %q", required)
		}
	}
}
