package outbound

import (
	"context"
	"errors"
	"testing"

	effect "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
)

type catalogReaderFunc func(context.Context) (CatalogSnapshot, error)

func (f catalogReaderFunc) ListCatalog(ctx context.Context) (CatalogSnapshot, error) { return f(ctx) }

func tagEnvelope() effect.Envelope {
	return effect.Envelope{Owner: effect.OwnerOutbound, Kind: effect.KindWeComTagCatalog,
		SourceRefDigest: effect.Hash("source"), TargetRefDigest: effect.Hash("target"), PayloadDigest: effect.Hash("payload"), PolicyVersionHash: effect.Hash("policy")}
}

func TestTagCatalogProviderCanonicalizesAndFiltersDeleted(t *testing.T) {
	provider, err := NewTagCatalogProvider(catalogReaderFunc(func(context.Context) (CatalogSnapshot, error) {
		return CatalogSnapshot{Groups: []CatalogGroup{{ID: "g2", Name: "two", Order: 2, Tags: []CatalogTag{{ID: "t2", Name: "two", Order: 2}, {ID: "gone", Name: "gone", Order: 1, Deleted: true}}}, {ID: "g1", Name: "one", Order: 1, Tags: []CatalogTag{{ID: "t1", Name: "one", Order: 1}}}}}, nil
	}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.Execute(context.Background(), tagEnvelope(), effect.Attempt{Number: 1, Generation: 1, Fence: 1})
	if err != nil || result.Completion != effect.StateExecuted || !result.Artifact.Valid() {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	want := `{"groups":[{"id":"g1","name":"one","order":1,"tags":[{"id":"t1","name":"one","order":1}]},{"id":"g2","name":"two","order":2,"tags":[{"id":"t2","name":"two","order":2}]}]}`
	if string(result.Artifact.Payload) != want {
		t.Fatalf("payload=%s", result.Artifact.Payload)
	}
}

func TestTagCatalogProviderFailureBoundaries(t *testing.T) {
	for name, readErr := range map[string]error{
		"pre-call":  &ReadError{Err: errors.New("unavailable")},
		"post-call": &ReadError{Err: errors.New("timeout"), CallAttempted: true},
	} {
		t.Run(name, func(t *testing.T) {
			provider, _ := NewTagCatalogProvider(catalogReaderFunc(func(context.Context) (CatalogSnapshot, error) { return CatalogSnapshot{}, readErr }))
			result, err := provider.Execute(context.Background(), tagEnvelope(), effect.Attempt{})
			if err == nil {
				t.Fatal("expected error")
			}
			if name == "pre-call" && (result.Completion != effect.StateRetryable || result.CallAttempted) {
				t.Fatalf("result=%+v", result)
			}
			if name == "post-call" && (result.Completion != effect.StateUnknown || !result.CallAttempted) {
				t.Fatalf("result=%+v", result)
			}
		})
	}
	provider, _ := NewTagCatalogProvider(catalogReaderFunc(func(context.Context) (CatalogSnapshot, error) {
		return CatalogSnapshot{Groups: []CatalogGroup{{ID: "g", Name: "g", Tags: []CatalogTag{{ID: "x", Name: "x"}, {ID: "x", Name: "again"}}}}}, nil
	}))
	result, err := provider.Execute(context.Background(), tagEnvelope(), effect.Attempt{})
	if err != nil || result.Completion != effect.StateUnknown || !result.CallAttempted || result.Artifact.Valid() {
		t.Fatalf("invalid=%+v err=%v", result, err)
	}
}

func TestTagCatalogProviderEmptySnapshotIsExecuted(t *testing.T) {
	provider, _ := NewTagCatalogProvider(catalogReaderFunc(func(context.Context) (CatalogSnapshot, error) { return CatalogSnapshot{Groups: []CatalogGroup{}}, nil }))
	result, err := provider.Execute(context.Background(), tagEnvelope(), effect.Attempt{})
	if err != nil || result.Completion != effect.StateExecuted || string(result.Artifact.Payload) != `{"groups":[]}` {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestCanonicalCatalogSnapshotRejectsMissingGroupsAndControlText(t *testing.T) {
	if _, ok := CanonicalCatalogSnapshot(CatalogSnapshot{}); ok {
		t.Fatal("missing groups accepted")
	}
	if _, ok := CanonicalCatalogSnapshot(CatalogSnapshot{Groups: []CatalogGroup{{ID: "g\x00", Name: "name", Tags: []CatalogTag{}}}}); ok {
		t.Fatal("control identifier accepted")
	}
}
