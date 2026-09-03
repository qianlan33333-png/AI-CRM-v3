package http

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	nethttp "net/http"
	"strconv"
	"strings"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerapp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/app"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	platformaudit "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

type Authenticator interface {
	Authenticate(context.Context, *nethttp.Request) (accessdomain.Principal, error)
}

type CSRFAuthorizer interface {
	AuthorizeCSRF(context.Context, *nethttp.Request) (accessdomain.Principal, error)
}

type Auditor interface {
	Append(context.Context, platformaudit.Event) (platformaudit.Event, error)
}

type Config struct {
	UnitOfWork platformport.UnitOfWork
	Auth       Authenticator
	CSRF       CSRFAuthorizer
	Directory  customerapp.Directory
	Store      customerapp.Store
	Identities identityport.DirectoryIdentityReader
	Audit      Auditor
}

type Handler struct {
	uow        platformport.UnitOfWork
	auth       Authenticator
	csrf       CSRFAuthorizer
	directory  customerapp.Directory
	store      customerapp.Store
	identities identityport.DirectoryIdentityReader
	audit      Auditor
}

func NewHandler(config Config) (*Handler, error) {
	if config.UnitOfWork == nil || config.Auth == nil || config.CSRF == nil || config.Directory.Store == nil || config.Store == nil || config.Identities == nil || config.Audit == nil {
		return nil, errors.New("customer HTTP dependencies are required")
	}
	return &Handler{uow: config.UnitOfWork, auth: config.Auth, csrf: config.CSRF, directory: config.Directory,
		store: config.Store, identities: config.Identities, audit: config.Audit}, nil
}

func (handler *Handler) Routes() nethttp.Handler {
	mux := nethttp.NewServeMux()
	mux.HandleFunc("GET /api/admin/customers", handler.list)
	mux.HandleFunc("GET /api/admin/customers/{customer_id}", handler.detail)
	mux.HandleFunc("POST /api/admin/customers/{customer_id}/phone-reveal", handler.revealPhone)
	return mux
}

func (handler *Handler) list(response nethttp.ResponseWriter, request *nethttp.Request) {
	if _, err := handler.auth.Authenticate(request.Context(), request); err != nil {
		handler.writeError(response, err)
		return
	}
	values := request.URL.Query()
	for key, entries := range values {
		if len(entries) != 1 || (key != "keyword" && key != "phone" && key != "status" && key != "cursor" && key != "limit") {
			handler.writeError(response, customerapp.ErrInvalidQuery)
			return
		}
	}
	limit := 0
	var err error
	if raw := values.Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || strconv.Itoa(limit) != raw {
			handler.writeError(response, customerapp.ErrInvalidQuery)
			return
		}
	}
	requestData := customerapp.ListRequest{Limit: limit, Cursor: values.Get("cursor"), Filters: customerapp.Filters{
		Keyword: values.Get("keyword"), Status: values.Get("status"),
	}}
	var page customerapp.Page
	err = handler.uow.Within(request.Context(), func(txContext context.Context) error {
		if phone := values.Get("phone"); phone != "" {
			phone, normalizeErr := normalizeCNPhoneSearch(phone)
			if normalizeErr != nil {
				return normalizeErr
			}
			customerID, found, queryErr := handler.identities.CustomerForPhone(txContext, phone)
			if queryErr != nil {
				return queryErr
			}
			requestData.Filters.PhoneCustomerID = customerID
			requestData.Filters.PhoneMatchNone = !found
		}
		var queryErr error
		page, queryErr = handler.directory.List(txContext, requestData)
		return queryErr
	})
	if err != nil {
		handler.writeError(response, err)
		return
	}
	writeJSON(response, nethttp.StatusOK, page)
}

func (handler *Handler) detail(response nethttp.ResponseWriter, request *nethttp.Request) {
	if _, err := handler.auth.Authenticate(request.Context(), request); err != nil {
		handler.writeError(response, err)
		return
	}
	id, err := positiveID(request.PathValue("customer_id"))
	if err != nil {
		handler.writeError(response, err)
		return
	}
	var detail customerapp.Detail
	var identities []identityport.DirectoryIdentitySummary
	var phones []identityport.MaskedPhone
	err = handler.uow.Within(request.Context(), func(txContext context.Context) error {
		var queryErr error
		detail, queryErr = handler.store.Detail(txContext, customerdomain.CustomerID(id))
		if queryErr != nil {
			return queryErr
		}
		identities, phones, queryErr = handler.identities.DirectoryIdentities(txContext, customerdomain.CustomerID(id))
		return queryErr
	})
	if err != nil {
		handler.writeError(response, err)
		return
	}
	writeJSON(response, nethttp.StatusOK, map[string]any{"customer": detail, "identities": identities, "phones": phones})
}

func (handler *Handler) revealPhone(response nethttp.ResponseWriter, request *nethttp.Request) {
	principal, err := handler.csrf.AuthorizeCSRF(request.Context(), request)
	if err != nil {
		handler.writeError(response, err)
		return
	}
	if !hasRole(principal, accessdomain.RoleAdmin) && !hasRole(principal, accessdomain.RoleSuperAdmin) {
		handler.writeError(response, accessdomain.ErrPermissionDenied)
		return
	}
	id, err := positiveID(request.PathValue("customer_id"))
	if err != nil {
		handler.writeError(response, err)
		return
	}
	var phone string
	var found bool
	err = handler.uow.Within(request.Context(), func(txContext context.Context) error {
		var queryErr error
		phone, found, queryErr = handler.identities.RevealPhone(txContext, customerdomain.CustomerID(id))
		if queryErr != nil {
			return queryErr
		}
		if !found {
			return customerapp.ErrNotFound
		}
		payload, marshalErr := json.Marshal(map[string]any{"purpose": "customer_detail_query"})
		if marshalErr != nil {
			return marshalErr
		}
		key, keyErr := revealAuditKey(principal.InternalID, id)
		if keyErr != nil {
			return keyErr
		}
		_, queryErr = handler.audit.Append(txContext, platformaudit.Event{IdempotencyKey: key,
			Action: "customer.phone_revealed", ActorType: string(principal.Kind), ActorID: strconv.FormatInt(principal.InternalID, 10),
			ResourceType: "customer", ResourceID: strconv.FormatInt(id, 10), Payload: payload})
		return queryErr
	})
	if err != nil {
		handler.writeError(response, err)
		return
	}
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Pragma", "no-cache")
	writeJSON(response, nethttp.StatusOK, map[string]any{"phone": localCNPhone(phone)})
}

func normalizeCNPhoneSearch(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if strings.HasPrefix(value, "+86") {
		value = strings.TrimPrefix(value, "+86")
	}
	if len(value) != 11 || value[0] != '1' || value[1] < '3' || value[1] > '9' {
		return "", identitydomain.ErrInvalidReference
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return "", identitydomain.ErrInvalidReference
		}
	}
	return "+86" + value, nil
}

func localCNPhone(value string) string {
	if strings.HasPrefix(value, "+86") {
		return strings.TrimPrefix(value, "+86")
	}
	return value
}

func revealAuditKey(actorID, customerID int64) (idempotency.Key, error) {
	random := make([]byte, 12)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return idempotency.Parse("phone-reveal:" + strconv.FormatInt(actorID, 10) + ":" + strconv.FormatInt(customerID, 10) + ":" + hex.EncodeToString(random))
}

func hasRole(principal accessdomain.Principal, role accessdomain.Role) bool {
	for _, current := range principal.Roles {
		if current == role {
			return true
		}
	}
	return false
}

func positiveID(raw string) (int64, error) {
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 1 || strconv.FormatInt(value, 10) != raw {
		return 0, customerapp.ErrInvalidQuery
	}
	return value, nil
}

func (handler *Handler) writeError(response nethttp.ResponseWriter, err error) {
	status, code := nethttp.StatusInternalServerError, "internal_error"
	switch {
	case errors.Is(err, accessdomain.ErrAuthentication), errors.Is(err, accessdomain.ErrInvalidPrincipal):
		status, code = nethttp.StatusUnauthorized, "authentication_required"
	case errors.Is(err, accessdomain.ErrCSRFRequired):
		status, code = nethttp.StatusForbidden, "csrf_required"
	case errors.Is(err, accessdomain.ErrPermissionDenied):
		status, code = nethttp.StatusForbidden, "permission_denied"
	case errors.Is(err, customerapp.ErrNotFound):
		status, code = nethttp.StatusNotFound, "customer_not_found"
	case errors.Is(err, customerapp.ErrInvalidQuery), errors.Is(err, customerapp.ErrInvalidCursor), errors.Is(err, identitydomain.ErrInvalidReference):
		status, code = nethttp.StatusBadRequest, "invalid_request"
	}
	writeJSON(response, status, map[string]any{"ok": false, "error": code})
}

func writeJSON(response nethttp.ResponseWriter, status int, payload any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(payload)
}
