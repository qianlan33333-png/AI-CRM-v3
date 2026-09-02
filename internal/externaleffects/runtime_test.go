package externaleffects

import (
	"errors"
	"testing"
)

func digestForTest(label string) Digest { return Hash("test", label) }
func envelopeForTest() Envelope {
	return Envelope{Owner: OwnerOutbound, Kind: KindOutboundMessage, SourceRefDigest: digestForTest("source"), TargetRefDigest: digestForTest("target"), PayloadDigest: digestForTest("payload"), PolicyVersionHash: digestForTest("policy")}
}

func TestClosedDigestOnlyEnvelopeAndStates(t *testing.T) {
	if !envelopeForTest().Valid() {
		t.Fatal("valid digest-only outbound envelope rejected")
	}
	bad := envelopeForTest()
	bad.PayloadDigest = "13800000000"
	if bad.Valid() {
		t.Fatal("raw payload accepted")
	}
	for _, kind := range []Kind{KindOutboundMessage, KindOutboundMedia, KindWeComTagCatalog, KindGroupMessage} {
		candidate := envelopeForTest()
		candidate.Kind = kind
		if !candidate.Valid() {
			t.Fatalf("frozen kind %q rejected", kind)
		}
	}
	if CanTransition(StateUnknown, StateQueued) || CanTransition(StateUnknown, StateCancelled) {
		t.Fatal("unknown result must require reconciliation")
	}
	if !CanTransition(StateUnknown, StateReconciled) {
		t.Fatal("unknown result must reconcile")
	}
}

func TestControlDigestDetectsPayloadDrift(t *testing.T) {
	first := ControlCommand{EffectID: "eer_7", ReceiptKey: digestForTest("key"), EvidenceDigest: digestForTest("evidence"), ActorAdminUserID: 7}
	second := first
	second.EvidenceDigest = digestForTest("other")
	if first.Digest("reconcile") == second.Digest("reconcile") {
		t.Fatal("reconcile evidence omitted from command digest")
	}
	second = first
	second.ActorAdminUserID = 8
	if first.Digest("reconcile") == second.Digest("reconcile") {
		t.Fatal("control actor omitted from command digest")
	}
}

func TestParseEffectIDMatchesThePublicContractExactly(t *testing.T) {
	for _, value := range []string{"eer_1", "eer_42", "eer_9223372036854775807"} {
		if _, err := parseEffectID(value); err != nil {
			t.Fatalf("valid %q: %v", value, err)
		}
	}
	for _, value := range []string{"", "eer_", "eer_0", "eer_01", "eer_1junk", "eer_+1", "eer_-1", "eer_1.0", "eer_9223372036854775808"} {
		if _, err := parseEffectID(value); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid %q: %v", value, err)
		}
	}
}
