// Package http exposes Media-owned admin compatibility routes. It never
// delegates a write to a provider: every response is a local material fact.
package http

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	mediaapp "github.com/qianlan33333-png/AI-CRM-v3/internal/media/app"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/media/domain"
)

type RequestSecurity interface {
	Authenticate(context.Context, *http.Request) (accessdomain.Principal, error)
	AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error)
}
type Handler struct {
	service  mediaapp.HTTPFacade
	security RequestSecurity
}

func NewHandler(service mediaapp.HTTPFacade, security RequestSecurity) (*Handler, error) {
	if service == nil || security == nil {
		return nil, errors.New("media HTTP dependencies are required")
	}
	return &Handler{service, security}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.service == nil || h.security == nil {
		writeError(w, 503, "unavailable")
		return
	}
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/"), "/")
	switch {
	case path == "image-library" || strings.HasPrefix(path, "image-library/"):
		h.images(w, r, strings.Trim(strings.TrimPrefix(path, "image-library"), "/"))
	case path == "attachment-library" || strings.HasPrefix(path, "attachment-library/"):
		h.attachments(w, r, strings.Trim(strings.TrimPrefix(path, "attachment-library"), "/"))
	case path == "miniprogram-library" || strings.HasPrefix(path, "miniprogram-library/"):
		h.miniprograms(w, r, strings.Trim(strings.TrimPrefix(path, "miniprogram-library"), "/"))
	case path == "group-invite-library" || strings.HasPrefix(path, "group-invite-library/"):
		h.groups(w, r, strings.Trim(strings.TrimPrefix(path, "group-invite-library"), "/"))
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
	if !readRole(p) {
		writeError(w, 403, "permission_denied")
		return false
	}
	return true
}
func (h *Handler) write(w http.ResponseWriter, r *http.Request) (accessdomain.Principal, bool) {
	p, err := h.security.AuthorizeCSRF(r.Context(), r)
	if err != nil {
		writeError(w, 403, "csrf_required")
		return accessdomain.Principal{}, false
	}
	if !writeRole(p) {
		writeError(w, 403, "permission_denied")
		return accessdomain.Principal{}, false
	}
	return p, true
}

// mutationKey preserves a supplied client key exactly. Frozen v2 Media
// controls omitted this header, so their compatibility route mints a fresh
// per-request key instead of accidentally coalescing independent clicks. The
// reserved prefix is recorded in Media audit facts by the store.
func mutationKey(r *http.Request) string {
	if key := strings.TrimSpace(r.Header.Get("Idempotency-Key")); key != "" {
		return key
	}
	var raw [20]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "server_compat_fallback_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return "server_compat_" + hex.EncodeToString(raw[:])
}
func readRole(p accessdomain.Principal) bool {
	return (p.Kind == accessdomain.KindAdmin || p.Kind == accessdomain.KindStaff) && p.InternalID > 0 && (hasRole(p, accessdomain.RoleViewer) || hasRole(p, accessdomain.RoleAdmin) || hasRole(p, accessdomain.RoleSuperAdmin))
}
func writeRole(p accessdomain.Principal) bool {
	return (p.Kind == accessdomain.KindAdmin || p.Kind == accessdomain.KindStaff) && p.InternalID > 0 && (hasRole(p, accessdomain.RoleAdmin) || hasRole(p, accessdomain.RoleSuperAdmin))
}
func hasRole(principal accessdomain.Principal, wanted accessdomain.Role) bool {
	for _, role := range principal.Roles {
		if role == wanted {
			return true
		}
	}
	return false
}
func method(w http.ResponseWriter, got, want string) bool {
	if got == want {
		return true
	}
	w.Header().Set("Allow", want)
	writeError(w, 405, "method_not_allowed")
	return false
}
func page(r *http.Request) (int, int, bool, string, error) {
	q := r.URL.Query()
	limit := 50
	if raw := q.Get("limit"); raw != "" {
		v, e := strconv.Atoi(raw)
		if e != nil {
			return 0, 0, false, "", e
		}
		limit = v
	}
	offset := 0
	if raw := q.Get("offset"); raw != "" {
		v, e := strconv.Atoi(raw)
		if e != nil {
			return 0, 0, false, "", e
		}
		offset = v
	}
	enabled := q.Get("enabled_only") == "true"
	return limit, offset, enabled, strings.TrimSpace(q.Get("q")), nil
}
func id(value string) (int64, error) {
	if value == "" || strings.HasPrefix(value, "0") {
		return 0, errors.New("invalid id")
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return 0, errors.New("invalid id")
		}
	}
	return strconv.ParseInt(value, 10, 64)
}
func resultError(w http.ResponseWriter, err error) {
	switch {
	case err == nil:
		return
	case errors.Is(err, mediaapp.ErrHTTPNotFound):
		writeError(w, 404, "not_found")
	case errors.Is(err, mediaapp.ErrHTTPConflict):
		writeError(w, 409, "conflict")
	case errors.Is(err, mediaapp.ErrHTTPReferences):
		writeError(w, 409, "has_references")
	case errors.Is(err, mediaapp.ErrHTTPInvalid), errors.Is(err, domain.ErrUnsafeImage), errors.Is(err, domain.ErrUnsafeAttachment):
		writeError(w, 400, "invalid_request")
	default:
		writeError(w, 503, "unavailable")
	}
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, code string) {
	compat := map[string]string{"invalid_request": "MALFORMED_REQUEST", "not_found": "NOT_FOUND", "conflict": "CONFLICT", "has_references": "CONFLICT", "unauthorized": "UNAUTHORIZED", "permission_denied": "FORBIDDEN", "csrf_required": "FORBIDDEN", "unavailable": "DEPENDENCY_UNAVAILABLE", "method_not_allowed": "METHOD_NOT_ALLOWED"}[code]
	if compat == "" {
		compat = "DEPENDENCY_UNAVAILABLE"
	}
	var raw [12]byte
	_, _ = rand.Read(raw[:])
	payload := map[string]any{"ok": false, "code": compat, "message": strings.ReplaceAll(strings.ToLower(compat), "_", " "), "request_id": "media_" + hex.EncodeToString(raw[:])}
	if code == "has_references" {
		payload["error"] = "material has local references"
		payload["references"] = map[string]any{"automation_agents": []int{}, "channels": []int{}, "radar_links": []int{}}
	}
	writeJSON(w, status, payload)
}

func writeReferenceConflict(w http.ResponseWriter, kind string) {
	references := map[string]any{"automation_agents": []int{}, "channels": []int{}, "radar_links": []int{}}
	if kind == "image" {
		references["miniprograms"] = []int{}
		references["group_invites"] = []int{}
	}
	writeJSON(w, http.StatusConflict, map[string]any{"ok": false, "code": "CONFLICT", "error": kind + "_has_references", "references": references})
}

func (h *Handler) images(w http.ResponseWriter, r *http.Request, tail string) {
	if tail == "" {
		if r.Method == http.MethodGet {
			if !h.read(w, r) {
				return
			}
			limit, offset, _, q, e := page(r)
			if e != nil {
				writeError(w, 400, "invalid_request")
				return
			}
			query, e := imageQuery(r, limit, offset, q)
			if e != nil {
				writeError(w, 400, "invalid_request")
				return
			}
			items, total, e := h.service.ListImagesFiltered(r.Context(), query)
			if e != nil {
				resultError(w, e)
				return
			}
			next := any(nil)
			if offset+len(items) < total {
				next = offset + len(items)
			}
			writeJSON(w, 200, map[string]any{"ok": true, "items": items, "images": items, "total": total, "limit": limit, "offset": offset, "count": len(items), "has_more": offset+len(items) < total, "next_offset": next, "source_status": "next_media_library", "route_owner": "ai_crm_next", "fallback_used": false, "real_external_call_executed": false, "storage_adapter_mode": "postgresql", "adapter_mode": "postgresql", "local_fact_only": true})
			return
		}
		if r.Method == http.MethodPost {
			h.imageCreate(w, r, "local_repository_write")
			return
		}
		method(w, r.Method, http.MethodGet+", "+http.MethodPost)
		return
	}
	// The frozen Media client posts new image files to this historical alias.
	// Keep the alias server-owned; it is not an independently exposed page.
	if tail == "upload" {
		if !method(w, r.Method, http.MethodPost) {
			return
		}
		h.imageCreate(w, r, "local_upload")
		return
	}
	if tail == "facets" {
		if !method(w, r.Method, http.MethodGet) || !h.read(w, r) {
			return
		}
		categories, tags, e := h.service.ImageFacets(r.Context())
		if e != nil {
			resultError(w, e)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "categories": categories, "tags": tags, "items": categories, "source_status": "next_media_library", "route_owner": "ai_crm_next", "fallback_used": false, "real_external_call_executed": false, "storage_adapter_mode": "postgresql", "adapter_mode": "postgresql"})
		return
	}
	parts := strings.Split(tail, "/")
	imageID, e := id(parts[0])
	if e != nil {
		writeError(w, 404, "not_found")
		return
	}
	if len(parts) == 3 && parts[1] == "variants" {
		if !method(w, r.Method, http.MethodGet) || !h.read(w, r) {
			return
		}
		item, content, _, e := h.service.Image(r.Context(), imageID)
		_ = item
		if e != nil {
			resultError(w, e)
			return
		}
		key := parts[2]
		if key != "original" && key != "thumb_160" && key != "thumb_320" && key != "mobile_1080" && key != "large_1440" {
			writeError(w, 404, "not_found")
			return
		}
		variant, variantType, err := mediaVariant(content, item["mime_type"].(string), key)
		if err != nil {
			resultError(w, err)
			return
		}
		sum := sha256.Sum256(variant)
		etag := "\"" + hex.EncodeToString(sum[:]) + "\""
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Type", variantType)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "private, max-age=3600")
		_, _ = w.Write(variant)
		return
	}
	if len(parts) != 1 {
		writeError(w, 404, "not_found")
		return
	}
	if r.Method == http.MethodGet {
		if !h.read(w, r) {
			return
		}
		item, content, _, e := h.service.Image(r.Context(), imageID)
		if e != nil {
			resultError(w, e)
			return
		}
		if r.URL.Query().Get("include_data") == "true" {
			item["data_url"] = "data:" + item["mime_type"].(string) + ";base64," + base64.StdEncoding.EncodeToString(content)
		}
		if variant := r.URL.Query().Get("variant"); variant != "" {
			if !validVariant(variant) {
				writeError(w, 400, "invalid_request")
				return
			}
			item["variant"] = variant
			item["variant_url"] = "/api/admin/image-library/" + strconv.FormatInt(imageID, 10) + "/variants/" + variant
		}
		writeJSON(w, 200, imageEnvelope(item))
		return
	}
	if r.Method == http.MethodPut {
		h.imageUpdate(w, r, imageID)
		return
	}
	if r.Method == http.MethodDelete {
		actor, ok := h.write(w, r)
		if !ok {
			return
		}
		out, e := h.service.Delete(r.Context(), "image", imageID, actor.InternalID, mutationKey(r))
		if e != nil {
			if errors.Is(e, mediaapp.ErrHTTPReferences) {
				writeReferenceConflict(w, "image")
				return
			}
			resultError(w, e)
			return
		}
		out["ok"], out["item_id"], out["local_only"], out["provider_call_executed"], out["real_external_call_executed"], out["references_cleared"] = true, imageID, true, false, false, map[string]any{"miniprograms": []int{}, "group_invites": []int{}, "automation_agents": []int{}, "channels": []int{}, "radar_links": []int{}}
		writeJSON(w, 200, out)
		return
	}
	method(w, r.Method, http.MethodGet+", "+http.MethodPut+", "+http.MethodDelete)
}
func imageQuery(r *http.Request, limit, offset int, q string) (mediaapp.ImageQuery, error) {
	values := r.URL.Query()
	enabled := true
	if raw, exists := values["enabled_only"]; exists {
		if len(raw) != 1 || (raw[0] != "true" && raw[0] != "false") {
			return mediaapp.ImageQuery{}, ErrInvalidQuery
		}
		enabled = raw[0] == "true"
	}
	onlyUnlabeled := false
	if raw, exists := values["only_unlabeled"]; exists {
		if len(raw) != 1 || (raw[0] != "true" && raw[0] != "false") {
			return mediaapp.ImageQuery{}, ErrInvalidQuery
		}
		onlyUnlabeled = raw[0] == "true"
	}
	normalize := func(raw string) ([]string, error) {
		if raw == "" {
			return nil, nil
		}
		seen := map[string]bool{}
		out := []string{}
		for _, item := range strings.Split(raw, ",") {
			item = strings.TrimSpace(item)
			if item == "" {
				continue
			}
			if len([]rune(item)) > 64 {
				return nil, ErrInvalidQuery
			}
			if !seen[item] {
				seen[item] = true
				out = append(out, item)
			}
		}
		if len(out) > 50 {
			return nil, ErrInvalidQuery
		}
		return out, nil
	}
	tags, err := normalize(values.Get("tags"))
	if err != nil {
		return mediaapp.ImageQuery{}, err
	}
	groups := [][]string{}
	for _, raw := range values["tag_group"] {
		group, err := normalize(raw)
		if err != nil {
			return mediaapp.ImageQuery{}, err
		}
		if len(group) > 0 {
			groups = append(groups, group)
		}
	}
	return mediaapp.ImageQuery{Limit: limit, Offset: offset, EnabledOnly: enabled, Query: q, Category: strings.TrimSpace(values.Get("category")), Tags: tags, TagGroups: groups, OnlyUnlabeled: onlyUnlabeled}, nil
}

var ErrInvalidQuery = errors.New("invalid media query")

func (h *Handler) imageCreate(w http.ResponseWriter, r *http.Request, source string) {
	actor, ok := h.write(w, r)
	if !ok {
		return
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		h.imageCreateJSON(w, r, actor, source)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, domain.MaxImageBytes+(1<<20))
	if err := r.ParseMultipartForm(11 << 20); err != nil {
		writeError(w, 400, "invalid_request")
		return
	}
	file, header, err := r.FormFile("image")
	if err != nil {
		writeError(w, 400, "invalid_request")
		return
	}
	defer file.Close()
	content, err := domain.ReadBounded(file)
	if err != nil {
		resultError(w, err)
		return
	}
	inspection, err := domain.Inspect(header.Filename, header.Header.Get("Content-Type"), content)
	if err != nil {
		resultError(w, err)
		return
	}
	enabled := true
	if raw := r.FormValue("enabled"); raw != "" {
		enabled = raw == "true"
	}
	out, err := h.service.CreateImage(r.Context(), actor.InternalID, mutationKey(r), mediaapp.ImageInput{FileName: header.Filename, MIME: inspection.MediaType, Name: nonempty(r.FormValue("name"), header.Filename), Description: r.FormValue("description"), Tags: r.FormValue("tags"), Category: r.FormValue("category"), Content: content, Width: inspection.Width, Height: inspection.Height, Enabled: enabled})
	if err != nil {
		resultError(w, err)
		return
	}
	writeJSON(w, 200, imageMutationEnvelope(out, source))
}

// imageCreateJSON preserves the canonical compatibility create contract. The
// frozen UI currently chooses the multipart alias, while non-UI callers may
// still use the documented data URL request.
func (h *Handler) imageCreateJSON(w http.ResponseWriter, r *http.Request, actor accessdomain.Principal, source string) {
	var body struct {
		DataURL     string   `json:"data_url"`
		FileName    string   `json:"file_name"`
		Name        string   `json:"name"`
		Description string   `json:"description"`
		Tags        []string `json:"tags"`
		Category    string   `json:"category"`
		Enabled     *bool    `json:"enabled"`
	}
	if decodeLimit(r, &body, (domain.MaxImageBytes*4)/3+(1<<20)) != nil || !strings.HasPrefix(body.DataURL, "data:") || strings.TrimSpace(body.FileName) == "" {
		writeError(w, 400, "invalid_request")
		return
	}
	comma := strings.IndexByte(body.DataURL, ',')
	metadata := ""
	if comma > 0 {
		metadata = body.DataURL[:comma]
	}
	if comma < 1 || !strings.HasPrefix(metadata, "data:") || !strings.HasSuffix(strings.ToLower(metadata), ";base64") {
		writeError(w, 400, "invalid_request")
		return
	}
	encoded := body.DataURL[comma+1:]
	content, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		writeError(w, 400, "invalid_request")
		return
	}
	mediaType := strings.TrimPrefix(strings.TrimSuffix(metadata, ";base64"), "data:")
	if body.FileName == "" {
		body.FileName = "image"
	}
	inspection, err := domain.Inspect(body.FileName, mediaType, content)
	if err != nil {
		resultError(w, err)
		return
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	out, err := h.service.CreateImage(r.Context(), actor.InternalID, mutationKey(r), mediaapp.ImageInput{FileName: body.FileName, MIME: inspection.MediaType, Name: nonempty(body.Name, body.FileName), Description: body.Description, Tags: strings.Join(body.Tags, ","), Category: body.Category, Content: content, Width: inspection.Width, Height: inspection.Height, Enabled: enabled})
	if err != nil {
		resultError(w, err)
		return
	}
	writeJSON(w, 200, imageMutationEnvelope(out, source))
}
func (h *Handler) imageUpdate(w http.ResponseWriter, r *http.Request, imageID int64) {
	actor, ok := h.write(w, r)
	if !ok {
		return
	}
	var patch map[string]any
	if decode(r, &patch) != nil {
		writeError(w, 400, "invalid_request")
		return
	}
	if !allowedFields(patch, "name", "description", "tags", "category", "enabled") {
		writeError(w, 400, "invalid_request")
		return
	}
	out, e := h.service.UpdateImage(r.Context(), imageID, actor.InternalID, mutationKey(r), patch)
	if e != nil {
		resultError(w, e)
		return
	}
	writeJSON(w, 200, imageMutationEnvelope(out, "local_repository_write"))
}
func nonempty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
func imageEnvelope(item map[string]any) map[string]any {
	return map[string]any{"ok": true, "item": item, "image": item, "item_id": item["id"], "source_status": "next_media_library", "route_owner": "ai_crm_next", "fallback_used": false, "local_only": true, "provider_call_executed": false, "real_external_call_executed": false, "storage_adapter_mode": "postgresql", "adapter_mode": "postgresql"}
}
func imageMutationEnvelope(item map[string]any, source string) map[string]any {
	response := imageEnvelope(item)
	response["source_status"] = source
	return response
}

func mediaVariant(content []byte, originalType, key string) ([]byte, string, error) {
	if key == "original" {
		return content, originalType, nil
	}
	limit := map[string]int{"thumb_160": 160, "thumb_320": 320, "mobile_1080": 1080, "large_1440": 1440}[key]
	if limit == 0 {
		return nil, "", mediaapp.ErrHTTPNotFound
	}
	source, _, err := image.Decode(bytes.NewReader(content))
	if err != nil {
		return nil, "", domain.ErrUnsafeImage
	}
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width < 1 || height < 1 {
		return nil, "", domain.ErrUnsafeImage
	}
	if width > limit || height > limit {
		if width >= height {
			height = height * limit / width
			width = limit
		} else {
			width = width * limit / height
			height = limit
		}
	}
	target := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			sourceX := bounds.Min.X + x*bounds.Dx()/width
			sourceY := bounds.Min.Y + y*bounds.Dy()/height
			target.Set(x, y, source.At(sourceX, sourceY))
		}
	}
	var encoded bytes.Buffer
	if err = png.Encode(&encoded, target); err != nil {
		return nil, "", err
	}
	return encoded.Bytes(), "image/png", nil
}

func validVariant(key string) bool {
	return key == "original" || key == "thumb_160" || key == "thumb_320" || key == "mobile_1080" || key == "large_1440"
}

func (h *Handler) attachments(w http.ResponseWriter, r *http.Request, tail string) {
	if strings.HasPrefix(tail, "uploads") {
		h.attachmentUpload(w, r, strings.Trim(strings.TrimPrefix(tail, "uploads"), "/"))
		return
	}
	if tail == "" || tail == "upload" {
		if r.Method == http.MethodGet && tail == "" {
			if !h.read(w, r) {
				return
			}
			limit, offset, enabled, q, e := page(r)
			if e != nil {
				writeError(w, 400, "invalid_request")
				return
			}
			items, total, e := h.service.ListAttachments(r.Context(), limit, offset, enabled, q)
			if e != nil {
				resultError(w, e)
				return
			}
			writeJSON(w, 200, map[string]any{"ok": true, "items": items, "attachments": items, "total": total, "limit": limit, "offset": offset, "count": len(items), "has_more": offset+len(items) < total, "local_only": true, "provider_call_executed": false, "real_external_call_executed": false})
			return
		}
		if r.Method == http.MethodPost {
			h.attachmentCreate(w, r)
			return
		}
		method(w, r.Method, http.MethodGet+", "+http.MethodPost)
		return
	}
	parts := strings.Split(tail, "/")
	attachmentID, e := id(parts[0])
	if e != nil {
		writeError(w, 404, "not_found")
		return
	}
	if len(parts) == 2 && parts[1] == "download" {
		if !method(w, r.Method, http.MethodGet) || !h.read(w, r) {
			return
		}
		item, content, e := h.service.Attachment(r.Context(), attachmentID)
		if e != nil {
			resultError(w, e)
			return
		}
		w.Header().Set("Content-Type", "application/pdf")
		w.Header().Set("Content-Disposition", `attachment; filename="`+strings.ReplaceAll(item["file_name"].(string), `"`, "_")+`"`)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(content)
		return
	}
	if len(parts) != 1 {
		writeError(w, 404, "not_found")
		return
	}
	if r.Method == http.MethodGet {
		if !h.read(w, r) {
			return
		}
		item, _, e := h.service.Attachment(r.Context(), attachmentID)
		if e != nil {
			resultError(w, e)
			return
		}
		response := map[string]any{}
		for key, value := range item {
			response[key] = value
		}
		response["item"] = item
		response["attachment"] = item
		response["ok"] = true
		response["local_only"] = true
		response["provider_call_executed"] = false
		response["real_external_call_executed"] = false
		writeJSON(w, 200, response)
		return
	}
	if r.Method == http.MethodPut {
		actor, ok := h.write(w, r)
		if !ok {
			return
		}
		var patch map[string]any
		if decode(r, &patch) != nil {
			writeError(w, 400, "invalid_request")
			return
		}
		if !allowedFields(patch, "name", "description", "tags", "enabled", "expected_version") {
			writeError(w, 400, "invalid_request")
			return
		}
		out, e := h.service.UpdateAttachment(r.Context(), attachmentID, actor.InternalID, mutationKey(r), patch)
		if e != nil {
			resultError(w, e)
			return
		}
		writeJSON(w, 200, attachmentEnvelope(out))
		return
	}
	if r.Method == http.MethodDelete {
		actor, ok := h.write(w, r)
		if !ok {
			return
		}
		out, e := h.service.Delete(r.Context(), "attachment", attachmentID, actor.InternalID, mutationKey(r))
		if e != nil {
			if errors.Is(e, mediaapp.ErrHTTPReferences) {
				writeReferenceConflict(w, "attachment")
				return
			}
			resultError(w, e)
			return
		}
		out["ok"] = true
		out["item_id"] = attachmentID
		out["local_only"] = true
		out["provider_call_executed"] = false
		out["real_external_call_executed"] = false
		writeJSON(w, 200, out)
		return
	}
	method(w, r.Method, http.MethodGet+", "+http.MethodPut+", "+http.MethodDelete)
}
func (h *Handler) attachmentCreate(w http.ResponseWriter, r *http.Request) {
	actor, ok := h.write(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, domain.MaxAttachmentBytes+(1<<20))
	if r.ParseMultipartForm(11<<20) != nil {
		writeError(w, 400, "invalid_request")
		return
	}
	file, header, e := r.FormFile("attachment")
	if e != nil {
		writeError(w, 400, "invalid_request")
		return
	}
	defer file.Close()
	content, e := domain.ReadAttachmentBounded(file)
	if e != nil {
		resultError(w, e)
		return
	}
	if _, e = domain.InspectAttachment(header.Filename, header.Header.Get("Content-Type"), content); e != nil {
		resultError(w, e)
		return
	}
	tags := splitCSV(r.FormValue("tags"))
	out, e := h.service.CreateAttachment(r.Context(), actor.InternalID, mutationKey(r), mediaapp.AttachmentInput{FileName: header.Filename, Name: nonempty(r.FormValue("name"), header.Filename), Description: r.FormValue("description"), Tags: tags, Content: content, Enabled: r.FormValue("enabled") != "false"})
	if e != nil {
		resultError(w, e)
		return
	}
	writeJSON(w, 200, attachmentEnvelope(out))
}

// attachmentUpload implements the frozen PDF uploader's init -> ordered parts
// -> complete protocol. Each transition has its own receipt/audit/outbox fact;
// no Provider is contacted and the completed bytes stay private to Media.
func (h *Handler) attachmentUpload(w http.ResponseWriter, r *http.Request, tail string) {
	if tail == "" {
		if !method(w, r.Method, http.MethodPost) {
			return
		}
		actor, ok := h.write(w, r)
		if !ok {
			return
		}
		var body struct {
			FileName    string `json:"file_name"`
			Name        string `json:"name"`
			Description string `json:"description"`
			Size        int64  `json:"size"`
			SHA256      string `json:"sha256"`
			Enabled     *bool  `json:"enabled"`
		}
		if decode(r, &body) != nil {
			writeError(w, 400, "invalid_request")
			return
		}
		enabled := true
		if body.Enabled != nil {
			enabled = *body.Enabled
		}
		uploadID, err := h.service.InitiateAttachmentUpload(r.Context(), actor.InternalID, mutationKey(r), mediaapp.AttachmentUploadInput{FileName: body.FileName, Name: body.Name, Description: body.Description, Size: body.Size, Digest: body.SHA256, Enabled: enabled})
		if err != nil {
			resultError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"upload_id": uploadID, "local_fact_only": true, "provider_call_executed": false})
		return
	}
	parts := strings.Split(tail, "/")
	uploadID, err := id(parts[0])
	if err != nil {
		writeError(w, 404, "not_found")
		return
	}
	if len(parts) == 3 && parts[1] == "parts" {
		if !method(w, r.Method, http.MethodPut) {
			return
		}
		actor, ok := h.write(w, r)
		if !ok {
			return
		}
		part, err := id(parts[2])
		if err != nil || part > 1<<31-1 {
			writeError(w, 404, "not_found")
			return
		}
		var body struct {
			SHA256  string `json:"sha256"`
			Content string `json:"content"`
		}
		if decodeLimit(r, &body, 2<<20) != nil {
			writeError(w, 400, "invalid_request")
			return
		}
		content, err := base64.StdEncoding.DecodeString(body.Content)
		if err != nil {
			writeError(w, 400, "invalid_request")
			return
		}
		if err = h.service.PutAttachmentUploadPart(r.Context(), uploadID, int(part), actor.InternalID, mutationKey(r), body.SHA256, content); err != nil {
			resultError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if len(parts) == 2 && parts[1] == "complete" {
		if !method(w, r.Method, http.MethodPost) {
			return
		}
		actor, ok := h.write(w, r)
		if !ok {
			return
		}
		attachmentID, err := h.service.CompleteAttachmentUpload(r.Context(), uploadID, actor.InternalID, mutationKey(r))
		if err != nil {
			resultError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"attachment_id": attachmentID, "local_fact_only": true, "provider_call_executed": false})
		return
	}
	writeError(w, 404, "not_found")
}
func splitCSV(value string) []string {
	if value == "" {
		return []string{}
	}
	return strings.Split(value, ",")
}

func (h *Handler) miniprograms(w http.ResponseWriter, r *http.Request, tail string) {
	if tail == "" {
		if r.Method == http.MethodGet {
			if !h.read(w, r) {
				return
			}
			limit, offset, enabled, q, e := page(r)
			if e != nil {
				writeError(w, 400, "invalid_request")
				return
			}
			items, total, e := h.service.ListMiniPrograms(r.Context(), limit, offset, enabled, q)
			if e != nil {
				resultError(w, e)
				return
			}
			writeJSON(w, 200, map[string]any{"ok": true, "items": items, "miniprograms": items, "mini_programs": items, "total": total, "limit": limit, "offset": offset, "count": len(items), "has_more": offset+len(items) < total, "local_only": true, "provider_call_executed": false, "real_external_call_executed": false})
			return
		}
		if r.Method == http.MethodPost {
			actor, ok := h.write(w, r)
			if !ok {
				return
			}
			var body map[string]any
			if decode(r, &body) != nil {
				writeError(w, 400, "invalid_request")
				return
			}
			if !allowedFields(body, "name", "appid", "app_id", "pagepath", "page_path", "title", "thumb_image_id", "enabled") {
				writeError(w, 400, "invalid_request")
				return
			}
			out, e := h.service.CreateMiniProgram(r.Context(), actor.InternalID, mutationKey(r), body)
			if e != nil {
				resultError(w, e)
				return
			}
			writeJSON(w, 200, miniMutation(out))
			return
		}
		method(w, r.Method, http.MethodGet+", "+http.MethodPost)
		return
	}
	parts := strings.Split(tail, "/")
	resourceID, e := id(parts[0])
	if e != nil {
		writeError(w, 404, "not_found")
		return
	}
	if len(parts) == 2 && parts[1] == "test-resolve" {
		if !method(w, r.Method, http.MethodPost) {
			return
		}
		actor, ok := h.write(w, r)
		if !ok {
			return
		}
		resolution, err := h.service.ResolveMiniProgramThumbnail(r.Context(), resourceID, actor.InternalID, mutationKey(r))
		if err != nil {
			resultError(w, err)
			return
		}
		item, err := h.service.MiniProgram(r.Context(), resourceID)
		if err != nil {
			resultError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "item": item, "miniprogram": item, "resolution": resolution, "changed": false, "thumb_media_id": "", "local_only": true, "provider_call_executed": false, "real_external_call_executed": false})
		return
	}
	if len(parts) != 1 {
		writeError(w, 404, "not_found")
		return
	}
	if r.Method == http.MethodGet {
		if !h.read(w, r) {
			return
		}
		out, e := h.service.MiniProgram(r.Context(), resourceID)
		if e != nil {
			resultError(w, e)
			return
		}
		writeJSON(w, 200, miniDetail(out))
		return
	}
	if r.Method == http.MethodPut {
		actor, ok := h.write(w, r)
		if !ok {
			return
		}
		var body map[string]any
		if decode(r, &body) != nil {
			writeError(w, 400, "invalid_request")
			return
		}
		if !allowedFields(body, "name", "appid", "app_id", "pagepath", "page_path", "title", "thumb_image_id", "enabled") {
			writeError(w, 400, "invalid_request")
			return
		}
		out, e := h.service.UpdateMiniProgram(r.Context(), resourceID, actor.InternalID, mutationKey(r), body)
		if e != nil {
			resultError(w, e)
			return
		}
		writeJSON(w, 200, miniMutation(out))
		return
	}
	if r.Method == http.MethodDelete {
		actor, ok := h.write(w, r)
		if !ok {
			return
		}
		out, e := h.service.Delete(r.Context(), "miniprogram", resourceID, actor.InternalID, mutationKey(r))
		if e != nil {
			resultError(w, e)
			return
		}
		out["ok"] = true
		out["item_id"] = resourceID
		out["local_only"] = true
		out["provider_call_executed"] = false
		out["real_external_call_executed"] = false
		writeJSON(w, 200, out)
		return
	}
	method(w, r.Method, http.MethodGet+", "+http.MethodPut+", "+http.MethodDelete)
}
func (h *Handler) groups(w http.ResponseWriter, r *http.Request, tail string) {
	if tail != "" {
		itemID, err := id(tail)
		if err != nil {
			writeError(w, 404, "not_found")
			return
		}
		switch r.Method {
		case http.MethodGet:
			if !h.read(w, r) {
				return
			}
			item, err := h.service.GroupInvite(r.Context(), itemID)
			if err != nil {
				resultError(w, err)
				return
			}
			writeJSON(w, 200, groupMutation(item))
		case http.MethodPut:
			actor, ok := h.write(w, r)
			if !ok {
				return
			}
			var body map[string]any
			if decode(r, &body) != nil {
				writeError(w, 400, "invalid_request")
				return
			}
			if !allowedFields(body, "name", "title", "description", "join_url", "cover_image_id", "enabled") {
				writeError(w, 400, "invalid_request")
				return
			}
			item, err := h.service.UpdateGroupInvite(r.Context(), itemID, actor.InternalID, mutationKey(r), body)
			if err != nil {
				resultError(w, err)
				return
			}
			writeJSON(w, 200, groupMutation(item))
		case http.MethodDelete:
			actor, ok := h.write(w, r)
			if !ok {
				return
			}
			item, err := h.service.ArchiveGroupInvite(r.Context(), itemID, actor.InternalID, mutationKey(r))
			if err != nil {
				resultError(w, err)
				return
			}
			response := groupMutation(item)
			response["archived"] = true
			writeJSON(w, 200, response)
		default:
			method(w, r.Method, http.MethodGet+", "+http.MethodPut+", "+http.MethodDelete)
		}
		return
	}
	if r.Method == http.MethodGet {
		if !h.read(w, r) {
			return
		}
		limit, offset, enabled, q, e := page(r)
		if e != nil {
			writeError(w, 400, "invalid_request")
			return
		}
		items, total, e := h.service.ListGroupInvites(r.Context(), limit, offset, enabled, q)
		if e != nil {
			resultError(w, e)
			return
		}
		writeJSON(w, 200, map[string]any{"ok": true, "items": items, "group_invites": items, "total": total, "limit": limit, "offset": offset, "count": len(items), "has_more": offset+len(items) < total, "local_only": true, "provider_call_executed": false, "real_external_call_executed": false})
		return
	}
	if r.Method == http.MethodPost {
		actor, ok := h.write(w, r)
		if !ok {
			return
		}
		var body map[string]any
		if decode(r, &body) != nil {
			writeError(w, 400, "invalid_request")
			return
		}
		if !allowedFields(body, "name", "title", "description", "join_url", "cover_image_id", "enabled") {
			writeError(w, 400, "invalid_request")
			return
		}
		out, e := h.service.CreateGroupInvite(r.Context(), actor.InternalID, mutationKey(r), body)
		if e != nil {
			resultError(w, e)
			return
		}
		writeJSON(w, 200, groupMutation(out))
		return
	}
	method(w, r.Method, http.MethodGet+", "+http.MethodPost)
}
func decode(r *http.Request, target any) error {
	return decodeLimit(r, target, 1<<20)
}
func decodeLimit(r *http.Request, target any, limit int64) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(r.Body, limit+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("invalid body")
	}
	return nil
}

func allowedFields(value map[string]any, allowed ...string) bool {
	set := make(map[string]struct{}, len(allowed))
	for _, field := range allowed {
		set[field] = struct{}{}
	}
	for field := range value {
		if _, ok := set[field]; !ok {
			return false
		}
	}
	return true
}

func groupMutation(item map[string]any) map[string]any {
	return map[string]any{"ok": true, "item": item, "group_invite": item, "item_id": item["id"], "local_only": true, "provider_call_executed": false, "real_external_call_executed": false}
}
func miniMutation(item map[string]any) map[string]any {
	return map[string]any{"ok": true, "item": item, "miniprogram": item, "item_id": item["id"], "changed": true, "thumb_resolve": nil, "local_only": true, "provider_call_executed": false, "real_external_call_executed": false}
}
func miniDetail(item map[string]any) map[string]any {
	return map[string]any{"ok": true, "item": item, "miniprogram": item, "item_id": item["id"], "changed": false, "thumb_resolve": nil, "local_only": true, "provider_call_executed": false, "real_external_call_executed": false}
}
func attachmentEnvelope(item map[string]any) map[string]any {
	response := map[string]any{}
	for key, value := range item {
		response[key] = value
	}
	response["ok"] = true
	response["item"] = item
	response["attachment"] = item
	response["local_only"] = true
	response["provider_call_executed"] = false
	response["real_external_call_executed"] = false
	return response
}
func mapKeys(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	return out
}
