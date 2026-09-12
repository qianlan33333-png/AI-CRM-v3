package outbound

import (
	"context"
	"testing"
)

func TestReadinessRequiresMaterialRefreshSourceCountMigrationPostgreSQL(t *testing.T) {
	ctx := context.Background()
	native, cleanup := materialTestDatabase(t, ctx)
	defer cleanup()
	if _, err := native.Exec(ctx, `
CREATE TABLE outbound_message_intents(content_snapshot JSONB,content_snapshot_digest TEXT,CONSTRAINT outbound_message_intents_content_snapshot_shape CHECK(TRUE));
CREATE TABLE outbound_commerce_push_intents(payload_mode TEXT);
CREATE TABLE outbound_commerce_push_endpoints();
CREATE TABLE outbound_commerce_push_history_batches();
CREATE TABLE outbound_commerce_push_history_rows();
CREATE TABLE outbound_commerce_push_history_batch_rows();`); err != nil {
		t.Fatal(err)
	}
	if err := Readiness(ctx, native); err != nil {
		t.Fatalf("0149 schema should be ready: %v", err)
	}
	if _, err := native.Exec(ctx, `ALTER TABLE outbound_material_refresh_items ALTER COLUMN source_count SET DEFAULT 1`); err != nil {
		t.Fatal(err)
	}
	if err := Readiness(ctx, native); err == nil {
		t.Fatal("readiness accepted the pre-0149 source_count default")
	}
}
