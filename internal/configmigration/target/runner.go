package target

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/configmigration/source"
	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

var (
	ErrInvalid = errors.New("invalid config definition import")
	ErrDrift   = errors.New("config definition import drift")
)

type Runner struct {
	UOW        platformport.UnitOfWork
	Products   productport.DefinitionImporter
	Coupons    couponport.DefinitionImporter
	GroupOps   groupopsport.DefinitionImporter
	Automation automationport.DefinitionImporter
}
type Result struct {
	BatchID    int64 `json:"batch_id"`
	NoOp       bool  `json:"no_op"`
	Products   int   `json:"products"`
	Coupons    int   `json:"coupons"`
	GroupOps   int   `json:"groupops"`
	Automation int   `json:"automation"`
}

func (r Runner) Apply(ctx context.Context, snap source.Snapshot, digest [32]byte, actor int64) (out Result, err error) {
	if r.UOW == nil || r.Products == nil || r.Coupons == nil || r.GroupOps == nil || r.Automation == nil || actor < 1 || snap.Validate() != nil || source.ValidateExpectedBaseline(snap) != nil {
		return out, ErrInvalid
	}
	manifest, err := json.Marshal(snap.Manifest)
	if err != nil {
		return out, ErrInvalid
	}
	err = r.UOW.Within(ctx, func(tx context.Context) error {
		t, e := platformpostgres.RequireTransaction(tx)
		if e != nil {
			return e
		}
		var active bool
		if e = t.QueryRow(tx, `SELECT EXISTS(SELECT 1 FROM admin_users WHERE id=$1 AND is_active)`, actor).Scan(&active); e != nil || !active {
			return ErrInvalid
		}
		var prior []byte
		e = t.QueryRow(tx, `SELECT id,snapshot_digest FROM config_definition_import_batches WHERE source_system=$1 AND batch_key=$2 FOR UPDATE`, snap.Manifest.SourceSystem, snap.Manifest.SourceRevision).Scan(&out.BatchID, &prior)
		if e == nil {
			if len(prior) != 32 || string(prior) != string(digest[:]) {
				return ErrDrift
			}
			out.NoOp = true
			return nil
		}
		if !errors.Is(e, pgx.ErrNoRows) {
			return e
		}
		e = t.QueryRow(tx, `INSERT INTO config_definition_import_batches(source_system,batch_key,snapshot_digest,actor_admin_user_id,status,manifest) VALUES($1,$2,$3,$4,'applying',$5::jsonb) RETURNING id`, snap.Manifest.SourceSystem, snap.Manifest.SourceRevision, digest[:], actor, manifest).Scan(&out.BatchID)
		if e != nil {
			return e
		}
		service := map[int64]source.ServicePeriod{}
		for _, x := range snap.ServicePeriods {
			service[x.TradeProductID] = x
		}
		productIDs := map[int64]productport.ID{}
		for _, x := range snap.Products {
			status := "disabled"
			var servicePeriodDurationDays int32
			if x.Status == "active" {
				status = "enabled"
			}
			if sp, ok := service[x.ID]; ok {
				status = "service_period_" + status
				servicePeriodDurationDays = sp.DurationDays
			}
			// CanonicalLegacyAdminProjection supplies the safe empty/null defaults
			// for every omitted media, tag, lead and completion field.
			projection, _ := json.Marshal(map[string]any{"schema_version": 1, "status": status, "enabled": x.Enabled, "buy_button_text": x.BuyButtonText, "require_mobile": x.RequireMobile, "lead_qr_title": x.LeadQRTitle, "lead_qr_subtitle": x.LeadQRSubtitle})
			p, e := r.Products.ImportDefinition(tx, productport.DefinitionImport{ProductCode: x.ProductCode, Name: x.Name, Description: x.Description, PriceMinor: x.PriceMinor, Currency: x.Currency, LegacyAdminProjection: projection, ServicePeriodDurationDays: servicePeriodDurationDays, Actor: actor, CreatedAt: x.CreatedAt, UpdatedAt: x.UpdatedAt})
			if e != nil {
				return e
			}
			productIDs[x.ID] = p.ID
			if e = mapRow(tx, out.BatchID, snap.Manifest.SourceSystem, "product", "wechat_pay_products", x.ID, x, int64(p.ID), "products", nil); e != nil {
				return e
			}
			if sp, ok := service[x.ID]; ok {
				if e = mapRow(tx, out.BatchID, snap.Manifest.SourceSystem, "product", "service_period_products", sp.ID, sp, int64(p.ID), "products", nil); e != nil {
					return e
				}
			}
			out.Products++
		}
		bindings := map[int64][]source.CouponBinding{}
		for _, x := range snap.CouponBindings {
			bindings[x.CouponID] = append(bindings[x.CouponID], x)
		}
		for _, x := range snap.Coupons {
			refs := []string{}
			for _, b := range bindings[x.ID] {
				kind := "standard_product"
				if _, ok := service[b.TradeProductID]; ok {
					kind = "service_period"
				}
				refs = append(refs, fmt.Sprintf("%s:%d", kind, productIDs[b.TradeProductID]))
			}
			sort.Strings(refs)
			c, e := r.Coupons.ImportDefinition(tx, couponport.DefinitionImport{Coupon: couponport.Coupon{Name: x.Name, DiscountAmountTotal: x.DiscountAmountTotal, Currency: x.Currency, Status: x.Status, TotalIssueLimit: x.TotalIssueLimit, PerUserIssueLimit: x.PerUserIssueLimit, ClaimStartsAt: x.ClaimStartsAt, ClaimEndsAt: x.ClaimEndsAt, ValidityMode: couponport.ValidityMode(x.ValidityMode), UseStartsAt: x.UseStartsAt, UseEndsAt: x.UseEndsAt, RelativeValidityDays: x.RelativeValidityDays, Instructions: x.Instructions, TargetRefs: refs}, Actor: actor, CreatedAt: x.CreatedAt, UpdatedAt: x.UpdatedAt})
			if e != nil {
				return e
			}
			if e = mapRow(tx, out.BatchID, snap.Manifest.SourceSystem, "coupon", "commerce_coupons", x.ID, x, int64(c.ID), "coupon_rules", sourceActors(x.SourceCreatedBy, x.SourceUpdatedBy)); e != nil {
				return e
			}
			for _, b := range bindings[x.ID] {
				if e = mapRow(tx, out.BatchID, snap.Manifest.SourceSystem, "coupon", "commerce_coupon_product_bindings", b.ID, b, int64(c.ID), "coupon_rules", nil); e != nil {
					return e
				}
			}
			out.Coupons++
		}
		nodes := map[int64][]source.GroupNode{}
		assets := map[int64][]source.GroupAsset{}
		for _, x := range snap.GroupNodes {
			nodes[x.PlanID] = append(nodes[x.PlanID], x)
		}
		for _, x := range snap.GroupAssets {
			assets[x.PlanID] = append(assets[x.PlanID], x)
		}
		for _, x := range snap.GroupPlans {
			ns := nodes[x.ID]
			sort.Slice(ns, func(i, j int) bool { return ns[i].SortOrder < ns[j].SortOrder })
			legacyPlan, _ := json.Marshal(map[string]any{"plan_code": x.PlanCode, "plan_type": x.PlanType, "source_status": x.Status, "description": x.Description})
			def := groupopsport.DefinitionImport{Name: x.Name, Status: groupopsport.PlanPaused, LegacyDefinition: legacyPlan, LegacyNodeDefinitions: map[int32]json.RawMessage{}, Actor: actor, CreatedAt: x.CreatedAt, UpdatedAt: x.UpdatedAt}
			for i, n := range ns {
				position := int32(i + 1)
				def.Nodes = append(def.Nodes, groupopsport.Node{Position: position, Kind: groupopsport.NodeMessage, MessageText: n.TextContent, MaterialPlan: groupopsport.MaterialPlan{References: []groupopsport.MaterialReference{}}})
				legacyNode, _ := json.Marshal(map[string]any{"day_index": n.DayIndex, "trigger_time_label": n.TriggerTimeLabel, "action_title": n.ActionTitle, "source_sort_order": n.SortOrder})
				def.LegacyNodeDefinitions[position] = legacyNode
			}
			for _, a := range assets[x.ID] {
				def.GroupAssets = append(def.GroupAssets, groupopsport.GroupAsset{AssetRef: a.ChatID})
			}
			p, e := r.GroupOps.ImportDefinition(tx, def)
			if e != nil {
				return e
			}
			if e = mapRow(tx, out.BatchID, snap.Manifest.SourceSystem, "groupops", "automation_group_ops_plans", x.ID, x, p.ID, "group_ops_plans", sourceActors(x.SourceCreatedBy, x.SourceUpdatedBy)); e != nil {
				return e
			}
			for _, n := range ns {
				if e = mapRow(tx, out.BatchID, snap.Manifest.SourceSystem, "groupops", "automation_group_ops_plan_nodes", n.ID, n, p.ID, "group_ops_plans", nil); e != nil {
					return e
				}
			}
			for _, a := range assets[x.ID] {
				if e = mapRow(tx, out.BatchID, snap.Manifest.SourceSystem, "groupops", "automation_group_ops_plan_groups", a.ID, a, p.ID, "group_ops_plans", nil); e != nil {
					return e
				}
			}
			out.GroupOps++
		}
		for _, x := range snap.Agents {
			st := automationport.AgentStatusPaused
			if x.Status == "archived" {
				st = automationport.AgentStatusArchived
			}
			a, e := r.Automation.ImportDefinition(tx, automationport.DefinitionImport{Agent: automationport.Agent{AgentCode: x.AgentCode, AgentName: x.AgentName, AutomationType: automationport.AutomationType(x.AutomationType), Status: st, DraftRolePrompt: x.DraftRolePrompt, DraftTaskPrompt: x.DraftTaskPrompt, PublishedRolePrompt: x.PublishedRolePrompt, PublishedTaskPrompt: x.PublishedTaskPrompt, DraftVersion: x.DraftVersion, PublishedVersion: x.PublishedVersion, FixedContentPackage: automationport.FixedContentPackage{ContentText: x.FixedContentText}, LegacyConfiguration: json.RawMessage(`{"source":"v2_config_definition"}`)}, Actor: actor, CreatedAt: x.CreatedAt, UpdatedAt: x.UpdatedAt})
			if e != nil {
				return e
			}
			if e = mapRow(tx, out.BatchID, snap.Manifest.SourceSystem, "automation", "automation_agent_runtime_config", x.ID, x, int64(a.ID), "automation_agents", nil); e != nil {
				return e
			}
			out.Automation++
		}
		counts, _ := json.Marshal(snap.Summary())
		_, e = t.Exec(tx, `UPDATE config_definition_import_batches SET status='applied',imported_counts=$2::jsonb,applied_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1 AND status='applying'`, out.BatchID, counts)
		return e
	})
	return out, err
}
func mapRow(ctx context.Context, batch int64, system, domain, kind string, id int64, row any, target int64, table string, actorLabels map[string]string) error {
	t, e := platformpostgres.RequireTransaction(ctx)
	if e != nil {
		return e
	}
	raw, e := json.Marshal(row)
	if e != nil {
		return e
	}
	d := sha256.Sum256(raw)
	if actorLabels == nil {
		actorLabels = map[string]string{}
	}
	actors, e := json.Marshal(actorLabels)
	if e != nil {
		return e
	}
	_, e = t.Exec(ctx, `INSERT INTO config_definition_import_source_maps(batch_id,source_system,domain,source_kind,source_key,source_digest,source_actor_labels,target_table,target_id) VALUES($1,$2,$3,$4,$5,$6,$7::jsonb,$8,$9)`, batch, system, domain, kind, fmt.Sprint(id), d[:], actors, table, target)
	return e
}

func sourceActors(createdBy, updatedBy string) map[string]string {
	actors := map[string]string{}
	if createdBy != "" {
		actors["created_by"] = createdBy
	}
	if updatedBy != "" {
		actors["updated_by"] = updatedBy
	}
	return actors
}
func DigestHex(d [32]byte) string { return hex.EncodeToString(d[:]) }

func (r Runner) Verify(ctx context.Context, snap source.Snapshot, digest [32]byte) (out Result, err error) {
	if r.UOW == nil || snap.Validate() != nil || source.ValidateExpectedBaseline(snap) != nil {
		return out, ErrInvalid
	}
	err = r.UOW.Within(ctx, func(tx context.Context) error {
		t, e := platformpostgres.RequireTransaction(tx)
		if e != nil {
			return e
		}
		var status string
		if e = t.QueryRow(tx, `SELECT id,status FROM config_definition_import_batches WHERE source_system=$1 AND batch_key=$2 AND snapshot_digest=$3 FOR UPDATE`, snap.Manifest.SourceSystem, snap.Manifest.SourceRevision, digest[:]).Scan(&out.BatchID, &status); e != nil {
			return e
		}
		if status != "applied" && status != "verified" {
			return ErrInvalid
		}
		checks := []struct {
			kind, table string
			want        int
		}{
			{"wechat_pay_products", "products", snap.Manifest.Counts["products"]}, {"service_period_products", "products", snap.Manifest.Counts["service_periods"]},
			{"commerce_coupons", "coupon_rules", snap.Manifest.Counts["coupons"]}, {"commerce_coupon_product_bindings", "coupon_rules", snap.Manifest.Counts["coupon_bindings"]},
			{"automation_group_ops_plans", "group_ops_plans", snap.Manifest.Counts["group_plans"]}, {"automation_group_ops_plan_nodes", "group_ops_plans", snap.Manifest.Counts["group_nodes"]}, {"automation_group_ops_plan_groups", "group_ops_plans", snap.Manifest.Counts["group_assets"]}, {"automation_agent_runtime_config", "automation_agents", snap.Manifest.Counts["agents"]},
		}
		for _, check := range checks {
			var n int
			if e = t.QueryRow(tx, `SELECT count(*) FROM config_definition_import_source_maps WHERE batch_id=$1 AND source_kind=$2 AND target_table=$3`, out.BatchID, check.kind, check.table).Scan(&n); e != nil || n != check.want {
				return ErrInvalid
			}
		}
		var unsafe int
		e = t.QueryRow(tx, `SELECT count(*) FROM config_definition_import_source_maps m WHERE m.batch_id=$1 AND ((m.target_table='products' AND NOT EXISTS (SELECT 1 FROM products p WHERE p.id=m.target_id AND p.images='[]'::jsonb AND NOT EXISTS (SELECT 1 FROM product_external_push_configurations x WHERE x.product_id=p.id))) OR (m.target_table='coupon_rules' AND NOT EXISTS (SELECT 1 FROM coupon_rules c WHERE c.id=m.target_id AND c.issued_count=0)) OR (m.target_table='group_ops_plans' AND NOT EXISTS (SELECT 1 FROM group_ops_plans g WHERE g.id=m.target_id AND g.status='paused')) OR (m.target_table='automation_agents' AND NOT EXISTS (SELECT 1 FROM automation_agents a WHERE a.id=m.target_id AND a.execution_enabled=FALSE)) OR (m.source_kind='service_period_products' AND NOT EXISTS (SELECT 1 FROM product_imported_service_period_definitions s WHERE s.product_id=m.target_id AND s.duration_days > 0)))`, out.BatchID).Scan(&unsafe)
		if e != nil || unsafe != 0 {
			return ErrInvalid
		}
		if status == "applied" {
			tag, e := t.Exec(tx, `UPDATE config_definition_import_batches SET status='verified',verified_at=clock_timestamp(),updated_at=clock_timestamp() WHERE id=$1 AND status='applied'`, out.BatchID)
			if e != nil || tag.RowsAffected() != 1 {
				return ErrInvalid
			}
		}
		out.Products = snap.Manifest.Counts["products"]
		out.Coupons = snap.Manifest.Counts["coupons"]
		out.GroupOps = snap.Manifest.Counts["group_plans"]
		out.Automation = snap.Manifest.Counts["agents"]
		return nil
	})
	return out, err
}

func (r Runner) Preflight(ctx context.Context, snap source.Snapshot, digest [32]byte, actor int64) error {
	if r.UOW == nil || actor < 1 || snap.Validate() != nil || source.ValidateExpectedBaseline(snap) != nil {
		return ErrInvalid
	}
	return r.UOW.Within(ctx, func(tx context.Context) error {
		t, err := platformpostgres.RequireTransaction(tx)
		if err != nil {
			return err
		}
		var active bool
		if err = t.QueryRow(tx, `SELECT EXISTS(SELECT 1 FROM admin_users WHERE id=$1 AND is_active)`, actor).Scan(&active); err != nil || !active {
			return ErrInvalid
		}
		var prior []byte
		err = t.QueryRow(tx, `SELECT snapshot_digest FROM config_definition_import_batches WHERE source_system=$1 AND batch_key=$2`, snap.Manifest.SourceSystem, snap.Manifest.SourceRevision).Scan(&prior)
		if err == nil && (len(prior) != 32 || string(prior) != string(digest[:])) {
			return ErrDrift
		}
		if err == nil {
			return nil
		}
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		codes := make([]string, len(snap.Products))
		for i := range snap.Products {
			codes[i] = snap.Products[i].ProductCode
		}
		var n int
		if err = t.QueryRow(tx, `SELECT count(*) FROM products WHERE product_code=ANY($1)`, codes).Scan(&n); err != nil || n != 0 {
			return ErrInvalid
		}
		agents := make([]string, len(snap.Agents))
		for i := range snap.Agents {
			agents[i] = snap.Agents[i].AgentCode
		}
		if err = t.QueryRow(tx, `SELECT count(*) FROM automation_agents WHERE agent_code=ANY($1)`, agents).Scan(&n); err != nil || n != 0 {
			return ErrInvalid
		}
		return nil
	})
}
