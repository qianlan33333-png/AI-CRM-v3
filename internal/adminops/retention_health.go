package adminops

import (
	"context"
	opsport "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/port"
	"time"
)

// RetentionHealth evaluates each required policy independently. An old success,
// an unrelated policy or an unfinished batch cannot hide a stopped cleaner.
func (s *RetentionService) RetentionHealth(ctx context.Context, at time.Time) (opsport.CheckObservation, error) {
	o := opsport.CheckObservation{ObservedAt: at, Metrics: map[string]int64{}, Status: "ok", Code: "all_policies_fresh"}
	if !s.enabled {
		o.Status = "uncovered"
		o.Code = "automatic_cleanup_disabled"
		return o, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	rows, e := s.pool.Query(ctx, `SELECT DISTINCT ON(policy) policy,state,started_at,completed_at FROM adminops_retention_runs ORDER BY policy,hour_key DESC,id DESC`)
	if e != nil {
		return o, e
	}
	defer rows.Close()
	type observation struct {
		state string
		start time.Time
		end   *time.Time
	}
	latest := map[string]observation{}
	for rows.Next() {
		var policy string
		var v observation
		if e = rows.Scan(&policy, &v.state, &v.start, &v.end); e != nil {
			return o, e
		}
		latest[policy] = v
	}
	if e = rows.Err(); e != nil {
		return o, e
	}
	for _, p := range s.Policies() {
		if !validRetentionPolicy(p.ID) {
			continue
		}
		o.Metrics["expected_policies"]++
		v, found := latest[p.ID]
		if !found {
			o.Metrics["missing_policies"]++
			continue
		}
		if v.state == "failed" {
			o.Metrics["failed_policies"]++
			continue
		}
		if v.state == "running" {
			if at.Sub(v.start) > 10*time.Minute {
				o.Metrics["stalled_policies"]++
			} else {
				o.Metrics["running_policies"]++
			}
			continue
		}
		if v.end == nil || at.Sub(*v.end) > 75*time.Minute {
			o.Metrics["stale_policies"]++
		} else {
			o.Metrics["fresh_policies"]++
		}
	}
	switch {
	case o.Metrics["failed_policies"] > 0 || o.Metrics["stalled_policies"] > 0:
		o.Status = "warning"
		o.Code = "cleanup_failed_or_stalled"
	case o.Metrics["missing_policies"] > 0:
		o.Status = "unknown"
		o.Code = "cleanup_policy_not_observed"
	case o.Metrics["stale_policies"] > 0:
		o.Status = "stale"
		o.Code = "cleanup_observation_expired"
	case o.Metrics["running_policies"] > 0:
		o.Status = "unknown"
		o.Code = "cleanup_in_progress"
	}
	return o, nil
}
