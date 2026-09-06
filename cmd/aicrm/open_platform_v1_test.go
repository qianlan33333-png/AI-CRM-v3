package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	archiveport "github.com/qianlan33333-png/AI-CRM-v3/internal/messagearchive/port"
	openplatformport "github.com/qianlan33333-png/AI-CRM-v3/internal/openplatform/port"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	radarport "github.com/qianlan33333-png/AI-CRM-v3/internal/radar/port"
	surveyport "github.com/qianlan33333-png/AI-CRM-v3/internal/survey/port"
)

func v1ExecutorForTest(t *testing.T, identity *openPlatformIdentityStub, profiles *openPlatformProfileStub) *openPlatformExecutor {
	t.Helper()
	executor, err := newOpenPlatformExecutor(identity, &openPlatformOrderStub{}, profiles, &openPlatformArchiveStub{}, &openPlatformTimelineStub{}, &openPlatformOwnerStub{}, configuredOpenPlatformScopes("corp-main", []string{"wechat-open-platform:shared"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	return executor
}

func TestV1CatalogOnlyPublishesComposedGrantedOperations(t *testing.T) {
	executor := v1ExecutorForTest(t, &openPlatformIdentityStub{}, &openPlatformProfileStub{})
	principal := accessdomain.MachinePrincipal{Scopes: []string{"read"}, Capabilities: []string{
		string(openplatformport.CapabilityPlatformCapabilitiesRead),
		string(openplatformport.CapabilityCustomerResolve),
		string(openplatformport.CapabilityCustomerRead),
		string(openplatformport.CapabilityCustomerActivityRead),
	}}
	items, err := executor.Available(context.Background(), principal)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]openplatformport.OperationID, 0, len(items))
	for _, item := range items {
		got = append(got, item.OperationID)
	}
	want := map[openplatformport.OperationID]bool{
		openplatformport.OperationCapabilitiesList: true,
		openplatformport.OperationCustomerResolve:  true,
		openplatformport.OperationCustomerContext:  true,
	}
	if len(got) != len(want) {
		t.Fatalf("available = %#v", got)
	}
	for _, operation := range got {
		if !want[operation] {
			t.Fatalf("unexpected operation %q", operation)
		}
	}
}

func TestV1ResolveUsesScopedOneIDAndNeverProvisions(t *testing.T) {
	identity := &openPlatformIdentityStub{result: identityport.ResolveResult{Status: identityport.ResolveFound, CustomerID: 42, IdentityID: 7}}
	executor := v1ExecutorForTest(t, identity, &openPlatformProfileStub{})
	input := json.RawMessage(`{"references":[{"kind":"unionid","scope":"wechat-open-platform:shared","value":"union-42"}]}`)
	result, err := executor.Invoke(context.Background(), openplatformport.Invocation{
		Operation: openplatformport.OperationCustomerResolve,
		Principal: accessdomain.MachinePrincipal{Scopes: []string{"read"}, Capabilities: []string{string(openplatformport.CapabilityCustomerResolve)}},
		Input:     input,
	})
	if err != nil {
		t.Fatal(err)
	}
	data := result.Data.(map[string]any)
	if customerID, ok := data["customer_id"].(customerdomain.CustomerID); !ok || customerID != 42 || data["identity_id"] != int64(7) || data["status"] != "found" {
		t.Fatalf("result = %#v", data)
	}
	if len(identity.calls) != 1 || identity.seen.Scope != "wechat-open-platform:shared" || identity.seen.Assurance != "declared" || identity.seen.Source != "open_platform.v1" {
		t.Fatalf("identity reference = %+v", identity.seen)
	}
}

func TestV1ResolveRejectsCallerAssuranceAndConflictingRoots(t *testing.T) {
	identity := &openPlatformIdentityStub{results: map[string]identityport.ResolveResult{
		"unionid|wechat-open-platform:shared|union-42": {Status: identityport.ResolveFound, CustomerID: 42},
		"unionid|wechat-open-platform:shared|union-43": {Status: identityport.ResolveFound, CustomerID: 43},
	}}
	executor := v1ExecutorForTest(t, identity, &openPlatformProfileStub{})
	principal := accessdomain.MachinePrincipal{Scopes: []string{"read"}, Capabilities: []string{string(openplatformport.CapabilityCustomerResolve)}}
	_, err := executor.Invoke(context.Background(), openplatformport.Invocation{Operation: openplatformport.OperationCustomerResolve, Principal: principal, Input: json.RawMessage(`{"references":[{"kind":"unionid","scope":"wechat-open-platform:shared","value":"union-42","assurance":"verified"}]}`)})
	if openplatformport.ErrorCodeOf(err) != openplatformport.ErrorValidation || len(identity.calls) != 0 {
		t.Fatalf("declared assurance err=%v calls=%d", err, len(identity.calls))
	}
	_, err = executor.Invoke(context.Background(), openplatformport.Invocation{Operation: openplatformport.OperationCustomerResolve, Principal: principal, Input: json.RawMessage(`{"references":[{"kind":"unionid","scope":"wechat-open-platform:shared","value":"union-42"},{"kind":"unionid","scope":"wechat-open-platform:shared","value":"union-43"}]}`)})
	if openplatformport.ErrorCodeOf(err) != openplatformport.ErrorIdentityConflict || len(identity.calls) != 2 {
		t.Fatalf("conflict err=%v calls=%d", err, len(identity.calls))
	}
}

func TestV1CustomerContextChecksOwnerScopeBeforeProfileRead(t *testing.T) {
	profiles := &openPlatformProfileStub{}
	executor := v1ExecutorForTest(t, &openPlatformIdentityStub{}, profiles)
	principal := accessdomain.MachinePrincipal{
		Scopes:       []string{"read"},
		Capabilities: []string{string(openplatformport.CapabilityCustomerRead)},
		OwnerScope:   accessdomain.OwnerScope{"customer_id": {"43"}},
	}
	_, err := executor.Invoke(context.Background(), openplatformport.Invocation{Operation: openplatformport.OperationCustomerContext, Principal: principal, Input: json.RawMessage(`{"customer_id":42}`)})
	if openplatformport.ErrorCodeOf(err) != openplatformport.ErrorNotFound || profiles.calls != 0 {
		t.Fatalf("scope err=%v profile_calls=%d", err, profiles.calls)
	}
}

func TestV1CustomerContextRequiresReadTokenAndCurrentGrant(t *testing.T) {
	executor := v1ExecutorForTest(t, &openPlatformIdentityStub{}, &openPlatformProfileStub{})
	_, err := executor.Invoke(context.Background(), openplatformport.Invocation{
		Operation: openplatformport.OperationCustomerContext,
		Principal: accessdomain.MachinePrincipal{Scopes: []string{"write"}, Capabilities: []string{string(openplatformport.CapabilityCustomerRead)}},
		Input:     json.RawMessage(`{"customer_id":42}`),
	})
	if openplatformport.ErrorCodeOf(err) != openplatformport.ErrorPermission {
		t.Fatalf("error = %v", err)
	}
}

func TestV1CustomerActivitiesMergeOwnerPagesAndBindCursor(t *testing.T) {
	at := func(hour int) time.Time { return time.Date(2026, 9, 6, hour, 0, 0, 0, time.UTC) }
	archive := &openPlatformArchiveStub{activityFn: func(query archiveport.CustomerQuery) (archiveport.CustomerPage, error) {
		all := []archiveport.MessageItem{{ID: 11, ChatType: "private", MessageType: "text", Direction: "customer_to_staff", RenderType: "supported", OccurredAt: at(12)}, {ID: 10, ChatType: "private", MessageType: "image", Direction: "staff_to_customer", RenderType: "unsupported", OccurredAt: at(10)}}
		return archiveport.CustomerPage{Items: afterMessagePosition(all, query.AfterAt, query.AfterID)}, nil
	}}
	orders := &openPlatformOrderStub{activityFn: func(query orderport.CustomerActivityQuery) (orderport.CustomerActivityPage, error) {
		all := []orderport.CustomerActivity{{OrderID: 21, Relationship: "payer", Provider: orderdomain.ProviderWeChatPay, Status: orderdomain.StatusPaid, Amount: orderdomain.Money{AmountMinor: 88, Currency: "CNY"}, OccurredAt: at(12)}, {OrderID: 20, Relationship: "beneficiary", Provider: orderdomain.ProviderWeChatPay, Status: orderdomain.StatusPaid, Amount: orderdomain.Money{AmountMinor: 99, Currency: "CNY"}, OccurredAt: at(9)}}
		return orderport.CustomerActivityPage{Items: afterOrderActivityPosition(all, query.AfterAt, query.AfterID)}, nil
	}}
	survey := &openPlatformSurveyStub{historyFn: func(query surveyport.CustomerHistoryQuery) (surveyport.CustomerHistoryWindow, error) {
		all := []surveyport.Submission{{ID: 41, QuestionnaireID: 7, QuestionnaireTitle: "Assessment", SubmittedAt: at(10)}}
		return surveyport.CustomerHistoryWindow{Items: afterSurveyPosition(all, query.AfterAt, query.AfterID)}, nil
	}}
	radar := &openPlatformRadarLinksStub{activityFn: func(query radarport.CustomerActivityQuery) (radarport.CustomerActivityPage, error) {
		all := []radarport.CustomerActivity{{EventID: 31, RadarID: 6, Stage: radarport.EventContentOpened, OccurredAt: at(11)}}
		return radarport.CustomerActivityPage{Items: afterRadarPosition(all, query.AfterAt, query.AfterID)}, nil
	}}
	identity := &openPlatformIdentityStub{}
	executor, err := newOpenPlatformExecutor(identity, orders, &openPlatformProfileStub{}, archive, &openPlatformTimelineStub{}, &openPlatformOwnerStub{}, configuredOpenPlatformScopes("corp-main", []string{"wechat-open-platform:shared"}, nil))
	if err != nil {
		t.Fatal(err)
	}
	executor.activityNow = func() time.Time { return at(13) }
	if err = executor.BindV1CustomerActivities(survey, radar, bytes.Repeat([]byte{7}, 32)); err != nil {
		t.Fatal(err)
	}
	principal := accessdomain.MachinePrincipal{ClientID: "reader-a", ClientRecord: 7, Audience: "external_integration", Scopes: []string{"read"}, Capabilities: []string{string(openplatformport.CapabilityCustomerActivityRead)}}
	first, err := executor.Invoke(context.Background(), openplatformport.Invocation{Operation: openplatformport.OperationCustomerActivities, Principal: principal, Input: json.RawMessage(`{"customer_id":42,"limit":2}`)})
	if err != nil {
		t.Fatal(err)
	}
	firstData := first.Data.(map[string]any)
	if got := v1ActivityIDs(t, firstData); !reflect.DeepEqual(got, []string{"message:11", "order:21"}) {
		t.Fatalf("first page = %v", got)
	}
	cursor, _ := firstData["next_cursor"].(string)
	if cursor == "" || archive.activityQuery.Limit != 3 || orders.activityQuery.Limit != 3 || radar.activityQuery.Limit != 3 || survey.historyQuery.Limit != 3 {
		t.Fatalf("lookahead/cursor archive=%+v order=%+v radar=%+v survey=%+v cursor=%q", archive.activityQuery, orders.activityQuery, radar.activityQuery, survey.historyQuery, cursor)
	}
	second, err := executor.Invoke(context.Background(), openplatformport.Invocation{Operation: openplatformport.OperationCustomerActivities, Principal: principal, Input: json.RawMessage(fmt.Sprintf(`{"customer_id":42,"limit":2,"cursor":%q}`, cursor))})
	if err != nil {
		t.Fatal(err)
	}
	secondData := second.Data.(map[string]any)
	if got := v1ActivityIDs(t, secondData); !reflect.DeepEqual(got, []string{"radar:31", "message:10"}) {
		t.Fatalf("second page = %v", got)
	}
	thirdCursor, _ := secondData["next_cursor"].(string)
	third, err := executor.Invoke(context.Background(), openplatformport.Invocation{Operation: openplatformport.OperationCustomerActivities, Principal: principal, Input: json.RawMessage(fmt.Sprintf(`{"customer_id":42,"limit":2,"cursor":%q}`, thirdCursor))})
	if err != nil {
		t.Fatal(err)
	}
	thirdData := third.Data.(map[string]any)
	if got := v1ActivityIDs(t, thirdData); !reflect.DeepEqual(got, []string{"survey:41", "order:20"}) || thirdData["next_cursor"] != nil {
		t.Fatalf("third page = %#v ids=%v", thirdData, got)
	}
	// The cursor contains the effective grant digest, so a newly narrowed or
	// expanded client grant cannot resume a former feed position.
	principal.Capabilities = append(principal.Capabilities, "other-capability")
	_, err = executor.Invoke(context.Background(), openplatformport.Invocation{Operation: openplatformport.OperationCustomerActivities, Principal: principal, Input: json.RawMessage(fmt.Sprintf(`{"customer_id":42,"cursor":%q}`, cursor))})
	if openplatformport.ErrorCodeOf(err) != openplatformport.ErrorValidation {
		t.Fatalf("grant-swapped cursor err=%v", err)
	}
}

func TestV1CustomerActivitiesFailClosedAndUseOneHundredItemLookahead(t *testing.T) {
	at := time.Date(2026, 9, 6, 13, 0, 0, 0, time.UTC)
	archive := &openPlatformArchiveStub{err: archiveport.ErrNotReady}
	orders := &openPlatformOrderStub{}
	survey := &openPlatformSurveyStub{}
	radar := &openPlatformRadarLinksStub{}
	executor, err := newOpenPlatformExecutor(&openPlatformIdentityStub{}, orders, &openPlatformProfileStub{}, archive, &openPlatformTimelineStub{}, &openPlatformOwnerStub{}, configuredOpenPlatformScopes("corp-main", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	executor.activityNow = func() time.Time { return at }
	if err = executor.BindV1CustomerActivities(survey, radar, bytes.Repeat([]byte{3}, 32)); err != nil {
		t.Fatal(err)
	}
	principal := accessdomain.MachinePrincipal{Scopes: []string{"read"}, Capabilities: []string{string(openplatformport.CapabilityCustomerActivityRead)}}
	_, err = executor.Invoke(context.Background(), openplatformport.Invocation{Operation: openplatformport.OperationCustomerActivities, Principal: principal, Input: json.RawMessage(`{"customer_id":42}`)})
	if openplatformport.ErrorCodeOf(err) != openplatformport.ErrorDependencyUnavailable || orders.activityCalls != 0 || radar.calls != 0 || survey.calls != 0 {
		t.Fatalf("owner failure err=%v subsequent calls order=%d radar=%d survey=%d", err, orders.activityCalls, radar.calls, survey.calls)
	}

	items := make([]orderport.CustomerActivity, 101)
	for index := range items {
		items[index] = orderport.CustomerActivity{OrderID: int64(101 - index), Relationship: "payer", Provider: orderdomain.ProviderWeChatPay, Status: orderdomain.StatusPaid, Amount: orderdomain.Money{AmountMinor: 1, Currency: "CNY"}, OccurredAt: at.Add(-time.Duration(index) * time.Minute)}
	}
	archive = &openPlatformArchiveStub{}
	orders = &openPlatformOrderStub{activityPage: orderport.CustomerActivityPage{Items: items}}
	executor, err = newOpenPlatformExecutor(&openPlatformIdentityStub{}, orders, &openPlatformProfileStub{}, archive, &openPlatformTimelineStub{}, &openPlatformOwnerStub{}, configuredOpenPlatformScopes("corp-main", nil, nil))
	if err != nil {
		t.Fatal(err)
	}
	executor.activityNow = func() time.Time { return at }
	if err = executor.BindV1CustomerActivities(&openPlatformSurveyStub{}, &openPlatformRadarLinksStub{}, bytes.Repeat([]byte{4}, 32)); err != nil {
		t.Fatal(err)
	}
	result, err := executor.Invoke(context.Background(), openplatformport.Invocation{Operation: openplatformport.OperationCustomerActivities, Principal: principal, Input: json.RawMessage(`{"customer_id":42,"types":["order"],"limit":100}`)})
	if err != nil {
		t.Fatal(err)
	}
	data := result.Data.(map[string]any)
	if len(v1ActivityIDs(t, data)) != 100 || data["next_cursor"] == nil || orders.activityQuery.Limit != 101 {
		t.Fatalf("100-item lookahead data=%#v order query=%+v", data, orders.activityQuery)
	}
}

func v1ActivityIDs(t *testing.T, data map[string]any) []string {
	t.Helper()
	items, ok := data["items"].([]map[string]any)
	if !ok {
		t.Fatalf("items type = %T", data["items"])
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		result = append(result, item["type"].(string)+":"+item["id"].(string))
	}
	return result
}

func afterMessagePosition(values []archiveport.MessageItem, at time.Time, id int64) []archiveport.MessageItem {
	result := []archiveport.MessageItem{}
	for _, value := range values {
		if at.IsZero() || value.OccurredAt.Before(at) || (value.OccurredAt.Equal(at) && value.ID < id) {
			result = append(result, value)
		}
	}
	return result
}
func afterOrderActivityPosition(values []orderport.CustomerActivity, at time.Time, id int64) []orderport.CustomerActivity {
	result := []orderport.CustomerActivity{}
	for _, value := range values {
		if at.IsZero() || value.OccurredAt.Before(at) || (value.OccurredAt.Equal(at) && value.OrderID < id) {
			result = append(result, value)
		}
	}
	return result
}
func afterSurveyPosition(values []surveyport.Submission, at time.Time, id surveyport.ID) []surveyport.Submission {
	result := []surveyport.Submission{}
	for _, value := range values {
		if at.IsZero() || value.SubmittedAt.Before(at) || (value.SubmittedAt.Equal(at) && value.ID < id) {
			result = append(result, value)
		}
	}
	return result
}
func afterRadarPosition(values []radarport.CustomerActivity, at time.Time, id int64) []radarport.CustomerActivity {
	result := []radarport.CustomerActivity{}
	for _, value := range values {
		if at.IsZero() || value.OccurredAt.Before(at) || (value.OccurredAt.Equal(at) && value.EventID < id) {
			result = append(result, value)
		}
	}
	return result
}
