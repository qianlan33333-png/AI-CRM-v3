// Package http exposes the authenticated transaction-management read and
// export surface. It returns canonical internal customer references only.
package http

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
)

const maxBody = 64 << 10

type RequestSecurity interface {
	Authenticate(context.Context, *http.Request) (accessdomain.Principal, error)
	AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error)
}

type Application interface {
	orderport.Query
	orderport.Exporter
}

type Handler struct {
	app      Application
	security RequestSecurity
}

func NewHandler(app Application, security RequestSecurity) (*Handler, error) {
	if app == nil || security == nil {
		return nil, errors.New("order HTTP dependencies are required")
	}
	return &Handler{app: app, security: security}, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	path := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case path == "/api/admin/orders" || path == "/api/admin/wechat-pay/orders" || path == "/api/admin/alipay/transactions":
		h.list(w, r, path)
	case strings.HasPrefix(path, "/api/admin/orders/"):
		h.orderTail(w, r, strings.TrimPrefix(path, "/api/admin/orders/"))
	case path == "/api/admin/refunds":
		h.emptyRefunds(w, r)
	case path == "/api/admin/exports/preview":
		h.preview(w, r)
	case path == "/api/admin/exports" || path == "/api/admin/wechat-pay/order-exports":
		h.export(w, r, path)
	case strings.HasPrefix(path, "/api/admin/wechat-pay/orders/") && strings.HasSuffix(path, "/external-push-deliveries"):
		h.emptyEffects(w, r)
	default:
		writeError(w, http.StatusNotFound, "not_found")
	}
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request, path string) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	if !h.read(w, r) {
		return
	}
	query, ok := parseListQuery(r.URL.Query())
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if path == "/api/admin/wechat-pay/orders" {
		query.Provider = domain.ProviderWeChatPay
	}
	if path == "/api/admin/alipay/transactions" {
		query.Provider = domain.ProviderAlipay
	}
	page, err := h.app.List(r.Context(), query)
	if err != nil {
		resultError(w, err)
		return
	}
	items := make([]orderResponse, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, responseFrom(item))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "orders": items, "total": int(query.Offset) + len(items), "limit": query.Limit, "offset": query.Offset, "has_more": page.NextCursor != "", "next_cursor": page.NextCursor})
}

func (h *Handler) orderTail(w http.ResponseWriter, r *http.Request, tail string) {
	parts := strings.Split(tail, "/")
	if len(parts) < 1 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	ref, err := url.PathUnescape(parts[0])
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	if !h.read(w, r) {
		return
	}
	order, err := h.app.GetByReference(r.Context(), ref)
	if err != nil {
		resultError(w, err)
		return
	}
	if len(parts) == 1 {
		response := responseFrom(order)
		response.RefundableAmountTotal = order.Amount.AmountMinor - order.RefundedMinor
		writeJSON(w, http.StatusOK, response)
		return
	}
	if len(parts) == 2 && parts[1] == "items" {
		items := make([]map[string]any, 0, len(order.Items))
		for _, item := range order.Items {
			items = append(items, map[string]any{"line_no": item.LineNo, "product_id": item.ProductID, "product_code": item.ProductCode, "name": item.ProductName, "unit_amount_minor": item.UnitAmountMinor, "quantity": item.Quantity, "line_amount_minor": item.LineAmountMinor, "status": order.Status, "created_at": order.CreatedAt})
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
		return
	}
	writeError(w, http.StatusNotFound, "not_found")
}

func (h *Handler) emptyRefunds(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	if !h.read(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": []any{}, "refunds": []any{}, "total": 0, "limit": 50, "has_more": false})
}

func (h *Handler) emptyEffects(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w, http.MethodGet)
		return
	}
	if !h.read(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": []any{}, "effects": []any{}, "total": 0})
}

type exportRequest struct {
	Resource string         `json:"resource"`
	Format   string         `json:"format"`
	Filter   map[string]any `json:"filter"`
	Filters  map[string]any `json:"filters"`
}

func (h *Handler) preview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	if _, ok := h.write(w, r); !ok {
		return
	}
	query, ok := decodeExport(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	preview, err := h.app.PreviewExport(r.Context(), query)
	if err != nil {
		resultError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"resource": "orders", "format": "csv", "total": preview.Rows, "truncated": preview.Truncated})
}

func (h *Handler) export(w http.ResponseWriter, r *http.Request, path string) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w, http.MethodPost)
		return
	}
	principal, ok := h.write(w, r)
	if !ok {
		return
	}
	query, ok := decodeExport(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	result, err := h.app.ExportCSV(r.Context(), query, principal.InternalID, key)
	if err != nil {
		resultError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="orders.csv"`)
	w.Header().Set("X-AICRM-Export-Receipt", strconv.FormatInt(result.ReceiptID, 10))
	w.Header().Set("Digest", "sha-256="+hex.EncodeToString(result.ContentDigest[:]))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(result.Content)
	_ = path
}

func decodeExport(r *http.Request) (orderport.ListQuery, bool) {
	decoder := json.NewDecoder(io.LimitReader(r.Body, maxBody))
	decoder.DisallowUnknownFields()
	var body exportRequest
	if decoder.Decode(&body) != nil || body.Resource != "orders" || body.Format != "csv" {
		return orderport.ListQuery{}, false
	}
	filter := body.Filter
	if filter == nil {
		filter = body.Filters
	}
	values := url.Values{}
	for key, value := range filter {
		if value != nil {
			values.Set(key, fmt.Sprint(value))
		}
	}
	if values.Get("identity") != "" || values.Get("mobile") != "" {
		return orderport.ListQuery{}, false
	}
	if values.Get("transaction_id") != "" {
		values.Set("order_ref", values.Get("transaction_id"))
	}
	if values.Get("product_code") != "" {
		values.Set("product", values.Get("product_code"))
	}
	return parseListQuery(values)
}

func parseListQuery(values url.Values) (orderport.ListQuery, bool) {
	allowed := map[string]bool{"cursor": true, "limit": true, "offset": true, "provider": true, "status": true, "payment_status": true, "order_ref": true, "customer_id": true, "product": true, "created_from": true, "created_to": true, "transaction_id": true, "product_code": true}
	for key := range values {
		if !allowed[key] || len(values[key]) != 1 {
			return orderport.ListQuery{}, false
		}
	}
	query := orderport.ListQuery{Cursor: values.Get("cursor"), Limit: 50, OrderRef: values.Get("order_ref"), Product: values.Get("product")}
	if raw := values.Get("limit"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || n < 1 || n > 100 {
			return query, false
		}
		query.Limit = int32(n)
	}
	if raw := values.Get("offset"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 32)
		if err != nil || n < 0 || n > 1000000 {
			return query, false
		}
		query.Offset = int32(n)
	}
	if raw := values.Get("customer_id"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n < 1 {
			return query, false
		}
		query.CustomerID = n
	}
	if raw := values.Get("provider"); raw != "" {
		switch raw {
		case "wechat", "wechat_pay":
			query.Provider = domain.ProviderWeChatPay
		case "wechat_shop":
			query.Provider = domain.ProviderWeChatShop
		case "alipay":
			query.Provider = domain.ProviderAlipay
		default:
			return query, false
		}
	}
	status := values.Get("status")
	if status == "" {
		status = values.Get("payment_status")
	}
	if status != "" {
		if status == "unpaid" {
			status = string(domain.StatusPendingPayment)
		}
		if status == "refunding" {
			status = string(domain.StatusPartiallyRefunded)
		}
		query.Status = domain.Status(status)
	}
	if raw := values.Get("created_from"); raw != "" {
		parsed, err := time.Parse("2006-01-02", raw)
		if err != nil {
			return query, false
		}
		query.CreatedFrom = &parsed
	}
	if raw := values.Get("created_to"); raw != "" {
		parsed, err := time.Parse("2006-01-02", raw)
		if err != nil {
			return query, false
		}
		parsed = parsed.AddDate(0, 0, 1)
		query.CreatedTo = &parsed
	}
	return query, true
}

type orderResponse struct {
	ID                    int64         `json:"id"`
	RecordOrigin          string        `json:"record_origin"`
	CreatedAt             time.Time     `json:"created_at"`
	MerchantOrderNo       string        `json:"merchant_order_no"`
	OutTradeNo            string        `json:"out_trade_no"`
	OrderNo               string        `json:"order_no"`
	PlatformTransactionNo string        `json:"platform_transaction_no"`
	TransactionID         string        `json:"transaction_id"`
	PayerName             string        `json:"payer_name"`
	PayerID               string        `json:"payer_id"`
	ProductCode           string        `json:"product_code"`
	ProductName           string        `json:"product_name"`
	AmountYuan            string        `json:"amount_yuan"`
	Currency              string        `json:"currency"`
	Status                domain.Status `json:"status"`
	StatusLabel           string        `json:"status_label"`
	Provider              string        `json:"provider"`
	ProviderLabel         string        `json:"provider_label"`
	DetailURL             string        `json:"detail_url"`
	RefundableAmountTotal int64         `json:"refundable_amount_total"`
}

func responseFrom(order domain.Snapshot) orderResponse {
	provider, label := string(order.Provider), string(order.Provider)
	if order.Provider == domain.ProviderWeChatPay {
		provider, label = "wechat", "微信支付"
	} else if order.Provider == domain.ProviderWeChatShop {
		label = "微信小店"
	} else if order.Provider == domain.ProviderAlipay {
		label = "支付宝"
	}
	origin := string(order.RecordOrigin)
	if order.RecordOrigin == domain.RecordOriginHistory {
		origin = "v1_history"
	}
	productCode, productName := "", ""
	if len(order.Items) > 0 {
		productCode, productName = order.Items[0].ProductCode, order.Items[0].ProductName
	}
	payer := ""
	payerName := "未归属"
	if order.PayerCustomerID != nil {
		payer = "customer:" + strconv.FormatInt(*order.PayerCustomerID, 10)
		payerName = "客户 #" + strconv.FormatInt(*order.PayerCustomerID, 10)
	}
	return orderResponse{ID: order.ID, RecordOrigin: origin, CreatedAt: order.CreatedAt, MerchantOrderNo: order.MerchantOrderNo, OutTradeNo: order.MerchantOrderNo, OrderNo: order.SourceKey, PlatformTransactionNo: order.ProviderTransactionNo, TransactionID: order.ProviderTransactionNo, PayerName: payerName, PayerID: payer, ProductCode: productCode, ProductName: productName, AmountYuan: fmt.Sprintf("%d.%02d", order.Amount.AmountMinor/100, order.Amount.AmountMinor%100), Currency: order.Amount.Currency, Status: order.Status, StatusLabel: string(order.Status), Provider: provider, ProviderLabel: label, DetailURL: "/admin/orderDetail.html?id=" + url.QueryEscape(order.MerchantOrderNo)}
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
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]any{"error": code})
}
func resultError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, orderport.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, orderport.ErrConflict):
		writeError(w, http.StatusConflict, "conflict")
	default:
		writeError(w, http.StatusServiceUnavailable, "unavailable")
	}
}
func methodNotAllowed(w http.ResponseWriter, allow string) {
	w.Header().Set("Allow", allow)
	writeError(w, http.StatusMethodNotAllowed, "method_not_allowed")
}
