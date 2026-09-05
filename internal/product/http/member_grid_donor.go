package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

// donorGridConfig is the frozen dd8 page's persisted view contract. The
// schema deliberately exposes only fields v3 can derive from the Order-owned
// entitlement projection; it never invents customer or learning telemetry.
type donorGridConfig struct {
	SchemaVersion int `json:"schema_version"`
	Filter        struct {
		Logic      string               `json:"logic"`
		Conditions []donorGridCondition `json:"conditions"`
	} `json:"filter"`
	Sorts  []donorGridOrder `json:"sorts"`
	Groups []donorGridOrder `json:"groups"`
}
type donorGridCondition struct {
	Field    string `json:"field"`
	Operator string `json:"operator"`
	Value    any    `json:"value,omitempty"`
}
type donorGridOrder struct {
	Field     string `json:"field"`
	Direction string `json:"direction"`
}

func defaultDonorGridConfig() donorGridConfig {
	var c donorGridConfig
	c.SchemaVersion, c.Filter.Logic = 1, "and"
	c.Filter.Conditions, c.Sorts, c.Groups = []donorGridCondition{}, []donorGridOrder{}, []donorGridOrder{}
	return c
}

func decodeDonorGridConfig(raw json.RawMessage) (donorGridConfig, error) {
	if len(raw) == 0 {
		return defaultDonorGridConfig(), nil
	}
	var c donorGridConfig
	if json.Unmarshal(raw, &c) != nil || c.SchemaVersion != 1 {
		return donorGridConfig{}, errors.New("invalid member-grid config")
	}
	if c.Filter.Logic == "" {
		c.Filter.Logic = "and"
	}
	if c.Filter.Logic != "and" && c.Filter.Logic != "or" || len(c.Filter.Conditions) > 20 || len(c.Sorts) > 1 || len(c.Groups) > 1 {
		return donorGridConfig{}, errors.New("invalid member-grid config")
	}
	for _, item := range c.Sorts {
		if item.Field != "remaining_days" || (item.Direction != "asc" && item.Direction != "desc") {
			return donorGridConfig{}, errors.New("unsupported member-grid sort")
		}
	}
	for _, item := range c.Groups {
		if item.Field != "remaining_days" || (item.Direction != "asc" && item.Direction != "desc") {
			return donorGridConfig{}, errors.New("unsupported member-grid group")
		}
	}
	seen := map[string]bool{}
	for _, item := range c.Filter.Conditions {
		if seen[item.Field] || (item.Field != "remaining_days" && item.Field != "remark") {
			return donorGridConfig{}, errors.New("unsupported member-grid filter")
		}
		seen[item.Field] = true
		if err := validateDonorGridCondition(item); err != nil {
			return donorGridConfig{}, err
		}
	}
	return c, nil
}

func validateDonorGridCondition(item donorGridCondition) error {
	if item.Field == "remark" {
		switch item.Operator {
		case "contains", "not_contains", "equals", "not_equals":
			value, ok := item.Value.(string)
			if !ok || len([]rune(value)) > 200 {
				return errors.New("invalid member-grid text filter")
			}
		case "is_empty", "is_not_empty":
		default:
			return errors.New("invalid member-grid text filter")
		}
		return nil
	}
	switch item.Operator {
	case "equals", "not_equals", "gt", "gte", "lt", "lte", "between":
	default:
		return errors.New("invalid member-grid number filter")
	}
	values := []any{item.Value}
	if item.Operator == "between" {
		var ok bool
		values, ok = item.Value.([]any)
		if !ok || len(values) != 2 {
			return errors.New("invalid member-grid number filter")
		}
	}
	for _, value := range values {
		number, ok := value.(float64)
		if !ok || math.Trunc(number) != number || number < -366000 || number > 366000 {
			return errors.New("invalid member-grid number filter")
		}
	}
	return nil
}

func donorGridOrderQuery(productID int64, c donorGridConfig, cursor string, limit int32) (orderport.ServicePeriodMemberQuery, error) {
	query := orderport.ServicePeriodMemberQuery{ServiceProductID: productID, Cursor: cursor, Limit: limit, FilterLogic: c.Filter.Logic}
	if len(c.Sorts) == 1 {
		query.Sort = "remaining_days_" + c.Sorts[0].Direction
	}
	for _, filter := range c.Filter.Conditions {
		if filter.Field == "remark" {
			value, _ := filter.Value.(string)
			query.Remark = &orderport.MemberGridTextFilter{Operator: filter.Operator, Value: value}
			continue
		}
		values := []int64{}
		if filter.Operator == "between" {
			for _, raw := range filter.Value.([]any) {
				values = append(values, int64(raw.(float64)))
			}
		} else {
			values = append(values, int64(filter.Value.(float64)))
		}
		query.RemainingDays = &orderport.MemberGridNumberFilter{Operator: filter.Operator, Values: values}
	}
	return query, nil
}

func donorGridSchema(editable bool) map[string]any {
	operator := func(id, label, kind string) map[string]any {
		return map[string]any{"id": id, "label": label, "value_kind": kind}
	}
	textOps := []any{operator("contains", "包含", "text"), operator("not_contains", "不包含", "text"), operator("equals", "等于", "scalar"), operator("not_equals", "不等于", "scalar"), operator("is_empty", "为空", "none"), operator("is_not_empty", "不为空", "none")}
	numberOps := []any{operator("equals", "等于", "scalar"), operator("not_equals", "不等于", "scalar"), operator("gt", "大于", "number"), operator("gte", "大于等于", "number"), operator("lt", "小于", "number"), operator("lte", "小于等于", "number"), operator("between", "介于", "range")}
	field := func(id, label, typ, icon string, ops []any, sortable, groupable, writable bool) map[string]any {
		return map[string]any{"id": id, "label": label, "type": typ, "icon": icon, "filter_operators": ops, "sortable": sortable, "groupable": groupable, "editable": writable, "options": []any{}}
	}
	fields := []any{
		field("member", "会员", "text", "text", []any{}, false, false, false),
		field("remaining_days", "剩余有效期", "number", "number", numberOps, true, true, false),
		field("formally_logged_in", "正式登录", "tri_state", "person", []any{}, false, false, false),
		field("token_usage", "token 消耗", "tri_state", "check", []any{}, false, false, false),
		field("learning_plan_progress", "学习计划进度", "progress", "progress", []any{}, false, false, false),
		field("open_count_7d", "近 7 天打开次数", "number", "number", []any{}, false, false, false),
		field("last_open_at", "最后打开时间", "datetime", "datetime", []any{}, false, false, false),
		field("renewal_count", "续费次数", "number", "number", []any{}, false, false, false),
		field("remark", "备注", "text", "text", textOps, false, false, editable),
		field("alliance", "联盟", "text", "text", []any{}, false, false, false),
	}
	return map[string]any{"schema_version": 1, "fields": fields, "limits": map[string]any{"filter_conditions": 20, "sorts": 1, "groups": 1, "page_size": 100}}
}

func donorGridViewResponse(v productport.MemberGridView) map[string]any {
	c, err := decodeDonorGridConfig(v.Config)
	if err != nil {
		c = defaultDonorGridConfig()
	}
	raw, _ := json.Marshal(c)
	return map[string]any{"id": strconvID(v.ID), "view_id": v.ID, "service_product_id": v.ProductID, "name": v.Name, "position": v.Position, "is_default": false, "version": v.Version, "config": json.RawMessage(raw), "created_at": v.CreatedAt.UTC(), "updated_at": v.UpdatedAt.UTC()}
}
func defaultDonorGridView() map[string]any {
	raw, _ := json.Marshal(defaultDonorGridConfig())
	return map[string]any{"id": "default", "name": "默认视图", "position": 0, "is_default": true, "version": 1, "config": json.RawMessage(raw)}
}
func strconvID(id productport.ID) string { return fmt.Sprintf("%d", id) }

func (h *Handler) donorGridRows(ctx context.Context, items []orderport.Entitlement, c donorGridConfig) ([]map[string]any, error) {
	ids := make([]customerdomain.CustomerID, 0, len(items))
	for _, item := range items {
		ids = append(ids, customerdomain.CustomerID(item.CustomerID))
	}
	names, err := h.names.DisplayNames(ctx, ids)
	if err != nil {
		return nil, err
	}
	now := time.Now().UTC()
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		remaining := int(math.Floor(item.EndAt.Sub(now).Hours() / 24))
		name := names[customerdomain.CustomerID(item.CustomerID)]
		if name == "" {
			name = "客户"
		}
		path := []any{}
		if len(c.Groups) == 1 {
			path = append(path, map[string]any{"field": "remaining_days", "value": remaining})
		}
		out = append(out, map[string]any{"record_id": memberGridMemberRef(item.ID), "unionid": memberGridMemberRef(item.ID), "version": item.Version, "values": map[string]any{"member": map[string]any{"primary": name, "secondary": ""}, "remaining_days": remaining, "formally_logged_in": "unmatched", "token_usage": "unmatched", "learning_plan_progress": map[string]any{"state": "unmatched"}, "open_count_7d": nil, "last_open_at": nil, "renewal_count": nil, "renewal_count_unavailable": true, "remark": item.Remark, "alliance": ""}, "group_path": path})
	}
	return out, nil
}

func (h *Handler) queryDonorGrid(ctx context.Context, productID int64, c donorGridConfig, cursor string, limit int32) ([]map[string]any, string, error) {
	if h.members == nil || h.names == nil {
		return nil, "", errors.New("member readers unavailable")
	}
	query, err := donorGridOrderQuery(productID, c, cursor, limit)
	if err != nil {
		return nil, "", err
	}
	page, err := h.members.ListServicePeriodMembers(ctx, query)
	if err != nil {
		return nil, "", err
	}
	rows, err := h.donorGridRows(ctx, page.Items, c)
	return rows, page.NextCursor, err
}

func (h *Handler) publicMemberGridShare(ctx context.Context, token string) (productport.MemberGridShare, bool) {
	if h == nil || h.workspace == nil || strings.TrimSpace(token) == "" {
		return productport.MemberGridShare{}, false
	}
	share, err := h.workspace.ResolveShare(ctx, strings.TrimSpace(token))
	if err != nil || !share.Enabled || share.ProductID < 1 {
		return productport.MemberGridShare{}, false
	}
	return share, true
}

func (h *Handler) publicMemberGridBootstrap(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || r.URL.RawQuery != "" {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
		} else {
			writeError(w, http.StatusBadRequest, "invalid_request")
		}
		return
	}
	share, ok := h.publicMemberGridShare(r.Context(), r.Header.Get("X-AICRM-Grid-Share-Token"))
	if !ok {
		writeError(w, http.StatusGone, "share_gone")
		return
	}
	product, err := h.service.GetServicePeriodProduct(r.Context(), share.ProductID)
	if err != nil {
		writeError(w, http.StatusGone, "share_gone")
		return
	}
	views, err := h.workspace.ListViews(r.Context(), share.ProductID)
	if err != nil {
		resultError(w, err)
		return
	}
	items := []any{defaultDonorGridView()}
	for _, view := range views {
		item := donorGridViewResponse(view)
		delete(item, "config")
		items = append(items, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "service_product_id": share.ProductID, "product": map[string]any{"title": product.Name}, "schema": donorGridSchema(false), "views": items})
}

func (h *Handler) publicMemberGridQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	share, ok := h.publicMemberGridShare(r.Context(), r.Header.Get("X-AICRM-Grid-Share-Token"))
	if !ok {
		writeError(w, http.StatusGone, "share_gone")
		return
	}
	var body struct {
		ViewID string `json:"view_id"`
		Cursor string `json:"cursor"`
		Limit  int32  `json:"limit"`
	}
	if decodeJSON(r, &body) != nil || body.ViewID == "" || body.Limit < 1 || body.Limit > 100 || len(body.Cursor) > 1024 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	config := defaultDonorGridConfig()
	if body.ViewID != "default" {
		id, err := parseID(body.ViewID)
		if err != nil {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		views, err := h.workspace.ListViews(r.Context(), share.ProductID)
		if err != nil {
			resultError(w, err)
			return
		}
		found := false
		for _, view := range views {
			if view.ID == productport.ID(id) {
				config, err = decodeDonorGridConfig(view.Config)
				if err != nil {
					writeError(w, http.StatusServiceUnavailable, "unavailable")
					return
				}
				found = true
				break
			}
		}
		if !found {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
	}
	rows, next, err := h.queryDonorGrid(r.Context(), int64(share.ProductID), config, body.Cursor, body.Limit)
	if err != nil {
		resultError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"rows": rows, "limit": body.Limit, "next_cursor": next, "has_more": next != ""})
}
