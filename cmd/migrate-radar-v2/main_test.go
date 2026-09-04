package main

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/radar"
)

func TestAdaptNeverCreatesIdentityAndForcesClosedContentShape(t *testing.T) {
	now := time.Now()
	base := sourceLink{ID: 1, PublicCode: "rd_abcdefghijklmnopqrstuv", Name: "guide", Title: "Guide", DestinationURL: "https://example.com", CreatedAt: now, UpdatedAt: now}
	content, reason, err := adapt(base)
	if err != nil || reason != "" || content.Type != radar.ContentTypeLink {
		t.Fatalf("content=%+v reason=%s err=%v", content, reason, err)
	}
	image := int64(9)
	base.CoverImageID = &image
	content, _, err = adapt(base)
	if err != nil || content.Type != radar.ContentTypeImage || content.DestinationURL != "" {
		t.Fatalf("image=%+v err=%v", content, err)
	}
	attachment := int64(10)
	base.AttachmentID = &attachment
	if _, reason, err = adapt(base); err == nil || reason != "ambiguous_media" {
		t.Fatalf("reason=%s err=%v", reason, err)
	}
}

func TestExtractStreamAcceptsOnlySafeRadarProjection(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	link := sourceLink{ID: 3, PublicCode: "rd_abcdefghijklmnopqrstuv", Name: "a", Title: "b", DestinationURL: "https://example.com", Status: "enabled", Version: 2, CreatedBy: 1, UpdatedBy: 1, CreatedAt: now, UpdatedAt: now}
	event := sourceEvent{ID: 7, LinkID: 3, Stage: "landing", CreatedAt: now}
	encode := func(value any) string { raw, _ := json.Marshal(value); return hex.EncodeToString(raw) }
	stream := strings.Join([]string{"__AICRM_RADAR_SNAPSHOT__|" + now.Format(time.RFC3339Nano), "__AICRM_RADAR_LINK__|" + encode(link), "__AICRM_RADAR_EVENT__|" + encode(event)}, "\n") + "\n"
	path := filepath.Join(t.TempDir(), "radar.stream")
	if err := os.WriteFile(path, []byte(stream), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := extractStream(path)
	if err != nil || len(got.Links) != 1 || len(got.Events) != 1 || got.Links[0].PublicCode != link.PublicCode || got.Digest == "" {
		t.Fatalf("snapshot=%+v err=%v", got, err)
	}
}
func TestSnapshotDigestRejectsMutation(t *testing.T) {
	now := time.Now().UTC()
	s := snapshot{Schema: 1, DonorCommit: donorCommit, CapturedAt: now, Links: []sourceLink{{ID: 1, PublicCode: "rd_abcdefghijklmnopqrstuv", Name: "a", Title: "b", DestinationURL: "https://example.com", CreatedAt: now, UpdatedAt: now}}}
	first := digest(s)
	s.Links[0].Title = "changed"
	if digest(s) == first {
		t.Fatal("digest did not bind snapshot content")
	}
}
