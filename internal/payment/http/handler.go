package paymenthttp

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	paymentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/app"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/provider"
	paymentsession "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/session"
)

const SessionCookieName = "aicrm_payment_session"
const maxBody = 64 << 10

type RequestSecurity interface {
	Authenticate(context.Context, *http.Request) (accessdomain.Principal, error)
	AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error)
}

type Application interface {
	Create(context.Context, paymentport.CreateCommand) (domain.Payment, error)
	RequestRefund(context.Context, paymentport.RefundCommand) (domain.Refund, error)
	ApplyVerifiedCallback(context.Context, paymentprovider.CallbackResult) error
	FindPayment(context.Context, domain.Provider, string) (domain.Payment, error)
	ListRefunds(context.Context, int32, int32) ([]paymentport.RefundProjection, int64, error)
}

type Handler struct {
	app           Application
	verifier      *paymentprovider.CallbackVerifier
	security      RequestSecurity
	writesEnabled bool
}

func NewHandler(app Application, verifier *paymentprovider.CallbackVerifier, security RequestSecurity, writesEnabled bool) (*Handler, error) {
	if app == nil || security == nil {
		return nil, errors.New("payment HTTP dependencies are required")
	}
	return &Handler{app: app, verifier: verifier, security: security, writesEnabled: writesEnabled}, nil
}

func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	path := strings.TrimSuffix(request.URL.Path, "/")
	switch {
	case path == "/api/v1/wechat-pay/checkouts":
		handler.checkout(writer, request)
	case path == "/api/admin/refunds":
		handler.refunds(writer, request)
	case path == "/api/public/wechat-pay/callbacks/payment" || path == "/api/public/wechat-pay/callbacks/refund":
		handler.callback(writer, request)
	case strings.HasPrefix(path, "/api/admin/wechat-pay/orders/") && strings.HasSuffix(path, "/refunds"):
		handler.compatRefund(writer, request, strings.TrimSuffix(strings.TrimPrefix(path, "/api/admin/wechat-pay/orders/"), "/refunds"))
	case strings.HasPrefix(path, "/api/admin/wechat-pay/orders/") && strings.HasSuffix(path, "/external-push-deliveries"):
		handler.orderEffects(writer, request)
	case strings.HasPrefix(path, "/api/admin/payments/") && strings.HasSuffix(path, "/refunds"):
		handler.refund(writer, request, strings.TrimSuffix(strings.TrimPrefix(path, "/api/admin/payments/"), "/refunds"))
	default:
		writeError(writer, http.StatusNotFound, "not_found")
	}
}

func (handler *Handler) refunds(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, http.MethodGet)
		return
	}
	if _, err := handler.security.Authenticate(request.Context(), request); err != nil {
		writeError(writer, http.StatusUnauthorized, "unauthorized")
		return
	}
	limit, offset := int64(50), int64(0)
	var err error
	if raw := request.URL.Query().Get("limit"); raw != "" {
		limit, err = strconv.ParseInt(raw, 10, 32)
	}
	if err == nil {
		if raw := request.URL.Query().Get("offset"); raw != "" {
			offset, err = strconv.ParseInt(raw, 10, 32)
		}
	}
	if err != nil || limit < 1 || limit > 100 || offset < 0 || offset > 1_000_000 {
		writeError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	rows, total, err := handler.app.ListRefunds(request.Context(), int32(limit), int32(offset))
	if err != nil {
		resultError(writer, err)
		return
	}
	items := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		effectID := int64(0)
		if strings.HasPrefix(row.Refund.EffectID, "eer_") {
			effectID, _ = strconv.ParseInt(strings.TrimPrefix(row.Refund.EffectID, "eer_"), 10, 64)
		}
		provider := string(row.Refund.Provider)
		if provider == "wechat_pay" {
			provider = "wechat"
		}
		items = append(items, map[string]any{
			"id": row.Refund.ID, "order_id": row.OrderID, "provider": provider,
			"order_no": row.MerchantOrder, "transaction_id": row.TransactionRef,
			"refund_id": row.Refund.RefundNo, "out_refund_no": row.Refund.RefundNo,
			"refund_amount_total": row.Refund.AmountMinor, "order_amount_minor": row.OrderAmount,
			"currency": row.Currency, "reason": row.Refund.Reason, "status": compatRefundStatus(row.Refund.Status),
			"external_effect_id": effectID, "external_effect_state": compatRefundStatus(row.Refund.Status),
			"auto_retry_allowed": false, "created_at": row.Refund.CreatedAt,
		})
	}
	writeJSON(writer, http.StatusOK, map[string]any{"items": items, "refunds": items, "total": total, "limit": limit, "offset": offset, "has_more": offset+int64(len(items)) < total})
}

func (handler *Handler) compatRefund(writer http.ResponseWriter, request *http.Request, orderRef string) {
	if !handler.writesEnabled {
		writeError(writer, http.StatusServiceUnavailable, "payment_provider_disabled")
		return
	}
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, http.MethodPost)
		return
	}
	principal, err := handler.security.AuthorizeCSRF(request.Context(), request)
	if err != nil || principal.Kind != accessdomain.KindAdmin || !hasRole(principal.Roles, accessdomain.RoleAdmin) {
		writeError(writer, http.StatusForbidden, "forbidden")
		return
	}
	var body struct {
		Provider                  string `json:"provider"`
		OrderNo                   string `json:"order_no"`
		AmountMinor               int64  `json:"refund_amount_total"`
		Reason                    string `json:"reason"`
		TransactionIDConfirmation string `json:"transaction_id_confirmation"`
		Checked                   bool   `json:"checked"`
		Operator                  string `json:"operator,omitempty"`
	}
	if !decodeJSON(writer, request, &body) {
		return
	}
	if orderRef == "" || (body.OrderNo != "" && body.OrderNo != orderRef) || body.TransactionIDConfirmation != orderRef || !body.Checked {
		writeError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	payment, err := handler.app.FindPayment(request.Context(), domain.ProviderWeChatPay, orderRef)
	if err != nil {
		resultError(writer, err)
		return
	}
	key := strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	digest := sha256.Sum256([]byte(key))
	refund, err := handler.app.RequestRefund(request.Context(), paymentport.RefundCommand{PaymentID: payment.ID, AmountMinor: body.AmountMinor, RefundNo: "RF-" + fmt.Sprintf("%x", digest[:12]), Reason: body.Reason, ActorScope: "admin:" + strconv.FormatInt(principal.InternalID, 10), IdempotencyKey: key})
	if err != nil {
		resultError(writer, err)
		return
	}
	writeJSON(writer, http.StatusAccepted, map[string]any{"id": refund.ID, "refund_id": refund.RefundNo, "out_refund_no": refund.RefundNo, "status": compatRefundStatus(refund.Status), "external_effect_id": strings.TrimPrefix(refund.EffectID, "eer_"), "auto_retry_allowed": false})
}

func (handler *Handler) orderEffects(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, http.MethodGet)
		return
	}
	if _, err := handler.security.Authenticate(request.Context(), request); err != nil {
		writeError(writer, http.StatusUnauthorized, "unauthorized")
		return
	}
	// Historical records intentionally have no External Effect. Native effect
	// details remain available through the canonical External Effects console.
	writeJSON(writer, http.StatusOK, map[string]any{"items": []any{}, "effects": []any{}, "total": 0})
}

func compatRefundStatus(status domain.RefundStatus) string {
	switch status {
	case domain.RefundRequested, domain.RefundEffectAccepted:
		return "pending_external_gate"
	case domain.RefundOutcomeUnknown:
		return "outcome_unknown"
	case domain.RefundCompleted:
		return "completed"
	default:
		return "final_failed"
	}
}

func (handler *Handler) checkout(writer http.ResponseWriter, request *http.Request) {
	if !handler.writesEnabled {
		writeError(writer, http.StatusServiceUnavailable, "payment_provider_disabled")
		return
	}
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, http.MethodPost)
		return
	}
	cookie, err := request.Cookie(SessionCookieName)
	if err != nil || cookie.Value == "" {
		writeError(writer, http.StatusUnauthorized, "payment_session_required")
		return
	}
	var body struct {
		OrderID int64 `json:"order_id"`
	}
	if !decodeJSON(writer, request, &body) {
		return
	}
	idempotency := request.Header.Get("Idempotency-Key")
	payment, err := handler.app.Create(request.Context(), paymentport.CreateCommand{OrderID: body.OrderID, SessionToken: cookie.Value, ActorScope: "public-checkout", IdempotencyKey: idempotency})
	if err != nil {
		resultError(writer, err)
		return
	}
	clearSessionCookie(writer)
	writeJSON(writer, http.StatusAccepted, map[string]any{"payment_id": payment.ID, "status": payment.Status, "effect_id": payment.EffectID})
}

func (handler *Handler) refund(writer http.ResponseWriter, request *http.Request, rawPaymentID string) {
	if !handler.writesEnabled {
		writeError(writer, http.StatusServiceUnavailable, "payment_provider_disabled")
		return
	}
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, http.MethodPost)
		return
	}
	principal, err := handler.security.AuthorizeCSRF(request.Context(), request)
	if err != nil || principal.Kind != accessdomain.KindAdmin || !hasRole(principal.Roles, accessdomain.RoleAdmin) {
		writeError(writer, http.StatusForbidden, "forbidden")
		return
	}
	paymentID, err := strconv.ParseInt(rawPaymentID, 10, 64)
	if err != nil || paymentID < 1 {
		writeError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	var body struct {
		AmountMinor int64  `json:"amount_minor"`
		RefundNo    string `json:"refund_no"`
		Reason      string `json:"reason"`
	}
	if !decodeJSON(writer, request, &body) {
		return
	}
	refund, err := handler.app.RequestRefund(request.Context(), paymentport.RefundCommand{PaymentID: paymentID, AmountMinor: body.AmountMinor, RefundNo: body.RefundNo, Reason: body.Reason, ActorScope: "admin:" + strconv.FormatInt(principal.InternalID, 10), IdempotencyKey: request.Header.Get("Idempotency-Key")})
	if err != nil {
		resultError(writer, err)
		return
	}
	writeJSON(writer, http.StatusAccepted, map[string]any{"refund_id": refund.ID, "status": refund.Status, "effect_id": refund.EffectID})
}

func (handler *Handler) callback(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost || handler.verifier == nil {
		writeError(writer, http.StatusNotFound, "not_found")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(writer, request.Body, maxBody))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_callback")
		return
	}
	headers, err := paymentprovider.CallbackHeaders(request)
	if err != nil {
		writeError(writer, http.StatusUnauthorized, "invalid_signature")
		return
	}
	callback, err := handler.verifier.Verify(request.Context(), body, headers)
	if err != nil || strings.HasSuffix(request.URL.Path, "/payment") != (callback.Kind == "payment") {
		writeError(writer, http.StatusUnauthorized, "invalid_signature")
		return
	}
	if err = handler.app.ApplyVerifiedCallback(request.Context(), callback); err != nil {
		resultError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"code": "SUCCESS", "message": "成功"})
}

func WriteTrustedSessionCookie(writer http.ResponseWriter, issued paymentsession.Issued) error {
	if issued.Token == "" || issued.ExpiresAt.IsZero() {
		return paymentsession.ErrInvalid
	}
	http.SetCookie(writer, &http.Cookie{Name: SessionCookieName, Value: issued.Token, Path: "/api/v1/wechat-pay/", Expires: issued.ExpiresAt, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	return nil
}

func clearSessionCookie(writer http.ResponseWriter) {
	http.SetCookie(writer, &http.Cookie{Name: SessionCookieName, Path: "/api/v1/wechat-pay/", MaxAge: -1, Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
}

func decodeJSON(writer http.ResponseWriter, request *http.Request, destination any) bool {
	if request.Header.Get("Content-Type") != "application/json" {
		writeError(writer, http.StatusBadRequest, "invalid_request")
		return false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(writer, request.Body, maxBody))
	decoder.DisallowUnknownFields()
	if decoder.Decode(destination) != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request")
		return false
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		writeError(writer, http.StatusBadRequest, "invalid_request")
		return false
	}
	return true
}

func resultError(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, paymentport.ErrInvalid):
		writeError(writer, http.StatusBadRequest, "invalid_request")
	case errors.Is(err, paymentport.ErrNotFound):
		writeError(writer, http.StatusNotFound, "not_found")
	case errors.Is(err, paymentport.ErrConflict):
		writeError(writer, http.StatusConflict, "conflict")
	default:
		writeError(writer, http.StatusServiceUnavailable, "unavailable")
	}
}

func methodNotAllowed(writer http.ResponseWriter, methods ...string) {
	writer.Header().Set("Allow", strings.Join(methods, ", "))
	writeError(writer, http.StatusMethodNotAllowed, "method_not_allowed")
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func writeError(writer http.ResponseWriter, status int, code string) {
	writeJSON(writer, status, map[string]string{"code": code})
}

func hasRole(roles []accessdomain.Role, expected accessdomain.Role) bool {
	for _, role := range roles {
		if role == expected {
			return true
		}
	}
	return false
}

var _ Application = (*paymentapp.Service)(nil)
