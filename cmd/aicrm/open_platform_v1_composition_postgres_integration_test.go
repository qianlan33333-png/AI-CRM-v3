package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accessapp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/app"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/access/credential"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	accessstore "github.com/qianlan33333-png/AI-CRM-v3/internal/access/store"
	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identityapp "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/app"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identitystore "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/store"
	orderapp "github.com/qianlan33333-png/AI-CRM-v3/internal/order/app"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	orderstore "github.com/qianlan33333-png/AI-CRM-v3/internal/order/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// TestOpenPlatformV1CompositionPostgreSQLJourney applies every production
// migration, composes cmd/aicrm's real HTTP root, and invokes all six current
// V1 operations. The seed only creates trusted owner facts; the machine path
// itself reaches each owner through the actual Composition adapters and Ports.
func TestOpenPlatformV1CompositionPostgreSQLJourney(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()

	dataKey := make([]byte, 32)
	if _, err := rand.Read(dataKey); err != nil {
		t.Fatal(err)
	}
	const signingKey = "01234567890123456789012345678901"
	application, err := compose(ctx, platformconfig.Runtime{
		Role:         platformconfig.RoleAPI,
		DatabaseURL:  databaseURL,
		PublicOrigin: "https://crm.example.test",
		ReleaseSHA:   "open-platform-v1-composition",
		WorkerOwner:  "open-platform-v1-composition",
		WorkerLimit:  1,
		GroupOps:     platformconfig.GroupOps{WebhookSecret: "open-platform-v1-composition-webhook-secret"},
		WeCom:        platformconfig.WeCom{CorpID: "open-read"},
		OpenPlatform: platformconfig.OpenPlatform{JWTSigningKey: signingKey},
		Survey: platformconfig.Survey{
			DataKey:              base64.RawStdEncoding.EncodeToString(dataKey),
			IdentityPhoneDataKey: base64.RawStdEncoding.EncodeToString(dataKey),
			OAuthOpenPlatformID:  "open-read",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()

	adminUser, bootstrapped, err := application.management.Bootstrap(ctx, accessapp.BootstrapInput{Username: "open-v1-owner", Password: "open-v1-owner-password", DisplayName: "Open V1 Owner"})
	if err != nil || !bootstrapped {
		t.Fatalf("bootstrap user=%+v created=%t err=%v", adminUser, bootstrapped, err)
	}
	admin := accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: adminUser.ID, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}}
	uow, err := platformpostgres.NewUnitOfWork(application.pool)
	if err != nil {
		t.Fatal(err)
	}

	oneID := identityapp.OneIDService{Store: identitystore.NewPostgresStore()}
	fact, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindUnionID, Scope: "wechat-open-platform:open-read", Value: "union-composed-v1", Source: "open-platform-v1-composition-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	var provision struct {
		CustomerID customerdomain.CustomerID
		IdentityID int64
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		resolved, provisionErr := oneID.ProvisionCustomerFromVerifiedIdentity(tx, fact)
		if provisionErr != nil {
			return provisionErr
		}
		provision.CustomerID, provision.IdentityID = resolved.CustomerID, resolved.IdentityID
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = openPlatformV1ReadSeed(ctx, application.pool.Native(), provision.CustomerID, provision.IdentityID, adminUser.ID); err != nil {
		t.Fatal(err)
	}
	ordersRepository, err := orderstore.NewPostgreSQL(application.pool.Native(), uow)
	if err != nil {
		t.Fatal(err)
	}
	orders := orderapp.NewService(uow, ordersRepository)
	payer, beneficiary := int64(provision.CustomerID), int64(provision.CustomerID)
	if _, err = orders.Create(ctx, orderport.CreateCommand{Actor: adminUser.ID, IdempotencyKey: "open-v1-composition-order-0001", Input: orderdomain.NewOrderInput{
		Provider: orderdomain.ProviderWeChatPay, SourceSystem: "open-v1-composition", SourceKey: "open-v1-composition-order-0001", MerchantOrderNo: "OPEN-V1-COMPOSITION-ORDER-0001",
		PayerCustomerID: &payer, BeneficiaryCustomerID: &beneficiary, Amount: orderdomain.Money{AmountMinor: 8800, Currency: "CNY"},
		Items: []orderdomain.ItemSnapshot{{LineNo: 1, ProductCode: "open-v1-composition", ProductName: "Open V1 Composition", UnitAmountMinor: 8800, Quantity: 1, LineAmountMinor: 8800}}, RecordOrigin: orderdomain.RecordOriginNative,
	}}); err != nil {
		t.Fatal(err)
	}

	machine := mustOpenPlatformV1CompositionMachine(t, uow, signingKey, "open-read")
	client, err := machine.CreateV1(ctx, admin, accessapp.CreateMachineClientInput{
		ClientID: "v1.composition-machine", DisplayName: "V1 Composition Machine", Purpose: "external_agent",
		Audiences: []string{"external_integration"}, Scopes: []string{"read", "write"},
		Capabilities: []string{"platform.capabilities.read", "customer.resolve", "customer.read", "customer.activity.read", "ai.review_plan.create", "operation.read"},
		OwnerScope:   accessdomain.OwnerScope{"customer_id": {fmt.Sprint(provision.CustomerID)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = machine.Activate(ctx, admin, client.Client.ClientID, client.Secret, true); err != nil {
		t.Fatal(err)
	}
	readToken := openPlatformV1CompositionOAuthToken(t, application.handler, client.Client.ClientID, client.Secret, "read")
	writeToken := openPlatformV1CompositionOAuthToken(t, application.handler, client.Client.ClientID, client.Secret, "write")
	allToken := openPlatformV1CompositionOAuthToken(t, application.handler, client.Client.ClientID, client.Secret, "read write")

	capabilities := openPlatformV1CompositionRequest(http.MethodGet, "/open/v1/capabilities", "", allToken)
	capabilitiesResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(capabilitiesResponse, capabilities)
	if capabilitiesResponse.Code != http.StatusOK {
		t.Fatalf("capabilities status=%d body=%s", capabilitiesResponse.Code, capabilitiesResponse.Body.String())
	}
	for _, operation := range []string{"platform.capabilities.list", "customer.resolve", "customer.context.get", "customer.activities.list", "ai.review_plan.create", "operation.get"} {
		if !strings.Contains(capabilitiesResponse.Body.String(), `"operation_id":"`+operation+`"`) {
			t.Fatalf("catalog omitted %s: %s", operation, capabilitiesResponse.Body.String())
		}
	}

	tools := openPlatformV1CompositionRequest(http.MethodPost, "/mcp", `{"jsonrpc":"2.0","id":"catalog","method":"tools/list","params":{}}`, allToken)
	tools.Header.Set("Content-Type", "application/json")
	toolsResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(toolsResponse, tools)
	if toolsResponse.Code != http.StatusOK || !strings.Contains(toolsResponse.Body.String(), `"list_customer_activities"`) || !strings.Contains(toolsResponse.Body.String(), `"create_ai_review_plan"`) {
		t.Fatalf("MCP catalog status=%d body=%s", toolsResponse.Code, toolsResponse.Body.String())
	}

	resolve := openPlatformV1CompositionRequest(http.MethodPost, "/open/v1/customers:resolve", `{"references":[{"kind":"unionid","scope":"wechat-open-platform:open-read","value":"union-composed-v1"}]}`, readToken)
	resolve.Header.Set("Content-Type", "application/json")
	resolveResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(resolveResponse, resolve)
	if resolveResponse.Code != http.StatusOK || !strings.Contains(resolveResponse.Body.String(), `"customer_id":`+fmt.Sprint(provision.CustomerID)) || !strings.Contains(resolveResponse.Body.String(), `"status":"found"`) {
		t.Fatalf("resolve status=%d body=%s", resolveResponse.Code, resolveResponse.Body.String())
	}

	contextRequest := openPlatformV1CompositionRequest(http.MethodGet, "/open/v1/customers/"+fmt.Sprint(provision.CustomerID), "", readToken)
	contextResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(contextResponse, contextRequest)
	if contextResponse.Code != http.StatusOK || !strings.Contains(contextResponse.Body.String(), `"display_name":"V1 Read Customer"`) {
		t.Fatalf("customer context status=%d body=%s", contextResponse.Code, contextResponse.Body.String())
	}

	activities := openPlatformV1CompositionRequest(http.MethodGet, "/open/v1/customers/"+fmt.Sprint(provision.CustomerID)+"/activities?types=message&types=survey&types=radar&types=order&limit=1", "", readToken)
	activitiesResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(activitiesResponse, activities)
	var firstPage struct {
		Data struct {
			Items []struct {
				ActivityID string `json:"activity_id"`
				Type       string `json:"type"`
				Source     string `json:"source"`
			} `json:"items"`
			NextCursor string `json:"next_cursor"`
		} `json:"data"`
	}
	if err = json.Unmarshal(activitiesResponse.Body.Bytes(), &firstPage); err != nil || activitiesResponse.Code != http.StatusOK || len(firstPage.Data.Items) != 1 || firstPage.Data.Items[0].ActivityID == "" || firstPage.Data.Items[0].Source == "" || firstPage.Data.NextCursor == "" {
		t.Fatalf("activities status=%d err=%v body=%s", activitiesResponse.Code, err, activitiesResponse.Body.String())
	}
	mcpActivities := openPlatformV1CompositionRequest(http.MethodPost, "/mcp", `{"jsonrpc":"2.0","id":"activities","method":"tools/call","params":{"name":"list_customer_activities","arguments":{"customer_id":`+fmt.Sprint(provision.CustomerID)+`,"types":["message","survey","radar","order"],"limit":100,"cursor":`+mustJSON(t, firstPage.Data.NextCursor)+`}}}`, readToken)
	mcpActivities.Header.Set("Content-Type", "application/json")
	mcpActivitiesResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(mcpActivitiesResponse, mcpActivities)
	allActivities := activitiesResponse.Body.String() + mcpActivitiesResponse.Body.String()
	if mcpActivitiesResponse.Code != http.StatusOK {
		t.Fatalf("MCP activities status=%d body=%s", mcpActivitiesResponse.Code, mcpActivitiesResponse.Body.String())
	}
	for _, kind := range []string{"message:", "survey:", "radar:", "order:"} {
		if !strings.Contains(allActivities, kind) {
			t.Fatalf("activities missing %s REST=%s MCP=%s", kind, activitiesResponse.Body.String(), mcpActivitiesResponse.Body.String())
		}
	}

	planBody, err := json.Marshal(map[string]any{
		"name": "Composed V1 review plan", "source_kind": "open_platform", "source_digest": effectport.Hash("open-v1-composition-plan"),
		"recipients": []any{map[string]any{"customer_id": provision.CustomerID, "staff_id": adminUser.ID, "content": []any{map[string]any{"kind": "text", "text": "review composed plan"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	create := openPlatformV1CompositionRequest(http.MethodPost, "/open/v1/ai/review-plans", string(planBody), writeToken)
	create.Header.Set("Content-Type", "application/json")
	create.Header.Set("Idempotency-Key", "open-v1-composition-plan-0001")
	createResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(createResponse, create)
	var created struct {
		Data struct {
			OperationID string `json:"operation_id"`
			ReviewState string `json:"review_state"`
		} `json:"data"`
	}
	if err = json.Unmarshal(createResponse.Body.Bytes(), &created); err != nil || createResponse.Code != http.StatusCreated || created.Data.OperationID == "" || created.Data.ReviewState != "pending_review" {
		t.Fatalf("create review plan status=%d err=%v body=%s", createResponse.Code, err, createResponse.Body.String())
	}
	status := openPlatformV1CompositionRequest(http.MethodPost, "/mcp", `{"jsonrpc":"2.0","id":"operation","method":"tools/call","params":{"name":"get_operation_status","arguments":{"operation_id":`+mustJSON(t, created.Data.OperationID)+`}}}`, readToken)
	status.Header.Set("Content-Type", "application/json")
	statusResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(statusResponse, status)
	if statusResponse.Code != http.StatusOK || !strings.Contains(statusResponse.Body.String(), `"review_state":"pending_review"`) || !strings.Contains(statusResponse.Body.String(), `"operation_state":"pending_review"`) {
		t.Fatalf("operation status=%d body=%s", statusResponse.Code, statusResponse.Body.String())
	}
}

func mustOpenPlatformV1CompositionMachine(t *testing.T, uow *platformpostgres.UnitOfWork, signingKey, corpID string) *accessapp.MachineService {
	t.Helper()
	machine, err := accessapp.NewMachineService(accessstore.NewPostgreSQL(), uow, credential.PasswordHasher{}, accessapp.MachineConfig{SigningKey: []byte(signingKey), CorpID: corpID})
	if err != nil {
		t.Fatal(err)
	}
	return machine
}

func openPlatformV1CompositionOAuthToken(t *testing.T, handler http.Handler, clientID, secret, scope string) string {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "https://crm.example.test/oauth/token", strings.NewReader("grant_type=client_credentials&audience=external_integration&scope="+strings.ReplaceAll(scope, " ", "+")))
	request.TLS = &tlsState
	request.RemoteAddr = "203.0.113.101:443"
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.SetBasicAuth(clientID, secret)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var issued struct {
		AccessToken string `json:"access_token"`
		Scope       string `json:"scope"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &issued); err != nil || response.Code != http.StatusOK || issued.AccessToken == "" || issued.Scope != scope {
		t.Fatalf("oauth scope=%s status=%d err=%v body=%s", scope, response.Code, err, response.Body.String())
	}
	return issued.AccessToken
}

func openPlatformV1CompositionRequest(method, path, body, bearer string) *http.Request {
	request := httptest.NewRequest(method, "https://crm.example.test"+path, strings.NewReader(body))
	request.TLS = &tlsState
	request.RemoteAddr = "203.0.113.101:443"
	request.Header.Set("Authorization", "Bearer "+bearer)
	return request
}
