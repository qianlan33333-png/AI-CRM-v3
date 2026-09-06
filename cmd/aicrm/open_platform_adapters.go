package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	archiveport "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/port"
	openplatformport "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/port"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
)

var errOpenPlatformRouteUnavailable = errors.New("open platform route is not composed")

type openPlatformExecutor struct {
	identity   identityport.Resolver
	orders     orderport.Query
	profiles   customerport.SidebarProfileService
	archive    archiveport.CustomerMessageReader
	weComScope string
}

func newOpenPlatformExecutor(identity identityport.Resolver, orders orderport.Query, profiles customerport.SidebarProfileService, archive archiveport.CustomerMessageReader, weComCorpID string) (*openPlatformExecutor, error) {
	if identity == nil || orders == nil || profiles == nil || archive == nil {
		return nil, errors.New("open platform core Port dependencies are required")
	}
	scope := ""
	if corpID := strings.TrimSpace(weComCorpID); corpID != "" {
		scope = "wecom-corp:" + corpID
	}
	return &openPlatformExecutor{identity: identity, orders: orders, profiles: profiles, archive: archive, weComScope: scope}, nil
}

func (executor *openPlatformExecutor) Execute(ctx context.Context, request openplatformport.Request) (openplatformport.Response, error) {
	switch request.Method + " " + request.Path {
	case "GET /api/identity/resolve", "GET /api/external/users/resolve":
		return executor.resolveIdentity(ctx, request.Query)
	case "GET /api/external/orders":
		return executor.listOrders(ctx, request.Query)
	case "GET /api/external/orders/{order_no}":
		return executor.getOrder(ctx, request.PathParts["order_no"])
	case "POST /mcp":
		return executor.callMCP(ctx, request.Body)
	default:
		return openplatformport.Response{}, errOpenPlatformRouteUnavailable
	}
}

func (executor *openPlatformExecutor) resolveIdentity(ctx context.Context, query url.Values) (openplatformport.Response, error) {
	reference, err := executor.referenceFromValues(query)
	if err != nil {
		return responseError(400, "invalid_request"), nil
	}
	result, err := executor.identity.Resolve(ctx, reference)
	if err != nil {
		return responseError(503, "identity_unavailable"), nil
	}
	if result.Status == identityport.ResolveNotFound {
		return responseError(404, "not_found"), nil
	}
	if result.Status == identityport.ResolveConflict {
		return responseError(409, "identity_conflict"), nil
	}
	if result.Status != identityport.ResolveFound || result.CustomerID < 1 {
		return responseError(409, "identity_pending"), nil
	}
	profile, profileErr := executor.profiles.ReadSidebarProfile(ctx, result.CustomerID)
	if profileErr != nil {
		return responseError(503, "customer_profile_unavailable"), nil
	}
	return responseOK(map[string]any{"ok": true, "identity": map[string]any{"customer_id": result.CustomerID, "identity_id": result.IdentityID, "status": result.Status}, "customer": profile}), nil
}

func (executor *openPlatformExecutor) listOrders(ctx context.Context, values url.Values) (openplatformport.Response, error) {
	query, err := executor.orderQuery(ctx, values)
	if err != nil {
		return responseError(400, "invalid_request"), nil
	}
	page, err := executor.orders.List(ctx, query)
	if err != nil {
		return responseForOrderError(err), nil
	}
	items := make([]map[string]any, 0, len(page.Items))
	for _, order := range page.Items {
		items = append(items, publicOrder(order))
	}
	return responseOK(map[string]any{"ok": true, "items": items, "total": page.Total, "limit": query.Limit, "next_cursor": page.NextCursor, "has_more": page.NextCursor != ""}), nil
}

func (executor *openPlatformExecutor) getOrder(ctx context.Context, reference string) (openplatformport.Response, error) {
	if strings.TrimSpace(reference) == "" {
		return responseError(400, "invalid_request"), nil
	}
	order, err := executor.orders.GetByReference(ctx, reference)
	if err != nil {
		return responseForOrderError(err), nil
	}
	return responseOK(map[string]any{"ok": true, "order": publicOrder(order)}), nil
}

func (executor *openPlatformExecutor) callMCP(ctx context.Context, body []byte) (openplatformport.Response, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var call struct {
		Method string `json:"method"`
		Params struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		} `json:"params"`
	}
	if err := decoder.Decode(&call); err != nil || call.Method != "tools/call" || strings.TrimSpace(call.Params.Name) == "" {
		return openplatformport.Response{}, errors.New("invalid MCP tool request")
	}
	if call.Params.Arguments == nil {
		call.Params.Arguments = map[string]any{}
	}
	customerID, err := executor.mcpCustomerID(ctx, call.Params.Arguments)
	if err != nil {
		return openplatformport.Response{}, err
	}
	profile, err := executor.profiles.ReadSidebarProfile(ctx, customerID)
	if err != nil {
		return openplatformport.Response{}, err
	}
	var content map[string]any
	switch call.Params.Name {
	case "resolve_customer":
		content = map[string]any{"customer_id": customerID, "customer": profile}
		if include, valid := optionalBool(call.Params.Arguments, "include_context"); !valid {
			return openplatformport.Response{}, errors.New("invalid include_context")
		} else if include {
			contextValue, contextErr := executor.customerContext(ctx, customerID, limitArgument(call.Params.Arguments, "recent_message_limit", 20))
			if contextErr != nil {
				return openplatformport.Response{}, contextErr
			}
			content["context"] = contextValue
		}
	case "get_customer_context":
		contextValue, contextErr := executor.customerContext(ctx, customerID, limitArgument(call.Params.Arguments, "recent_message_limit", 20))
		if contextErr != nil {
			return openplatformport.Response{}, contextErr
		}
		content = map[string]any{"customer_id": customerID, "customer": profile, "context": contextValue}
	case "get_recent_messages":
		messages, messageErr := executor.recentMessages(ctx, customerID, limitArgument(call.Params.Arguments, "limit", 20))
		if errors.Is(messageErr, archiveport.ErrNotReady) {
			content = map[string]any{"customer_id": customerID, "messages": []any{}, "archive_status": "not_ready"}
			break
		}
		if messageErr != nil {
			return openplatformport.Response{}, messageErr
		}
		content = map[string]any{"customer_id": customerID, "messages": messages}
	default:
		return openplatformport.Response{}, errors.New("unknown MCP tool")
	}
	return responseOK(map[string]any{"content": []map[string]any{{"type": "json", "json": content}}, "structuredContent": content}), nil
}

func (executor *openPlatformExecutor) customerContext(ctx context.Context, customerID customerdomain.CustomerID, limit int) (map[string]any, error) {
	messages, err := executor.recentMessages(ctx, customerID, limit)
	if errors.Is(err, archiveport.ErrNotReady) {
		return map[string]any{"messages": []any{}, "archive_status": "not_ready"}, nil
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{"messages": messages, "timeline_status": "not_available"}, nil
}

func (executor *openPlatformExecutor) recentMessages(ctx context.Context, customerID customerdomain.CustomerID, limit int) ([]archiveport.MessageItem, error) {
	if limit < 1 || limit > 100 {
		return nil, errors.New("invalid message limit")
	}
	page, err := executor.archive.CustomerMessages(ctx, archiveport.CustomerQuery{CustomerID: customerID, Limit: limit})
	if err != nil {
		return nil, err
	}
	return page.Items, nil
}

func (executor *openPlatformExecutor) mcpCustomerID(ctx context.Context, arguments map[string]any) (customerdomain.CustomerID, error) {
	if raw, ok := arguments["customer_ref"]; ok {
		value, ok := raw.(string)
		if !ok || strings.TrimSpace(value) == "" {
			return 0, errors.New("invalid customer_ref")
		}
		if strings.HasPrefix(value, "customer:") {
			id, err := strconv.ParseInt(strings.TrimPrefix(value, "customer:"), 10, 64)
			if err != nil || id < 1 {
				return 0, errors.New("invalid customer_ref")
			}
			return customerdomain.CustomerID(id), nil
		}
		if isCN11(value) {
			return executor.resolveReference(ctx, identitydomain.Reference{Kind: identitydomain.KindPhone, Scope: "phone:cn11", Value: value, Assurance: identitydomain.AssuranceDeclared, Source: "open_platform.mcp"})
		}
		return executor.resolveReference(ctx, identitydomain.Reference{Kind: identitydomain.KindWeComExternalUserID, Scope: executor.weComScope, Value: value, Assurance: identitydomain.AssuranceDeclared, Source: "open_platform.mcp"})
	}
	value, ok := arguments["external_userid"].(string)
	if !ok || strings.TrimSpace(value) == "" {
		return 0, errors.New("customer_ref or external_userid is required")
	}
	return executor.resolveReference(ctx, identitydomain.Reference{Kind: identitydomain.KindWeComExternalUserID, Scope: executor.weComScope, Value: value, Assurance: identitydomain.AssuranceDeclared, Source: "open_platform.mcp"})
}

func (executor *openPlatformExecutor) resolveReference(ctx context.Context, reference identitydomain.Reference) (customerdomain.CustomerID, error) {
	resolved, err := executor.identity.Resolve(ctx, reference)
	if err != nil {
		return 0, err
	}
	if resolved.Status != identityport.ResolveFound || resolved.CustomerID < 1 {
		return 0, errors.New("customer not found")
	}
	return resolved.CustomerID, nil
}

func (executor *openPlatformExecutor) referenceFromValues(values url.Values) (identitydomain.Reference, error) {
	if kind := strings.TrimSpace(values.Get("kind")); kind != "" {
		reference := identitydomain.Reference{Kind: identitydomain.Kind(kind), Scope: values.Get("scope"), Value: values.Get("value"), Assurance: identitydomain.AssuranceDeclared, Source: "open_platform.api"}
		if _, err := identitydomain.Normalize(reference); err != nil {
			return identitydomain.Reference{}, err
		}
		return reference, nil
	}
	if value := strings.TrimSpace(values.Get("external_userid")); value != "" {
		return identitydomain.Reference{Kind: identitydomain.KindWeComExternalUserID, Scope: firstNonEmpty(values.Get("external_userid_scope"), executor.weComScope), Value: value, Assurance: identitydomain.AssuranceDeclared, Source: "open_platform.api"}, nil
	}
	if value := strings.TrimSpace(values.Get("mobile")); value != "" {
		return identitydomain.Reference{Kind: identitydomain.KindPhone, Scope: "phone:cn11", Value: value, Assurance: identitydomain.AssuranceDeclared, Source: "open_platform.api"}, nil
	}
	if value := strings.TrimSpace(values.Get("unionid")); value != "" {
		return identitydomain.Reference{Kind: identitydomain.KindUnionID, Scope: values.Get("scope"), Value: value, Assurance: identitydomain.AssuranceDeclared, Source: "open_platform.api"}, nil
	}
	if value := strings.TrimSpace(values.Get("openid")); value != "" {
		return identitydomain.Reference{Kind: identitydomain.KindOAOpenID, Scope: values.Get("scope"), Value: value, Assurance: identitydomain.AssuranceDeclared, Source: "open_platform.api"}, nil
	}
	return identitydomain.Reference{}, errors.New("identity query missing")
}

func (executor *openPlatformExecutor) orderQuery(ctx context.Context, values url.Values) (orderport.ListQuery, error) {
	for key, value := range values {
		if len(value) != 1 {
			return orderport.ListQuery{}, errors.New("duplicate query value")
		}
		switch key {
		case "provider", "limit", "cursor", "order_no", "transaction_id", "product_code", "payment_status", "created_from", "created_to", "external_userid", "mobile", "unionid", "scope":
		default:
			return orderport.ListQuery{}, errors.New("unsupported query parameter")
		}
	}
	query := orderport.ListQuery{Cursor: values.Get("cursor"), Limit: 50, OrderRef: firstNonEmpty(values.Get("order_no"), values.Get("transaction_id")), Product: values.Get("product_code")}
	if value := values.Get("limit"); value != "" {
		limit, err := strconv.ParseInt(value, 10, 32)
		if err != nil || limit < 1 || limit > 100 {
			return query, errors.New("invalid limit")
		}
		query.Limit = int32(limit)
	}
	if provider := values.Get("provider"); provider != "" && provider != "all" {
		switch provider {
		case "wechat", "wechat_pay":
			query.Provider = orderdomain.ProviderWeChatPay
		case "wechat_shop":
			query.Provider = orderdomain.ProviderWeChatShop
		case "alipay":
			query.Provider = orderdomain.ProviderAlipay
		default:
			return query, errors.New("invalid provider")
		}
	}
	if status := values.Get("payment_status"); status != "" {
		if status == "unpaid" {
			status = string(orderdomain.StatusPendingPayment)
		}
		if status == "refunding" || status == "refund_processing" {
			status = string(orderdomain.StatusPartiallyRefunded)
		}
		query.Status = orderdomain.Status(status)
	}
	var err error
	if query.CreatedFrom, err = unixSeconds(values.Get("created_from")); err != nil {
		return query, err
	}
	if query.CreatedTo, err = unixSeconds(values.Get("created_to")); err != nil {
		return query, err
	}
	if anyIdentityValue(values) {
		reference, referenceErr := executor.referenceFromValues(values)
		if referenceErr != nil {
			return query, referenceErr
		}
		customer, resolveErr := executor.resolveReference(ctx, reference)
		if resolveErr != nil {
			return query, resolveErr
		}
		query.CustomerID = int64(customer)
	}
	return query, nil
}

func publicOrder(order orderdomain.Snapshot) map[string]any {
	productCode := ""
	if len(order.Items) > 0 {
		productCode = order.Items[0].ProductCode
	}
	provider := string(order.Provider)
	if order.Provider == orderdomain.ProviderWeChatPay {
		provider = "wechat"
	}
	return map[string]any{"provider": provider, "order_no": order.MerchantOrderNo, "transaction_id": order.ProviderTransactionNo, "created_at": order.CreatedAt, "product_code": productCode, "payment_status": order.Status, "amount_total": order.Amount.AmountMinor, "currency": order.Amount.Currency, "is_paid": order.Status == orderdomain.StatusPaid || order.Status == orderdomain.StatusPartiallyRefunded || order.Status == orderdomain.StatusRefunded, "is_refunded": order.RefundedMinor > 0, "refunded_amount_total": order.RefundedMinor, "detail_url": "/api/external/orders/" + url.PathEscape(order.MerchantOrderNo) + "?provider=" + url.QueryEscape(provider)}
}

func responseOK(body any) openplatformport.Response {
	return openplatformport.Response{Status: 200, Body: body}
}
func responseError(status int, code string) openplatformport.Response {
	return openplatformport.Response{Status: status, Body: map[string]any{"ok": false, "error_code": code}}
}
func responseForOrderError(err error) openplatformport.Response {
	if errors.Is(err, orderport.ErrNotFound) {
		return responseError(404, "not_found")
	}
	if errors.Is(err, orderport.ErrConflict) {
		return responseError(409, "conflict")
	}
	return responseError(503, "order_unavailable")
}
func optionalBool(values map[string]any, key string) (bool, bool) {
	value, ok := values[key]
	if !ok {
		return false, true
	}
	result, ok := value.(bool)
	return result, ok
}
func limitArgument(values map[string]any, key string, fallback int) int {
	value, ok := values[key]
	if !ok {
		return fallback
	}
	number, ok := value.(json.Number)
	if !ok {
		return 0
	}
	result, err := number.Int64()
	if err != nil || result < 1 || result > 100 {
		return 0
	}
	return int(result)
}
func anyIdentityValue(values url.Values) bool {
	return values.Get("external_userid") != "" || values.Get("mobile") != "" || values.Get("unionid") != ""
}
func isCN11(value string) bool {
	if len(value) != 11 || value[0] != '1' {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}
func unixSeconds(value string) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	seconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil || seconds < 0 || seconds > 9_999_999_999 {
		return nil, errors.New("invalid unix seconds")
	}
	parsed := time.Unix(seconds, 0).UTC()
	return &parsed, nil
}
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

var _ openplatformport.Executor = (*openPlatformExecutor)(nil)
