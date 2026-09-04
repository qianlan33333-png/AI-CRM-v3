package http

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	nethttp "net/http"
	"strconv"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerapp "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/app"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
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
	UnitOfWork        platformport.UnitOfWork
	Auth              Authenticator
	CSRF              CSRFAuthorizer
	Directory         customerapp.Directory
	Store             customerapp.Store
	Identities        identityport.DirectoryIdentityReader
	Audit             Auditor
	Canonical         customerport.CanonicalCustomerResolver
	Owners            customerport.CustomerOwnerReader
	Tags              customerport.CustomerTagReader
	Surveys           customerport.CustomerSurveyReader
	Timeline          customerport.CustomerTimelineReader
	Chat              customerport.CustomerChatActivityReader
	Orders            orderport.CustomerOrderSummaryReader
	ProfileSigningKey []byte
}

type Handler struct {
	uow               platformport.UnitOfWork
	auth              Authenticator
	csrf              CSRFAuthorizer
	directory         customerapp.Directory
	store             customerapp.Store
	identities        identityport.DirectoryIdentityReader
	audit             Auditor
	canonical         customerport.CanonicalCustomerResolver
	owners            customerport.CustomerOwnerReader
	tags              customerport.CustomerTagReader
	surveys           customerport.CustomerSurveyReader
	timeline          customerport.CustomerTimelineReader
	chat              customerport.CustomerChatActivityReader
	orders            orderport.CustomerOrderSummaryReader
	profileSigningKey []byte
}

func NewHandler(config Config) (*Handler, error) {
	if config.UnitOfWork == nil || config.Auth == nil || config.CSRF == nil || config.Directory.Store == nil || config.Store == nil || config.Identities == nil || config.Audit == nil ||
		config.Canonical == nil || config.Owners == nil || config.Tags == nil || config.Surveys == nil || config.Timeline == nil || config.Chat == nil || len(config.ProfileSigningKey) < 32 {
		return nil, errors.New("customer HTTP dependencies are required")
	}
	return &Handler{uow: config.UnitOfWork, auth: config.Auth, csrf: config.CSRF, directory: config.Directory,
		store: config.Store, identities: config.Identities, audit: config.Audit, canonical: config.Canonical,
		owners: config.Owners, tags: config.Tags, surveys: config.Surveys, timeline: config.Timeline, chat: config.Chat, orders: config.Orders,
		profileSigningKey: append([]byte(nil), config.ProfileSigningKey...)}, nil
}

func (handler *Handler) Routes() nethttp.Handler {
	mux := nethttp.NewServeMux()
	mux.HandleFunc("GET /api/admin/customers", handler.list)
	mux.HandleFunc("GET /api/admin/customers/{customer_id}", handler.detail)
	mux.HandleFunc("GET /api/admin/customers/{customer_id}/360", handler.customer360)
	mux.HandleFunc("POST /api/admin/customers/{customer_id}/phone-reveal", handler.revealPhone)
	mux.HandleFunc("GET /api/admin/customers/{customer_id}/owners", handler.ownerSection)
	mux.HandleFunc("GET /api/admin/customers/{customer_id}/tags", handler.tagSection)
	mux.HandleFunc("GET /api/admin/customers/{customer_id}/survey-answers", handler.surveySection)
	mux.HandleFunc("GET /api/admin/customers/{customer_id}/timeline", handler.timelineSection)
	mux.HandleFunc("GET /api/admin/customers/{customer_id}/chat-activity", handler.chatSection)
	return mux
}

func (handler *Handler) customer360(response nethttp.ResponseWriter, request *nethttp.Request) {
	response.Header().Set("Cache-Control", "private, no-store")
	canonicalID, ok := handler.authorizedCanonical(response, request)
	if !ok {
		return
	}
	if len(request.URL.Query()) != 0 {
		handler.writeError(response, customerapp.ErrInvalidQuery)
		return
	}
	type section struct {
		Status    string `json:"status"`
		Data      any    `json:"data,omitempty"`
		ErrorCode string `json:"error_code,omitempty"`
	}
	degraded := func() section { return section{Status: "degraded", ErrorCode: "section_unavailable"} }
	identitySection, profileSection, orderSection, surveySection, timelineSection := degraded(), degraded(), degraded(), degraded(), degraded()

	var detail customerapp.Detail
	var identities []identityport.DirectoryIdentitySummary
	var phones []identityport.MaskedPhone
	if err := handler.uow.Within(request.Context(), func(txctx context.Context) error {
		var err error
		detail, err = handler.store.Detail(txctx, canonicalID)
		if err != nil {
			return err
		}
		identities, phones, err = handler.identities.DirectoryIdentities(txctx, canonicalID)
		return err
	}); err == nil {
		safeIdentities := make([]map[string]string, 0, len(identities))
		for _, identity := range identities {
			if identity.Kind != identitydomain.KindPhone {
				safeIdentities = append(safeIdentities, map[string]string{"type": safeIdentityType(identity.Kind), "summary": safeIdentitySummary(identity.Kind)})
			}
		}
		safePhones := make([]map[string]string, 0, len(phones))
		for _, phone := range phones {
			safePhones = append(safePhones, map[string]string{"masked": localCNPhone(phone.Masked)})
		}
		identitySection = section{Status: "ready", Data: map[string]any{"identities": safeIdentities, "phones": safePhones}}
		profileSection = section{Status: "ready", Data: safeDirectoryDetail(detail)}
	}

	orderSummary, orderErr := orderport.CustomerOrderSummary{}, errors.New("order section unavailable")
	if handler.orders != nil {
		orderSummary, orderErr = handler.orders.CustomerOrderSummary(request.Context(), int64(canonicalID), 20)
	}
	if orderErr == nil {
		orderSection = section{Status: "ready", Data: orderSummary}
	}

	surveys, surveyErr := handler.surveys.CustomerSurveys(request.Context(), canonicalID, customerport.PageQuery{Limit: 20, Watermark: time.Now().UTC()})
	if surveyErr == nil {
		surveySection = section{Status: string(surveys.Status.State), Data: map[string]any{"total": len(surveys.Items), "recent": surveys.Items}}
		if surveySection.Status == "" {
			surveySection.Status = "ready"
		}
	}
	timeline, timelineErr := handler.timeline.CustomerTimeline(request.Context(), canonicalID, customerport.PageQuery{Limit: 30, Watermark: time.Now().UTC()})
	if timelineErr == nil {
		timelineSection = section{Status: string(timeline.Status.State), Data: timeline.Items}
		if timelineSection.Status == "" {
			timelineSection.Status = "ready"
		}
	}

	riskLevel := "low"
	reasons := []string{}
	if identitySection.Status != "ready" {
		riskLevel = "unknown"
		reasons = append(reasons, "identity_section_unavailable")
	}
	if orderSummary.Failed > 0 {
		riskLevel = "medium"
		reasons = append(reasons, "payment_failures_present")
	}
	if orderSummary.Refunded > 0 {
		riskLevel = "medium"
		reasons = append(reasons, "refunds_present")
	}
	writePrivateJSON(response, nethttp.StatusOK, map[string]any{
		"canonical_customer_id": canonicalID,
		"identity_summary":      identitySection,
		"profile":               profileSection,
		"order_summary":         orderSection,
		"questionnaire_summary": surveySection,
		"risk":                  section{Status: "ready", Data: map[string]any{"level": riskLevel, "reasons": reasons}},
		"recent_touchpoints":    timelineSection,
	})
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
	response.Header().Set("Cache-Control", "private, no-store")
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
	var canonical customerport.CanonicalCustomer
	var identities []identityport.DirectoryIdentitySummary
	var phones []identityport.MaskedPhone
	err = handler.uow.Within(request.Context(), func(txContext context.Context) error {
		var queryErr error
		canonical, queryErr = handler.canonical.ResolveCanonicalCustomer(txContext, customerdomain.CustomerID(id))
		if queryErr != nil {
			return queryErr
		}
		detail, queryErr = handler.store.Detail(txContext, canonical.CustomerID)
		if queryErr != nil {
			return queryErr
		}
		identities, phones, queryErr = handler.identities.DirectoryIdentities(txContext, canonical.CustomerID)
		return queryErr
	})
	if err != nil {
		handler.writeError(response, err)
		return
	}
	safeIdentities := make([]map[string]string, 0, len(identities))
	for _, identity := range identities {
		if identity.Kind == identitydomain.KindPhone {
			continue
		}
		safeIdentities = append(safeIdentities, map[string]string{"type": safeIdentityType(identity.Kind), "summary": safeIdentitySummary(identity.Kind)})
	}
	safePhones := make([]map[string]string, 0, len(phones))
	for _, phone := range phones {
		safePhones = append(safePhones, map[string]string{"masked": localCNPhone(phone.Masked)})
	}
	sections := map[string]customerport.SectionStatus{
		"owners": handler.owners.CapabilityStatus(), "tags": handler.tags.CapabilityStatus(), "surveys": handler.surveys.CapabilityStatus(),
		"timeline": handler.timeline.CapabilityStatus(), "chat_activity": handler.chat.CapabilityStatus(),
	}
	writePrivateJSON(response, nethttp.StatusOK, map[string]any{"requested_customer_id": canonical.RequestedCustomerID,
		"canonical_customer_id": canonical.CustomerID, "merged_from_customer_id": mergedFrom(canonical), "customer": safeDirectoryDetail(detail),
		"identities": safeIdentities, "phones": safePhones, "sections": sections})
}

func safeDirectoryDetail(detail customerapp.Detail) map[string]any {
	return map[string]any{"customer_id": detail.CustomerID, "status": detail.CustomerStatus, "display_name": detail.DisplayName,
		"oneid": detail.OneIDLabel, "last_synced_at": detail.LastSyncedAt, "updated_at": detail.UpdatedAt, "gender": detail.Gender,
		"contact_type": detail.ContactType, "corp_name": detail.CorpName, "source": detail.Source}
}

func (handler *Handler) ownerSection(response nethttp.ResponseWriter, request *nethttp.Request) {
	response.Header().Set("Cache-Control", "private, no-store")
	id, ok := handler.authorizedCanonical(response, request)
	if !ok {
		return
	}
	if len(request.URL.Query()) != 0 {
		handler.writeError(response, customerapp.ErrInvalidQuery)
		return
	}
	page, err := handler.owners.CustomerOwners(request.Context(), id)
	if err != nil {
		handler.writeError(response, err)
		return
	}
	writePrivateJSON(response, nethttp.StatusOK, map[string]any{"customer_id": id, "items": page.Items, "next_cursor": "",
		"source_status": page.Status.State, "source_error_code": page.Status.ErrorCode, "as_of": page.Status.AsOf,
		"truncated": false, "unmatched_count": page.UnmatchedCount})
}

func (handler *Handler) tagSection(response nethttp.ResponseWriter, request *nethttp.Request) {
	response.Header().Set("Cache-Control", "private, no-store")
	id, ok := handler.authorizedCanonical(response, request)
	if !ok {
		return
	}
	if len(request.URL.Query()) != 0 {
		handler.writeError(response, customerapp.ErrInvalidQuery)
		return
	}
	page, err := handler.tags.CustomerTags(request.Context(), id)
	if err != nil {
		handler.writeError(response, err)
		return
	}
	writePrivateJSON(response, nethttp.StatusOK, map[string]any{"customer_id": id, "items": page.Items, "next_cursor": "",
		"source_status": page.Status.State, "source_error_code": page.Status.ErrorCode, "as_of": page.Status.AsOf, "truncated": false})
}

func (handler *Handler) surveySection(response nethttp.ResponseWriter, request *nethttp.Request) {
	response.Header().Set("Cache-Control", "private, no-store")
	id, ok := handler.authorizedCanonical(response, request)
	if !ok {
		return
	}
	if !onlyQueryKeys(request, "cursor", "limit") {
		handler.writeError(response, customerapp.ErrInvalidQuery)
		return
	}
	query, limit, err := profilePageQuery(request.URL.Query().Get("limit"), request.URL.Query().Get("cursor"), "surveys", "", id, handler.profileSigningKey)
	if err != nil {
		handler.writeError(response, err)
		return
	}
	page, err := handler.surveys.CustomerSurveys(request.Context(), id, query)
	if err != nil {
		handler.writeError(response, err)
		return
	}
	items, next, truncated, err := pageSurveys(page.Items, limit, id, query, handler.profileSigningKey)
	if err != nil {
		handler.writeError(response, err)
		return
	}
	writePrivateJSON(response, nethttp.StatusOK, map[string]any{"customer_id": id, "items": items, "next_cursor": next,
		"source_status": page.Status.State, "source_error_code": page.Status.ErrorCode, "as_of": page.Status.AsOf, "truncated": truncated})
}

func (handler *Handler) timelineSection(response nethttp.ResponseWriter, request *nethttp.Request) {
	response.Header().Set("Cache-Control", "private, no-store")
	id, ok := handler.authorizedCanonical(response, request)
	if !ok {
		return
	}
	if !onlyQueryKeys(request, "cursor", "limit", "event_type") {
		handler.writeError(response, customerapp.ErrInvalidQuery)
		return
	}
	filter := request.URL.Query().Get("event_type")
	if filter != "" && !validProfileFilter(filter) {
		handler.writeError(response, customerapp.ErrInvalidQuery)
		return
	}
	query, limit, err := profilePageQuery(request.URL.Query().Get("limit"), request.URL.Query().Get("cursor"), "timeline", filter, id, handler.profileSigningKey)
	if err != nil {
		handler.writeError(response, err)
		return
	}
	page, err := handler.timeline.CustomerTimeline(request.Context(), id, query)
	if err != nil {
		handler.writeError(response, err)
		return
	}
	items, next, truncated, err := pageTimeline(page.Items, limit, id, filter, query, handler.profileSigningKey)
	if err != nil {
		handler.writeError(response, err)
		return
	}
	writePrivateJSON(response, nethttp.StatusOK, map[string]any{"customer_id": id, "items": items, "next_cursor": next,
		"source_status": page.Status.State, "source_error_code": page.Status.ErrorCode, "as_of": page.Status.AsOf, "truncated": truncated})
}

func (handler *Handler) chatSection(response nethttp.ResponseWriter, request *nethttp.Request) {
	response.Header().Set("Cache-Control", "private, no-store")
	id, ok := handler.authorizedCanonical(response, request)
	if !ok {
		return
	}
	if !onlyQueryKeys(request, "cursor", "limit", "chat_type") {
		handler.writeError(response, customerapp.ErrInvalidQuery)
		return
	}
	filter := request.URL.Query().Get("chat_type")
	if filter != "" && filter != "private" && filter != "group" {
		handler.writeError(response, customerapp.ErrInvalidQuery)
		return
	}
	query, limit, err := profilePageQuery(request.URL.Query().Get("limit"), request.URL.Query().Get("cursor"), "chat_activity", filter, id, handler.profileSigningKey)
	if err != nil {
		handler.writeError(response, err)
		return
	}
	page, err := handler.chat.CustomerChatActivity(request.Context(), id, query)
	if err != nil {
		handler.writeError(response, err)
		return
	}
	items, next, truncated, err := pageChat(page.Items, limit, id, filter, query, handler.profileSigningKey)
	if err != nil {
		handler.writeError(response, err)
		return
	}
	writePrivateJSON(response, nethttp.StatusOK, map[string]any{"customer_id": id, "items": items, "next_cursor": next,
		"source_status": page.Status.State, "source_error_code": page.Status.ErrorCode, "as_of": page.Status.AsOf, "truncated": truncated})
}

func (handler *Handler) authorizedCanonical(response nethttp.ResponseWriter, request *nethttp.Request) (customerdomain.CustomerID, bool) {
	if _, err := handler.auth.Authenticate(request.Context(), request); err != nil {
		handler.writeError(response, err)
		return 0, false
	}
	rawID, err := positiveID(request.PathValue("customer_id"))
	if err != nil {
		handler.writeError(response, err)
		return 0, false
	}
	var canonical customerport.CanonicalCustomer
	err = handler.uow.Within(request.Context(), func(txContext context.Context) error {
		var resolveErr error
		canonical, resolveErr = handler.canonical.ResolveCanonicalCustomer(txContext, customerdomain.CustomerID(rawID))
		return resolveErr
	})
	if err != nil {
		handler.writeError(response, err)
		return 0, false
	}
	return canonical.CustomerID, true
}

func pageSurveys(items []customerport.SurveyItem, limit int, id customerdomain.CustomerID, query customerport.PageQuery, key []byte) ([]customerport.SurveyItem, string, bool, error) {
	if len(items) <= limit {
		return items, "", false, nil
	}
	items = items[:limit]
	last := items[len(items)-1]
	next, err := nextProfileCursor("surveys", id, "", query, last.SubmittedAt, last.ID, key)
	return items, next, true, err
}

func pageTimeline(items []customerport.TimelineItem, limit int, id customerdomain.CustomerID, filter string, query customerport.PageQuery, key []byte) ([]customerport.TimelineItem, string, bool, error) {
	if len(items) <= limit {
		return items, "", false, nil
	}
	items = items[:limit]
	last := items[len(items)-1]
	next, err := nextProfileCursor("timeline", id, filter, query, last.OccurredAt, last.ID, key)
	return items, next, true, err
}

func pageChat(items []customerport.ChatActivityItem, limit int, id customerdomain.CustomerID, filter string, query customerport.PageQuery, key []byte) ([]customerport.ChatActivityItem, string, bool, error) {
	if len(items) <= limit {
		return items, "", false, nil
	}
	items = items[:limit]
	last := items[len(items)-1]
	next, err := nextProfileCursor("chat_activity", id, filter, query, last.OccurredAt, last.ID, key)
	return items, next, true, err
}

func onlyQueryKeys(request *nethttp.Request, allowed ...string) bool {
	set := map[string]struct{}{}
	for _, key := range allowed {
		set[key] = struct{}{}
	}
	for key, values := range request.URL.Query() {
		if _, ok := set[key]; !ok || len(values) != 1 {
			return false
		}
	}
	return true
}

func validProfileFilter(value string) bool {
	if len(value) > 96 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' && character != '.' {
			return false
		}
	}
	return true
}

func (handler *Handler) revealPhone(response nethttp.ResponseWriter, request *nethttp.Request) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Pragma", "no-cache")
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
	var canonical customerport.CanonicalCustomer
	err = handler.uow.Within(request.Context(), func(txContext context.Context) error {
		var queryErr error
		canonical, queryErr = handler.canonical.ResolveCanonicalCustomer(txContext, customerdomain.CustomerID(id))
		if queryErr != nil {
			return queryErr
		}
		phone, found, queryErr = handler.identities.RevealPhone(txContext, canonical.CustomerID)
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
		key, keyErr := revealAuditKey(principal.InternalID, int64(canonical.CustomerID))
		if keyErr != nil {
			return keyErr
		}
		_, queryErr = handler.audit.Append(txContext, platformaudit.Event{IdempotencyKey: key,
			Action: "customer.phone_revealed", ActorType: string(principal.Kind), ActorID: strconv.FormatInt(principal.InternalID, 10),
			ResourceType: "customer", ResourceID: strconv.FormatInt(int64(canonical.CustomerID), 10), Payload: payload})
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
	value := raw
	if len(value) != 11 || value[0] != '1' || value[1] < '3' || value[1] > '9' {
		return "", identitydomain.ErrInvalidReference
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return "", identitydomain.ErrInvalidReference
		}
	}
	return value, nil
}

func localCNPhone(value string) string {
	return value
}

func safeIdentityType(kind identitydomain.Kind) string {
	switch kind {
	case identitydomain.KindWeComExternalUserID:
		return "wecom"
	case identitydomain.KindUnionID:
		return "wechat_open_platform"
	case identitydomain.KindMPOpenID:
		return "wechat_miniprogram"
	case identitydomain.KindOAOpenID:
		return "wechat_official_account"
	case identitydomain.KindAlipayUserID, identitydomain.KindAlipayOAuthUserID, identitydomain.KindAlipayOAuthOpenID, identitydomain.KindAlipayBuyerID, identitydomain.KindAlipayBuyerOpenID:
		return "alipay"
	case identitydomain.KindFirstPartyMemberID:
		return "member"
	default:
		return "other"
	}
}

func safeIdentitySummary(kind identitydomain.Kind) string {
	switch safeIdentityType(kind) {
	case "wecom":
		return "企微身份已验证"
	case "wechat_open_platform":
		return "微信开放平台身份已关联"
	case "wechat_miniprogram":
		return "微信小程序身份已关联"
	case "wechat_official_account":
		return "微信公众号身份已关联"
	case "alipay":
		return "支付宝身份已关联"
	case "member":
		return "会员身份已关联"
	default:
		return "其他身份已关联"
	}
}

func mergedFrom(value customerport.CanonicalCustomer) any {
	if !value.Merged {
		return nil
	}
	return value.RequestedCustomerID
}

func writePrivateJSON(response nethttp.ResponseWriter, status int, payload any) {
	response.Header().Set("Cache-Control", "private, no-store")
	response.Header().Set("Pragma", "no-cache")
	writeJSON(response, status, payload)
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
	case errors.Is(err, customerport.ErrCapabilityNotReady):
		status, code = nethttp.StatusServiceUnavailable, "capability_not_ready"
	case errors.Is(err, customerport.ErrSectionUnavailable):
		status, code = nethttp.StatusServiceUnavailable, "section_unavailable"
	}
	writeJSON(response, status, map[string]any{"ok": false, "error": code})
}

func writeJSON(response nethttp.ResponseWriter, status int, payload any) {
	response.Header().Set("Content-Type", "application/json; charset=utf-8")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(payload)
}
