package main

import (
	"context"
	"crypto/sha256"
	"errors"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	"testing"
	"time"
)

type sidebarVariantStub struct{ err error }

func (s sidebarVariantStub) GetEnabledImageVariant(context.Context, int64, string) (mediaport.ImageVariant, error) {
	return mediaport.ImageVariant{Content: []byte("trusted-image"), MediaType: "image/png"}, s.err
}

type sidebarPreparerStub struct {
	result outboundport.SidebarImagePreparation
	calls  int
	source outboundport.SidebarImagePreparationSource
}

func (s *sidebarPreparerStub) PrepareSidebarImage(_ context.Context, in outboundport.SidebarImagePreparationSource, _ time.Time) (outboundport.SidebarImagePreparation, error) {
	s.calls++
	s.source = in
	return s.result, nil
}
func TestSidebarPreparationUsesEnabledBytesAndReadyReceipt(t *testing.T) {
	through := time.Now().Add(6 * time.Minute)
	for _, item := range []struct {
		state string
		want  error
	}{
		{"queued", mediaport.ErrSidebarMaterialPreparing}, {"outcome_unknown", mediaport.ErrSidebarMaterialOutcomeUnknown}, {"permanent_failed", mediaport.ErrSidebarMaterialPreparationFailed}, {"ready", nil},
	} {
		t.Run(item.state, func(t *testing.T) {
			preparer := &sidebarPreparerStub{result: outboundport.SidebarImagePreparation{State: item.state, MediaID: "provider-id", ReadyUntil: through.Add(time.Hour)}}
			adapter := sidebarImagePreparation{enabled: true, images: sidebarVariantStub{}, preparer: preparer, scope: "corp:agent"}
			result, err := adapter.ReadSidebarImageForSend(context.Background(), 5, through)
			if !errors.Is(err, item.want) {
				t.Fatalf("error=%v want=%v", err, item.want)
			}
			if preparer.source.SourceDigest != sha256.Sum256([]byte("trusted-image")) || preparer.source.ImageID != 5 || preparer.source.Scope != "corp:agent" {
				t.Fatal("source not bound to enabled media snapshot")
			}
			if item.want == nil && result.MediaID != "provider-id" {
				t.Fatal("missing actual provider receipt")
			}
		})
	}
	preparer := &sidebarPreparerStub{}
	adapter := sidebarImagePreparation{enabled: true, images: sidebarVariantStub{err: errors.New("disabled image")}, preparer: preparer}
	if _, err := adapter.ReadSidebarImageForSend(context.Background(), 5, through); err == nil || preparer.calls != 0 {
		t.Fatal("disabled image queued upload")
	}
}
