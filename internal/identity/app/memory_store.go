package app

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"sort"
	"strconv"
	"sync"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
)

var ErrMergeNotReversible = errors.New("merge is not reversible")

type memoryCustomer struct {
	ID       customerdomain.CustomerID
	Status   customerdomain.Status
	MergedTo customerdomain.CustomerID
}

type memoryIdentity struct {
	StoredIdentity
	Active bool
}

type memoryLinkIntent struct {
	ID        int64
	Hash      string
	Command   LinkIntentCommand
	Status    string
	ExpiresAt time.Time
}

// MemoryStore serializes every operation with a mutex. It is a test double for
// the Store transaction contract, not a production persistence implementation.
type MemoryStore struct {
	mu sync.Mutex

	nextCustomerID  customerdomain.CustomerID
	nextIdentityID  int64
	nextCandidateID int64
	nextConflictID  int64
	nextMergeID     int64
	nextIntentID    int64

	customers     map[customerdomain.CustomerID]memoryCustomer
	identities    map[int64]memoryIdentity
	identityByKey map[string]int64
	candidates    map[int64]MergeCandidate
	conflicts     map[string]Conflict
	merges        map[int64]MergeRecord
	movedByMerge  map[int64][]int64
	intentsByHash map[string]memoryLinkIntent
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		customers:     make(map[customerdomain.CustomerID]memoryCustomer),
		identities:    make(map[int64]memoryIdentity),
		identityByKey: make(map[string]int64),
		candidates:    make(map[int64]MergeCandidate),
		conflicts:     make(map[string]Conflict),
		merges:        make(map[int64]MergeRecord),
		movedByMerge:  make(map[int64][]int64),
		intentsByHash: make(map[string]memoryLinkIntent),
	}
}

func (store *MemoryStore) Resolve(_ context.Context, reference identitydomain.NormalizedReference) (StoredIdentity, bool, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	identityID, found := store.identityByKey[identityKey(reference)]
	if !found {
		return StoredIdentity{}, false, nil
	}
	identity := store.identities[identityID]
	if !identity.Active {
		return StoredIdentity{}, false, nil
	}
	identity.CustomerID = store.rootLocked(identity.CustomerID)
	return identity.StoredIdentity, true, nil
}

func (store *MemoryStore) Provision(_ context.Context, fact identitydomain.VerifiedFact) (ProvisionedIdentity, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	reference := fact.Reference()
	if existingID, found := store.identityByKey[identityKey(reference)]; found {
		existing := store.identities[existingID]
		return ProvisionedIdentity{CustomerID: store.rootLocked(existing.CustomerID), IdentityID: existingID}, nil
	}
	customerID := store.createCustomerLocked()
	identityID := store.createIdentityLocked(customerID, reference)
	return ProvisionedIdentity{CustomerID: customerID, IdentityID: identityID, Created: true}, nil
}

func (store *MemoryStore) Link(_ context.Context, command LinkCommand) (LinkResult, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.linkLocked(command)
}

func (store *MemoryStore) CreateLinkIntent(_ context.Context, command LinkIntentCommand) (CreatedLinkIntent, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.rootLocked(command.SourceCustomerID) < 1 {
		return CreatedLinkIntent{}, ErrInvalidLinkCommand
	}
	rawToken, err := randomLinkToken()
	if err != nil {
		return CreatedLinkIntent{}, err
	}
	hash := linkTokenHash(rawToken)
	store.nextIntentID++
	intent := memoryLinkIntent{ID: store.nextIntentID, Hash: hash, Command: command, Status: "pending", ExpiresAt: command.ExpiresAt}
	store.intentsByHash[hash] = intent
	return CreatedLinkIntent{ID: intent.ID, Token: rawToken, ExpiresAt: intent.ExpiresAt}, nil
}

func (store *MemoryStore) ConsumeLinkIntent(_ context.Context, command ConsumeLinkIntentCommand) (LinkResult, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	hash := linkTokenHash(command.Token)
	intent, found := store.intentsByHash[hash]
	if !found || intent.Status != "pending" {
		return LinkResult{Status: LinkIntentReplay}, nil
	}
	if !intent.ExpiresAt.After(time.Now()) {
		intent.Status = "expired"
		store.intentsByHash[hash] = intent
		return LinkResult{Status: LinkIntentExpired}, nil
	}
	reference := command.Target.Reference()
	if reference.Kind != intent.Command.TargetKind || (intent.Command.ExpectedScope != "" && reference.Scope != intent.Command.ExpectedScope) {
		return LinkResult{Status: LinkScopeMismatch}, nil
	}
	result, err := store.linkLocked(LinkCommand{
		SourceCustomerID: intent.Command.SourceCustomerID,
		Target:           command.Target,
		Evidence:         command.Evidence,
	})
	if err != nil {
		return LinkResult{}, err
	}
	intent.Status = "consumed"
	store.intentsByHash[hash] = intent
	return result, nil
}

func (store *MemoryStore) linkLocked(command LinkCommand) (LinkResult, error) {

	sourceRoot := store.rootLocked(command.SourceCustomerID)
	if sourceRoot < 1 {
		return LinkResult{}, ErrInvalidLinkCommand
	}
	reference := command.Target.Reference()
	identityID, exists := store.identityByKey[identityKey(reference)]
	if !exists {
		identityID = store.createIdentityLocked(sourceRoot, reference)
		return LinkResult{Status: LinkAttached, CustomerID: sourceRoot, IdentityID: identityID}, nil
	}
	target := store.identities[identityID]
	targetRoot := store.rootLocked(target.CustomerID)
	if targetRoot == sourceRoot {
		return LinkResult{Status: LinkAlreadyLinked, CustomerID: sourceRoot, IdentityID: identityID}, nil
	}

	reason := "cross_root_link_requires_confirmation"
	if command.Evidence.Strength != identitydomain.EvidenceStrong {
		reason = "non_strong_evidence"
	}
	candidate := store.createCandidateLocked(sourceRoot, targetRoot, command.Evidence, reason)
	return LinkResult{Status: LinkCandidate, CustomerID: sourceRoot, IdentityID: identityID, Candidate: &candidate}, nil
}

func (store *MemoryStore) ConfirmMerge(_ context.Context, command ConfirmMergeCommand) (LinkResult, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	candidate, found := store.candidates[command.CandidateID]
	if !found || candidate.Status != "open" || candidate.Evidence.Strength != identitydomain.EvidenceStrong {
		return LinkResult{}, ErrInvalidLinkCommand
	}
	left, right := store.rootLocked(candidate.LeftCustomerID), store.rootLocked(candidate.RightCustomerID)
	if left < 1 || right < 1 || left == right ||
		(command.SurvivorCustomerID != left && command.SurvivorCustomerID != right) {
		return LinkResult{}, ErrInvalidLinkCommand
	}
	if reason := store.strongConflictLocked(left, right); reason != "" {
		conflict := store.createConflictLocked(left, right, reason, candidate.Evidence)
		candidate.Status = "rejected"
		store.candidates[candidate.ID] = candidate
		return LinkResult{Status: LinkConflict, CustomerID: command.SurvivorCustomerID, Candidate: &candidate, Conflict: &conflict}, nil
	}
	if store.hasWeComLocked(left) != store.hasWeComLocked(right) {
		wecomRoot := left
		if !store.hasWeComLocked(left) {
			wecomRoot = right
		}
		if command.SurvivorCustomerID != wecomRoot {
			return LinkResult{}, ErrInvalidLinkCommand
		}
	}
	loser := left
	if command.SurvivorCustomerID == left {
		loser = right
	}
	merge := store.mergeLocked(loser, command.SurvivorCustomerID, candidate.Evidence, command.Operator)
	candidate.Status = "confirmed"
	store.candidates[candidate.ID] = candidate
	return LinkResult{Status: LinkMerged, CustomerID: command.SurvivorCustomerID, Candidate: &candidate, Merge: &merge}, nil
}

func (store *MemoryStore) RevertMerge(_ context.Context, mergeID int64) (MergeRecord, error) {
	store.mu.Lock()
	defer store.mu.Unlock()
	merge, found := store.merges[mergeID]
	if !found || merge.Reversed {
		return MergeRecord{}, ErrMergeNotReversible
	}
	from := store.customers[merge.FromCustomerID]
	if from.Status != customerdomain.StatusMerged || from.MergedTo != merge.ToCustomerID {
		return MergeRecord{}, ErrMergeNotReversible
	}
	for _, identityID := range store.movedByMerge[mergeID] {
		identity := store.identities[identityID]
		if identity.Active && store.rootLocked(identity.CustomerID) == merge.ToCustomerID {
			identity.CustomerID = merge.FromCustomerID
			store.identities[identityID] = identity
		}
	}
	from.Status = customerdomain.StatusActive
	from.MergedTo = 0
	store.customers[from.ID] = from
	merge.Reversed = true
	store.merges[mergeID] = merge
	return merge, nil
}

func (store *MemoryStore) CustomerCount() int {
	store.mu.Lock()
	defer store.mu.Unlock()
	return len(store.customers)
}

func (store *MemoryStore) ActiveIdentityCount() int {
	store.mu.Lock()
	defer store.mu.Unlock()
	count := 0
	for _, identity := range store.identities {
		if identity.Active {
			count++
		}
	}
	return count
}

func (store *MemoryStore) Root(customerID customerdomain.CustomerID) customerdomain.CustomerID {
	store.mu.Lock()
	defer store.mu.Unlock()
	return store.rootLocked(customerID)
}

func (store *MemoryStore) createCustomerLocked() customerdomain.CustomerID {
	store.nextCustomerID++
	customer := memoryCustomer{ID: store.nextCustomerID, Status: customerdomain.StatusActive}
	store.customers[customer.ID] = customer
	return customer.ID
}

func (store *MemoryStore) createIdentityLocked(customerID customerdomain.CustomerID, reference identitydomain.NormalizedReference) int64 {
	store.nextIdentityID++
	identity := memoryIdentity{StoredIdentity: StoredIdentity{
		ID: store.nextIdentityID, CustomerID: customerID, Reference: reference,
	}, Active: true}
	store.identities[identity.ID] = identity
	store.identityByKey[identityKey(reference)] = identity.ID
	return identity.ID
}

func (store *MemoryStore) rootLocked(customerID customerdomain.CustomerID) customerdomain.CustomerID {
	seen := map[customerdomain.CustomerID]struct{}{}
	for customerID > 0 {
		if _, loop := seen[customerID]; loop {
			return 0
		}
		seen[customerID] = struct{}{}
		customer, found := store.customers[customerID]
		if !found {
			return 0
		}
		if customer.Status != customerdomain.StatusMerged {
			return customerID
		}
		customerID = customer.MergedTo
	}
	return 0
}

func (store *MemoryStore) hasWeComLocked(customerID customerdomain.CustomerID) bool {
	for _, identity := range store.identities {
		if identity.Active && store.rootLocked(identity.CustomerID) == customerID && identity.Reference.Kind == identitydomain.KindWeComExternalUserID {
			return true
		}
	}
	return false
}

func (store *MemoryStore) strongConflictLocked(left, right customerdomain.CustomerID) string {
	if store.hasWeComLocked(left) && store.hasWeComLocked(right) {
		return "two_wecom_roots"
	}
	for _, leftIdentity := range store.identities {
		if !leftIdentity.Active || store.rootLocked(leftIdentity.CustomerID) != left || !singleValueStrong(leftIdentity.Reference.Kind) {
			continue
		}
		for _, rightIdentity := range store.identities {
			if rightIdentity.Active && store.rootLocked(rightIdentity.CustomerID) == right &&
				rightIdentity.Reference.Kind == leftIdentity.Reference.Kind &&
				rightIdentity.Reference.Scope == leftIdentity.Reference.Scope &&
				rightIdentity.Reference.NormalizedValue != leftIdentity.Reference.NormalizedValue {
				return "single_value_strong_namespace"
			}
		}
	}
	return ""
}

func (store *MemoryStore) mergeLocked(loser, survivor customerdomain.CustomerID, evidence identitydomain.LinkEvidence, operator string) MergeRecord {
	store.nextMergeID++
	moved := make([]int64, 0)
	for identityID, identity := range store.identities {
		if identity.Active && store.rootLocked(identity.CustomerID) == loser {
			identity.CustomerID = survivor
			store.identities[identityID] = identity
			moved = append(moved, identityID)
		}
	}
	sort.Slice(moved, func(i, j int) bool { return moved[i] < moved[j] })
	loserCustomer := store.customers[loser]
	loserCustomer.Status = customerdomain.StatusMerged
	loserCustomer.MergedTo = survivor
	store.customers[loser] = loserCustomer
	merge := MergeRecord{ID: store.nextMergeID, FromCustomerID: loser, ToCustomerID: survivor, Evidence: evidence, Rule: "confirmed_candidate", Operator: operator}
	store.merges[merge.ID] = merge
	store.movedByMerge[merge.ID] = moved
	return merge
}

func (store *MemoryStore) createCandidateLocked(left, right customerdomain.CustomerID, evidence identitydomain.LinkEvidence, reason string) MergeCandidate {
	store.nextCandidateID++
	candidate := MergeCandidate{ID: store.nextCandidateID, LeftCustomerID: left, RightCustomerID: right, Evidence: evidence, Reason: reason, Status: "open"}
	store.candidates[candidate.ID] = candidate
	return candidate
}

func (store *MemoryStore) createConflictLocked(left, right customerdomain.CustomerID, reason string, evidence identitydomain.LinkEvidence) Conflict {
	if right < left {
		left, right = right, left
	}
	key := strconv.FormatInt(int64(left), 10) + ":" + strconv.FormatInt(int64(right), 10) + ":" + reason
	if existing, found := store.conflicts[key]; found {
		return existing
	}
	store.nextConflictID++
	conflict := Conflict{ID: store.nextConflictID, LeftCustomerID: left, RightCustomerID: right, Reason: reason, Evidence: evidence}
	store.conflicts[key] = conflict
	return conflict
}

func identityKey(reference identitydomain.NormalizedReference) string {
	return string(reference.Kind) + "\x00" + reference.Scope + "\x00" + reference.NormalizedValue
}

func singleValueStrong(kind identitydomain.Kind) bool {
	switch kind {
	case identitydomain.KindWeComExternalUserID, identitydomain.KindUnionID,
		identitydomain.KindMPOpenID, identitydomain.KindOAOpenID,
		identitydomain.KindAlipayUserID, identitydomain.KindAlipayOAuthUserID,
		identitydomain.KindAlipayOAuthOpenID, identitydomain.KindAlipayBuyerID,
		identitydomain.KindAlipayBuyerOpenID, identitydomain.KindFirstPartyMemberID:
		return true
	default:
		return false
	}
}

func randomLinkToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

func linkTokenHash(token string) string {
	digest := sha256.Sum256([]byte(token))
	return base64.RawStdEncoding.EncodeToString(digest[:])
}
