package query

import (
	"context"
	"encoding/hex"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	"strings"
)

type hxcCoverageIndex struct {
	phone, union                 map[string]bool
	phoneComplete, unionComplete bool
	scope                        string
}

func (s PostgreSQL) hxcCoverageIndexes(subjects []identityport.HXCSubject, complete bool) hxcCoverageIndex {
	x := hxcCoverageIndex{phone: map[string]bool{}, union: map[string]bool{}, phoneComplete: complete && len(subjects) > 0, unionComplete: complete && len(subjects) > 0}
	for _, v := range subjects {
		if v.ConflictReason != "" {
			x.phoneComplete = false
			x.unionComplete = false
		}
		n, e := identitydomain.Normalize(identitydomain.Reference{Kind: identitydomain.KindPhone, Scope: "phone:cn11", Value: v.Phone, Assurance: identitydomain.AssuranceDeclared, Source: "hxc"})
		if e != nil || v.Phone == "" {
			x.phoneComplete = false
		} else {
			d := s.phoneVault.LookupDigest(n.NormalizedValue)
			key := hex.EncodeToString(d[:])
			if x.phone[key] {
				x.phoneComplete = false
			}
			x.phone[key] = true
			x.phone["e164:+86"+n.NormalizedValue] = true
		}
		n, e = identitydomain.Normalize(identitydomain.Reference{Kind: identitydomain.KindUnionID, Scope: v.UnionIDScope, Value: v.UnionID, Assurance: identitydomain.AssuranceVerified, Source: "hxc"})
		if e != nil || v.UnionID == "" || !v.UnionIDVerified {
			x.unionComplete = false
		} else {
			if x.scope != "" && x.scope != n.Scope {
				x.unionComplete = false
			}
			x.scope = n.Scope
			key := n.Scope + "\x00" + n.NormalizedValue
			if x.union[key] {
				x.unionComplete = false
			}
			x.union[key] = true
		}
	}
	return x
}
func (s PostgreSQL) InspectHXCRegistrationCoverage(ctx context.Context, subjects []identityport.HXCSubject, complete bool) (map[customerdomain.CustomerID]identityport.HXCRegistrationState, error) {
	if s.phoneVault == nil || len(subjects) > 100000 {
		return nil, ErrInvalidQuery
	}
	out := map[customerdomain.CustomerID]identityport.HXCRegistrationState{}
	x := s.hxcCoverageIndexes(subjects, complete)
	// Reuse the authoritative positive matcher. Any source conflict disables
	// negative inference, including cases lacking a unique candidate root.
	for start := 0; start < len(subjects); start += 1000 {
		end := start + 1000
		if end > len(subjects) {
			end = len(subjects)
		}
		results, e := s.InspectHXCSubjects(ctx, subjects[start:end])
		if e != nil {
			return nil, e
		}
		for _, r := range results {
			if r.Disposition == identityport.HXCMatched && r.CustomerID > 0 {
				out[r.CustomerID] = identityport.HXCRegistered
			} else if r.Disposition == identityport.HXCConflict || r.Disposition == identityport.HXCInvalid {
				x.phoneComplete = false
				x.unionComplete = false
			}
		}
	}
	tx, e := platformpostgres.RequireTransaction(ctx)
	if e != nil {
		return nil, e
	}
	rows, e := tx.Query(ctx, `SELECT ci.customer_id,ci.kind,ci.scope_key,ci.assurance,COALESCE(ci.normalized_value,''),COALESCE(encode(ci.normalized_value_digest,'hex'),'') FROM customer_identities ci JOIN customers c ON c.id=ci.customer_id WHERE ci.status='active' AND ci.kind IN ('phone','unionid') AND c.status<>'merged' ORDER BY ci.customer_id,ci.kind,ci.id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	type candidate struct {
		count                  int
		known                  bool
		phoneCount, unionCount int
	}
	candidates := map[customerdomain.CustomerID]candidate{}
	for rows.Next() {
		var id customerdomain.CustomerID
		var kind, scope, assurance, value, digest string
		if e = rows.Scan(&id, &kind, &scope, &assurance, &value, &digest); e != nil {
			return nil, e
		}
		c, ok := candidates[id]
		if !ok {
			c.known = true
		}
		c.count++
		if assurance != "verified" {
			c.known = false
		}
		if kind == "phone" {
			c.phoneCount++
			key := digest
			if scope == "phone:e164" {
				key = "e164:" + value
			} else if scope != "phone:cn11" {
				c.known = false
			}
			if !x.phoneComplete || key == "" || x.phone[key] {
				c.known = false
			}
		}
		if kind == "unionid" {
			c.unionCount++
			if !x.unionComplete || scope != x.scope || strings.TrimSpace(value) == "" || x.union[scope+"\x00"+value] {
				c.known = false
			}
		}
		candidates[id] = c
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	for id, c := range candidates {
		if out[id] == identityport.HXCRegistered {
			continue
		}
		out[id] = identityport.HXCRegistrationUnknown
		if complete && c.known && c.count > 0 && c.phoneCount <= 1 && c.unionCount <= 1 {
			out[id] = identityport.HXCUnregistered
		}
	}
	return out, nil
}

var _ identityport.HXCRegistrationCoverage = PostgreSQL{}
