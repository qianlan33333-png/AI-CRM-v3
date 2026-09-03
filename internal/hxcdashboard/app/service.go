package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/domain"
	hxcport "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/port"
	hxcstore "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/store"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

var ErrNotReady = errors.New("hxc dashboard is not ready")
var ErrConflict = errors.New("hxc dashboard refresh already active")

type JobEnqueuer interface {
	Enqueue(context.Context, int64) error
}
type Audit interface {
	Append(context.Context, platformaudit.Event) (platformaudit.Event, error)
}

type Service struct {
	Enabled    bool
	Scope      string
	SubjectKey []byte
	Source     hxcport.CurrentSource
	Identity   identityport.HXCUnionIDBatchResolver
	Store      *hxcstore.PostgreSQL
	Enqueuer   JobEnqueuer
	Audit      Audit
	UOW        platformport.UnitOfWork
	Now        func() time.Time
}

func (s Service) Ready() bool {
	return s.Enabled && strings.HasPrefix(s.Scope, "wechat-open-platform:") && len(s.SubjectKey) >= 32 && s.Source != nil && s.Source.Ready() && s.Identity != nil && s.Store != nil && s.Enqueuer != nil && s.Audit != nil && s.UOW != nil
}
func (s Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

func (s Service) Create(ctx context.Context, runKey, trigger string, requestedBy int64) (domain.RefreshRun, bool, error) {
	if !s.Ready() || runKey == "" || !validTrigger(trigger, requestedBy) {
		return domain.RefreshRun{}, false, ErrNotReady
	}
	digest := sha256.Sum256([]byte(trigger + "\x00" + runKey))
	var run domain.RefreshRun
	var replay bool
	err := s.UOW.Within(ctx, func(txCtx context.Context) error {
		var err error
		run, replay, err = s.Store.CreateRun(txCtx, runKey, digest, trigger, requestedBy)
		if err != nil {
			if errors.Is(err, hxcstore.ErrActiveRefresh) {
				return ErrConflict
			}
			return err
		}
		if replay {
			return nil
		}
		if err = s.Enqueuer.Enqueue(txCtx, run.ID); err != nil {
			return err
		}
		actor, actorID := "system", ""
		if requestedBy > 0 {
			actor = "admin"
			actorID = strconv.FormatInt(requestedBy, 10)
		}
		payload, _ := json.Marshal(map[string]string{"trigger": trigger})
		_, err = s.Audit.Append(txCtx, platformaudit.Event{IdempotencyKey: idempotency.Key("hxc-dashboard-refresh-created:" + hex.EncodeToString(digest[:])), Action: "hxc.dashboard_refresh_created", ActorType: actor, ActorID: actorID, ResourceType: "hxc_dashboard_refresh", ResourceID: strconv.FormatInt(run.ID, 10), Payload: payload})
		return err
	})
	return run, replay, err
}
func validTrigger(trigger string, requestedBy int64) bool {
	return trigger == "manual" && requestedBy > 0 || (trigger == "scheduled" || trigger == "initial") && requestedBy == 0
}
func (s Service) Get(ctx context.Context, id int64) (domain.RefreshRun, error) {
	return s.Store.GetRun(ctx, id, "")
}

func (s Service) Refresh(ctx context.Context, runID int64) error {
	if !s.Ready() || runID < 1 {
		return ErrNotReady
	}
	run, err := s.Get(ctx, runID)
	if err != nil {
		return err
	}
	if run.Status == domain.RefreshSucceeded || run.Status == domain.RefreshFailed {
		return nil
	}
	if err = s.UOW.Within(ctx, func(txCtx context.Context) error { return s.Store.MarkRunning(txCtx, runID) }); err != nil {
		return err
	}
	if err = s.Source.Preflight(ctx); err != nil {
		return s.fail(ctx, runID, "source_preflight_failed", err)
	}
	asOf := s.now()
	snapshot, err := s.Source.ReadSnapshot(ctx, asOf)
	if err != nil {
		return s.fail(ctx, runID, "source_read_failed", err)
	}
	projection, err := s.project(ctx, snapshot)
	if err != nil {
		return s.fail(ctx, runID, "identity_resolution_failed", err)
	}
	if err = s.UOW.Within(ctx, func(txCtx context.Context) error {
		return s.Store.MarkPublishing(txCtx, runID, int64(len(projection.Rows)))
	}); err != nil {
		return err
	}
	err = s.UOW.Within(ctx, func(txCtx context.Context) error {
		id, err := s.Store.Publish(txCtx, runID, projection)
		if err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]any{"projection_id": id, "total": projection.Counts.Total})
		_, err = s.Audit.Append(txCtx, platformaudit.Event{IdempotencyKey: idempotency.Key(fmt.Sprintf("hxc-dashboard-published:%d", runID)), Action: "hxc.dashboard_published", ActorType: "system", ResourceType: "hxc_dashboard_projection", ResourceID: strconv.FormatInt(id, 10), Payload: payload})
		return err
	})
	if err != nil {
		return s.fail(ctx, runID, "publish_failed", err)
	}
	return nil
}

func (s Service) project(ctx context.Context, snapshot hxcport.Snapshot) (domain.Projection, error) {
	projection := domain.Projection{AsOf: snapshot.AsOf, Watermark: snapshot.Watermark, SourceDigest: snapshot.Digest, Rows: make([]domain.ProjectionRow, len(snapshot.Rows))}
	refs := make([]identityport.ScopedUnionID, 0, len(snapshot.Rows))
	for i, row := range snapshot.Rows {
		digest, ref, err := domain.Subject(s.SubjectKey, row.HXCUserID)
		if err != nil {
			return domain.Projection{}, err
		}
		projection.Rows[i] = domain.ProjectionRow{SubjectDigest: digest, UserRef: ref, Stage: domain.Classify(row, snapshot.AsOf), SourceRow: row, IdentityState: domain.Unmatched}
		projection.Rows[i].HXCUserID = ""
		if row.UnionID != "" {
			refs = append(refs, identityport.ScopedUnionID{Position: i, Scope: s.Scope, UnionID: row.UnionID})
		}
		projection.Rows[i].UnionID = ""
	}
	for start := 0; start < len(refs); start += 1000 {
		end := start + 1000
		if end > len(refs) {
			end = len(refs)
		}
		var results []identityport.ScopedUnionIDResult
		err := s.UOW.Within(ctx, func(txCtx context.Context) error {
			var err error
			results, err = s.Identity.ResolveHXCUnionIDs(txCtx, refs[start:end])
			return err
		})
		if err != nil {
			return domain.Projection{}, err
		}
		for _, result := range results {
			row := &projection.Rows[result.Position]
			switch result.Status {
			case identityport.ResolveFound:
				row.IdentityState = domain.Matched
				row.CustomerID = result.CustomerID
			case identityport.ResolveConflict:
				row.IdentityState = domain.Conflict
			}
		}
	}
	owners := map[int64][]int{}
	for i, row := range projection.Rows {
		if row.IdentityState == domain.Matched {
			owners[int64(row.CustomerID)] = append(owners[int64(row.CustomerID)], i)
		}
	}
	for _, positions := range owners {
		if len(positions) > 1 {
			for _, i := range positions {
				projection.Rows[i].IdentityState = domain.Conflict
				projection.Rows[i].CustomerID = 0
			}
		}
	}
	h := sha256.New()
	sort.Slice(projection.Rows, func(i, j int) bool {
		return string(projection.Rows[i].SubjectDigest[:]) < string(projection.Rows[j].SubjectDigest[:])
	})
	for _, row := range projection.Rows {
		encoded, _ := json.Marshal(row)
		h.Write(encoded)
		projection.Counts.Total++
		switch row.Stage {
		case domain.ActiveUsed:
			projection.Counts.ActiveUsed++
		case domain.ActiveUnused:
			projection.Counts.ActiveUnused++
		default:
			projection.Counts.RegisteredNoActiveMembership++
		}
		switch row.IdentityState {
		case domain.Matched:
			projection.Counts.Matched++
		case domain.Conflict:
			projection.Counts.Conflict++
		default:
			projection.Counts.Unmatched++
		}
	}
	copy(projection.ProjectionDigest[:], h.Sum(nil))
	return projection, nil
}
func (s Service) fail(ctx context.Context, id int64, code string, cause error) error {
	_ = s.UOW.Within(context.WithoutCancel(ctx), func(txCtx context.Context) error { return s.Store.Fail(txCtx, id, code) })
	return fmt.Errorf("%s: %w", code, cause)
}
