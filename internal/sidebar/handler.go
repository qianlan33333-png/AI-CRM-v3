package sidebar

import (
	"context"
	"crypto/sha256"
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

	couponport "github.com/qianlan33333-png/AI-CRM-v3/internal/coupon/port"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
	radarport "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/port"
)

var ErrInvalidContext = errors.New("invalid sidebar context")

type Principal struct {
	CorpID     string
	EmployeeID string
}

type ContextVerifier interface {
	VerifySidebarContext(context.Context, string) (Principal, customerdomain.CustomerID, error)
}

type Config struct {
	Contexts     ContextVerifier
	Profiles     customerport.SidebarProfileService
	Surveys      customerport.CustomerSurveyReader
	Timeline     customerport.CustomerTimelineReader
	Products     productport.ProductOptionReader
	ProductByID  productport.ProductTargetReader
	Orders       orderport.Query
	Entitlements orderport.EntitlementService
	Coupons      couponport.CustomerCouponReader
	Materials    mediaport.ImageLibraryReader
	MaterialSend mediaport.SidebarImageSendReader
	Radar        radarport.Manager
	Sends        outboundport.SidebarSendAccepter
	PublicOrigin string
	Now          func() time.Time
}

type Handler struct{ config Config }

func NewHandler(config Config) (*Handler, error) {
	if config.Contexts == nil || config.Profiles == nil || config.Surveys == nil || config.Timeline == nil || config.Products == nil || config.ProductByID == nil || config.Orders == nil || config.Entitlements == nil || config.Coupons == nil || config.Materials == nil || config.MaterialSend == nil || config.Radar == nil || config.Sends == nil {
		return nil, errors.New("sidebar dependencies are required")
	}
	origin, err := url.Parse(strings.TrimRight(config.PublicOrigin, "/"))
	if err != nil || origin.Scheme != "https" || origin.Host == "" || origin.Path != "" {
		return nil, errors.New("sidebar public origin must be an https origin")
	}
	config.PublicOrigin = origin.String()
	return &Handler{config: config}, nil
}

func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/sidebar/v2/workbench", h.workbench)
	mux.HandleFunc("GET /api/sidebar/v2/profile", h.profile)
	mux.HandleFunc("PUT /api/sidebar/v2/profile", h.updateProfile)
	mux.HandleFunc("POST /api/sidebar/v2/phone-binding", h.bindPhone)
	mux.HandleFunc("GET /api/sidebar/v2/questionnaires", h.questionnaires)
	mux.HandleFunc("GET /api/sidebar/v2/timeline", h.timeline)
	mux.HandleFunc("GET /api/sidebar/v2/products", h.products)
	mux.HandleFunc("GET /api/sidebar/v2/orders", h.orders)
	mux.HandleFunc("GET /api/sidebar/v2/periodic-orders", h.periodicOrders)
	mux.HandleFunc("PUT /api/sidebar/v2/periodic-orders/{entitlement_id}/remark", h.updateRemark)
	mux.HandleFunc("GET /api/sidebar/v2/coupons", h.coupons)
	mux.HandleFunc("GET /api/sidebar/v2/materials", h.materials)
	mux.HandleFunc("GET /api/sidebar/v2/radar-links", h.radarLinks)
	mux.HandleFunc("POST /api/sidebar/v2/send-intents", h.createSendIntent)
	mux.HandleFunc("POST /api/sidebar/v2/send-intents/{intent_id}/outcome", h.completeSendIntent)
	return mux
}

func (h *Handler) workbench(w http.ResponseWriter, r *http.Request) {
	_, customerID, ok := h.authorize(w, r)
	if !ok {
		return
	}
	profile, err := h.config.Profiles.ReadSidebarProfile(r.Context(), customerID)
	if err != nil {
		h.sectionError(w)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{
		"customer_id": customerID,
		"status":      profile.Status,
		"tabs":        []string{"profile", "questionnaires", "products", "orders", "coupons", "materials"},
	})
}

func (h *Handler) profile(w http.ResponseWriter, r *http.Request) {
	_, customerID, ok := h.authorize(w, r)
	if !ok {
		return
	}
	profile, err := h.config.Profiles.ReadSidebarProfile(r.Context(), customerID)
	if err != nil {
		h.sectionError(w)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"customer": profile, "capability": "ready"})
}

func (h *Handler) updateProfile(w http.ResponseWriter, r *http.Request) {
	principal, customerID, ok := h.authorize(w, r)
	if !ok {
		return
	}
	var body struct {
		DisplayName     string `json:"display_name"`
		Gender          int16  `json:"gender"`
		CorpName        string `json:"corp_name"`
		ExpectedVersion int64  `json:"expected_version"`
	}
	if !decodeStrict(w, r, &body) {
		return
	}
	result, err := h.config.Profiles.UpdateSidebarProfile(r.Context(), customerport.SidebarProfileUpdate{CustomerID: customerID, EmployeeID: principal.EmployeeID, DisplayName: body.DisplayName, Gender: body.Gender, CorpName: body.CorpName, ExpectedVersion: body.ExpectedVersion, IdempotencyKey: idempotencyKey(r)})
	if err != nil {
		h.commandError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"customer": result})
}

func (h *Handler) bindPhone(w http.ResponseWriter, r *http.Request) {
	principal, customerID, ok := h.authorize(w, r)
	if !ok {
		return
	}
	var body struct {
		Phone string `json:"phone"`
	}
	if !decodeStrict(w, r, &body) {
		return
	}
	result, err := h.config.Profiles.BindSidebarPhone(r.Context(), customerport.SidebarPhoneBind{CustomerID: customerID, EmployeeID: principal.EmployeeID, Phone: body.Phone, IdempotencyKey: idempotencyKey(r)})
	if err != nil {
		h.commandError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *Handler) questionnaires(w http.ResponseWriter, r *http.Request) {
	_, customerID, ok := h.authorize(w, r)
	if !ok {
		return
	}
	limit, ok := boundedLimit(w, r, 20, 50)
	if !ok {
		return
	}
	page, err := h.config.Surveys.CustomerSurveys(r.Context(), customerID, customerport.PageQuery{Limit: limit, Watermark: h.now()})
	if err != nil {
		h.sectionError(w)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"customer_id": customerID, "items": page.Items, "source_status": page.Status.State, "as_of": page.Status.AsOf})
}

func (h *Handler) timeline(w http.ResponseWriter, r *http.Request) {
	_, customerID, ok := h.authorize(w, r)
	if !ok {
		return
	}
	limit, ok := boundedLimit(w, r, 20, 50)
	if !ok {
		return
	}
	page, err := h.config.Timeline.CustomerTimeline(r.Context(), customerID, customerport.PageQuery{Limit: limit, Watermark: h.now()})
	if err != nil {
		h.sectionError(w)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"customer_id": customerID, "items": page.Items, "source_status": page.Status.State, "as_of": page.Status.AsOf})
}

func (h *Handler) products(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := h.authorize(w, r); !ok {
		return
	}
	limit, ok := boundedLimit(w, r, int(productport.ProductOptionDefaultLimit), int(productport.ProductOptionMaximumLimit))
	if !ok {
		return
	}
	page, err := h.config.Products.ListProductOptions(r.Context(), productport.ProductOptionQuery{Q: r.URL.Query().Get("q"), ProductType: productport.ProductOptionType(r.URL.Query().Get("product_type")), Limit: int32(limit)})
	if err != nil {
		h.sectionError(w)
		return
	}
	h.writeJSON(w, http.StatusOK, page)
}

func (h *Handler) orders(w http.ResponseWriter, r *http.Request) {
	_, customerID, ok := h.authorize(w, r)
	if !ok {
		return
	}
	limit, ok := boundedLimit(w, r, 20, 50)
	if !ok {
		return
	}
	page, err := h.config.Orders.List(r.Context(), orderport.ListQuery{CustomerID: int64(customerID), Limit: int32(limit)})
	if err != nil {
		h.sectionError(w)
		return
	}
	h.writeJSON(w, http.StatusOK, page)
}

func (h *Handler) periodicOrders(w http.ResponseWriter, r *http.Request) {
	_, customerID, ok := h.authorize(w, r)
	if !ok {
		return
	}
	limit, ok := boundedLimit(w, r, 20, 50)
	if !ok {
		return
	}
	page, err := h.config.Entitlements.ListCustomerEntitlements(r.Context(), int64(customerID), int32(limit))
	if err != nil {
		h.sectionError(w)
		return
	}
	h.writeJSON(w, http.StatusOK, page)
}

func (h *Handler) updateRemark(w http.ResponseWriter, r *http.Request) {
	principal, customerID, ok := h.authorize(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("entitlement_id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	var body struct {
		Remark          string `json:"remark"`
		ExpectedVersion int64  `json:"expected_version"`
	}
	if !decodeStrict(w, r, &body) {
		return
	}
	result, err := h.config.Entitlements.UpdateEntitlementRemark(r.Context(), orderport.RemarkCommand{EntitlementID: id, CustomerID: int64(customerID), EmployeeID: principal.EmployeeID, Remark: body.Remark, ExpectedVersion: body.ExpectedVersion, IdempotencyKey: idempotencyKey(r)})
	if err != nil {
		h.commandError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *Handler) coupons(w http.ResponseWriter, r *http.Request) {
	_, customerID, ok := h.authorize(w, r)
	if !ok {
		return
	}
	limit, ok := boundedLimit(w, r, 20, 50)
	if !ok {
		return
	}
	page, err := h.config.Coupons.ListCustomerCoupons(r.Context(), int64(customerID), int32(limit))
	if err != nil {
		h.sectionError(w)
		return
	}
	h.writeJSON(w, http.StatusOK, page)
}

func (h *Handler) materials(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := h.authorize(w, r); !ok {
		return
	}
	limit, ok := boundedLimit(w, r, 20, 100)
	if !ok {
		return
	}
	page, err := h.config.Materials.ListImages(r.Context(), mediaport.ImageListQuery{Limit: int64(limit), EnabledOnly: true, Search: r.URL.Query().Get("q"), Category: r.URL.Query().Get("category")})
	if err != nil {
		h.sectionError(w)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"items": page.Items, "total": page.Total, "limit": page.Limit, "offset": page.Offset})
}

func (h *Handler) radarLinks(w http.ResponseWriter, r *http.Request) {
	if _, _, ok := h.authorize(w, r); !ok {
		return
	}
	limit, ok := boundedLimit(w, r, 20, 50)
	if !ok {
		return
	}
	page, err := h.config.Radar.List(r.Context(), radarport.ListQuery{Status: radarport.StatusEnabled, Limit: int32(limit)})
	if err != nil {
		h.sectionError(w)
		return
	}
	type item struct {
		ID          int64  `json:"id"`
		Name        string `json:"name"`
		Title       string `json:"title"`
		URL         string `json:"url"`
		ContentType string `json:"content_type"`
	}
	items := make([]item, 0, len(page.Items))
	for _, row := range page.Items {
		items = append(items, item{ID: int64(row.Link.ID), Name: row.Link.Name, Title: row.Link.Title, URL: h.config.PublicOrigin + "/r/" + url.PathEscape(string(row.Link.PublicCode)), ContentType: string(row.Link.Content.Type)})
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": page.Total})
}

func (h *Handler) createSendIntent(w http.ResponseWriter, r *http.Request) {
	principal, customerID, ok := h.authorize(w, r)
	if !ok {
		return
	}
	var body struct {
		ResourceKind string                        `json:"resource_kind"`
		ResourceID   string                        `json:"resource_id"`
		ProductType  productport.ProductOptionType `json:"product_type,omitempty"`
	}
	if !decodeStrict(w, r, &body) {
		return
	}
	payload, err := h.sendPayload(r.Context(), body.ResourceKind, body.ResourceID, body.ProductType)
	if err != nil {
		if errors.Is(err, mediaport.ErrSidebarMaterialNotReady) {
			writeError(w, http.StatusServiceUnavailable, "capability_not_ready")
			return
		}
		writeError(w, http.StatusNotFound, "resource_not_available")
		return
	}
	digest := sha256.Sum256(payload)
	result, err := h.config.Sends.AcceptSidebarSend(r.Context(), outboundport.SidebarSendCommand{CustomerID: int64(customerID), EmployeeID: principal.EmployeeID, ResourceKind: body.ResourceKind, ResourceID: body.ResourceID, ContentDigest: digest, Payload: payload, IdempotencyKey: idempotencyKey(r)})
	if err != nil {
		h.commandError(w, err)
		return
	}
	h.writeJSON(w, http.StatusAccepted, result)
}

func (h *Handler) completeSendIntent(w http.ResponseWriter, r *http.Request) {
	principal, customerID, ok := h.authorize(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("intent_id"), 10, 64)
	if err != nil || id < 1 {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	var body struct {
		Grant    string `json:"grant"`
		Outcome  string `json:"outcome"`
		Evidence string `json:"evidence"`
	}
	if !decodeStrict(w, r, &body) {
		return
	}
	evidence := sha256.Sum256([]byte(strings.TrimSpace(body.Evidence)))
	result, err := h.config.Sends.CompleteSidebarSend(r.Context(), outboundport.SidebarSendOutcomeCommand{IntentID: id, CustomerID: int64(customerID), EmployeeID: principal.EmployeeID, Grant: body.Grant, Outcome: body.Outcome, EvidenceDigest: evidence})
	if err != nil {
		h.commandError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *Handler) sendPayload(ctx context.Context, kind, rawID string, productType productport.ProductOptionType) ([]byte, error) {
	id, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || id < 1 {
		return nil, errors.New("invalid resource")
	}
	switch kind {
	case "product":
		product, err := h.config.ProductByID.ReadProductTarget(ctx, productType, productport.ID(id))
		if err != nil {
			return nil, err
		}
		content := fmt.Sprintf("%s\n价格：¥%.2f", product.Name, float64(product.PriceMinor)/100)
		return json.Marshal(map[string]any{"msgtype": "text", "text": map[string]string{"content": content}})
	case "material":
		material, err := h.config.MaterialSend.ReadSidebarImageForSend(ctx, id, h.now().Add(6*time.Minute))
		if err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"msgtype": "image", "image": map[string]string{"mediaid": material.MediaID}})
	case "radar_link":
		detail, err := h.config.Radar.Get(ctx, radarport.RadarID(id))
		if err != nil || detail.Link.Status != radarport.StatusEnabled {
			return nil, errors.New("radar unavailable")
		}
		link := h.config.PublicOrigin + "/r/" + url.PathEscape(string(detail.Link.PublicCode))
		return json.Marshal(map[string]any{"msgtype": "link", "link": map[string]string{"title": detail.Link.Title, "desc": detail.Link.Description, "url": link}})
	default:
		return nil, errors.New("unsupported resource")
	}
}

func (h *Handler) authorize(w http.ResponseWriter, r *http.Request) (Principal, customerdomain.CustomerID, bool) {
	header := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(header, "Bearer ") {
		writeError(w, http.StatusUnauthorized, "authentication_required")
		return Principal{}, 0, false
	}
	principal, customerID, err := h.config.Contexts.VerifySidebarContext(r.Context(), strings.TrimSpace(strings.TrimPrefix(header, "Bearer ")))
	if err != nil || principal.CorpID == "" || principal.EmployeeID == "" || customerID < 1 {
		writeError(w, http.StatusUnauthorized, "invalid_context")
		return Principal{}, 0, false
	}
	return principal, customerID, true
}

func (h *Handler) commandError(w http.ResponseWriter, err error) {
	code := "invalid_request"
	status := http.StatusBadRequest
	if strings.Contains(err.Error(), "conflict") {
		code, status = "conflict", http.StatusConflict
	}
	writeError(w, status, code)
}
func (h *Handler) sectionError(w http.ResponseWriter) {
	writeError(w, http.StatusServiceUnavailable, "section_unavailable")
}
func (h *Handler) writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
func (h *Handler) now() time.Time {
	if h.config.Now != nil {
		return h.config.Now().UTC()
	}
	return time.Now().UTC()
}

func boundedLimit(w http.ResponseWriter, r *http.Request, fallback, maximum int) (int, bool) {
	allowed := map[string]bool{"limit": true, "q": true, "category": true, "product_type": true}
	for key, values := range r.URL.Query() {
		if !allowed[key] || len(values) != 1 {
			writeError(w, http.StatusBadRequest, "invalid_request")
			return 0, false
		}
	}
	if r.URL.Query().Get("limit") == "" {
		return fallback, true
	}
	value, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || value < 1 || value > maximum {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return 0, false
	}
	return value, true
}

func decodeStrict(w http.ResponseWriter, r *http.Request, target any) bool {
	if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		writeError(w, http.StatusUnsupportedMediaType, "json_required")
		return false
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(target) != nil || !errors.Is(decoder.Decode(&struct{}{}), io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_request")
		return false
	}
	return true
}

func idempotencyKey(r *http.Request) string {
	return strings.TrimSpace(r.Header.Get("Idempotency-Key"))
}
func writeError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code}})
}

func ContentDigest(payload json.RawMessage) string {
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:])
}
