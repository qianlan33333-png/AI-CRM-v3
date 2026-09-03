package channel

import (
	"os"
	"strings"
	"testing"
)

func TestChannelCenterMigrationContract(t *testing.T) {
	contents, err := os.ReadFile("../../migrations/0029_channel_center.sql")
	if err != nil {
		t.Fatal(err)
	}
	source := string(contents)
	for _, required := range []string{
		"Owner: internal/channel", "Forward-only", "CREATE TABLE channels", "CREATE TABLE channel_config_versions",
		"CREATE TABLE channel_assignees", "CREATE TABLE channel_operation_receipts", "channels_code_unique_ci",
		"channels_current_config_fk", "channel_config_versions_immutable", "channel_assignees_immutable",
		"channel_operation_receipts_guard", "channels are archive-only", "audit_events", "outbox_events",
	} {
		if !strings.Contains(source, required) {
			t.Fatalf("migration missing %q", required)
		}
	}
	for _, forbidden := range []string{" external_userid ", " unionid ", " openid ", " phone ", " raw_state ", " welcome_code "} {
		if strings.Contains(strings.ToLower(source), forbidden) {
			t.Fatalf("migration contains forbidden identity/provider field %q", forbidden)
		}
	}
}
