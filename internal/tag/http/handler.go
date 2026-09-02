// Package http provides the frozen v2 tag compatibility routes.  It accepts
// only catalog metadata commands; customer tagging and provider calls have no
// route in this module.
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

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	tagapp "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/app"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/tag/domain"
	tagport "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/port"
	tagstore "github.com/qianlan33333-png/AI-CRM-v3/internal/tag/store"
)

type RequestSecurity interface {
	Authenticate(context.Context, *http.Request) (accessdomain.Principal, error)
	AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error)
}
type Handler struct {
	catalog  *tagapp.Service
	sync     *tagapp.SyncService
	gate     tagport.ExecutionGateReader
	security RequestSecurity
}

func NewHandler(catalog *tagapp.Service, sync *tagapp.SyncService, gate tagport.ExecutionGateReader, security RequestSecurity) (*Handler, error) {
	if catalog == nil || sync == nil || gate == nil || security == nil {
		return nil, errors.New("tag HTTP dependencies are required")
	}
	return &Handler{catalog, sync, gate, security}, nil
}
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/wecom/"), "/")
	switch {
	case p == "tags":
		h.tags(w, r, "")
	case strings.HasPrefix(p, "tags/"):
		h.tags(w, r, strings.TrimPrefix(p, "tags/"))
	case p == "tag-groups":
		h.groups(w, r, "")
	case strings.HasPrefix(p, "tag-groups/"):
		h.groups(w, r, strings.TrimPrefix(p, "tag-groups/"))
	default:
		writeError(w, 404, "not_found")
	}
}
func canRead(p accessdomain.Principal) bool {
	if p.InternalID < 1 || (p.Kind != accessdomain.KindAdmin && p.Kind != accessdomain.KindStaff) {
		return false
	}
	for _, r := range p.Roles {
		if r == accessdomain.RoleViewer || r == accessdomain.RoleAdmin || r == accessdomain.RoleSuperAdmin {
			return true
		}
	}
	return false
}
func canWrite(p accessdomain.Principal) bool {
	if !canRead(p) {
		return false
	}
	for _, r := range p.Roles {
		if r == accessdomain.RoleAdmin || r == accessdomain.RoleSuperAdmin {
			return true
		}
	}
	return false
}
func (h *Handler) read(w http.ResponseWriter, r *http.Request) bool {
	p, e := h.security.Authenticate(r.Context(), r)
	if e != nil {
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
	p, e := h.security.Authenticate(r.Context(), r)
	if e != nil {
		writeError(w, 401, "unauthorized")
		return accessdomain.Principal{}, false
	}
	if !canWrite(p) {
		writeError(w, 403, "forbidden")
		return accessdomain.Principal{}, false
	}
	if _, e = h.security.AuthorizeCSRF(r.Context(), r); e != nil {
		writeError(w, 403, "csrf_required")
		return accessdomain.Principal{}, false
	}
	return p, true
}
func decode(r *http.Request, v any) error {
	d := json.NewDecoder(io.LimitReader(r.Body, 32<<10))
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return e
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}
func command(p accessdomain.Principal, key, trace string) domain.Command {
	if key == "" {
		key = compatKey()
	}
	return domain.Command{Actor: p.InternalID, IdempotencyKey: key, TraceID: trace}
}
func compatKey() string {
	var raw [20]byte
	_, _ = rand.Read(raw[:])
	return "server_compat_" + hex.EncodeToString(raw[:])
}
func parseID(raw string) (int64, bool) {
	n, e := strconv.ParseInt(raw, 10, 64)
	return n, e == nil && n > 0 && strconv.FormatInt(n, 10) == raw
}
func (h *Handler) tags(w http.ResponseWriter, r *http.Request, tail string) {
	if tail == "sync" || tail == "sync-due" {
		if r.Method != http.MethodPost {
			method(w, http.MethodPost)
			return
		}
		p, ok := h.mutate(w, r)
		if !ok {
			return
		}
		var body struct {
			IdempotencyKey string `json:"idempotency_key"`
			TraceID        string `json:"trace_id"`
		}
		if decode(r, &body) != nil {
			writeError(w, 400, "invalid_request")
			return
		}
		kind := tagport.SyncManual
		if tail == "sync-due" {
			kind = tagport.SyncDue
		}
		result, e := h.sync.Request(r.Context(), tagport.SyncCommand{Actor: p.InternalID, IdempotencyKey: nonempty(body.IdempotencyKey, compatKey()), TraceID: body.TraceID, Kind: kind})
		if e != nil {
			resultError(w, e)
			return
		}
		writeJSON(w, 202, map[string]any{"ok": true, "accepted": true, "state": "queued", "receipt_id": result.ReceiptID, "event_id": result.EventID, "river_job_id": result.QueueJobID, "effect_id": result.EffectID, "effect_state": result.EffectState, "accept_receipt_id": result.AcceptReceiptID, "queue_receipt_id": result.QueueReceiptID, "provider_call_attempted": false, "provider_success_claimed": false, "real_external_call_executed": false, "sync_executed": false})
		return
	}
	if tail == "live/gate" {
		if r.Method != http.MethodGet {
			method(w, http.MethodGet)
			return
		}
		if !h.read(w, r) {
			return
		}
		gate, e := h.gate.Get(r.Context())
		if e != nil {
			writeError(w, 503, "unavailable")
			return
		}
		writeJSON(w, 200, gate)
		return
	}
	if tail == "" {
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			method(w, http.MethodGet+", "+http.MethodPost)
			return
		}
		if r.Method == http.MethodGet {
			if !h.read(w, r) {
				return
			}
			h.catalogList(w, r)
			return
		}
		p, ok := h.mutate(w, r)
		if !ok {
			return
		}
		var body struct {
			GroupID        int64  `json:"group_id"`
			GroupName      string `json:"group_name"`
			TagName        string `json:"tag_name"`
			IdempotencyKey string `json:"idempotency_key"`
			TraceID        string `json:"trace_id"`
		}
		if decode(r, &body) != nil {
			writeError(w, 400, "invalid_request")
			return
		}
		c := command(p, body.IdempotencyKey, body.TraceID)
		c.GroupID, c.GroupName, c.TagName = body.GroupID, body.GroupName, body.TagName
		v, e := h.catalog.CreateTag(r.Context(), c)
		if e != nil {
			resultError(w, e)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "tag": v, "item": v, "reason": "tag_created", "source_status": "local_catalog", "route_owner": "ai_crm_next", "real_external_call_executed": false})
		return
	}
	id, ok := parseID(tail)
	if !ok {
		writeError(w, 404, "not_found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		if !h.read(w, r) {
			return
		}
		v, e := h.catalog.GetTag(r.Context(), id)
		if e != nil {
			resultError(w, e)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "tag": v, "item": v, "source_status": "local_catalog", "route_owner": "ai_crm_next", "real_external_call_executed": false})
	case http.MethodPatch, http.MethodPut:
		p, ok := h.mutate(w, r)
		if !ok {
			return
		}
		var body struct {
			TagName        string `json:"tag_name"`
			IdempotencyKey string `json:"idempotency_key"`
			TraceID        string `json:"trace_id"`
		}
		if decode(r, &body) != nil {
			writeError(w, 400, "invalid_request")
			return
		}
		c := command(p, body.IdempotencyKey, body.TraceID)
		c.TagID, c.TagName = id, body.TagName
		v, e := h.catalog.UpdateTag(r.Context(), c)
		if e != nil {
			resultError(w, e)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "tag": v, "item": v, "reason": "tag_updated", "source_status": "local_catalog", "route_owner": "ai_crm_next", "real_external_call_executed": false})
	case http.MethodDelete:
		p, ok := h.mutate(w, r)
		if !ok {
			return
		}
		var body struct {
			IdempotencyKey string `json:"idempotency_key"`
			TraceID        string `json:"trace_id"`
		}
		if decode(r, &body) != nil && r.ContentLength > 0 {
			writeError(w, 400, "invalid_request")
			return
		}
		c := command(p, body.IdempotencyKey, body.TraceID)
		c.TagID = id
		v, e := h.catalog.ArchiveTag(r.Context(), c)
		if e != nil {
			resultError(w, e)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "tag": v, "item": v, "reason": "tag_archived", "source_status": "local_catalog", "route_owner": "ai_crm_next", "real_external_call_executed": false})
	default:
		method(w, http.MethodGet+", "+http.MethodPatch+", "+http.MethodPut+", "+http.MethodDelete)
	}
}
func (h *Handler) groups(w http.ResponseWriter, r *http.Request, tail string) {
	if tail == "" {
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			method(w, http.MethodGet+", "+http.MethodPost)
			return
		}
		if r.Method == http.MethodGet {
			if !h.read(w, r) {
				return
			}
			catalog, e := h.catalog.List(r.Context())
			if e != nil {
				resultError(w, e)
				return
			}
			writeJSON(w, 200, map[string]any{"ok": true, "groups": catalog.Groups, "items": catalog.Groups, "count": len(catalog.Groups), "source_status": "local_catalog", "route_owner": "ai_crm_next", "real_external_call_executed": false})
			return
		}
		p, ok := h.mutate(w, r)
		if !ok {
			return
		}
		var body struct {
			GroupName      string `json:"group_name"`
			FirstTagName   string `json:"first_tag_name"`
			IdempotencyKey string `json:"idempotency_key"`
			TraceID        string `json:"trace_id"`
		}
		if decode(r, &body) != nil {
			writeError(w, 400, "invalid_request")
			return
		}
		c := command(p, body.IdempotencyKey, body.TraceID)
		c.GroupName, c.FirstTagName = body.GroupName, body.FirstTagName
		g, t, e := h.catalog.CreateGroup(r.Context(), c)
		if e != nil {
			resultError(w, e)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "group": g, "tag": t, "reason": "group_created", "source_status": "local_catalog", "route_owner": "ai_crm_next", "real_external_call_executed": false})
		return
	}
	id, ok := parseID(tail)
	if !ok {
		writeError(w, 404, "not_found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		if !h.read(w, r) {
			return
		}
		v, e := h.catalog.GetGroup(r.Context(), id)
		if e != nil {
			resultError(w, e)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "group": v, "item": v, "source_status": "local_catalog", "route_owner": "ai_crm_next", "real_external_call_executed": false})
	case http.MethodPatch, http.MethodPut:
		p, ok := h.mutate(w, r)
		if !ok {
			return
		}
		var body struct {
			GroupName      string `json:"group_name"`
			IdempotencyKey string `json:"idempotency_key"`
			TraceID        string `json:"trace_id"`
		}
		if decode(r, &body) != nil {
			writeError(w, 400, "invalid_request")
			return
		}
		c := command(p, body.IdempotencyKey, body.TraceID)
		c.GroupID, c.GroupName = id, body.GroupName
		v, e := h.catalog.UpdateGroup(r.Context(), c)
		if e != nil {
			resultError(w, e)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "group": v, "item": v, "reason": "group_updated", "source_status": "local_catalog", "route_owner": "ai_crm_next", "real_external_call_executed": false})
	case http.MethodDelete:
		p, ok := h.mutate(w, r)
		if !ok {
			return
		}
		var body struct {
			IdempotencyKey string `json:"idempotency_key"`
			TraceID        string `json:"trace_id"`
		}
		if decode(r, &body) != nil && r.ContentLength > 0 {
			writeError(w, 400, "invalid_request")
			return
		}
		c := command(p, body.IdempotencyKey, body.TraceID)
		c.GroupID = id
		v, e := h.catalog.ArchiveGroup(r.Context(), c)
		if e != nil {
			resultError(w, e)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "group": v, "item": v, "reason": "group_archived", "source_status": "local_catalog", "route_owner": "ai_crm_next", "real_external_call_executed": false})
	default:
		method(w, http.MethodGet+", "+http.MethodPatch+", "+http.MethodPut+", "+http.MethodDelete)
	}
}
func (h *Handler) catalogList(w http.ResponseWriter, r *http.Request) {
	c, e := h.catalog.List(r.Context())
	if e != nil {
		resultError(w, e)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "items": c.Tags, "tags": c.Tags, "groups": c.Groups, "count": len(c.Tags), "total_tags": len(c.Tags), "tag_limit": domain.TagLimit, "synced_at": c.SyncedAt, "source_status": "local_catalog", "read_model_status": "ready", "route_owner": "ai_crm_next", "fallback_used": false, "real_external_call_executed": false, "sync_executed": false, "fixture_used": false})
}
func nonempty(v, fallback string) string {
	if strings.TrimSpace(v) != "" {
		return v
	}
	return fallback
}
func method(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeError(w, 405, "method_not_allowed")
}
func resultError(w http.ResponseWriter, e error) {
	switch {
	case errors.Is(e, tagapp.ErrInvalidCommand), errors.Is(e, tagapp.ErrInvalidSync), errors.Is(e, tagstore.ErrInvalid):
		writeError(w, 400, "invalid_request")
	case errors.Is(e, tagapp.ErrNotFound), errors.Is(e, tagstore.ErrNotFound):
		writeError(w, 404, "not_found")
	case errors.Is(e, tagapp.ErrConflict), errors.Is(e, tagapp.ErrSyncConflict), errors.Is(e, tagstore.ErrConflict):
		writeError(w, 409, "conflict")
	case errors.Is(e, tagapp.ErrReferenced):
		writeError(w, 409, "referenced")
	default:
		writeError(w, 503, "unavailable")
	}
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func writeError(w http.ResponseWriter, status int, code string) {
	compat := map[string]string{"invalid_request": "MALFORMED_REQUEST", "not_found": "NOT_FOUND", "conflict": "CONFLICT", "referenced": "CONFLICT", "unauthorized": "UNAUTHORIZED", "forbidden": "FORBIDDEN", "csrf_required": "FORBIDDEN", "unavailable": "DEPENDENCY_UNAVAILABLE", "method_not_allowed": "METHOD_NOT_ALLOWED"}[code]
	if compat == "" {
		compat = "DEPENDENCY_UNAVAILABLE"
	}
	writeJSON(w, status, map[string]any{"ok": false, "code": compat, "error": code})
}
