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
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
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
	GetCheckout(context.Context, string, string) (paymentport.Handoff, error)
	RequestRefund(context.Context, paymentport.RefundCommand) (domain.Refund, error)
	ApplyVerifiedCallback(context.Context, paymentprovider.CallbackResult) error
	ApplyVerifiedShopCallback(context.Context, paymentport.ShopRefundCallback) error
	ReconcileShopRefund(context.Context, int64) (domain.Refund, error)
	ReconcileWeChatPayPayment(context.Context, int64) (domain.Payment, error)
	ReconcileWeChatPayRefund(context.Context, int64) (domain.Refund, error)
	FindPayment(context.Context, domain.Provider, string) (domain.Payment, error)
	ListRefunds(context.Context, int32, int32) ([]paymentport.RefundProjection, int64, error)
	ListOrderEffects(context.Context, domain.Provider, string) ([]paymentport.EffectProjection, error)
}

type SessionIdentityVerifier interface {
	VerifyCode(context.Context, string) (identitydomain.VerifiedFact, error)
}

type TrustedSessionIssuer interface {
	IssueTrusted(context.Context, paymentsession.IssueCommand) (paymentsession.Issued, error)
}

type H5OAuthApplication interface {
	Enabled() bool
	Start(context.Context, string) (string, error)
	Complete(context.Context, string, string) (paymentsession.Issued, string, error)
}

type Handler struct {
	app               Application
	verifier          *paymentprovider.CallbackVerifier
	security          RequestSecurity
	writesEnabled     bool
	shopWritesEnabled bool
	shopVerifier      paymentport.ShopCallbackVerifier
	sessionVerifier   SessionIdentityVerifier
	sessionIssuer     TrustedSessionIssuer
	h5OAuth           H5OAuthApplication
}

func (handler *Handler) SetH5OAuth(application H5OAuthApplication) error {
	if handler == nil || application == nil {
		return paymentport.ErrInvalid
	}
	handler.h5OAuth = application
	return nil
}

func (handler *Handler) SetTrustedSessionIssuer(verifier SessionIdentityVerifier, issuer TrustedSessionIssuer) error {
	if handler == nil || verifier == nil || issuer == nil {
		return paymentport.ErrInvalid
	}
	handler.sessionVerifier, handler.sessionIssuer = verifier, issuer
	return nil
}

func (handler *Handler) SetShopCallbackVerifier(verifier paymentport.ShopCallbackVerifier) error {
	if handler == nil || verifier == nil {
		return paymentport.ErrInvalid
	}
	handler.shopVerifier = verifier
	return nil
}

func NewHandler(app Application, verifier *paymentprovider.CallbackVerifier, security RequestSecurity, writesEnabled bool, shopEnabled ...bool) (*Handler, error) {
	if app == nil || security == nil {
		return nil, errors.New("payment HTTP dependencies are required")
	}
	handler := &Handler{app: app, verifier: verifier, security: security, writesEnabled: writesEnabled}
	if len(shopEnabled) > 0 {
		handler.shopWritesEnabled = shopEnabled[0]
	}
	return handler, nil
}

func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	path := strings.TrimSuffix(request.URL.Path, "/")
	switch {
	case path == "/api/h5/wechat-pay/oauth/start":
		handler.startH5OAuth(writer, request)
	case path == "/api/h5/wechat-pay/oauth/callback":
		handler.completeH5OAuth(writer, request)
	case path == "/api/v1/wechat-pay/sessions":
		handler.issueSession(writer, request)
	case path == "/api/v1/wechat-pay/checkouts":
		handler.checkout(writer, request)
	case strings.HasPrefix(path, "/api/v1/wechat-pay/checkouts/"):
		handler.checkoutStatus(writer, request, strings.TrimPrefix(path, "/api/v1/wechat-pay/checkouts/"))
	case path == "/api/admin/refunds":
		handler.refunds(writer, request)
	case path == "/api/public/wechat-pay/callbacks/payment" || path == "/api/public/wechat-pay/callbacks/refund":
		handler.callback(writer, request)
	case path == "/api/public/wechat-shop/callbacks/refund":
		handler.shopCallback(writer, request)
	case strings.HasPrefix(path, "/api/admin/wechat-shop/refunds/") && strings.HasSuffix(path, "/reconcile"):
		handler.reconcileShopRefund(writer, request, strings.TrimSuffix(strings.TrimPrefix(path, "/api/admin/wechat-shop/refunds/"), "/reconcile"))
	case strings.HasPrefix(path, "/api/admin/wechat-pay/payments/") && strings.HasSuffix(path, "/reconcile"):
		handler.reconcileWeChatPay(writer, request, strings.TrimSuffix(strings.TrimPrefix(path, "/api/admin/wechat-pay/payments/"), "/reconcile"), false)
	case strings.HasPrefix(path, "/api/admin/wechat-pay/refunds/") && strings.HasSuffix(path, "/reconcile"):
		handler.reconcileWeChatPay(writer, request, strings.TrimSuffix(strings.TrimPrefix(path, "/api/admin/wechat-pay/refunds/"), "/reconcile"), true)
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

func (handler *Handler) startH5OAuth(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet || handler.h5OAuth == nil || !handler.h5OAuth.Enabled() {
		writeError(writer, http.StatusServiceUnavailable, "payment_h5_oauth_disabled")
		return
	}
	if !strings.Contains(strings.ToLower(request.UserAgent()), "micromessenger") || len(request.URL.Query()) != 1 {
		writeError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	location, err := handler.h5OAuth.Start(request.Context(), request.URL.Query().Get("return_url"))
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	http.Redirect(writer, request, location, http.StatusFound)
}

func (handler *Handler) completeH5OAuth(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet || handler.h5OAuth == nil || !handler.h5OAuth.Enabled() || len(request.URL.Query()) != 2 {
		writeError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	issued, returnPath, err := handler.h5OAuth.Complete(request.Context(), request.URL.Query().Get("state"), request.URL.Query().Get("code"))
	if err != nil {
		writeError(writer, http.StatusUnauthorized, "identity_verification_failed")
		return
	}
	if err = WriteTrustedSessionCookie(writer, issued); err != nil {
		writeError(writer, http.StatusServiceUnavailable, "unavailable")
		return
	}
	http.Redirect(writer, request, returnPath, http.StatusFound)
}

func (handler *Handler) issueSession(writer http.ResponseWriter, request *http.Request) {
	if !handler.writesEnabled || handler.sessionVerifier == nil || handler.sessionIssuer == nil {
		writeError(writer, http.StatusServiceUnavailable, "payment_provider_disabled")
		return
	}
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, http.MethodPost)
		return
	}
	var body struct {
		Code string `json:"code"`
	}
	if !decodeJSON(writer, request, &body) {
		return
	}
	fact, err := handler.sessionVerifier.VerifyCode(request.Context(), body.Code)
	if err != nil || !fact.Valid() {
		writeError(writer, http.StatusUnauthorized, "identity_verification_failed")
		return
	}
	codeDigest := sha256.Sum256([]byte(body.Code))
	issued, err := handler.sessionIssuer.IssueTrusted(request.Context(), paymentsession.IssueCommand{Fact: fact, IdempotencyKey: "payment-session:" + fmt.Sprintf("%x", codeDigest[:])})
	if err != nil {
		resultError(writer, err)
		return
	}
	if err = WriteTrustedSessionCookie(writer, issued); err != nil {
		writeError(writer, http.StatusServiceUnavailable, "unavailable")
		return
	}
	writeJSON(writer, http.StatusCreated, map[string]any{"expires_at": issued.ExpiresAt, "verified": true})
}

func (handler *Handler) refunds(writer http.ResponseWriter, request *http.Request) {
	if request.Method == http.MethodPost {
		handler.shopRefund(writer, request)
		return
	}
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, http.MethodGet, http.MethodPost)
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

func (handler *Handler) shopRefund(writer http.ResponseWriter, request *http.Request) {
	if !handler.shopWritesEnabled {
		writeError(writer, http.StatusServiceUnavailable, "payment_provider_disabled")
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
		ProductID                 string `json:"product_id"`
		SKUID                     string `json:"sku_id"`
		RefundCount               int64  `json:"refund_count"`
		AmountMinor               int64  `json:"refund_amount_total"`
		ReasonCode                string `json:"reason_code"`
		Reason                    string `json:"reason"`
		TransactionIDConfirmation string `json:"transaction_id_confirmation"`
		Checked                   bool   `json:"checked"`
		Operator                  string `json:"operator,omitempty"`
	}
	if !decodeJSON(writer, request, &body) {
		return
	}
	key := strings.TrimSpace(request.Header.Get("Idempotency-Key"))
	if body.Provider != "wechat_shop" || body.OrderNo == "" || body.TransactionIDConfirmation != body.OrderNo || !body.Checked || key == "" {
		writeError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	payment, err := handler.app.FindPayment(request.Context(), domain.ProviderWeChatShop, body.OrderNo)
	if err != nil {
		resultError(writer, err)
		return
	}
	digest := sha256.Sum256([]byte(strconv.FormatInt(principal.InternalID, 10) + "\x00" + key))
	refund, err := handler.app.RequestRefund(request.Context(), paymentport.RefundCommand{
		PaymentID: payment.ID, AmountMinor: body.AmountMinor, RefundNo: "SRF-" + fmt.Sprintf("%x", digest[:12]), Reason: body.Reason,
		ActorScope: "admin:" + strconv.FormatInt(principal.InternalID, 10), IdempotencyKey: key,
		ProviderOrderID: body.OrderNo, ProductID: body.ProductID, SKUID: body.SKUID, RefundCount: body.RefundCount, ReasonCode: body.ReasonCode,
	})
	if err != nil {
		resultError(writer, err)
		return
	}
	writeJSON(writer, http.StatusAccepted, map[string]any{"id": refund.ID, "refund_id": refund.RefundNo, "out_refund_no": refund.RefundNo, "provider": "wechat_shop", "state": compatRefundStatus(refund.Status), "status": compatRefundStatus(refund.Status), "external_effect_id": strings.TrimPrefix(refund.EffectID, "eer_"), "real_external_call_executed": false, "delivery_proven": false})
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
	orderRef := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSuffix(request.URL.Path, "/external-push-deliveries"), "/api/admin/wechat-pay/orders/"), "/")
	if orderRef == "" {
		writeError(writer, http.StatusBadRequest, "invalid_request")
		return
	}
	effects, err := handler.app.ListOrderEffects(request.Context(), domain.ProviderWeChatPay, orderRef)
	if err != nil {
		resultError(writer, err)
		return
	}
	items := make([]map[string]any, 0, len(effects))
	for _, effect := range effects {
		items = append(items, map[string]any{
			"id": effect.EffectID, "external_effect_id": effect.EffectID,
			"kind": effect.Kind, "status": effect.State, "state": effect.State,
			"attempt_count": effect.AttemptCount, "created_at": effect.UpdatedAt, "updated_at": effect.UpdatedAt,
		})
	}
	writeJSON(writer, http.StatusOK, map[string]any{"items": items, "effects": items, "total": len(items)})
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
		ProductID   int64  `json:"product_id,omitempty"`
		ProductType string `json:"product_kind,omitempty"`
		MobileE164  string `json:"mobile,omitempty"`
	}
	if !decodeJSON(writer, request, &body) {
		return
	}
	idempotency := request.Header.Get("Idempotency-Key")
	payment, err := handler.app.Create(request.Context(), paymentport.CreateCommand{ProductID: body.ProductID, ProductType: body.ProductType, MobileE164: body.MobileE164, SessionToken: cookie.Value, ActorScope: "public-checkout", IdempotencyKey: idempotency})
	if err != nil {
		resultError(writer, err)
		return
	}
	writeJSON(writer, http.StatusAccepted, map[string]any{"order_id": payment.OrderID, "merchant_order_no": payment.MerchantOrderNo, "payment_id": payment.ID, "status": payment.Status, "effect_id": payment.EffectID})
}

func (handler *Handler) checkoutStatus(writer http.ResponseWriter, request *http.Request, merchantOrderNo string) {
	if !handler.writesEnabled {
		writeError(writer, http.StatusServiceUnavailable, "payment_provider_disabled")
		return
	}
	if request.Method != http.MethodGet {
		methodNotAllowed(writer, http.MethodGet)
		return
	}
	cookie, err := request.Cookie(SessionCookieName)
	if err != nil || cookie.Value == "" || merchantOrderNo == "" {
		writeError(writer, http.StatusUnauthorized, "payment_session_required")
		return
	}
	handoff, err := handler.app.GetCheckout(request.Context(), merchantOrderNo, cookie.Value)
	if err != nil {
		resultError(writer, err)
		return
	}
	result := map[string]any{"payment_id": handoff.PaymentID, "merchant_order_no": handoff.MerchantOrder, "status": handoff.Status, "ready": len(handoff.Payload) > 0}
	status := http.StatusAccepted
	if len(handoff.Payload) > 0 {
		var providerPayload map[string]any
		if json.Unmarshal(handoff.Payload, &providerPayload) != nil {
			writeError(writer, http.StatusServiceUnavailable, "unavailable")
			return
		}
		result["handoff"] = providerPayload
		result["expires_at"] = handoff.ExpiresAt
		status = http.StatusOK
	}
	if handoff.Status == domain.StatusPaid || handoff.Status == domain.StatusFailed || handoff.Status == domain.StatusCancelled {
		clearSessionCookie(writer)
	}
	writeJSON(writer, status, result)
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

func (handler *Handler) shopCallback(writer http.ResponseWriter, request *http.Request) {
	if !handler.shopWritesEnabled || handler.shopVerifier == nil {
		writeError(writer, http.StatusServiceUnavailable, "payment_provider_disabled")
		return
	}
	query, err := exactShopCallbackQuery(request)
	if err != nil {
		writeError(writer, http.StatusBadRequest, "invalid_callback")
		return
	}
	if request.Method == http.MethodGet {
		echo, verifyErr := handler.shopVerifier.VerifyURL(request.Context(), query)
		if verifyErr != nil {
			writeError(writer, http.StatusUnauthorized, "invalid_signature")
			return
		}
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write([]byte(echo))
		return
	}
	if request.Method != http.MethodPost {
		methodNotAllowed(writer, http.MethodGet, http.MethodPost)
		return
	}
	body, readErr := io.ReadAll(http.MaxBytesReader(writer, request.Body, 128<<10))
	if readErr != nil {
		writeError(writer, http.StatusBadRequest, "invalid_callback")
		return
	}
	callback, verifyErr := handler.shopVerifier.VerifyRefund(request.Context(), body, query)
	if verifyErr != nil {
		writeError(writer, http.StatusUnauthorized, "invalid_signature")
		return
	}
	if applyErr := handler.app.ApplyVerifiedShopCallback(request.Context(), callback); applyErr != nil {
		resultError(writer, applyErr)
		return
	}
	writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write([]byte("success"))
}

func (handler *Handler) reconcileShopRefund(writer http.ResponseWriter, request *http.Request, rawID string) {
	if !handler.shopWritesEnabled {
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
	refundID, err := strconv.ParseInt(rawID, 10, 64)
	var empty struct{}
	if err != nil || refundID < 1 || strings.TrimSpace(request.Header.Get("Idempotency-Key")) == "" || !decodeJSON(writer, request, &empty) {
		if err != nil || refundID < 1 || strings.TrimSpace(request.Header.Get("Idempotency-Key")) == "" {
			writeError(writer, http.StatusBadRequest, "invalid_request")
		}
		return
	}
	refund, err := handler.app.ReconcileShopRefund(request.Context(), refundID)
	if err != nil {
		resultError(writer, err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"id": refund.ID, "refund_id": refund.RefundNo, "provider": "wechat_shop", "status": compatRefundStatus(refund.Status), "state": compatRefundStatus(refund.Status), "delivery_proven": refund.Status == domain.RefundCompleted, "real_external_call_executed": true, "updated_at": refund.UpdatedAt})
}

func (handler *Handler) reconcileWeChatPay(writer http.ResponseWriter, request *http.Request, rawID string, refund bool) {
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
	id, err := strconv.ParseInt(rawID, 10, 64)
	var empty struct{}
	if err != nil || id < 1 || strings.TrimSpace(request.Header.Get("Idempotency-Key")) == "" || !decodeJSON(writer, request, &empty) {
		if err != nil || id < 1 || strings.TrimSpace(request.Header.Get("Idempotency-Key")) == "" {
			writeError(writer, http.StatusBadRequest, "invalid_request")
		}
		return
	}
	if refund {
		value, callErr := handler.app.ReconcileWeChatPayRefund(request.Context(), id)
		if callErr != nil {
			resultError(writer, callErr)
			return
		}
		writeJSON(writer, http.StatusOK, map[string]any{"id": value.ID, "refund_id": value.RefundNo, "provider": "wechat", "status": compatRefundStatus(value.Status), "state": compatRefundStatus(value.Status), "delivery_proven": value.Status == domain.RefundCompleted, "real_external_call_executed": true, "updated_at": value.UpdatedAt})
		return
	}
	value, callErr := handler.app.ReconcileWeChatPayPayment(request.Context(), id)
	if callErr != nil {
		resultError(writer, callErr)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"id": value.ID, "merchant_order_no": value.MerchantOrderNo, "provider": "wechat", "status": value.Status, "delivery_proven": value.Status == domain.StatusPaid, "real_external_call_executed": true, "updated_at": value.UpdatedAt})
}

func exactShopCallbackQuery(request *http.Request) (map[string]string, error) {
	if request == nil || request.URL == nil {
		return nil, paymentport.ErrInvalid
	}
	names := []string{"timestamp", "nonce"}
	if request.Method == http.MethodGet {
		names = append(names, "signature", "echostr")
	} else if request.Method == http.MethodPost {
		names = append(names, "msg_signature")
	} else {
		return nil, paymentport.ErrInvalid
	}
	values := request.URL.Query()
	result := make(map[string]string, len(names))
	for _, name := range names {
		items := values[name]
		if len(items) != 1 || items[0] == "" || strings.TrimSpace(items[0]) != items[0] {
			return nil, paymentport.ErrInvalid
		}
		result[name] = items[0]
	}
	return result, nil
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
