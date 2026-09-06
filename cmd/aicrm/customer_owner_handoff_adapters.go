package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"strconv"
	"strings"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

// customerOwnerHandoffCandidates is a Composition-only adapter. It composes
// three owning ports: Access maps local staff IDs, WeCom verifies the exact
// current follow relation, and Identity reads an already canonical external
// identity. It creates or links no identity and writes no WeCom fact.
type customerOwnerHandoffCandidates struct {
	staff interface {
		UserByID(context.Context, int64, bool) (accessdomain.User, error)
	}
	relationships wecomport.OwnerHandoffRelationshipReader
	identities    identityport.ExternalIdentityValueReader
	owners        interface {
		LocalOwner(context.Context, customerdomain.CustomerID, bool) (customerport.LocalOwner, bool, error)
	}
}

func (a customerOwnerHandoffCandidates) ResolveOwnerHandoffCandidates(ctx context.Context, mode customerport.OwnerHandoffMode, sourceStaffID, targetStaffID int64, corpScope string, ids []customerdomain.CustomerID) ([]customerport.OwnerHandoffCandidate, error) {
	if a.staff == nil || a.owners == nil || len(ids) == 0 || (mode != customerport.OwnerHandoffLocalOnly && mode != customerport.OwnerHandoffWeComThenCRM) {
		return nil, errors.New("owner handoff candidate adapter unavailable")
	}
	source, err := a.staff.UserByID(ctx, sourceStaffID, false)
	if err != nil || source.ID != sourceStaffID {
		return nil, errors.New("owner handoff source unavailable")
	}
	target, err := a.staff.UserByID(ctx, targetStaffID, false)
	if err != nil || target.ID != targetStaffID || !target.Active {
		return nil, errors.New("owner handoff target unavailable")
	}
	out := make([]customerport.OwnerHandoffCandidate, 0, len(ids))
	for _, customerID := range ids {
		owner, found, ownerErr := a.owners.LocalOwner(ctx, customerID, false)
		if ownerErr != nil {
			return nil, ownerErr
		}
		candidate := customerport.OwnerHandoffCandidate{CustomerID: customerID, State: "ready"}
		if found {
			candidate.ExpectedLocalOwnerID, candidate.ExpectedLocalVersion = owner.StaffID, owner.Version
		}
		if mode == customerport.OwnerHandoffLocalOnly {
			candidate.RelationshipDigest = sha256.Sum256([]byte(strings.Join([]string{"owner-handoff-local-v1", strconv.FormatInt(int64(customerID), 10), strconv.FormatInt(candidate.ExpectedLocalOwnerID, 10), strconv.FormatInt(candidate.ExpectedLocalVersion, 10)}, "\x00")))
			out = append(out, candidate)
			continue
		}
		if source.WeComUserID == "" || target.WeComUserID == "" {
			candidate.State, candidate.Reason = "unresolved", "staff_wecom_identity_missing"
			out = append(out, candidate)
			continue
		}
		relation, relationErr := a.relationships.OwnerHandoffRelationship(ctx, customerID, corpScope, source.WeComUserID)
		if relationErr != nil {
			return nil, relationErr
		}
		if !relation.Active {
			candidate.State, candidate.Reason = "unresolved", "source_follow_relationship_missing"
			out = append(out, candidate)
			continue
		}
		externalID, identityFound, identityErr := a.identities.VerifiedExternalIdentityValue(ctx, customerID, identitydomain.KindWeComExternalUserID, corpScope)
		if identityErr != nil {
			return nil, identityErr
		}
		if !identityFound || externalID == "" {
			candidate.State, candidate.Reason = "unresolved", "canonical_wecom_identity_missing"
			out = append(out, candidate)
			continue
		}
		candidate.RelationshipDigest, candidate.SourceUserID, candidate.TargetUserID, candidate.ExternalUserID = relation.VersionDigest, source.WeComUserID, target.WeComUserID, externalID
		out = append(out, candidate)
	}
	return out, nil
}

var _ customerport.OwnerHandoffCandidateResolver = customerOwnerHandoffCandidates{}
