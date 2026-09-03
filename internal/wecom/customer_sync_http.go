package wecom

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	nethttp "net/http"
	"strconv"
	"strings"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
)

type CustomerSyncAuthenticator interface {
	Authenticate(context.Context, *nethttp.Request) (accessdomain.Principal, error)
}
type CustomerSyncCSRF interface {
	AuthorizeCSRF(context.Context, *nethttp.Request) (accessdomain.Principal, error)
}

type CustomerSyncHTTPHandler struct {
	Service CustomerSyncService
	Auth    CustomerSyncAuthenticator
	CSRF    CustomerSyncCSRF
}

func (handler CustomerSyncHTTPHandler) Routes() nethttp.Handler {
	mux := nethttp.NewServeMux()
	mux.HandleFunc("POST /api/admin/customer-sync-runs", handler.create)
	mux.HandleFunc("GET /api/admin/customer-sync-runs", handler.list)
	mux.HandleFunc("GET /api/admin/customer-sync-runs/{run_id}", handler.get)
	return mux
}

func (handler CustomerSyncHTTPHandler) create(response nethttp.ResponseWriter, request *nethttp.Request) {
	principal, err := handler.CSRF.AuthorizeCSRF(request.Context(), request)
	if err != nil {
		writeSyncError(response, err)
		return
	}
	if !principal.IsSuperAdmin() {
		writeSyncError(response, accessdomain.ErrPermissionDenied)
		return
	}
	if request.Body != nil && request.ContentLength > 0 {
		writeSyncError(response, errors.New("body_not_allowed"))
		return
	}
	rawKey := strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	if _, err = idempotency.Parse(rawKey); err != nil {
		writeSyncError(response, err)
		return
	}
	digest := sha256.Sum256([]byte(rawKey))
	run, replay, err := handler.Service.Create(request.Context(), CreateCustomerSyncRun{RunKey: "manual:" + hex.EncodeToString(digest[:]), Trigger: "manual",
		CorpScope: "wecom-corp:" + handler.Service.CorpID, RequestedBy: principal.InternalID})
	if err != nil {
		writeSyncError(response, err)
		return
	}
	status := nethttp.StatusAccepted
	if replay {
		status = nethttp.StatusOK
	}
	writeSyncJSON(response, status, map[string]any{"run": run, "replayed": replay})
}

func (handler CustomerSyncHTTPHandler) list(response nethttp.ResponseWriter, request *nethttp.Request) {
	if _, err := handler.Auth.Authenticate(request.Context(), request); err != nil {
		writeSyncError(response, err)
		return
	}
	limit := 20
	if raw := request.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || strconv.Itoa(value) != raw || value < 1 || value > 100 {
			writeSyncError(response, errors.New("invalid_limit"))
			return
		}
		limit = value
	}
	for key, values := range request.URL.Query() {
		if key != "limit" || len(values) != 1 {
			writeSyncError(response, errors.New("invalid_query"))
			return
		}
	}
	runs, err := handler.Service.List(request.Context(), limit)
	if err != nil {
		writeSyncError(response, err)
		return
	}
	writeSyncJSON(response, nethttp.StatusOK, map[string]any{"items": runs})
}

func (handler CustomerSyncHTTPHandler) get(response nethttp.ResponseWriter, request *nethttp.Request) {
	if _, err := handler.Auth.Authenticate(request.Context(), request); err != nil {
		writeSyncError(response, err)
		return
	}
	id, err := strconv.ParseInt(request.PathValue("run_id"), 10, 64)
	if err != nil || id < 1 || strconv.FormatInt(id, 10) != request.PathValue("run_id") {
		writeSyncError(response, errors.New("invalid_id"))
		return
	}
	run, err := handler.Service.Get(request.Context(), id)
	if err != nil {
		writeSyncError(response, err)
		return
	}
	writeSyncJSON(response, nethttp.StatusOK, run)
}

func writeSyncError(response nethttp.ResponseWriter, err error) {
	status, code := nethttp.StatusInternalServerError, "internal_error"
	switch {
	case errors.Is(err, accessdomain.ErrAuthentication), errors.Is(err, accessdomain.ErrInvalidPrincipal):
		status, code = 401, "authentication_required"
	case errors.Is(err, accessdomain.ErrCSRFRequired):
		status, code = 403, "csrf_required"
	case errors.Is(err, accessdomain.ErrPermissionDenied):
		status, code = 403, "permission_denied"
	case errors.Is(err, ErrSyncConflict):
		status, code = 409, "sync_already_active"
	case errors.Is(err, ErrSyncNotFound):
		status, code = 404, "sync_run_not_found"
	case errors.Is(err, ErrSyncNotReady):
		status, code = 503, "provider_disabled"
	case errors.Is(err, idempotency.ErrInvalidKey), strings.HasPrefix(err.Error(), "invalid_") || err.Error() == "body_not_allowed":
		status, code = 400, "invalid_request"
	}
	writeSyncJSON(response, status, map[string]any{"ok": false, "error": code})
}
func writeSyncJSON(response nethttp.ResponseWriter, status int, payload any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(payload)
}
