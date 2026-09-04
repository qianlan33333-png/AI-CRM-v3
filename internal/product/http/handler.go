// Package http exposes the bounded Product compatibility surface used by the
// frozen v2 admin bundle.  The handler only composes Product-owned
// applications; Media, Tag, Channel, OneID, orders, entitlements and member
// data stay behind their own ports (or are explicitly fail-closed).
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
	productapp "github.com/qianlan33333-png/AI-CRM-v3/internal/product/app"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

const (
	maxProductBodyBytes = 128 << 10
	maxProductLimit     = productapp.MaximumLimit
	maxProductOffset    = productapp.MaximumLegacyOffset
)

// RequestSecurity is the stable v3 request boundary.  A Product handler never
// trusts actor fields from the donor request body.
type RequestSecurity interface {
	Authenticate(context.Context, *http.Request) (accessdomain.Principal, error)
	AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error)
}

// CatalogApplication is the transport-neutral ordinary Product application.
// The concrete app service is supplied by the composition root.
type CatalogApplication interface {
	List(context.Context, string, int32) (productport.Page, error)
	Get(context.Context, productport.ID) (productport.Product, error)
	Create(context.Context, productport.CreateCommand) (productport.Product, error)
	Update(context.Context, productport.UpdateCommand) (productport.Product, error)
}

type Handler struct {
	catalog   CatalogApplication
	lifecycle productport.LocalProductLifecycleApplication
	service   productport.ServicePeriodApplication
	external  productport.CommerceExternalPushApplication
	security  RequestSecurity
}

func NewHandler(catalog CatalogApplication, lifecycle productport.LocalProductLifecycleApplication, service productport.ServicePeriodApplication, external productport.CommerceExternalPushApplication, security RequestSecurity) (*Handler, error) {
	if catalog == nil || lifecycle == nil || service == nil || external == nil || security == nil {
		return nil, errors.New("product HTTP dependencies are required")
	}
	return &Handler{catalog: catalog, lifecycle: lifecycle, service: service, external: external, security: security}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.catalog == nil || h.lifecycle == nil || h.service == nil || h.external == nil || h.security == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable")
		return
	}
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case path == "/api/v1/products":
		h.ordinaryRoot(w, r)
	case strings.HasPrefix(path, "/api/v1/products/"):
		h.ordinaryTail(w, r, strings.TrimPrefix(path, "/api/v1/products/"))
	case path == "/api/admin/wechat-pay/products":
		h.ordinaryAdminRoot(w, r)
	case strings.HasPrefix(path, "/api/admin/wechat-pay/products/"):
		h.ordinaryAdminTail(w, r, strings.TrimPrefix(path, "/api/admin/wechat-pay/products/"))
	case path == "/api/admin/service-period-products":
		h.serviceRoot(w, r)
	case strings.HasPrefix(path, "/api/admin/service-period-products/"):
		h.serviceTail(w, r, strings.TrimPrefix(path, "/api/admin/service-period-products/"))
	default:
		writeError(w, http.StatusNotFound, "not_found")
	}
}

func (h *Handler) ordinaryRoot(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if !h.read(w, r) {
			return
		}
		if !onlyQuery(r, "cursor", "limit") {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		limit, ok := queryLimit(r, productapp.DefaultLimit)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		cursor := r.URL.Query().Get("cursor")
		page, err := h.catalog.List(r.Context(), cursor, limit)
		if err != nil {
			resultError(w, err)
			return
		}
		items := make([]productResponse, 0, len(page.Items))
		for _, item := range page.Items {
			projected, projectionErr := productResponseFrom(item)
			if projectionErr != nil {
				resultError(w, projectionErr)
				return
			}
			items = append(items, projected)
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": page.NextCursor})
	case http.MethodPost:
		principal, ok := h.write(w, r)
		if !ok {
			return
		}
		if r.URL.RawQuery != "" {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		var body createRequest
		if decodeJSON(r, &body) != nil {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		key, err := requestIdempotencyKey(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		projection := body.AdminProjection
		if len(projection) == 0 {
			projection = productapp.DefaultLegacyAdminProjection()
		}
		product, err := h.catalog.Create(r.Context(), productport.CreateCommand{
			ProductCode: body.ProductCode, Name: body.Name, Description: body.Description,
			PriceMinor: body.PriceMinor, Currency: body.Currency, StockQuantity: body.StockQuantity,
			Images: body.Images, LegacyAdminProjection: projection, Actor: principal.InternalID,
			IdempotencyKey: key,
		})
		if err != nil {
			resultError(w, err)
			return
		}
		projected, projectionErr := productResponseFrom(product)
		if projectionErr != nil {
			resultError(w, projectionErr)
			return
		}
		writeJSON(w, http.StatusCreated, projected)
	default:
		methodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}

func (h *Handler) ordinaryTail(w http.ResponseWriter, r *http.Request, tail string) {
	parts := strings.Split(tail, "/")
	if len(parts) == 2 && parts[1] == "local-entitlements" {
		if r.Method != http.MethodGet {
			methodNotAllowed(w, http.MethodGet)
			return
		}
		if !h.read(w, r) || !onlyQuery(r, "limit") {
			if r.URL.RawQuery != "" && !onlyQuery(r, "limit") {
				writeError(w, http.StatusBadRequest, "invalid_request")
			}
			return
		}
		id, err := parseID(parts[0])
		if err != nil {
			writeError(w, http.StatusNotFound, "not_found")
			return
		}
		limit, ok := queryLimit(r, 100)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		if limit > 100 {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		// Entitlements are intentionally not owned by Product in PR04.  Validate
		// the Product through its application and return a truthful empty local
		// projection; no order/customer/entitlement table is queried.
		if _, err = h.catalog.Get(r.Context(), productport.ID(id)); err != nil {
			resultError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": []any{}})
		return
	}
	if len(parts) != 1 {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	id, err := parseID(parts[0])
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	switch r.Method {
	case http.MethodGet:
		if !h.read(w, r) {
			return
		}
		if r.URL.RawQuery != "" {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		product, getErr := h.catalog.Get(r.Context(), productport.ID(id))
		if getErr != nil {
			resultError(w, getErr)
			return
		}
		projected, projectionErr := productResponseFrom(product)
		if projectionErr != nil {
			resultError(w, projectionErr)
			return
		}
		writeJSON(w, http.StatusOK, projected)
	case http.MethodPut:
		principal, ok := h.write(w, r)
		if !ok {
			return
		}
		if r.URL.RawQuery != "" {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		var body updateRequest
		if decodeJSON(r, &body) != nil {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		key, keyErr := requestIdempotencyKey(r)
		if keyErr != nil {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		product, updateErr := h.catalog.Update(r.Context(), productport.UpdateCommand{
			ID: productport.ID(id), ExpectedVersion: body.ExpectedVersion, Name: body.Name,
			Description: body.Description, PriceMinor: body.PriceMinor, Currency: body.Currency,
			StockQuantity: body.StockQuantity, Images: body.Images, LegacyAdminProjection: body.AdminProjection,
			Actor: principal.InternalID, IdempotencyKey: key,
		})
		if updateErr != nil {
			resultError(w, updateErr)
			return
		}
		projected, projectionErr := productResponseFrom(product)
		if projectionErr != nil {
			resultError(w, projectionErr)
			return
		}
		writeJSON(w, http.StatusOK, projected)
	default:
		methodNotAllowed(w, http.MethodGet+", "+http.MethodPut)
	}
}

func (h *Handler) ordinaryAdminRoot(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		// The frozen ordinary list uses /api/v1/products. This compatibility
		// root is deliberately not a second list contract.
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	methodNotAllowed(w, http.MethodGet)
}

func (h *Handler) ordinaryAdminTail(w http.ResponseWriter, r *http.Request, tail string) {
	parts := strings.Split(tail, "/")
	if len(parts) < 1 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	id, err := parseID(parts[0])
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	switch strings.Join(parts[1:], "/") {
	case "enable":
		h.localEnable(w, r, id, true)
	case "disable":
		h.localEnable(w, r, id, false)
	case "copy":
		h.localCopy(w, r, id)
	case "share":
		h.localShare(w, r, id)
	case "external-push":
		h.externalRoute(w, r, id, productport.ExternalPushWeChatPay)
	case "external-push/test":
		h.externalTest(w, r, id, productport.ExternalPushWeChatPay)
	case "":
		if r.Method != http.MethodDelete {
			methodNotAllowed(w, http.MethodDelete)
			return
		}
		h.localDelete(w, r, id)
	default:
		writeError(w, http.StatusNotFound, "not_found")
	}
}

func (h *Handler) localEnable(w http.ResponseWriter, r *http.Request, id int64, enabled bool) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	principal, ok := h.write(w, r)
	if !ok {
		return
	}
	var body versionRequest
	if decodeJSON(r, &body) != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	key, err := requestIdempotencyKey(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	result, err := h.lifecycle.SetLocalProductEnabled(r.Context(), productport.SetLocalProductEnabledCommand{ID: productport.ID(id), ExpectedVersion: body.ExpectedVersion, Enabled: enabled, Actor: principal.InternalID, IdempotencyKey: key})
	if err != nil {
		resultError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) localCopy(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	principal, ok := h.write(w, r)
	if !ok {
		return
	}
	var body versionRequest
	if decodeJSON(r, &body) != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	key, err := requestIdempotencyKey(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	result, err := h.lifecycle.CopyLocalProduct(r.Context(), productport.CopyLocalProductCommand{ID: productport.ID(id), ExpectedVersion: body.ExpectedVersion, Actor: principal.InternalID, IdempotencyKey: key})
	if err != nil {
		resultError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (h *Handler) localDelete(w http.ResponseWriter, r *http.Request, id int64) {
	principal, ok := h.write(w, r)
	if !ok {
		return
	}
	var body versionRequest
	if decodeOptionalJSON(r, &body) != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	key, err := requestIdempotencyKey(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	result, err := h.lifecycle.DeleteLocalProduct(r.Context(), productport.DeleteLocalProductCommand{ID: productport.ID(id), ExpectedVersion: body.ExpectedVersion, Actor: principal.InternalID, IdempotencyKey: key})
	if err != nil {
		resultError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) localShare(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	if !h.read(w, r) {
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	result, err := h.lifecycle.ShareLocalProduct(r.Context(), productport.ID(id))
	if err != nil {
		if errors.Is(err, productapp.ErrLocalProductNotEnabled) {
			writeError(w, http.StatusConflict, "product_not_enabled")
			return
		}
		resultError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) serviceRoot(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		if !h.read(w, r) {
			return
		}
		if !onlyQuery(r, "limit", "offset") {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		limit, ok := queryLimit(r, productapp.DefaultLimit)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		offset, ok := queryOffset(r)
		if !ok {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		page, err := h.service.ListServicePeriodProducts(r.Context(), limit, offset)
		if err != nil {
			resultError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, page)
	case http.MethodPost:
		principal, ok := h.write(w, r)
		if !ok {
			return
		}
		if r.URL.RawQuery != "" {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		var body serviceCreateRequest
		if decodeJSON(r, &body) != nil {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		key, err := requestIdempotencyKey(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		projection := body.AdminProjection
		if len(projection) == 0 {
			projection = productapp.DefaultLegacyAdminProjection()
		}
		product, err := h.service.CreateServicePeriodProduct(r.Context(), productport.CreateServicePeriodProductCommand{
			ProductCode: body.ProductCode, Name: body.Name, Description: body.Description,
			PriceMinor: body.PriceMinor, Currency: body.Currency, StockQuantity: body.StockQuantity,
			Images: body.Images, AdminProjection: projection, Actor: principal.InternalID, IdempotencyKey: key,
		})
		if err != nil {
			resultError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "product": product})
	default:
		methodNotAllowed(w, http.MethodGet+", "+http.MethodPost)
	}
}

func (h *Handler) serviceTail(w http.ResponseWriter, r *http.Request, tail string) {
	parts := strings.Split(tail, "/")
	if len(parts) < 1 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	id, err := parseID(parts[0])
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if len(parts) == 1 {
		h.serviceDetail(w, r, id)
		return
	}
	suffix := strings.Join(parts[1:], "/")
	switch suffix {
	case "enable":
		h.serviceEnable(w, r, id, true)
	case "disable":
		h.serviceEnable(w, r, id, false)
	case "copy":
		h.serviceCopy(w, r, id)
	case "share":
		h.serviceShare(w, r, id)
	case "external-push":
		h.externalRoute(w, r, id, productport.ExternalPushServicePeriod)
	case "external-push/test":
		h.externalTest(w, r, id, productport.ExternalPushServicePeriod)
	case "members":
		h.serviceMembers(w, r, id)
	case "member-grid/access":
		h.memberGridAccess(w, r, id)
	case "member-grid/schema":
		h.memberGridSchema(w, r, id)
	case "member-views":
		h.memberViews(w, r, id)
	case "member-grid/share-settings":
		h.memberGridShareSettings(w, r, id)
	default:
		// Member Grid data, writes, history and customer/entitlement joins are
		// outside PR04. Unknown and mutating paths fail closed.
		writeError(w, http.StatusNotFound, "not_found")
	}
}

func (h *Handler) serviceDetail(w http.ResponseWriter, r *http.Request, id int64) {
	switch r.Method {
	case http.MethodGet:
		if !h.read(w, r) {
			return
		}
		if r.URL.RawQuery != "" {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		product, err := h.service.GetServicePeriodProduct(r.Context(), productport.ID(id))
		if err != nil {
			resultError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "product": product})
	case http.MethodPut:
		principal, ok := h.write(w, r)
		if !ok {
			return
		}
		if r.URL.RawQuery != "" {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		var body serviceUpdateRequest
		if decodeJSON(r, &body) != nil {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		key, err := requestIdempotencyKey(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		product, err := h.service.UpdateServicePeriodProduct(r.Context(), productport.UpdateServicePeriodProductCommand{
			ID: productport.ID(id), ExpectedVersion: body.ExpectedVersion, Name: body.Name,
			Description: body.Description, PriceMinor: body.PriceMinor, Currency: body.Currency,
			StockQuantity: body.StockQuantity, Images: body.Images, AdminProjection: body.AdminProjection,
			Actor: principal.InternalID, IdempotencyKey: key,
		})
		if err != nil {
			resultError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "product": product})
	case http.MethodDelete:
		principal, ok := h.write(w, r)
		if !ok {
			return
		}
		var body versionRequest
		if decodeJSON(r, &body) != nil {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		key, err := requestIdempotencyKey(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		product, err := h.service.ArchiveServicePeriodProduct(r.Context(), productport.ArchiveServicePeriodProductCommand{ID: productport.ID(id), ExpectedVersion: body.ExpectedVersion, Actor: principal.InternalID, IdempotencyKey: key})
		if err != nil {
			resultError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "product": product})
	default:
		methodNotAllowed(w, http.MethodGet+", "+http.MethodPut+", "+http.MethodDelete)
	}
}

func (h *Handler) serviceEnable(w http.ResponseWriter, r *http.Request, id int64, enabled bool) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	principal, ok := h.write(w, r)
	if !ok {
		return
	}
	var body versionRequest
	if decodeJSON(r, &body) != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	key, err := requestIdempotencyKey(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	product, err := h.service.SetServicePeriodProductEnabled(r.Context(), productport.SetServicePeriodProductEnabledCommand{ID: productport.ID(id), ExpectedVersion: body.ExpectedVersion, Enabled: enabled, Actor: principal.InternalID, IdempotencyKey: key})
	if err != nil {
		resultError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "product": product})
}

func (h *Handler) serviceCopy(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	principal, ok := h.write(w, r)
	if !ok {
		return
	}
	var body versionRequest
	if decodeJSON(r, &body) != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	key, err := requestIdempotencyKey(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	product, err := h.service.CopyServicePeriodProduct(r.Context(), productport.CopyServicePeriodProductCommand{ID: productport.ID(id), ExpectedVersion: body.ExpectedVersion, Actor: principal.InternalID, IdempotencyKey: key})
	if err != nil {
		resultError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "product": product})
}

func (h *Handler) serviceShare(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	if !h.read(w, r) {
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	product, err := h.service.GetServicePeriodProduct(r.Context(), productport.ID(id))
	if err != nil {
		resultError(w, err)
		return
	}
	if product.Lifecycle != productport.ServicePeriodEnabled {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "service_product_id": id, "public_path": "/p/service_period/" + strconv.FormatInt(id, 10), "local_only": true, "real_external_call_executed": false})
}

func (h *Handler) externalRoute(w http.ResponseWriter, r *http.Request, id int64, kind productport.ExternalPushProductKind) {
	switch r.Method {
	case http.MethodGet:
		if !h.read(w, r) {
			return
		}
		if r.URL.RawQuery != "" {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		configuration, err := h.external.GetExternalPushConfiguration(r.Context(), productport.ID(id), kind)
		if err != nil {
			resultError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, configuration)
	case http.MethodPut:
		principal, ok := h.write(w, r)
		if !ok {
			return
		}
		if r.URL.RawQuery != "" {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		var body externalConfigurationRequest
		if decodeJSON(r, &body) != nil {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		key, err := requestIdempotencyKey(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return
		}
		configuration, err := h.external.SaveExternalPushConfiguration(r.Context(), productport.SaveExternalPushConfigurationCommand{ProductID: productport.ID(id), ProductKind: kind, Enabled: body.Enabled, ConfigurationReference: body.ConfigurationReference, Actor: principal.InternalID, IdempotencyKey: key})
		if err != nil {
			resultError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, configuration)
	default:
		methodNotAllowed(w, http.MethodGet+", "+http.MethodPut)
	}
}

func (h *Handler) externalTest(w http.ResponseWriter, r *http.Request, id int64, kind productport.ExternalPushProductKind) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	principal, ok := h.write(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if err := decodeEmptyJSON(r); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	key, err := requestIdempotencyKey(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	test, err := h.external.QueueExternalPushTest(r.Context(), productport.QueueExternalPushTestCommand{ProductID: productport.ID(id), ProductKind: kind, Actor: principal.InternalID, IdempotencyKey: key})
	if err != nil {
		resultError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, test)
}

func (h *Handler) serviceMembers(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	if !h.read(w, r) {
		return
	}
	if !onlyQuery(r, "state", "source", "limit", "cursor") {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	query := r.URL.Query()
	if raw := query.Get("state"); raw != "" && raw != "active" && raw != "expired" && raw != "removed" && raw != "all" {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if raw := query.Get("source"); raw != "" && raw != "manual" && raw != "paid_order" {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if len(query.Get("cursor")) > 1024 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	limit, ok := queryLimit(r, productapp.DefaultLimit)
	if !ok || limit > 100 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if _, err := h.service.GetServicePeriodProduct(r.Context(), productport.ID(id)); err != nil {
		resultError(w, err)
		return
	}
	// PR04 does not own members. This exact empty list is a truthful
	// compatibility projection for the active form's metadata read.
	writeJSON(w, http.StatusOK, map[string]any{"items": []any{}, "limit": limit, "next_cursor": "", "has_more": false})
}

func (h *Handler) memberGridAccess(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	if !h.read(w, r) {
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if _, err := h.service.GetServicePeriodProduct(r.Context(), productport.ID(id)); err != nil {
		resultError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"product_id": id, "can_view": false, "can_query": false, "can_edit": false, "can_manage_views": false, "can_share": false})
}

func (h *Handler) memberGridSchema(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	if !h.read(w, r) {
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if _, err := h.service.GetServicePeriodProduct(r.Context(), productport.ID(id)); err != nil {
		resultError(w, err)
		return
	}
	columns := []map[string]any{
		{"key": "member_ref", "label": "成员引用", "type": "string", "nullable": false},
		{"key": "service_product_id", "label": "周期商品", "type": "integer", "nullable": false},
		{"key": "customer_id", "label": "客户", "type": "integer", "nullable": false},
		{"key": "state", "label": "状态", "type": "enum", "nullable": false},
		{"key": "source", "label": "来源", "type": "enum", "nullable": false},
		{"key": "starts_at", "label": "开始时间", "type": "timestamp", "nullable": false},
		{"key": "expires_at", "label": "到期时间", "type": "timestamp", "nullable": true},
		{"key": "expired_at", "label": "过期时间", "type": "timestamp", "nullable": true},
		{"key": "removed_at", "label": "移除时间", "type": "timestamp", "nullable": true},
		{"key": "version", "label": "版本", "type": "integer", "nullable": false},
		{"key": "updated_at", "label": "更新时间", "type": "timestamp", "nullable": false},
		{"key": "display_name", "label": "显示名", "type": "string", "nullable": false},
	}
	writeJSON(w, http.StatusOK, map[string]any{"service_product_id": id, "columns": columns})
}

func (h *Handler) memberViews(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	if !h.read(w, r) {
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if _, err := h.service.GetServicePeriodProduct(r.Context(), productport.ID(id)); err != nil {
		resultError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"product_id": id, "views": []map[string]any{{"id": "default", "name": "默认视图", "source": "built_in", "read_only": true}}})
}

func (h *Handler) memberGridShareSettings(w http.ResponseWriter, r *http.Request, id int64) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	if !h.read(w, r) {
		return
	}
	if r.URL.RawQuery != "" {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if _, err := h.service.GetServicePeriodProduct(r.Context(), productport.ID(id)); err != nil {
		resultError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"service_product_id": id, "saved_views": []any{}, "collaborators": []any{},
		"external_share_supported": false, "external_share_enabled": false,
		"external_share_version": 0, "real_external_call_executed": false,
		"collaborator_edit_is_local_metadata_only":    true,
		"collaborator_edit_grants_central_permission": false,
	})
}

type createRequest struct {
	ProductCode     string          `json:"product_code"`
	Name            string          `json:"name"`
	Description     string          `json:"description"`
	PriceMinor      int64           `json:"price_minor"`
	Currency        string          `json:"currency"`
	StockQuantity   int32           `json:"stock_quantity"`
	Images          []string        `json:"images"`
	AdminProjection json.RawMessage `json:"admin_projection"`
}

type updateRequest struct {
	ExpectedVersion int64           `json:"expected_version"`
	Name            string          `json:"name"`
	Description     string          `json:"description"`
	PriceMinor      int64           `json:"price_minor"`
	Currency        string          `json:"currency"`
	StockQuantity   int32           `json:"stock_quantity"`
	Images          []string        `json:"images"`
	AdminProjection json.RawMessage `json:"admin_projection"`
}

type serviceCreateRequest struct {
	ProductCode     string          `json:"product_code"`
	Name            string          `json:"name"`
	Description     string          `json:"description"`
	PriceMinor      int64           `json:"price_minor"`
	Currency        string          `json:"currency"`
	StockQuantity   int32           `json:"stock_quantity"`
	Images          []string        `json:"images"`
	AdminProjection json.RawMessage `json:"admin_projection"`
}

type serviceUpdateRequest struct {
	ExpectedVersion int64           `json:"expected_version"`
	Name            string          `json:"name"`
	Description     string          `json:"description"`
	PriceMinor      int64           `json:"price_minor"`
	Currency        string          `json:"currency"`
	StockQuantity   int32           `json:"stock_quantity"`
	Images          []string        `json:"images"`
	AdminProjection json.RawMessage `json:"admin_projection"`
}

type versionRequest struct {
	ExpectedVersion int64 `json:"expected_version"`
}

type externalConfigurationRequest struct {
	Enabled                bool   `json:"enabled"`
	ConfigurationReference string `json:"configuration_reference"`
}

type productResponse struct {
	ID               productport.ID                    `json:"id"`
	ProductCode      string                            `json:"product_code"`
	Name             string                            `json:"name"`
	Description      string                            `json:"description"`
	PriceMinor       int64                             `json:"price_minor"`
	Currency         string                            `json:"currency"`
	StockQuantity    int32                             `json:"stock_quantity"`
	Images           []string                          `json:"images"`
	AdminProjection  json.RawMessage                   `json:"admin_projection"`
	CreatedBy        int64                             `json:"created_by"`
	CreatedAt        interface{}                       `json:"created_at"`
	UpdatedAt        interface{}                       `json:"updated_at"`
	Version          int64                             `json:"version"`
	Lifecycle        productport.LocalProductLifecycle `json:"lifecycle"`
	Enabled          bool                              `json:"enabled"`
	PaidOrderCount   int64                             `json:"paid_order_count"`
	RefundOrderCount int64                             `json:"refund_order_count"`
	SoldCount        int64                             `json:"sold_count"`
}

func productResponseFrom(value productport.Product) (productResponse, error) {
	local, err := productapp.ProjectLocalProduct(value)
	if err != nil || value.PaidOrderCount < 0 || value.RefundOrderCount < 0 || value.SoldCount < 0 || value.SoldCount != maxInt64(0, value.PaidOrderCount-value.RefundOrderCount) {
		return productResponse{}, productapp.ErrUnavailable
	}
	return productResponse{ID: value.ID, ProductCode: value.ProductCode, Name: value.Name, Description: value.Description, PriceMinor: value.PriceMinor, Currency: value.Currency, StockQuantity: value.StockQuantity, Images: append([]string(nil), value.Images...), AdminProjection: append(json.RawMessage(nil), value.LegacyAdminProjection...), CreatedBy: value.CreatedBy, CreatedAt: value.CreatedAt.UTC(), UpdatedAt: value.UpdatedAt.UTC(), Version: value.Version, Lifecycle: local.Lifecycle, Enabled: local.Enabled, PaidOrderCount: value.PaidOrderCount, RefundOrderCount: value.RefundOrderCount, SoldCount: value.SoldCount}, nil
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

func (h *Handler) read(w http.ResponseWriter, r *http.Request) bool {
	principal, err := h.security.Authenticate(r.Context(), r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return false
	}
	if !canRead(principal) {
		writeError(w, http.StatusForbidden, "permission_denied")
		return false
	}
	return true
}

func (h *Handler) write(w http.ResponseWriter, r *http.Request) (accessdomain.Principal, bool) {
	principal, err := h.security.Authenticate(r.Context(), r)
	if err != nil {
		writeError(w, http.StatusUnauthorized, "unauthorized")
		return accessdomain.Principal{}, false
	}
	if !canWrite(principal) {
		writeError(w, http.StatusForbidden, "permission_denied")
		return accessdomain.Principal{}, false
	}
	if _, err = h.security.AuthorizeCSRF(r.Context(), r); err != nil {
		writeError(w, http.StatusForbidden, "csrf_required")
		return accessdomain.Principal{}, false
	}
	return principal, true
}

func canRead(principal accessdomain.Principal) bool {
	if principal.InternalID < 1 || (principal.Kind != accessdomain.KindAdmin && principal.Kind != accessdomain.KindStaff) {
		return false
	}
	for _, role := range principal.Roles {
		if role == accessdomain.RoleViewer || role == accessdomain.RoleAdmin || role == accessdomain.RoleSuperAdmin {
			return true
		}
	}
	return false
}

func canWrite(principal accessdomain.Principal) bool {
	if !canRead(principal) {
		return false
	}
	for _, role := range principal.Roles {
		if role == accessdomain.RoleAdmin || role == accessdomain.RoleSuperAdmin {
			return true
		}
	}
	return false
}

func decodeJSON(r *http.Request, value any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxProductBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}

func decodeOptionalJSON(r *http.Request, value any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxProductBodyBytes))
	decoder.DisallowUnknownFields()
	err := decoder.Decode(value)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}

func decodeEmptyJSON(r *http.Request) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxProductBodyBytes))
	var value any
	if err := decoder.Decode(&value); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("trailing JSON")
	}
	return nil
}

func requestIdempotencyKey(r *http.Request) (string, error) {
	values := r.Header.Values("Idempotency-Key")
	if len(values) > 1 {
		return "", errors.New("duplicate idempotency key")
	}
	if len(values) == 1 {
		return values[0], nil
	}
	return compatibilityIdempotencyKey(rand.Read)
}

func compatibilityIdempotencyKey(read func([]byte) (int, error)) (string, error) {
	var raw [20]byte
	if _, err := read(raw[:]); err != nil {
		return "", err
	}
	return "product_compat_" + hex.EncodeToString(raw[:]), nil
}

func parseID(value string) (int64, error) {
	if value == "" || strings.HasPrefix(value, "0") {
		return 0, errors.New("invalid id")
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return 0, errors.New("invalid id")
		}
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 1 || strconv.FormatInt(parsed, 10) != value {
		return 0, errors.New("invalid id")
	}
	return parsed, nil
}

func onlyQuery(r *http.Request, allowed ...string) bool {
	set := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		set[name] = struct{}{}
	}
	for key, values := range r.URL.Query() {
		if _, ok := set[key]; !ok || len(values) != 1 {
			return false
		}
	}
	return true
}

func queryLimit(r *http.Request, defaultValue int32) (int32, bool) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return defaultValue, true
	}
	value, err := parseInt32(raw)
	return value, err == nil && value >= 1 && value <= maxProductLimit
}

func queryOffset(r *http.Request) (int32, bool) {
	raw := r.URL.Query().Get("offset")
	if raw == "" {
		return 0, true
	}
	value, err := parseInt32(raw)
	return value, err == nil && value >= 0 && value <= maxProductOffset
}

func parseInt32(raw string) (int32, error) {
	if raw == "" || (len(raw) > 1 && raw[0] == '0') {
		return 0, errors.New("invalid integer")
	}
	value, err := strconv.ParseInt(raw, 10, 32)
	return int32(value), err
}

func resultError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, productapp.ErrInvalidProduct), errors.Is(err, productapp.ErrInvalidCursor):
		writeError(w, http.StatusBadRequest, "invalid_request")
	case errors.Is(err, productapp.ErrNotFound), errors.Is(err, productport.ErrProductReadNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, productapp.ErrConflict), errors.Is(err, productapp.ErrExternalPushNotConfigured), errors.Is(err, productport.ErrProductConflict):
		writeError(w, http.StatusConflict, "conflict")
	default:
		writeError(w, http.StatusServiceUnavailable, "unavailable")
	}
}

func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code string) {
	compat := map[string]string{"invalid_request": "MALFORMED_REQUEST", "not_found": "NOT_FOUND", "conflict": "CONFLICT", "unauthorized": "UNAUTHORIZED", "permission_denied": "FORBIDDEN", "csrf_required": "FORBIDDEN", "unavailable": "DEPENDENCY_UNAVAILABLE", "method_not_allowed": "METHOD_NOT_ALLOWED", "product_not_enabled": "product_not_enabled"}[code]
	if compat == "" {
		compat = "DEPENDENCY_UNAVAILABLE"
	}
	writeJSON(w, status, map[string]any{"ok": false, "code": compat, "message": strings.ReplaceAll(strings.ToLower(compat), "_", " ")})
}
