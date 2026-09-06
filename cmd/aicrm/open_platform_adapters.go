package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	customerport "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	archiveport "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/port"
	openplatformport "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/port"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
)

var (
	errOpenPlatformRouteUnavailable    = errors.New("open platform route is not composed")
	errOpenPlatformResourceOutOfScope  = errors.New("machine client cannot access this resource")
	errOpenPlatformIdentityConflict    = errors.New("identity references resolve to different customers")
	errOpenPlatformIdentityNotFound    = errors.New("identity reference not found")
	errOpenPlatformIdentityPending     = errors.New("identity reference is pending")
	errOpenPlatformIdentityScopeDenied = errors.New("identity scope is not configured for this platform")
)

type openPlatformIdentityScopes struct {
	WeComScope   string
	UnionScopes  []string
	OpenIDScopes []string
}

type openPlatformExecutor struct {
	identity     identityport.Resolver
	orders       orderport.Query
	scopedOrders orderport.CustomerScopedQuery
	profiles     customerport.SidebarProfileService
	archive      archiveport.CustomerMessageReader
	timeline     customerport.CustomerTimelineReader
	owners       wecomport.AudiencePrimaryOwnerReader
	scopes       openPlatformIdentityScopes
}

func newOpenPlatformExecutor(identity identityport.Resolver, orders orderport.Query, profiles customerport.SidebarProfileService, archive archiveport.CustomerMessageReader, timeline customerport.CustomerTimelineReader, owners wecomport.AudiencePrimaryOwnerReader, scopes openPlatformIdentityScopes) (*openPlatformExecutor, error) {
	if identity == nil || orders == nil || profiles == nil || archive == nil || timeline == nil || owners == nil {
		return nil, errors.New("open platform core Port dependencies are required")
	}
	scopedOrders, ok := orders.(orderport.CustomerScopedQuery)
	if !ok || scopedOrders == nil {
		return nil, errors.New("open platform customer-scoped order Port is required")
	}
	scopes.WeComScope = strings.TrimSpace(scopes.WeComScope)
	scopes.UnionScopes = distinctScopes(scopes.UnionScopes, "wechat-open-platform:")
	scopes.OpenIDScopes = distinctScopes(scopes.OpenIDScopes, "wechat-app:")
	return &openPlatformExecutor{identity: identity, orders: orders, scopedOrders: scopedOrders, profiles: profiles, archive: archive, timeline: timeline, owners: owners, scopes: scopes}, nil
}

func configuredOpenPlatformScopes(corpID string, unionScopes, appIDs []string) openPlatformIdentityScopes {
	weComScope := ""
	if corpID = strings.TrimSpace(corpID); corpID != "" {
		weComScope = "wecom-corp:" + corpID
	}
	openIDScopes := make([]string, 0, len(appIDs))
	for _, appID := range appIDs {
		if appID = strings.TrimSpace(appID); appID != "" {
			openIDScopes = append(openIDScopes, "wechat-app:"+appID)
		}
	}
	return openPlatformIdentityScopes{WeComScope: weComScope, UnionScopes: unionScopes, OpenIDScopes: openIDScopes}
}

func distinctScopes(values []string, prefix string) []string {
	result := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, raw := range values {
		value := strings.TrimSpace(raw)
		if !strings.HasPrefix(value, prefix) || len(value) == len(prefix) {
			continue
		}
		if _, exists := seen[value]; !exists {
			seen[value] = struct{}{}
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result
}

func (executor *openPlatformExecutor) Execute(ctx context.Context, request openplatformport.Request) (openplatformport.Response, error) {
	switch request.Method + " " + request.Path {
	case "GET /api/identity/resolve", "GET /api/external/users/resolve":
		return executor.resolveIdentity(ctx, request.Query, request.Principal)
	case "GET /api/external/orders":
		return executor.listOrders(ctx, request.Query, request.Principal)
	case "GET /api/external/orders/{order_no}":
		return executor.getOrder(ctx, request.PathParts["order_no"], request.Principal)
	case "POST /mcp":
		return executor.callMCP(ctx, request.Body, request.Principal)
	default:
		return openplatformport.Response{}, errOpenPlatformRouteUnavailable
	}
}

func (executor *openPlatformExecutor) resolveIdentity(ctx context.Context, query url.Values, principal accessdomain.MachinePrincipal) (openplatformport.Response, error) {
	references, err := executor.referencesFromValues(query)
	if err != nil {
		return responseForIdentityError(err), nil
	}
	result, err := executor.resolveReferences(ctx, references)
	if err != nil {
		return responseForIdentityError(err), nil
	}
	if err := executor.ensureCustomerScope(ctx, principal, result.CustomerID, references); err != nil {
		return responseError(404, "not_found"), nil
	}
	profile, profileErr := executor.profiles.ReadSidebarProfile(ctx, result.CustomerID)
	if profileErr != nil {
		return responseError(503, "customer_profile_unavailable"), nil
	}
	return responseOK(map[string]any{"ok": true, "identity": map[string]any{"customer_id": result.CustomerID, "identity_id": result.IdentityID, "status": result.Status}, "customer": profile}), nil
}

// ensureCustomerScope resolves all scope keys from trusted machine state and
// canonical/WeCom read ports. Request query values are never used as scope
// evidence. This check happens before a customer profile, archive, or order
// Port can read the resource.
func (executor *openPlatformExecutor) ensureCustomerScope(ctx context.Context, principal accessdomain.MachinePrincipal, customerID customerdomain.CustomerID, references []identitydomain.Reference) error {
	if len(principal.OwnerScope) == 0 {
		return nil
	}
	resources := map[string]string{"customer_id": strconv.FormatInt(int64(customerID), 10)}
	if principal.CorpID != "" {
		resources["corp_id"] = principal.CorpID
	}
	for _, reference := range references {
		resources[string(reference.Kind)] = reference.Value
		if reference.Kind == identitydomain.KindWeComExternalUserID {
			resources["external_userid"] = reference.Value
		}
	}
	if _, requiresOwner := principal.OwnerScope["owner_userid"]; requiresOwner {
		if executor.scopes.WeComScope == "" {
			return errOpenPlatformResourceOutOfScope
		}
		owners, err := executor.owners.AudiencePrimaryOwners(ctx, []customerdomain.CustomerID{customerID})
		if err != nil || len(owners) != 1 || owners[0].CustomerID != customerID || owners[0].Status != "known" || owners[0].OwnerUserID == "" || owners[0].CorpScope != executor.scopes.WeComScope {
			return errOpenPlatformResourceOutOfScope
		}
		resources["owner_userid"] = owners[0].OwnerUserID
	}
	if !principal.OwnerScope.Allows(resources) {
		return errOpenPlatformResourceOutOfScope
	}
	return nil
}

// allowsUnboundScope is only safe for routes whose owning Query Port has no
// resource scope input. The V3 service is single-corporation, so corp_id can
// be checked from the credential record; every customer or owner scope is
// denied before the broad query starts.
func (executor *openPlatformExecutor) allowsUnboundScope(principal accessdomain.MachinePrincipal) bool {
	if len(principal.OwnerScope) == 0 {
		return true
	}
	return principal.OwnerScope.Allows(map[string]string{"corp_id": principal.CorpID})
}

func (executor *openPlatformExecutor) listOrders(ctx context.Context, values url.Values, principal accessdomain.MachinePrincipal) (openplatformport.Response, error) {
	query, references, err := executor.orderQuery(ctx, values)
	if err != nil {
		return responseError(400, "invalid_request"), nil
	}
	if query.CustomerID > 0 {
		if err := executor.ensureCustomerScope(ctx, principal, customerdomain.CustomerID(query.CustomerID), references); err != nil {
			return responseError(404, "not_found"), nil
		}
	} else if !executor.allowsUnboundScope(principal) {
		return responseError(404, "not_found"), nil
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

func (executor *openPlatformExecutor) getOrder(ctx context.Context, reference string, principal accessdomain.MachinePrincipal) (openplatformport.Response, error) {
	if strings.TrimSpace(reference) == "" {
		return responseError(400, "invalid_request"), nil
	}
	if customerID, scoped := scopedOrderCustomerID(principal); scoped {
		order, err := executor.scopedOrders.GetByReferenceForCustomer(ctx, reference, customerID)
		if err != nil {
			return responseForOrderError(err), nil
		}
		return responseOK(map[string]any{"ok": true, "order": publicOrder(order)}), nil
	}
	if !executor.allowsUnboundScope(principal) {
		// Owner scopes and multi-customer constraints do not have an Order
		// resource predicate for detail lookup. Refuse them rather than read
		// broadly and inspect the returned order in this adapter.
		return responseError(404, "not_found"), nil
	}
	order, err := executor.orders.GetByReference(ctx, reference)
	if err != nil {
		return responseForOrderError(err), nil
	}
	return responseOK(map[string]any{"ok": true, "order": publicOrder(order)}), nil
}

// scopedOrderCustomerID accepts only the owner-scope shape that the Order
// Port can enforce atomically: one concrete customer plus an optional matching
// corporation. Other constraints must not fall back to an unrestricted detail
// query.
func scopedOrderCustomerID(principal accessdomain.MachinePrincipal) (int64, bool) {
	values, exists := principal.OwnerScope["customer_id"]
	if !exists || len(values) != 1 {
		return 0, false
	}
	customerID, err := strconv.ParseInt(values[0], 10, 64)
	if err != nil || customerID < 1 {
		return 0, false
	}
	if !principal.OwnerScope.Allows(map[string]string{"customer_id": strconv.FormatInt(customerID, 10), "corp_id": principal.CorpID}) {
		return 0, false
	}
	return customerID, true
}

func (executor *openPlatformExecutor) callMCP(ctx context.Context, body []byte, principal accessdomain.MachinePrincipal) (openplatformport.Response, error) {
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
	customerID, references, err := executor.mcpCustomerID(ctx, call.Params.Arguments)
	if err != nil {
		return openplatformport.Response{}, err
	}
	if err := executor.ensureCustomerScope(ctx, principal, customerID, references); err != nil {
		return openplatformport.Response{}, errOpenPlatformResourceOutOfScope
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
			contextValue, contextErr := executor.customerContext(ctx, customerID, limitArgument(call.Params.Arguments, "recent_message_limit", 20), limitArgument(call.Params.Arguments, "timeline_limit", 20))
			if contextErr != nil {
				return openplatformport.Response{}, contextErr
			}
			content["context"] = contextValue
		}
	case "get_customer_context":
		contextValue, contextErr := executor.customerContext(ctx, customerID, limitArgument(call.Params.Arguments, "recent_message_limit", 20), limitArgument(call.Params.Arguments, "timeline_limit", 20))
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

func (executor *openPlatformExecutor) customerContext(ctx context.Context, customerID customerdomain.CustomerID, messageLimit, timelineLimit int) (map[string]any, error) {
	if timelineLimit < 1 || timelineLimit > 100 {
		return nil, errors.New("invalid timeline limit")
	}
	timeline, timelineErr := executor.timeline.CustomerTimeline(ctx, customerID, customerport.PageQuery{Limit: timelineLimit, Watermark: time.Now().UTC()})
	if timelineErr != nil {
		return nil, timelineErr
	}
	messages, messageErr := executor.recentMessages(ctx, customerID, messageLimit)
	if errors.Is(messageErr, archiveport.ErrNotReady) {
		return map[string]any{"messages": []any{}, "archive_status": "not_ready", "timeline": timeline.Items, "timeline_status": timeline.Status.State}, nil
	}
	if messageErr != nil {
		return nil, messageErr
	}
	return map[string]any{"messages": messages, "timeline": timeline.Items, "timeline_status": timeline.Status.State}, nil
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

func (executor *openPlatformExecutor) mcpCustomerID(ctx context.Context, arguments map[string]any) (customerdomain.CustomerID, []identitydomain.Reference, error) {
	var directCustomer customerdomain.CustomerID
	references := make([]identitydomain.Reference, 0, 2)
	if raw, exists := arguments["customer_ref"]; exists {
		value, ok := raw.(string)
		if !ok || strings.TrimSpace(value) == "" {
			return 0, nil, errors.New("invalid customer_ref")
		}
		value = strings.TrimSpace(value)
		if strings.HasPrefix(value, "customer:") {
			id, err := strconv.ParseInt(strings.TrimPrefix(value, "customer:"), 10, 64)
			if err != nil || id < 1 {
				return 0, nil, errors.New("invalid customer_ref")
			}
			directCustomer = customerdomain.CustomerID(id)
		} else if isCN11(value) {
			references = append(references, identitydomain.Reference{Kind: identitydomain.KindPhone, Scope: "phone:cn11", Value: value, Assurance: identitydomain.AssuranceDeclared, Source: "open_platform.mcp"})
		} else {
			reference, referenceErr := executor.trustedReference(identitydomain.KindWeComExternalUserID, "", value, "open_platform.mcp")
			if referenceErr != nil {
				return 0, nil, referenceErr
			}
			references = append(references, reference)
		}
	}
	if raw, exists := arguments["external_userid"]; exists {
		value, ok := raw.(string)
		if !ok || strings.TrimSpace(value) == "" {
			return 0, nil, errors.New("invalid external_userid")
		}
		reference, referenceErr := executor.trustedReference(identitydomain.KindWeComExternalUserID, "", strings.TrimSpace(value), "open_platform.mcp")
		if referenceErr != nil {
			return 0, nil, referenceErr
		}
		references = append(references, reference)
	}
	if directCustomer == 0 && len(references) == 0 {
		return 0, nil, errors.New("customer_ref or external_userid is required")
	}
	if len(references) == 0 {
		return directCustomer, nil, nil
	}
	resolved, err := executor.resolveReferences(ctx, references)
	if err != nil {
		return 0, nil, err
	}
	if directCustomer != 0 && resolved.CustomerID != directCustomer {
		return 0, nil, errOpenPlatformIdentityConflict
	}
	return resolved.CustomerID, references, nil
}

func (executor *openPlatformExecutor) referencesFromValues(values url.Values) ([]identitydomain.Reference, error) {
	getOne := func(key string) (string, error) {
		items, found := values[key]
		if !found {
			return "", nil
		}
		if len(items) != 1 {
			return "", errors.New("duplicate identity parameter")
		}
		return strings.TrimSpace(items[0]), nil
	}
	kind, err := getOne("kind")
	if err != nil {
		return nil, err
	}
	if kind != "" {
		for _, key := range []string{"external_userid", "mobile", "unionid", "openid"} {
			value, valueErr := getOne(key)
			if valueErr != nil || value != "" {
				return nil, errors.New("generic identity cannot mix aliases")
			}
		}
		scope, scopeErr := getOne("scope")
		value, valueErr := getOne("value")
		if scopeErr != nil || valueErr != nil {
			return nil, errors.New("invalid generic identity")
		}
		reference, err := executor.trustedReference(identitydomain.Kind(kind), scope, value, "open_platform.api")
		if err != nil {
			return nil, err
		}
		return []identitydomain.Reference{reference}, nil
	}
	references := make([]identitydomain.Reference, 0, 4)
	if value, valueErr := getOne("external_userid"); valueErr != nil {
		return nil, valueErr
	} else if value != "" {
		scope, scopeErr := getOne("external_userid_scope")
		if scopeErr != nil {
			return nil, scopeErr
		}
		reference, err := executor.trustedReference(identitydomain.KindWeComExternalUserID, scope, value, "open_platform.api")
		if err != nil {
			return nil, err
		}
		references = append(references, reference)
	}
	if value, valueErr := getOne("mobile"); valueErr != nil {
		return nil, valueErr
	} else if value != "" {
		reference, err := executor.trustedReference(identitydomain.KindPhone, "", value, "open_platform.api")
		if err != nil {
			return nil, err
		}
		references = append(references, reference)
	}
	sharedScope, scopeErr := getOne("scope")
	if scopeErr != nil {
		return nil, scopeErr
	}
	if value, valueErr := getOne("unionid"); valueErr != nil {
		return nil, valueErr
	} else if value != "" {
		reference, err := executor.trustedReference(identitydomain.KindUnionID, sharedScope, value, "open_platform.api")
		if err != nil {
			return nil, err
		}
		references = append(references, reference)
	}
	if value, valueErr := getOne("openid"); valueErr != nil {
		return nil, valueErr
	} else if value != "" {
		reference, err := executor.trustedReference(identitydomain.KindOAOpenID, sharedScope, value, "open_platform.api")
		if err != nil {
			return nil, err
		}
		references = append(references, reference)
	}
	if len(references) == 0 {
		return nil, errors.New("identity query missing")
	}
	return references, nil
}

func (executor *openPlatformExecutor) trustedReference(kind identitydomain.Kind, requestedScope, value, source string) (identitydomain.Reference, error) {
	requestedScope = strings.TrimSpace(requestedScope)
	scope := requestedScope
	switch kind {
	case identitydomain.KindWeComExternalUserID:
		if executor.scopes.WeComScope == "" {
			return identitydomain.Reference{}, errOpenPlatformIdentityScopeDenied
		}
		if scope == "" {
			scope = executor.scopes.WeComScope
		}
		if scope != executor.scopes.WeComScope {
			return identitydomain.Reference{}, errOpenPlatformIdentityScopeDenied
		}
	case identitydomain.KindPhone:
		if scope != "" && scope != "phone:cn11" {
			return identitydomain.Reference{}, errOpenPlatformIdentityScopeDenied
		}
		scope = "phone:cn11"
	case identitydomain.KindUnionID:
		scope, value := selectTrustedScope(executor.scopes.UnionScopes, scope, value)
		if value == "" {
			return identitydomain.Reference{}, errOpenPlatformIdentityScopeDenied
		}
		return identitydomain.Reference{Kind: kind, Scope: scope, Value: value, Assurance: identitydomain.AssuranceDeclared, Source: source}, nil
	case identitydomain.KindOAOpenID:
		scope, value := selectTrustedScope(executor.scopes.OpenIDScopes, scope, value)
		if value == "" {
			return identitydomain.Reference{}, errOpenPlatformIdentityScopeDenied
		}
		return identitydomain.Reference{Kind: kind, Scope: scope, Value: value, Assurance: identitydomain.AssuranceDeclared, Source: source}, nil
	}
	reference := identitydomain.Reference{Kind: kind, Scope: scope, Value: value, Assurance: identitydomain.AssuranceDeclared, Source: source}
	if _, err := identitydomain.Normalize(reference); err != nil {
		return identitydomain.Reference{}, err
	}
	return reference, nil
}

// selectTrustedScope preserves old callers that omitted an internal scope only
// when Composition has one unambiguous configured candidate. A supplied scope
// still has to be one of those configured values.
func selectTrustedScope(available []string, requested, value string) (string, string) {
	if requested != "" {
		for _, candidate := range available {
			if candidate == requested {
				return candidate, value
			}
		}
		return "", ""
	}
	if len(available) == 1 {
		return available[0], value
	}
	return "", ""
}

func (executor *openPlatformExecutor) resolveReferences(ctx context.Context, references []identitydomain.Reference) (identityport.ResolveResult, error) {
	var found identityport.ResolveResult
	for index, reference := range references {
		result, err := executor.identity.Resolve(ctx, reference)
		if err != nil {
			return identityport.ResolveResult{}, err
		}
		switch result.Status {
		case identityport.ResolveFound:
			if result.CustomerID < 1 {
				return identityport.ResolveResult{}, errOpenPlatformIdentityPending
			}
		case identityport.ResolveNotFound:
			return identityport.ResolveResult{}, errOpenPlatformIdentityNotFound
		case identityport.ResolveConflict:
			return identityport.ResolveResult{}, errOpenPlatformIdentityConflict
		default:
			return identityport.ResolveResult{}, errOpenPlatformIdentityPending
		}
		if index == 0 {
			found = result
			continue
		}
		if result.CustomerID != found.CustomerID {
			return identityport.ResolveResult{}, errOpenPlatformIdentityConflict
		}
	}
	return found, nil
}

func (executor *openPlatformExecutor) orderQuery(ctx context.Context, values url.Values) (orderport.ListQuery, []identitydomain.Reference, error) {
	for key, value := range values {
		if len(value) != 1 {
			return orderport.ListQuery{}, nil, errors.New("duplicate query value")
		}
		switch key {
		case "provider", "limit", "cursor", "order_no", "transaction_id", "product_code", "payment_status", "created_from", "created_to", "external_userid", "external_userid_scope", "mobile", "unionid", "openid", "kind", "value", "scope":
		default:
			return orderport.ListQuery{}, nil, errors.New("unsupported query parameter")
		}
	}
	query := orderport.ListQuery{Cursor: values.Get("cursor"), Limit: 50, OrderRef: firstNonEmpty(values.Get("order_no"), values.Get("transaction_id")), Product: values.Get("product_code")}
	if value := values.Get("limit"); value != "" {
		limit, err := strconv.ParseInt(value, 10, 32)
		if err != nil || limit < 1 || limit > 100 {
			return query, nil, errors.New("invalid limit")
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
			return query, nil, errors.New("invalid provider")
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
		return query, nil, err
	}
	if query.CreatedTo, err = unixSeconds(values.Get("created_to")); err != nil {
		return query, nil, err
	}
	if anyIdentityValue(values) {
		references, referenceErr := executor.referencesFromValues(values)
		if referenceErr != nil {
			return query, nil, referenceErr
		}
		resolved, resolveErr := executor.resolveReferences(ctx, references)
		if resolveErr != nil {
			return query, nil, resolveErr
		}
		query.CustomerID = int64(resolved.CustomerID)
		return query, references, nil
	}
	return query, nil, nil
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
func responseForIdentityError(err error) openplatformport.Response {
	switch {
	case errors.Is(err, errOpenPlatformIdentityNotFound):
		return responseError(404, "not_found")
	case errors.Is(err, errOpenPlatformIdentityConflict):
		return responseError(409, "identity_conflict")
	case errors.Is(err, errOpenPlatformIdentityPending), errors.Is(err, errOpenPlatformIdentityScopeDenied):
		return responseError(409, "identity_pending")
	default:
		return responseError(400, "invalid_request")
	}
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
	return values.Get("kind") != "" || values.Get("external_userid") != "" || values.Get("mobile") != "" || values.Get("unionid") != "" || values.Get("openid") != ""
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

// openPlatformOwnerAdapter gives the read-only WeCom owner fact the same UoW
// boundary as every other composed customer projection.
type openPlatformOwnerAdapter struct {
	uow    platformport.UnitOfWork
	reader wecomport.AudiencePrimaryOwnerReader
}

func (adapter openPlatformOwnerAdapter) AudiencePrimaryOwners(ctx context.Context, customerIDs []customerdomain.CustomerID) ([]wecomport.AudiencePrimaryOwner, error) {
	var owners []wecomport.AudiencePrimaryOwner
	err := adapter.uow.Within(ctx, func(tx context.Context) error {
		var err error
		owners, err = adapter.reader.AudiencePrimaryOwners(tx, customerIDs)
		return err
	})
	return owners, err
}

var _ wecomport.AudiencePrimaryOwnerReader = openPlatformOwnerAdapter{}
