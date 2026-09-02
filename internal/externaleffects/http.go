package externaleffects

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
)

// RequestSecurity is supplied by the composition root. Read APIs require an
// authenticated administrator; all controls additionally require CSRF.
type RequestSecurity interface {
	Authenticate(ctx context.Context, request *http.Request) (accessdomain.Principal, error)
	AuthorizeCSRF(ctx context.Context, request *http.Request) (accessdomain.Principal, error)
}

type HTTPHandler struct {
	repository *Repository
	security   RequestSecurity
}

func NewHTTPHandler(repository *Repository, security RequestSecurity) (*HTTPHandler, error) {
	if repository == nil || security == nil {
		return nil, ErrInvalid
	}
	return &HTTPHandler{repository: repository, security: security}, nil
}

func (h *HTTPHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.repository == nil || h.security == nil {
		writeEffectError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/admin/external-effects")
	if path == "" || path == "/" {
		if r.Method != http.MethodGet {
			writeEffectError(w, http.StatusMethodNotAllowed, "method_not_allowed")
			return
		}
		if !h.read(w, r) {
			return
		}
		limit := 50
		if raw := r.URL.Query().Get("limit"); raw != "" {
			var err error
			limit, err = strconv.Atoi(raw)
			if err != nil {
				writeEffectError(w, http.StatusBadRequest, "invalid_request")
				return
			}
		}
		items, err := h.repository.List(r.Context(), limit)
		if err != nil {
			writeEffectError(w, http.StatusInternalServerError, "unavailable")
			return
		}
		writeEffectJSON(w, http.StatusOK, map[string]any{"items": items})
		return
	}
	if path == "/diagnostics" {
		if r.Method != http.MethodGet || !h.read(w, r) {
			return
		}
		value, err := h.repository.Diagnostics(r.Context())
		if err != nil {
			writeEffectError(w, http.StatusInternalServerError, "unavailable")
			return
		}
		writeEffectJSON(w, http.StatusOK, value)
		return
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeEffectError(w, http.StatusNotFound, "not_found")
		return
	}
	id := parts[0]
	if len(parts) == 1 {
		if r.Method != http.MethodGet || !h.read(w, r) {
			return
		}
		value, err := h.repository.Get(r.Context(), id)
		h.writeResult(w, value, err)
		return
	}
	if len(parts) != 2 || r.Method != http.MethodPost || !h.mutate(w, r) {
		return
	}
	key := digestHeader(r.Header.Get("Idempotency-Key"))
	if key == "" {
		writeEffectError(w, http.StatusBadRequest, "idempotency_key_required")
		return
	}
	command := ControlCommand{EffectID: id, ReceiptKey: key}
	var value Projection
	var err error
	switch parts[1] {
	case "cancel":
		value, _, err = h.repository.Cancel(r.Context(), command)
	case "retry":
		value, _, err = h.repository.Retry(r.Context(), command)
	case "reconcile":
		var body struct {
			EvidenceDigest string `json:"evidence_digest"`
		}
		if decodeEffectJSON(r, &body) != nil {
			writeEffectError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		command.EvidenceDigest = Digest(body.EvidenceDigest)
		value, _, err = h.repository.Reconcile(r.Context(), command)
	default:
		writeEffectError(w, http.StatusNotFound, "not_found")
		return
	}
	h.writeResult(w, value, err)
}

func (h *HTTPHandler) read(w http.ResponseWriter, r *http.Request) bool {
	if _, err := h.security.Authenticate(r.Context(), r); err != nil {
		writeEffectError(w, http.StatusForbidden, "forbidden")
		return false
	}
	return true
}
func (h *HTTPHandler) mutate(w http.ResponseWriter, r *http.Request) bool {
	if _, err := h.security.AuthorizeCSRF(r.Context(), r); err != nil {
		writeEffectError(w, http.StatusForbidden, "csrf_required")
		return false
	}
	return true
}
func (h *HTTPHandler) writeResult(w http.ResponseWriter, v Projection, err error) {
	switch {
	case err == nil:
		writeEffectJSON(w, http.StatusOK, v)
	case errors.Is(err, ErrNotFound):
		writeEffectError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, ErrPayloadMismatch), errors.Is(err, ErrTransition), errors.Is(err, ErrReconcileRequired):
		writeEffectError(w, http.StatusConflict, "state_conflict")
	case errors.Is(err, ErrInvalid):
		writeEffectError(w, http.StatusBadRequest, "invalid_request")
	default:
		writeEffectError(w, http.StatusInternalServerError, "unavailable")
	}
}
func digestHeader(raw string) Digest {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 200 {
		return ""
	}
	sum := sha256.Sum256([]byte(raw))
	return Digest("sha256:" + hex.EncodeToString(sum[:]))
}
func decodeEffectJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 16<<10))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}
func writeEffectJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeEffectError(w http.ResponseWriter, status int, code string) {
	writeEffectJSON(w, status, map[string]any{"ok": false, "error": code, "code": code})
}
