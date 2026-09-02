package externaleffects

import "testing"

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
	first := ControlCommand{EffectID: "eer_7", ReceiptKey: digestForTest("key"), EvidenceDigest: digestForTest("evidence")}
	second := first
	second.EvidenceDigest = digestForTest("other")
	if first.Digest("reconcile") == second.Digest("reconcile") {
		t.Fatal("reconcile evidence omitted from command digest")
	}
}
