package http

import (
	"net/http"
	"strings"

	mediaapp "github.com/qianlan33333-png/AI-CRM-v3/internal/media/app"
)

func groupQuery(r *http.Request) (*string, error) {
	if _, ok := r.URL.Query()["category"]; !ok {
		return nil, nil
	}
	value, err := scalarQuery(r, "category")
	if err != nil {
		return nil, err
	}
	if len([]rune(value)) > 100 || strings.TrimSpace(value) != value {
		return nil, mediaapp.ErrHTTPInvalid
	}
	return &value, nil
}
func (h *Handler) materialGroupRoute(w http.ResponseWriter, r *http.Request, kind, tail string) bool {
	parts := strings.Split(tail, "/")
	isFacets := tail == "groups"
	isUpdate := len(parts) == 2 && parts[1] == "group"
	if !isFacets && !isUpdate {
		return false
	}
	service, ok := h.service.(mediaapp.MaterialGrouping)
	if !ok {
		writeError(w, 503, "unavailable")
		return true
	}
	if isFacets {
		if !method(w, r.Method, http.MethodGet) || !h.read(w, r) {
			return true
		}
		groups, e := service.MaterialGroups(r.Context(), kind)
		if e != nil {
			resultError(w, e)
			return true
		}
		writeJSON(w, 200, map[string]any{"items": groups})
		return true
	}
	if !method(w, r.Method, http.MethodPut) {
		return true
	}
	actor, ok := h.write(w, r)
	if !ok {
		return true
	}
	resource, e := id(parts[0])
	if e != nil {
		writeError(w, 400, "invalid_request")
		return true
	}
	var body struct {
		Category string `json:"category"`
		Version  int64  `json:"expected_version"`
	}
	if decode(r, &body) != nil {
		writeError(w, 400, "invalid_request")
		return true
	}
	result, e := service.SetMaterialGroup(r.Context(), kind, resource, actor.InternalID, body.Version, mutationKey(r), body.Category)
	if e != nil {
		resultError(w, e)
		return true
	}
	writeJSON(w, 200, result)
	return true
}
