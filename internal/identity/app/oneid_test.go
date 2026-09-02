package app

import (
	"context"
	"sync"
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
)

func TestResolveDoesNotProvisionCustomer(t *testing.T) {
	store := NewMemoryStore()
	service := OneIDService{Store: store}
	reference := identitydomain.Reference{
		Kind: identitydomain.KindWeComExternalUserID, Scope: "wecom-corp:main", Value: "external-1",
		Assurance: identitydomain.AssuranceDeclared, Source: "http.body",
	}
	result, err := service.Resolve(context.Background(), reference)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != identityport.ResolveNotFound || store.CustomerCount() != 0 || store.ActiveIdentityCount() != 0 {
		t.Fatalf("Resolve()=%+v customers=%d identities=%d", result, store.CustomerCount(), store.ActiveIdentityCount())
	}
}

func TestProvisionRequiresOpaqueVerifiedFact(t *testing.T) {
	store := NewMemoryStore()
	service := OneIDService{Store: store}
	if _, err := service.ProvisionCustomerFromVerifiedIdentity(context.Background(), identitydomain.VerifiedFact{}); err == nil {
		t.Fatal("zero VerifiedFact must be rejected")
	}
	if _, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{
		Kind: identitydomain.KindWeComExternalUserID, Scope: "wecom-corp:", Value: "external-1", Source: "provider.adapter",
	}); err == nil {
		t.Fatal("invalid provider input must not construct a verified fact")
	}
}

func TestProvisionConcurrentScopedIdentityCreatesOneCustomer(t *testing.T) {
	store := NewMemoryStore()
	service := OneIDService{Store: store}
	fact := verifiedFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:main", "external-1")

	const workers = 20
	results := make(chan customerdomain.CustomerID, workers)
	errs := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			result, err := service.ProvisionCustomerFromVerifiedIdentity(context.Background(), fact)
			if err != nil {
				errs <- err
				return
			}
			results <- result.CustomerID
		}()
	}
	group.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	var customerID customerdomain.CustomerID
	for result := range results {
		if customerID == 0 {
			customerID = result
		}
		if result != customerID {
			t.Fatalf("concurrent provision returned customer %d after %d", result, customerID)
		}
	}
	if store.CustomerCount() != 1 || store.ActiveIdentityCount() != 1 {
		t.Fatalf("customers=%d identities=%d", store.CustomerCount(), store.ActiveIdentityCount())
	}
}

func TestSameValueDifferentScopesDoesNotResolveTogether(t *testing.T) {
	store := NewMemoryStore()
	service := OneIDService{Store: store}
	first, err := service.ProvisionCustomerFromVerifiedIdentity(context.Background(), verifiedFact(t, identitydomain.KindMPOpenID, "wechat-app:a", "same"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.ProvisionCustomerFromVerifiedIdentity(context.Background(), verifiedFact(t, identitydomain.KindMPOpenID, "wechat-app:b", "same"))
	if err != nil {
		t.Fatal(err)
	}
	if first.CustomerID == second.CustomerID || store.ActiveIdentityCount() != 2 {
		t.Fatalf("scope isolation failed: first=%+v second=%+v", first, second)
	}
}

func TestWeakLinkCreatesCandidateWithoutMerge(t *testing.T) {
	store, service, wecom, alipay, wecomFact := twoRoots(t)
	result, err := service.LinkVerifiedIdentity(context.Background(), LinkCommand{
		SourceCustomerID: alipay.CustomerID,
		Target:           wecomFact,
		Evidence:         evidence(identitydomain.EvidenceWeak),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != LinkCandidate || result.Candidate == nil || store.Root(alipay.CustomerID) != alipay.CustomerID || store.Root(wecom.CustomerID) != wecom.CustomerID {
		t.Fatalf("weak link=%+v", result)
	}
}

func TestConfirmedStrongLinkPrefersWeComRootAndCanBeReverted(t *testing.T) {
	store, service, wecom, alipay, wecomFact := twoRoots(t)
	candidateResult, err := service.LinkVerifiedIdentity(context.Background(), LinkCommand{
		SourceCustomerID: alipay.CustomerID,
		Target:           wecomFact,
		Evidence:         evidence(identitydomain.EvidenceStrong),
	})
	if err != nil {
		t.Fatal(err)
	}
	if candidateResult.Status != LinkCandidate || candidateResult.Candidate == nil || store.Root(alipay.CustomerID) != alipay.CustomerID {
		t.Fatalf("cross-root link must remain candidate: %+v", candidateResult)
	}
	result, err := service.ConfirmMerge(context.Background(), ConfirmMergeCommand{
		CandidateID: candidateResult.Candidate.ID, SurvivorCustomerID: wecom.CustomerID, Operator: "admin-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != LinkMerged || result.Merge == nil || result.Merge.ToCustomerID != wecom.CustomerID || store.Root(alipay.CustomerID) != wecom.CustomerID {
		t.Fatalf("merge=%+v root=%d", result, store.Root(alipay.CustomerID))
	}
	reverted, err := service.RevertConfirmedMerge(context.Background(), result.Merge.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reverted.Reversed || store.Root(alipay.CustomerID) != alipay.CustomerID {
		t.Fatalf("revert=%+v root=%d", reverted, store.Root(alipay.CustomerID))
	}
}

func TestConfirmMergeNeverGuessesSurvivorOrOverridesWeComPriority(t *testing.T) {
	store, service, wecom, alipay, wecomFact := twoRoots(t)
	candidateResult, err := service.LinkVerifiedIdentity(context.Background(), LinkCommand{
		SourceCustomerID: alipay.CustomerID, Target: wecomFact, Evidence: evidence(identitydomain.EvidenceStrong),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ConfirmMerge(context.Background(), ConfirmMergeCommand{
		CandidateID: candidateResult.Candidate.ID, SurvivorCustomerID: alipay.CustomerID, Operator: "admin-1",
	}); err == nil {
		t.Fatal("operator must not select a non-WeCom survivor")
	}
	if store.Root(alipay.CustomerID) != alipay.CustomerID || store.Root(wecom.CustomerID) != wecom.CustomerID {
		t.Fatal("failed confirmation must not mutate roots")
	}
}

func TestTwoWeComRootsCreateConflictAndNeverMerge(t *testing.T) {
	store := NewMemoryStore()
	service := OneIDService{Store: store}
	firstFact := verifiedFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:main", "external-a")
	secondFact := verifiedFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:main", "external-b")
	first, err := service.ProvisionCustomerFromVerifiedIdentity(context.Background(), firstFact)
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.ProvisionCustomerFromVerifiedIdentity(context.Background(), secondFact)
	if err != nil {
		t.Fatal(err)
	}
	candidateResult, err := service.LinkVerifiedIdentity(context.Background(), LinkCommand{
		SourceCustomerID: first.CustomerID, Target: secondFact, Evidence: evidence(identitydomain.EvidenceStrong),
	})
	if err != nil {
		t.Fatal(err)
	}
	if candidateResult.Status != LinkCandidate || candidateResult.Candidate == nil {
		t.Fatalf("cross-root link must produce candidate: %+v", candidateResult)
	}
	result, err := service.ConfirmMerge(context.Background(), ConfirmMergeCommand{
		CandidateID: candidateResult.Candidate.ID, SurvivorCustomerID: first.CustomerID, Operator: "admin-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != LinkConflict || result.Conflict == nil || result.Conflict.Reason != "two_wecom_roots" ||
		store.Root(first.CustomerID) != first.CustomerID || store.Root(second.CustomerID) != second.CustomerID {
		t.Fatalf("double WeCom conflict=%+v", result)
	}
}

func TestLinkIntentIsSingleUseAndAttachesMissingIdentityWithoutSecondRoot(t *testing.T) {
	store := NewMemoryStore()
	service := OneIDService{Store: store}
	source, err := service.ProvisionCustomerFromVerifiedIdentity(context.Background(), verifiedFact(t, identitydomain.KindAlipayOAuthUserID, "alipay-app:main:production", "ali-1"))
	if err != nil {
		t.Fatal(err)
	}
	intent, err := service.CreateLinkIntent(context.Background(), LinkIntentCommand{
		SourceCustomerID: source.CustomerID, Purpose: LinkIntentBindWeCom,
		TargetKind: identitydomain.KindWeComExternalUserID, ExpectedScope: "wecom-corp:main",
		ExpiresAt: time.Now().Add(time.Minute), Source: "trusted.binding.entry",
	})
	if err != nil {
		t.Fatal(err)
	}
	if intent.Token == "" {
		t.Fatal("link intent must return token once")
	}
	result, err := service.ConsumeLinkIntent(context.Background(), ConsumeLinkIntentCommand{
		Token: intent.Token, Target: verifiedFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:main", "external-1"), Evidence: evidence(identitydomain.EvidenceStrong),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != LinkAttached || result.CustomerID != source.CustomerID || store.CustomerCount() != 1 {
		t.Fatalf("consume result=%+v customers=%d", result, store.CustomerCount())
	}
	replay, err := service.ConsumeLinkIntent(context.Background(), ConsumeLinkIntentCommand{
		Token: intent.Token, Target: verifiedFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:main", "external-1"), Evidence: evidence(identitydomain.EvidenceStrong),
	})
	if err != nil {
		t.Fatal(err)
	}
	if replay.Status != LinkIntentReplay {
		t.Fatalf("replay=%+v", replay)
	}
}

func TestLinkIntentAcrossExistingRootsStillCreatesCandidate(t *testing.T) {
	store, service, wecom, alipay, wecomFact := twoRoots(t)
	intent, err := service.CreateLinkIntent(context.Background(), LinkIntentCommand{
		SourceCustomerID: alipay.CustomerID, Purpose: LinkIntentBindWeCom, TargetKind: identitydomain.KindWeComExternalUserID,
		ExpectedScope: "wecom-corp:main", ExpiresAt: time.Now().Add(time.Minute), Source: "trusted.binding.entry",
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.ConsumeLinkIntent(context.Background(), ConsumeLinkIntentCommand{
		Token: intent.Token, Target: wecomFact, Evidence: evidence(identitydomain.EvidenceStrong),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != LinkCandidate || result.Candidate == nil || store.Root(alipay.CustomerID) != alipay.CustomerID || store.Root(wecom.CustomerID) != wecom.CustomerID {
		t.Fatalf("link intent must not merge roots: %+v", result)
	}
}

func TestLinkIntentRejectsExpiredAndScopeMismatchedConsumption(t *testing.T) {
	store := NewMemoryStore()
	service := OneIDService{Store: store}
	source, err := service.ProvisionCustomerFromVerifiedIdentity(context.Background(), verifiedFact(t, identitydomain.KindAlipayOAuthUserID, "alipay-app:main:production", "ali-1"))
	if err != nil {
		t.Fatal(err)
	}
	expired, err := store.CreateLinkIntent(context.Background(), LinkIntentCommand{
		SourceCustomerID: source.CustomerID, Purpose: LinkIntentBindWeCom, TargetKind: identitydomain.KindWeComExternalUserID,
		ExpiresAt: time.Now().Add(-time.Second), Source: "trusted.binding.entry",
	})
	if err != nil {
		t.Fatal(err)
	}
	expiredResult, err := service.ConsumeLinkIntent(context.Background(), ConsumeLinkIntentCommand{
		Token: expired.Token, Target: verifiedFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:main", "external-expired"), Evidence: evidence(identitydomain.EvidenceStrong),
	})
	if err != nil || expiredResult.Status != LinkIntentExpired {
		t.Fatalf("expired consumption result=%+v err=%v", expiredResult, err)
	}
	intent, err := service.CreateLinkIntent(context.Background(), LinkIntentCommand{
		SourceCustomerID: source.CustomerID, Purpose: LinkIntentBindWeCom, TargetKind: identitydomain.KindWeComExternalUserID,
		ExpectedScope: "wecom-corp:main", ExpiresAt: time.Now().Add(time.Minute), Source: "trusted.binding.entry",
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.ConsumeLinkIntent(context.Background(), ConsumeLinkIntentCommand{
		Token: intent.Token, Target: verifiedFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:other", "external-1"), Evidence: evidence(identitydomain.EvidenceStrong),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != LinkScopeMismatch || store.ActiveIdentityCount() != 1 {
		t.Fatalf("scope mismatch=%+v identities=%d", result, store.ActiveIdentityCount())
	}
}

func twoRoots(t *testing.T) (*MemoryStore, OneIDService, identityport.ProvisionResult, identityport.ProvisionResult, identitydomain.VerifiedFact) {
	t.Helper()
	store := NewMemoryStore()
	service := OneIDService{Store: store}
	wecomFact := verifiedFact(t, identitydomain.KindWeComExternalUserID, "wecom-corp:main", "external-1")
	wecom, err := service.ProvisionCustomerFromVerifiedIdentity(context.Background(), wecomFact)
	if err != nil {
		t.Fatal(err)
	}
	alipay, err := service.ProvisionCustomerFromVerifiedIdentity(context.Background(), verifiedFact(t, identitydomain.KindAlipayOAuthUserID, "alipay-app:main:production", "ali-1"))
	if err != nil {
		t.Fatal(err)
	}
	return store, service, wecom, alipay, wecomFact
}

func verifiedFact(t *testing.T, kind identitydomain.Kind, scope, value string) identitydomain.VerifiedFact {
	t.Helper()
	fact, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{
		Kind: kind, Scope: scope, Value: value, Source: "provider.adapter",
	})
	if err != nil {
		t.Fatal(err)
	}
	return fact
}

func evidence(strength identitydomain.EvidenceStrength) identitydomain.LinkEvidence {
	return identitydomain.LinkEvidence{
		Type: "signed_link_intent", Strength: strength, Source: "provider.adapter", EventID: "event-1",
		Digest: "digest-only", PolicyVersion: "oneid-v1",
	}
}
