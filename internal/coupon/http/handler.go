// Package http adapts the frozen coupon-rule browser contract to the v3
// rule-only application.  It deliberately exposes no claim, redemption,
// holder, order, payment, entitlement, or public-link operation.
package http

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	couponapp "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/app"
	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

type RequestSecurity interface {
	Authenticate(context.Context, *http.Request) (accessdomain.Principal, error)
	AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error)
}

type Handler struct {
	rules    couponport.RuleApplication
	options  productport.ProductOptionReader
	security RequestSecurity
}

func NewHandler(rules couponport.RuleApplication, options productport.ProductOptionReader, security RequestSecurity) (*Handler, error) {
	if rules == nil || options == nil || security == nil {
		return nil, errors.New("coupon HTTP dependencies are required")
	}
	return &Handler{rules: rules, options: options, security: security}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	tail := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/coupons"), "/")
	if tail == "product-options" {
		h.productOptions(w, r)
		return
	}
	if tail == "" {
		switch r.Method {
		case http.MethodGet:
			if h.read(w, r) {
				h.list(w, r)
			}
		case http.MethodPost:
			if p, ok := h.mutate(w, r); ok {
				h.upsert(w, r, p, 0)
			}
		default:
			method(w, "GET, POST")
		}
		return
	}
	parts := strings.Split(tail, "/")
	if len(parts) > 2 || len(parts) == 0 {
		writeError(w, 404, "not_found")
		return
	}
	id, ok := parseID(parts[0])
	if !ok {
		writeError(w, 404, "not_found")
		return
	}
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			if h.read(w, r) {
				h.detail(w, r, couponport.ID(id))
			}
		case http.MethodPut:
			if p, ok := h.mutate(w, r); ok {
				h.upsert(w, r, p, couponport.ID(id))
			}
		case http.MethodDelete:
			if p, ok := h.mutate(w, r); ok {
				h.command(w, r, p, couponport.ID(id), "delete")
			}
		default:
			method(w, "GET, PUT, DELETE")
		}
		return
	}
	switch parts[1] {
	case "publish", "stop", "archive", "copy":
		if r.Method != http.MethodPost {
			method(w, "POST")
			return
		}
		p, ok := h.mutate(w, r)
		if !ok {
			return
		}
		h.command(w, r, p, couponport.ID(id), parts[1])
	default:
		writeError(w, 404, "not_found")
	}
}

func (h *Handler) read(w http.ResponseWriter, r *http.Request) bool {
	p, err := h.security.Authenticate(r.Context(), r)
	if err != nil {
		writeError(w, 401, "unauthorized")
		return false
	}
	if !canRead(p) {
		writeError(w, 403, "forbidden")
		return false
	}
	return true
}
func (h *Handler) mutate(w http.ResponseWriter, r *http.Request) (accessdomain.Principal, bool) {
	p, err := h.security.Authenticate(r.Context(), r)
	if err != nil {
		writeError(w, 401, "unauthorized")
		return accessdomain.Principal{}, false
	}
	if !canWrite(p) {
		writeError(w, 403, "forbidden")
		return accessdomain.Principal{}, false
	}
	if _, err = h.security.AuthorizeCSRF(r.Context(), r); err != nil {
		writeError(w, 403, "csrf_required")
		return accessdomain.Principal{}, false
	}
	return p, true
}
func canRead(p accessdomain.Principal) bool {
	if p.InternalID < 1 || (p.Kind != accessdomain.KindAdmin && p.Kind != accessdomain.KindStaff) {
		return false
	}
	for _, role := range p.Roles {
		if role == accessdomain.RoleViewer || role == accessdomain.RoleAdmin || role == accessdomain.RoleSuperAdmin {
			return true
		}
	}
	return false
}
func canWrite(p accessdomain.Principal) bool {
	if !canRead(p) {
		return false
	}
	for _, role := range p.Roles {
		if role == accessdomain.RoleAdmin || role == accessdomain.RoleSuperAdmin {
			return true
		}
	}
	return false
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if !only(q, "limit", "offset", "q", "status") {
		writeError(w, 400, "invalid_request")
		return
	}
	limit, ok := intQuery(q.Get("limit"), couponapp.DefaultLimit, 1, couponapp.MaximumLimit)
	if !ok {
		writeError(w, 400, "invalid_request")
		return
	}
	offset, ok := intQuery(q.Get("offset"), 0, 0, couponapp.MaximumOffset)
	if !ok {
		writeError(w, 400, "invalid_request")
		return
	}
	page, err := h.rules.List(r.Context(), int32(limit), int32(offset), q.Get("q"), q.Get("status"))
	if err != nil {
		resultError(w, err)
		return
	}
	items := couponList(page.Items)
	writeJSON(w, 200, map[string]any{"ok": true, "coupons": items, "items": items, "total": page.Total, "limit": page.Limit, "offset": page.Offset})
}
func (h *Handler) detail(w http.ResponseWriter, r *http.Request, id couponport.ID) {
	c, err := h.rules.Get(r.Context(), id)
	if err != nil {
		resultError(w, err)
		return
	}
	v := legacyCoupon(c)
	writeJSON(w, 200, map[string]any{"ok": true, "coupon": v, "data": map[string]any{"coupon": v}})
}

type upsertRequest struct {
	Name                 string   `json:"name"`
	DiscountAmountTotal  int64    `json:"discount_amount_total"`
	TotalIssueLimit      int64    `json:"total_issue_limit"`
	PerUserIssueLimit    *int64   `json:"per_user_issue_limit"`
	ClaimStartsAt        string   `json:"claim_starts_at"`
	ClaimEndsAt          string   `json:"claim_ends_at"`
	ValidityMode         string   `json:"validity_mode"`
	UseStartsAt          *string  `json:"use_starts_at"`
	UseEndsAt            *string  `json:"use_ends_at"`
	RelativeValidityDays *int32   `json:"relative_validity_days"`
	Instructions         *string  `json:"instructions"`
	TargetRefs           []string `json:"target_refs"`
}

func (h *Handler) upsert(w http.ResponseWriter, r *http.Request, p accessdomain.Principal, id couponport.ID) {
	var req upsertRequest
	if decode(r, &req) != nil {
		writeError(w, 400, "invalid_request")
		return
	}
	key, err := idempotencyKey(r)
	if err != nil {
		writeError(w, 400, "invalid_request")
		return
	}
	start, err := time.Parse(time.RFC3339, req.ClaimStartsAt)
	if err != nil {
		writeError(w, 400, "invalid_request")
		return
	}
	end, err := time.Parse(time.RFC3339, req.ClaimEndsAt)
	if err != nil {
		writeError(w, 400, "invalid_request")
		return
	}
	var useStart, useEnd *time.Time
	if req.UseStartsAt != nil {
		v, e := time.Parse(time.RFC3339, *req.UseStartsAt)
		if e != nil {
			writeError(w, 400, "invalid_request")
			return
		}
		useStart = &v
	}
	if req.UseEndsAt != nil {
		v, e := time.Parse(time.RFC3339, *req.UseEndsAt)
		if e != nil {
			writeError(w, 400, "invalid_request")
			return
		}
		useEnd = &v
	}
	perUser := int64(1)
	if req.PerUserIssueLimit != nil {
		perUser = *req.PerUserIssueLimit
	}
	instructions := ""
	if req.Instructions != nil {
		instructions = *req.Instructions
	}
	cmd := couponport.UpsertCommand{Coupon: couponport.Coupon{ID: id, Name: req.Name, DiscountAmountTotal: req.DiscountAmountTotal, TotalIssueLimit: req.TotalIssueLimit, PerUserIssueLimit: perUser, ClaimStartsAt: start, ClaimEndsAt: end, ValidityMode: couponport.ValidityMode(req.ValidityMode), UseStartsAt: useStart, UseEndsAt: useEnd, RelativeValidityDays: req.RelativeValidityDays, Instructions: instructions, TargetRefs: req.TargetRefs}, Actor: p.InternalID, IdempotencyKey: key}
	var c couponport.Coupon
	if id == 0 {
		c, err = h.rules.Create(r.Context(), cmd)
	} else {
		c, err = h.rules.UpdateDraft(r.Context(), cmd)
	}
	if err != nil {
		resultError(w, err)
		return
	}
	v := legacyCoupon(c)
	if id == 0 {
		writeJSON(w, 200, map[string]any{"ok": true, "coupon": v, "coupon_id": c.ID, "fallback_used": false, "create_replay_safe": true, "real_external_call_executed": false})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "coupon": v, "fallback_used": false, "real_external_call_executed": false})
}
func (h *Handler) command(w http.ResponseWriter, r *http.Request, p accessdomain.Principal, id couponport.ID, operation string) {
	if decodeOptionalJSON(r, &struct{}{}) != nil {
		writeError(w, 400, "invalid_request")
		return
	}
	key, err := idempotencyKey(r)
	if err != nil {
		writeError(w, 400, "invalid_request")
		return
	}
	var c couponport.Coupon
	switch operation {
	case "publish":
		c, err = h.rules.Publish(r.Context(), id, p.InternalID, key)
	case "stop":
		c, err = h.rules.Stop(r.Context(), id, p.InternalID, key)
	case "archive":
		c, err = h.rules.Archive(r.Context(), id, p.InternalID, key)
	case "copy":
		c, err = h.rules.Copy(r.Context(), id, p.InternalID, key)
	case "delete":
		c, err = h.rules.Delete(r.Context(), id, p.InternalID, key)
	}
	if err != nil {
		resultError(w, err)
		return
	}
	v := legacyCoupon(c)
	if operation == "delete" {
		writeJSON(w, 200, map[string]any{"ok": true, "coupon": v})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "coupon": v, "fallback_used": false, "real_external_call_executed": false})
}
func (h *Handler) productOptions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		method(w, "GET")
		return
	}
	if !h.read(w, r) {
		return
	}
	q := r.URL.Query()
	if !only(q, "q", "product_type", "limit", "offset") {
		writeError(w, 400, "invalid_request")
		return
	}
	kind := q.Get("product_type")
	if kind == "" {
		kind = "all"
	}
	if kind != "all" && kind != "standard_product" && kind != "service_period" {
		writeError(w, 400, "invalid_request")
		return
	}
	limit, ok := intQuery(q.Get("limit"), 50, 1, 100)
	if !ok {
		writeError(w, 400, "invalid_request")
		return
	}
	offset, ok := intQuery(q.Get("offset"), 0, 0, couponapp.MaximumOffset)
	if !ok {
		writeError(w, 400, "invalid_request")
		return
	}
	productType := productport.ProductOptionAll
	if kind == "standard_product" {
		productType = productport.ProductOptionStandard
	}
	if kind == "service_period" {
		productType = productport.ProductOptionServicePeriod
	}
	page, err := h.options.ListProductOptions(r.Context(), productport.ProductOptionQuery{Q: q.Get("q"), ProductType: productType, Limit: int32(limit), Offset: int32(offset)})
	if err != nil {
		writeError(w, 503, "unavailable")
		return
	}
	out := make([]any, 0, len(page.Items))
	for _, item := range page.Items {
		if item.ID < 1 || item.Name == "" || item.Currency != "CNY" || item.PriceMinor < 0 {
			writeError(w, 503, "unavailable")
			return
		}
		prefix := "standard_product"
		if item.ProductType == productport.ProductOptionServicePeriod {
			prefix = "service_period"
		}
		if item.ProductType != productport.ProductOptionStandard && item.ProductType != productport.ProductOptionServicePeriod {
			writeError(w, 503, "unavailable")
			return
		}
		out = append(out, map[string]any{"id": item.ID, "target_ref": prefix + ":" + strconv.FormatInt(int64(item.ID), 10), "name": item.Name, "price_minor": item.PriceMinor, "currency": "CNY"})
	}
	if page.Total < 0 || page.Limit != int32(limit) || page.Offset != int32(offset) {
		writeError(w, 503, "unavailable")
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "items": out, "total": page.Total, "limit": page.Limit, "offset": page.Offset})
}

func legacyCoupon(c couponport.Coupon) map[string]any {
	return map[string]any{"id": c.ID, "resource_id": c.ID, "name": c.Name, "discount_amount_total": c.DiscountAmountTotal, "currency": "CNY", "status": c.Status, "availability_status": c.AvailabilityStatus, "total_issue_limit": c.TotalIssueLimit, "per_user_issue_limit": c.PerUserIssueLimit, "issued_count": c.IssuedCount, "claim_starts_at": c.ClaimStartsAt.Format(time.RFC3339), "claim_ends_at": c.ClaimEndsAt.Format(time.RFC3339), "validity_mode": c.ValidityMode, "use_starts_at": nullableTime(c.UseStartsAt), "use_ends_at": nullableTime(c.UseEndsAt), "relative_validity_days": c.RelativeValidityDays, "instructions": c.Instructions, "target_refs": c.TargetRefs, "created_by": c.CreatedBy, "updated_by": c.UpdatedBy, "version": c.Version, "created_at": c.CreatedAt.Format(time.RFC3339), "updated_at": c.UpdatedAt.Format(time.RFC3339)}
}
func couponList(items []couponport.Coupon) []any {
	out := make([]any, 0, len(items))
	for _, c := range items {
		out = append(out, legacyCoupon(c))
	}
	return out
}
func nullableTime(v *time.Time) any {
	if v == nil {
		return nil
	}
	return v.Format(time.RFC3339)
}
func parseID(raw string) (int64, bool) {
	n, err := strconv.ParseInt(raw, 10, 64)
	return n, err == nil && n > 0 && strconv.FormatInt(n, 10) == raw
}
func intQuery(raw string, fallback, minimum, maximum int32) (int32, bool) {
	if raw == "" {
		return fallback, true
	}
	n, err := strconv.ParseInt(raw, 10, 32)
	return int32(n), err == nil && n >= int64(minimum) && n <= int64(maximum) && strconv.FormatInt(n, 10) == raw
}
func only(q map[string][]string, keys ...string) bool {
	allowed := map[string]bool{}
	for _, k := range keys {
		allowed[k] = true
	}
	for k, v := range q {
		if !allowed[k] || len(v) != 1 {
			return false
		}
	}
	return true
}
func decode(r *http.Request, target any) error {
	d := json.NewDecoder(io.LimitReader(r.Body, 32<<10))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}
func decodeOptionalJSON(r *http.Request, target any) error {
	d := json.NewDecoder(io.LimitReader(r.Body, 32<<10))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}
func idempotencyKey(r *http.Request) (string, error) {
	values := r.Header.Values("Idempotency-Key")
	if len(values) > 1 {
		return "", errors.New("duplicate idempotency key")
	}
	if len(values) == 1 {
		if strings.TrimSpace(values[0]) != values[0] || len(values[0]) < 16 || len(values[0]) > 128 {
			return "", errors.New("invalid idempotency key")
		}
		return values[0], nil
	}
	var raw [20]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "server_compat_" + hex.EncodeToString(raw[:]), nil
}
func resultError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, couponapp.ErrNotFound):
		writeError(w, 404, "not_found")
	case errors.Is(err, couponapp.ErrConflict), errors.Is(err, couponapp.ErrRulesFrozen):
		writeError(w, 409, "conflict")
	case errors.Is(err, couponapp.ErrInvalidCoupon), errors.Is(err, couponapp.ErrInvalidTarget):
		writeError(w, 400, "invalid_request")
	default:
		writeError(w, 503, "unavailable")
	}
}
func method(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeError(w, 405, "method_not_allowed")
}
func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]any{"ok": false, "error": code, "code": code, "message": code})
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
