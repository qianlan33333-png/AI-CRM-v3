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

// decodeOptionalJSON permits the legacy DELETE empty-body form, but does not
// turn an unknown-length/chunked malformed body into an implicit empty one.
func decodeOptionalJSON(r *http.Request, v any) error {
	d := json.NewDecoder(io.LimitReader(r.Body, 32<<10))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
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

type writeMetadata struct {
	// Actor exists in the frozen DTO but is deliberately ignored: only the
	// authenticated v3 principal can become the audit actor.
	Actor          json.RawMessage `json:"actor"`
	IdempotencyKey string          `json:"idempotency_key"`
	TraceID        string          `json:"trace_id"`
	DryRun         bool            `json:"dry_run"`
}

func command(p accessdomain.Principal, key, trace string) domain.Command {
	return domain.Command{Actor: p.InternalID, IdempotencyKey: key, TraceID: trace}
}

func idempotencyKey(r *http.Request, body string) (string, error) {
	if strings.TrimSpace(body) != body {
		return "", errors.New("idempotency key has surrounding whitespace")
	}
	values := r.Header.Values("Idempotency-Key")
	if len(values) > 1 {
		return "", errors.New("duplicate idempotency key")
	}
	header := ""
	if len(values) == 1 {
		header = values[0]
		if strings.TrimSpace(header) != header {
			return "", errors.New("idempotency key has surrounding whitespace")
		}
	}
	if body != "" && header != "" && body != header {
		return "", errors.New("idempotency key mismatch")
	}
	if body != "" {
		return body, nil
	}
	if header != "" {
		return header, nil
	}
	return compatKey(), nil
}

func opaqueRequestID() string {
	return "tagreq_" + strings.TrimPrefix(compatKey(), "server_compat_")
}

func mutationEnvelope(reason string, dryRun bool) map[string]any {
	return map[string]any{
		"ok":                          true,
		"reason":                      reason,
		"source_status":               "local_catalog",
		"route_owner":                 "ai_crm_next",
		"fallback_used":               false,
		"real_external_call_executed": false,
		"sync_executed":               false,
		"fixture_used":                false,
		"dry_run":                     dryRun,
	}
}

func validatedMutationEnvelope(operation string) map[string]any {
	return mutationEnvelope(operation+"_validated", true)
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
	if tail == "sync-status" {
		if r.Method != http.MethodGet {
			method(w, http.MethodGet)
			return
		}
		if !h.read(w, r) {
			return
		}
		status, err := h.sync.Status(r.Context())
		if err != nil {
			resultError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "sync": status})
		return
	}
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
		key, e := idempotencyKey(r, body.IdempotencyKey)
		if e != nil {
			writeError(w, 400, "invalid_request")
			return
		}
		result, e := h.sync.Request(r.Context(), tagport.SyncCommand{Actor: p.InternalID, IdempotencyKey: key, TraceID: body.TraceID, Kind: kind})
		if e != nil {
			resultError(w, e)
			return
		}
		writeJSON(w, 202, map[string]any{"ok": true, "accepted": true, "state": "queued", "receipt_id": result.ReceiptID, "event_id": result.EventID, "river_job_id": result.QueueJobID, "effect_river_job_id": result.QueueJobID, "effect_id": result.EffectID, "effect_state": result.EffectState, "accept_receipt_id": result.AcceptReceiptID, "queue_receipt_id": result.QueueReceiptID, "provider_call_attempted": false, "provider_success_claimed": false, "real_external_call_executed": false, "sync_executed": false})
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
			GroupID   int64  `json:"group_id"`
			GroupName string `json:"group_name"`
			TagName   string `json:"tag_name"`
			writeMetadata
		}
		if decode(r, &body) != nil {
			writeError(w, 400, "invalid_request")
			return
		}
		key, e := idempotencyKey(r, body.IdempotencyKey)
		if e != nil {
			writeError(w, 400, "invalid_request")
			return
		}
		c := command(p, key, body.TraceID)
		c.GroupID, c.GroupName, c.TagName = body.GroupID, body.GroupName, body.TagName
		if body.DryRun {
			if c.GroupID < 1 || !domain.ValidCommand(c, c.GroupName, c.TagName) {
				writeError(w, 400, "invalid_request")
				return
			}
			writeJSON(w, 200, validatedMutationEnvelope("tag_create"))
			return
		}
		v, e := h.catalog.CreateTag(r.Context(), c)
		if e != nil {
			resultError(w, e)
			return
		}
		response := mutationEnvelope("tag_created", false)
		response["tag"] = legacyTag(v)
		writeJSON(w, 200, response)
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
		writeJSON(w, 200, map[string]any{"ok": true, "tag": legacyTag(v), "source_status": "local_catalog", "real_external_call_executed": false, "sync_executed": false})
	case http.MethodPatch, http.MethodPut:
		p, ok := h.mutate(w, r)
		if !ok {
			return
		}
		var body struct {
			TagName string `json:"tag_name"`
			writeMetadata
		}
		if decode(r, &body) != nil {
			writeError(w, 400, "invalid_request")
			return
		}
		key, e := idempotencyKey(r, body.IdempotencyKey)
		if e != nil {
			writeError(w, 400, "invalid_request")
			return
		}
		c := command(p, key, body.TraceID)
		c.TagID, c.TagName = id, body.TagName
		if body.DryRun {
			if !domain.ValidCommand(c, c.TagName) {
				writeError(w, 400, "invalid_request")
				return
			}
			if r.Method == http.MethodPut {
				w.WriteHeader(http.StatusOK)
				return
			}
			writeJSON(w, 200, validatedMutationEnvelope("tag_update"))
			return
		}
		v, e := h.catalog.UpdateTag(r.Context(), c)
		if e != nil {
			resultError(w, e)
			return
		}
		if r.Method == http.MethodPut {
			w.WriteHeader(http.StatusOK)
			return
		}
		response := mutationEnvelope("tag_updated", false)
		response["tag"] = legacyTag(v)
		writeJSON(w, 200, response)
	case http.MethodDelete:
		p, ok := h.mutate(w, r)
		if !ok {
			return
		}
		var body struct {
			writeMetadata
		}
		if decodeOptionalJSON(r, &body) != nil {
			writeError(w, 400, "invalid_request")
			return
		}
		key, e := idempotencyKey(r, body.IdempotencyKey)
		if e != nil {
			writeError(w, 400, "invalid_request")
			return
		}
		c := command(p, key, body.TraceID)
		c.TagID = id
		if body.DryRun {
			if !domain.ValidCommand(c) {
				writeError(w, 400, "invalid_request")
				return
			}
			writeJSON(w, 200, validatedMutationEnvelope("tag_archive"))
			return
		}
		v, e := h.catalog.ArchiveTag(r.Context(), c)
		if e != nil {
			resultError(w, e)
			return
		}
		response := mutationEnvelope("tag_archived", false)
		response["tag"] = legacyTag(v)
		writeJSON(w, 200, response)
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
			groups := legacyGroups(catalog.Groups, catalog.Tags)
			writeJSON(w, 200, map[string]any{"ok": true, "groups": groups, "items": groups, "count": len(groups), "source_status": "local_catalog", "real_external_call_executed": false, "sync_executed": false})
			return
		}
		p, ok := h.mutate(w, r)
		if !ok {
			return
		}
		var body struct {
			GroupName    string `json:"group_name"`
			FirstTagName string `json:"first_tag_name"`
			writeMetadata
		}
		if decode(r, &body) != nil {
			writeError(w, 400, "invalid_request")
			return
		}
		key, e := idempotencyKey(r, body.IdempotencyKey)
		if e != nil {
			writeError(w, 400, "invalid_request")
			return
		}
		c := command(p, key, body.TraceID)
		c.GroupName, c.FirstTagName = body.GroupName, body.FirstTagName
		if body.DryRun {
			if !domain.ValidCommand(c, c.GroupName, c.FirstTagName) {
				writeError(w, 400, "invalid_request")
				return
			}
			writeJSON(w, 200, validatedMutationEnvelope("group_create"))
			return
		}
		g, t, e := h.catalog.CreateGroup(r.Context(), c)
		if e != nil {
			resultError(w, e)
			return
		}
		response := mutationEnvelope("group_created", false)
		response["group"], response["tag"] = legacyMutationGroup(g), legacyTag(t)
		writeJSON(w, 200, response)
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
		catalog, e := h.catalog.List(r.Context())
		if e != nil {
			resultError(w, e)
			return
		}
		var group map[string]any
		for _, item := range legacyGroups(catalog.Groups, catalog.Tags) {
			if item["group_id"] == id {
				group = item
				break
			}
		}
		if group == nil {
			writeError(w, 404, "not_found")
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "group": group, "source_status": "local_catalog", "real_external_call_executed": false, "sync_executed": false})
	case http.MethodPatch, http.MethodPut:
		p, ok := h.mutate(w, r)
		if !ok {
			return
		}
		var body struct {
			GroupName string `json:"group_name"`
			writeMetadata
		}
		if decode(r, &body) != nil {
			writeError(w, 400, "invalid_request")
			return
		}
		key, e := idempotencyKey(r, body.IdempotencyKey)
		if e != nil {
			writeError(w, 400, "invalid_request")
			return
		}
		c := command(p, key, body.TraceID)
		c.GroupID, c.GroupName = id, body.GroupName
		if body.DryRun {
			if !domain.ValidCommand(c, c.GroupName) {
				writeError(w, 400, "invalid_request")
				return
			}
			if r.Method == http.MethodPut {
				w.WriteHeader(http.StatusOK)
				return
			}
			writeJSON(w, 200, validatedMutationEnvelope("group_update"))
			return
		}
		v, e := h.catalog.UpdateGroup(r.Context(), c)
		if e != nil {
			resultError(w, e)
			return
		}
		if r.Method == http.MethodPut {
			w.WriteHeader(http.StatusOK)
			return
		}
		response := mutationEnvelope("group_updated", false)
		response["group"] = legacyMutationGroup(v)
		writeJSON(w, 200, response)
	case http.MethodDelete:
		p, ok := h.mutate(w, r)
		if !ok {
			return
		}
		var body struct {
			writeMetadata
		}
		if decodeOptionalJSON(r, &body) != nil {
			writeError(w, 400, "invalid_request")
			return
		}
		key, e := idempotencyKey(r, body.IdempotencyKey)
		if e != nil {
			writeError(w, 400, "invalid_request")
			return
		}
		c := command(p, key, body.TraceID)
		c.GroupID = id
		if body.DryRun {
			if !domain.ValidCommand(c) {
				writeError(w, 400, "invalid_request")
				return
			}
			writeJSON(w, 200, validatedMutationEnvelope("group_archive"))
			return
		}
		v, e := h.catalog.ArchiveGroup(r.Context(), c)
		if e != nil {
			resultError(w, e)
			return
		}
		response := mutationEnvelope("group_archived", false)
		response["group"] = legacyMutationGroup(v)
		writeJSON(w, 200, response)
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
	tags := legacyTags(c.Tags)
	writeJSON(w, 200, map[string]any{"ok": true, "items": tags, "tags": tags, "groups": legacyGroups(c.Groups, c.Tags), "count": len(tags), "total_tags": len(tags), "tag_limit": domain.TagLimit, "synced_at": c.SyncedAt, "source_status": "local_catalog", "read_model_status": "ready", "route_owner": "ai_crm_next", "fallback_used": false, "real_external_call_executed": false, "sync_executed": false, "fixture_used": false})
}

func legacyTag(tag domain.Tag) map[string]any {
	return map[string]any{"tag_id": tag.ID, "id": tag.ID, "group_id": tag.GroupID, "group_name": tag.GroupName, "tag_name": tag.Name, "name": tag.Name, "sort_order": tag.SortOrder}
}

func legacyTags(tags []domain.Tag) []map[string]any {
	items := make([]map[string]any, 0, len(tags))
	for _, tag := range tags {
		items = append(items, legacyTag(tag))
	}
	return items
}

func legacyGroup(group domain.Group) map[string]any {
	return map[string]any{"group_id": group.ID, "group_name": group.Name, "name": group.Name, "sort_order": group.SortOrder}
}

func legacyMutationGroup(group domain.Group) map[string]any {
	return map[string]any{"group_id": group.ID, "group_name": group.Name, "sort_order": group.SortOrder}
}

func legacyGroups(groups []domain.Group, tags []domain.Tag) []map[string]any {
	items := make([]map[string]any, 0, len(groups))
	byID := make(map[int64]map[string]any, len(groups))
	for _, group := range groups {
		item := legacyGroup(group)
		item["tags"] = []map[string]any{}
		items = append(items, item)
		byID[group.ID] = item
	}
	for _, tag := range tags {
		if group := byID[tag.GroupID]; group != nil {
			group["tags"] = append(group["tags"].([]map[string]any), legacyTag(tag))
		}
	}
	return items
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
	case errors.Is(e, tagapp.ErrSyncInProgress), errors.Is(e, tagport.ErrSyncInProgress):
		writeError(w, 409, "sync_in_progress")
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
	compat := map[string]string{"invalid_request": "MALFORMED_REQUEST", "not_found": "NOT_FOUND", "conflict": "CONFLICT", "sync_in_progress": "CONFLICT", "referenced": "CONFLICT", "unauthorized": "UNAUTHORIZED", "forbidden": "FORBIDDEN", "csrf_required": "FORBIDDEN", "unavailable": "DEPENDENCY_UNAVAILABLE", "method_not_allowed": "METHOD_NOT_ALLOWED"}[code]
	if compat == "" {
		compat = "DEPENDENCY_UNAVAILABLE"
	}
	legacyCode := map[string]string{"invalid_request": "input_error", "not_found": "not_found", "unauthorized": "unauthorized", "forbidden": "unauthorized", "csrf_required": "unauthorized", "unavailable": "production_unavailable", "conflict": "input_error", "sync_in_progress": "sync_in_progress", "referenced": "input_error", "method_not_allowed": "input_error"}[code]
	if legacyCode == "" {
		legacyCode = "production_unavailable"
	}
	// Return the frozen legacy envelope as well as the v3 error triplet. The
	// duplicated fields preserve existing browser detail semantics without
	// weakening the current API's machine-readable error contract.
	writeJSON(w, status, map[string]any{
		"ok":                          false,
		"error_code":                  legacyCode,
		"detail":                      code,
		"source_status":               "local_catalog_error",
		"route_owner":                 "ai_crm_next",
		"fallback_used":               false,
		"real_external_call_executed": false,
		"sync_executed":               false,
		"fixture_used":                false,
		"code":                        compat,
		"message":                     code,
		"request_id":                  opaqueRequestID(),
		"error":                       code,
	})
}
