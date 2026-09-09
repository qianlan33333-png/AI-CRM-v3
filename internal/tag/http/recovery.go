package http

import (
	"errors"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"net/http"
	"strings"
)

func (h *Handler) recoverMutation(w http.ResponseWriter, r *http.Request, tail string) {
	if r.Method != http.MethodPost {
		method(w, http.MethodPost)
		return
	}
	principal, ok := h.mutate(w, r)
	if !ok {
		return
	}
	raw := strings.TrimSuffix(strings.TrimPrefix(tail, "mutations/"), "/retry")
	id, ok := parseID(raw)
	if !ok {
		writeError(w, 404, "not_found")
		return
	}
	key, err := idempotencyKey(r, "")
	if err != nil {
		writeError(w, 400, "invalid_request")
		return
	}
	result, err := h.catalog.RetryMutation(r.Context(), id, command(principal, key, opaqueRequestID()))
	if err != nil {
		if errors.Is(err, effectport.ErrReconciliationConflict) {
			writeJSON(w, 409, map[string]any{"ok": false, "error": "tag_retry_unsafe", "message": "当前结果不能安全重试，请先核对企微"})
			return
		}
		resultError(w, err)
		return
	}
	writeJSON(w, 202, map[string]any{"ok": true, "effect_id": result.ID, "effect_state": result.State, "message": "原企微任务已重新受理，等待执行", "real_external_call_executed": false})
}
