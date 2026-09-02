package wecom

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	customerdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/customer/domain"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/audit"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/webhook"
)

var fixedNow = time.Date(2026, 9, 2, 8, 0, 0, 0, time.UTC)

func TestCallbackCryptoVerificationAndFailures(t *testing.T) {
	crypto, err := NewCallbackCrypto("callback-token", "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG", "wx-corp")
	if err != nil {
		t.Fatal(err)
	}
	crypto.now = func() time.Time { return fixedNow }
	timestamp := "1788336000"
	encrypted := encryptForTest(t, crypto, []byte("<xml><ToUserName>wx-corp</ToUserName><Event>change_external_contact</Event><ChangeType>add_external_contact</ChangeType><ExternalUserID>x</ExternalUserID><UserID>u</UserID></xml>"))
	signature := callbackSignature("callback-token", timestamp, "nonce", encrypted)
	plain, err := crypto.VerifyAndDecrypt(signature, timestamp, "nonce", encrypted)
	if err != nil || !strings.Contains(string(plain), "add_external_contact") {
		t.Fatalf("decrypt err=%v plain=%q", err, plain)
	}
	if _, err = crypto.VerifyAndDecrypt("bad", timestamp, "nonce", encrypted); !errors.Is(err, ErrSignature) {
		t.Fatalf("signature error=%v", err)
	}
	if _, err = crypto.VerifyAndDecrypt(signature, "1788335600", "nonce", encrypted); !errors.Is(err, ErrCallbackExpired) {
		t.Fatalf("expiry error=%v", err)
	}
	futureTimestamp := "1788336061"
	if _, err = crypto.VerifyAndDecrypt(callbackSignature("callback-token", futureTimestamp, "nonce", encrypted), futureTimestamp, "nonce", encrypted); !errors.Is(err, ErrCallbackExpired) {
		t.Fatalf("future window error=%v", err)
	}
	wrongCorp := encryptRawForTest(t, crypto.key, []byte("<xml/>"), "other-corp")
	if _, err = crypto.VerifyAndDecrypt(callbackSignature("callback-token", timestamp, "nonce", wrongCorp), timestamp, "nonce", wrongCorp); !errors.Is(err, ErrCorpMismatch) {
		t.Fatalf("corp error=%v", err)
	}
}

func TestCallbackHandlerDurableBeforeACKAndRejectsBadInput(t *testing.T) {
	crypto, _ := NewCallbackCrypto("callback-token", "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG", "wx-corp")
	crypto.now = func() time.Time { return fixedNow }
	inboxStore := &memoryWebhookStore{}
	inbox, _ := webhook.NewService(inboxStore)
	handler := CallbackHandler{Enabled: true, Crypto: crypto, Inbox: inbox, UOW: directUOW{}}
	plain := []byte("<xml><ToUserName>wx-corp</ToUserName><Event>change_external_contact</Event><ChangeType>add_external_contact</ChangeType><ExternalUserID>x</ExternalUserID><UserID>u</UserID></xml>")
	encrypted := encryptForTest(t, crypto, plain)
	timestamp := "1788336000"
	query := "?msg_signature=" + callbackSignature("callback-token", timestamp, "n", encrypted) + "&timestamp=" + timestamp + "&nonce=n"
	req := httptest.NewRequest(http.MethodPost, "/wecom/external-contact/callback"+query, strings.NewReader("<xml><Encrypt><![CDATA["+encrypted+"]]></Encrypt></xml>"))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusOK || response.Body.String() != "success" || len(inboxStore.deliveries) != 1 {
		t.Fatalf("status=%d body=%q deliveries=%d", response.Code, response.Body.String(), len(inboxStore.deliveries))
	}
	bad := httptest.NewRequest(http.MethodPost, "/wecom/external-contact/callback"+query, strings.NewReader("<xml>"))
	badResponse := httptest.NewRecorder()
	handler.ServeHTTP(badResponse, bad)
	if badResponse.Code != http.StatusBadRequest {
		t.Fatalf("malformed status=%d", badResponse.Code)
	}
	oversize := httptest.NewRequest(http.MethodPost, "/wecom/external-contact/callback"+query, strings.NewReader(strings.Repeat("x", maxCallbackBody+1)))
	oversizeResponse := httptest.NewRecorder()
	handler.ServeHTTP(oversizeResponse, oversize)
	if oversizeResponse.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversize status=%d", oversizeResponse.Code)
	}
	disabledResponse := httptest.NewRecorder()
	CallbackHandler{}.ServeHTTP(disabledResponse, req)
	if disabledResponse.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled status=%d", disabledResponse.Code)
	}
}

func TestCallbackHandlerDeduplicatesEquivalentXMLAndRejectsToUserNameMismatch(t *testing.T) {
	crypto, _ := NewCallbackCrypto("callback-token", "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG", "wx-corp")
	crypto.now = func() time.Time { return fixedNow }
	store := &memoryWebhookStore{}
	inbox, _ := webhook.NewService(store)
	handler := CallbackHandler{Enabled: true, Crypto: crypto, Inbox: inbox, UOW: directUOW{}}
	first := []byte("<xml><ToUserName>wx-corp</ToUserName><Event>change_external_contact</Event><ChangeType>add_external_contact</ChangeType><ExternalUserID>external</ExternalUserID><UserID>employee</UserID></xml>")
	second := []byte("<xml><UserID>employee</UserID><ExternalUserID>external</ExternalUserID><ChangeType>add_external_contact</ChangeType><Event>change_external_contact</Event><ToUserName>wx-corp</ToUserName></xml>")
	for _, plain := range [][]byte{first, second} {
		encrypted := encryptForTest(t, crypto, plain)
		timestamp := "1788336000"
		query := "?msg_signature=" + callbackSignature("callback-token", timestamp, "n", encrypted) + "&timestamp=" + timestamp + "&nonce=n"
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/wecom/external-contact/callback"+query, strings.NewReader("<xml><Encrypt><![CDATA["+encrypted+"]]></Encrypt></xml>")))
		if response.Code != http.StatusOK {
			t.Fatalf("equivalent callback status=%d", response.Code)
		}
	}
	if len(store.deliveries) != 1 {
		t.Fatalf("expected one durable event, got %d", len(store.deliveries))
	}
	mismatch := []byte("<xml><ToUserName>another-corp</ToUserName><Event>change_external_contact</Event><ChangeType>add_external_contact</ChangeType><ExternalUserID>external</ExternalUserID><UserID>employee</UserID></xml>")
	encrypted := encryptForTest(t, crypto, mismatch)
	timestamp := "1788336000"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/wecom/external-contact/callback?msg_signature="+callbackSignature("callback-token", timestamp, "n", encrypted)+"&timestamp="+timestamp+"&nonce=n", strings.NewReader("<xml><Encrypt><![CDATA["+encrypted+"]]></Encrypt></xml>")))
	if response.Code != http.StatusForbidden {
		t.Fatalf("corp mismatch status=%d", response.Code)
	}
}

func TestProcessorProcessesDuplicateDeliveryOnce(t *testing.T) {
	store := &memoryWebhookStore{}
	inbox, _ := webhook.NewService(store)
	payload := json.RawMessage(`{"event":"change_external_contact","change_type":"add_external_contact","external_userid":"external-1","userid":"employee-1"}`)
	key, _ := idempotency.Parse("wecom:external-contact:duplicate-0001")
	if _, err := inbox.Ingest(context.Background(), webhook.Ingest{Provider: "wecom.external_contact", IdempotencyKey: key, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	if replay, err := inbox.Ingest(context.Background(), webhook.Ingest{Provider: "wecom.external_contact", IdempotencyKey: key, Payload: payload}); err != nil || !replay.Replay {
		t.Fatalf("duplicate ingest replay=%v err=%v", replay.Replay, err)
	}
	otherKey, _ := idempotency.Parse("other-provider:delivery:000001")
	if _, err := inbox.Ingest(context.Background(), webhook.Ingest{Provider: "other.provider", IdempotencyKey: otherKey, Payload: payload}); err != nil {
		t.Fatal(err)
	}
	identity := &fakeIdentity{}
	relationships := &memoryRelationships{}
	auditStore := &memoryAuditStore{}
	auditService, _ := audit.NewService(auditStore)
	processor := InboxProcessor{Enabled: true, CorpID: "wx-corp", Inbox: inbox, UOW: directUOW{}, Identity: identity, Relationships: relationships, Audit: auditService}
	if count, err := processor.ProcessOnce(context.Background(), "oneshot", 10); err != nil || count != 1 {
		t.Fatalf("first count=%d err=%v", count, err)
	}
	if count, err := processor.ProcessOnce(context.Background(), "oneshot", 10); err != nil || count != 0 {
		t.Fatalf("replay count=%d err=%v", count, err)
	}
	if identity.calls != 1 || !relationships.active[relationshipKey("wx-corp", "employee-1", 42)] {
		t.Fatalf("identity calls=%d active=%v", identity.calls, relationships.active)
	}
}

func TestProcessorDoesNotProvisionFromDeleteEvent(t *testing.T) {
	store := &memoryWebhookStore{}
	inbox, _ := webhook.NewService(store)
	payload := json.RawMessage(`{"event":"change_external_contact","change_type":"del_follow_user","external_userid":"external-1","userid":"employee-1"}`)
	key, _ := idempotency.Parse("wecom:external-contact:delete-000001")
	_, _ = inbox.Ingest(context.Background(), webhook.Ingest{Provider: "wecom.external_contact", IdempotencyKey: key, Payload: payload})
	identity := &fakeIdentity{find: false}
	auditService, _ := audit.NewService(&memoryAuditStore{})
	processor := InboxProcessor{Enabled: true, CorpID: "wx-corp", Inbox: inbox, UOW: directUOW{}, Identity: identity, Relationships: &memoryRelationships{}, Audit: auditService}
	if count, err := processor.ProcessOnce(context.Background(), "oneshot", 1); err != nil || count != 1 || identity.calls != 0 {
		t.Fatalf("count=%d calls=%d err=%v", count, identity.calls, err)
	}
}

func TestOAuthStateSingleUseExpiryAndOpenRedirect(t *testing.T) {
	states := &memoryOAuthStates{states: map[[32]byte]storedOAuthState{}}
	client := &fakeOAuthClient{}
	service := OAuthService{Enabled: true, CorpID: "wx-corp", StateStore: states, UOW: directUOW{}, Client: client, AllowedPaths: map[string]struct{}{"/sidebar": {}}, Now: func() time.Time { return fixedNow }, Random: func(value []byte) error {
		for i := range value {
			value[i] = byte(i + 1)
		}
		return nil
	}}
	if _, err := service.Start(context.Background(), OAuthSidebar, "https://evil.example"); !errors.Is(err, ErrOpenRedirect) {
		t.Fatalf("redirect error=%v", err)
	}
	start, err := service.Start(context.Background(), OAuthSidebar, "/sidebar")
	if err != nil {
		t.Fatal(err)
	}
	_, state, err := service.ConsumeAndExchange(context.Background(), OAuthSidebar, start.State, "code-not-logged")
	if err != nil || state.Redirect != "/sidebar" {
		t.Fatalf("consume err=%v state=%+v", err, state)
	}
	if _, _, err = service.ConsumeAndExchange(context.Background(), OAuthSidebar, start.State, "code"); !errors.Is(err, ErrInvalidOAuth) {
		t.Fatalf("replay err=%v", err)
	}
	states.states[oauthDigest("expired")] = storedOAuthState{state: OAuthState{Purpose: OAuthSidebar, Redirect: "/sidebar", ExpiresAt: fixedNow.Add(-time.Second)}, nonce: oauthDigest("nonce")}
	if _, _, err = service.ConsumeAndExchange(context.Background(), OAuthSidebar, "expired.nonce", "code"); !errors.Is(err, ErrInvalidOAuth) {
		t.Fatalf("expired err=%v", err)
	}
	start, err = service.Start(context.Background(), OAuthSidebar, "/sidebar")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = service.ConsumeAndExchange(context.Background(), OAuthSidebar, start.State+"x", "code"); !errors.Is(err, ErrInvalidOAuth) {
		t.Fatalf("nonce tamper err=%v", err)
	}
}

func TestContextTokenTamperingExpiryAndRelationshipTermination(t *testing.T) {
	relationships := &memoryRelationships{active: map[string]bool{relationshipKey("wx-corp", "employee", 42): true}}
	service := ContextTokenService{CorpID: "wx-corp", SigningKey: bytes32(), Relationships: relationships, UOW: directUOW{}, Now: func() time.Time { return fixedNow }, TTL: time.Minute}
	token, err := service.Issue(context.Background(), SidebarPrincipal{CorpID: "wx-corp", EmployeeID: "employee"}, 42)
	if err != nil {
		t.Fatal(err)
	}
	if _, customer, err := service.Verify(context.Background(), token); err != nil || customer != 42 {
		t.Fatalf("verify customer=%d err=%v", customer, err)
	}
	if _, _, err := service.Verify(context.Background(), token+"x"); !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("tamper err=%v", err)
	}
	relationships.active[relationshipKey("wx-corp", "employee", 42)] = false
	if _, _, err := service.Verify(context.Background(), token); !errors.Is(err, ErrRelationship) {
		t.Fatalf("terminated err=%v", err)
	}
	expired := ContextTokenService{CorpID: "wx-corp", SigningKey: bytes32(), Relationships: &memoryRelationships{active: map[string]bool{relationshipKey("wx-corp", "employee", 42): true}}, UOW: directUOW{}, Now: func() time.Time { return fixedNow }, TTL: time.Nanosecond}
	expiredToken, _ := expired.Issue(context.Background(), SidebarPrincipal{CorpID: "wx-corp", EmployeeID: "employee"}, 42)
	expired.Now = func() time.Time { return fixedNow.Add(time.Second) }
	if _, _, err := expired.Verify(context.Background(), expiredToken); !errors.Is(err, ErrInvalidContext) {
		t.Fatalf("expired token err=%v", err)
	}
}

func TestSidebarHTTPContractsAndDisabledProvider(t *testing.T) {
	relationships := &memoryRelationships{active: map[string]bool{relationshipKey("wx-corp", "employee", 42): true}}
	contextTokens := ContextTokenService{CorpID: "wx-corp", SigningKey: bytes32(), Relationships: relationships, UOW: directUOW{}, Now: func() time.Time { return fixedNow }}
	oauthStates := &memoryOAuthStates{states: map[[32]byte]storedOAuthState{}}
	signer := &fakeJSSDKSigner{}
	options := HTTPHandlerOptions{
		OAuth:             OAuthService{Enabled: true, CorpID: "wx-corp", StateStore: oauthStates, UOW: directUOW{}, Client: fakeOAuthClient{}},
		ContextTokens:     contextTokens,
		JSSDKSigner:       signer,
		JSSDKOrigin:       "https://crm.example",
		PrincipalResolver: fakePrincipal{},
		CustomerViewer:    fakeCustomerViewer{},
		ExistingIdentity:  fakeExistingIdentity{},
		SessionIssuer:     fakeSessionIssuer{},
		CookieSecure:      true,
	}
	handler, err := NewHTTPHandler(options)
	if err != nil {
		t.Fatal(err)
	}
	jssdk := httptest.NewRecorder()
	handler.ServeHTTP(jssdk, httptest.NewRequest(http.MethodGet, "/api/sidebar/jssdk-config?url=https%3A%2F%2Fcrm.example%2Fsidebar%3Fx%3D1", nil))
	if jssdk.Code != http.StatusOK || !strings.Contains(jssdk.Body.String(), "signature") {
		t.Fatalf("jssdk status=%d body=%s", jssdk.Code, jssdk.Body.String())
	}
	if signer.calls != 1 {
		t.Fatalf("signer calls=%d", signer.calls)
	}
	wrongOrigin := httptest.NewRecorder()
	handler.ServeHTTP(wrongOrigin, httptest.NewRequest(http.MethodGet, "/api/sidebar/jssdk-config?url=https%3A%2F%2Fevil.example%2Fsidebar", nil))
	if wrongOrigin.Code != http.StatusBadRequest {
		t.Fatalf("wrong origin status=%d", wrongOrigin.Code)
	}
	unauthenticated := options
	unauthenticated.PrincipalResolver = failingPrincipal{}
	unauthenticatedResponse := httptest.NewRecorder()
	handleJSSDK(unauthenticatedResponse, httptest.NewRequest(http.MethodGet, "/api/sidebar/jssdk-config?url=https%3A%2F%2Fcrm.example%2Fsidebar", nil), unauthenticated)
	if unauthenticatedResponse.Code != http.StatusUnauthorized || signer.calls != 1 {
		t.Fatalf("unauthenticated status=%d signer=%d", unauthenticatedResponse.Code, signer.calls)
	}
	wrongCorp := options
	wrongCorp.PrincipalResolver = wrongCorpPrincipal{}
	wrongCorpResponse := httptest.NewRecorder()
	handleJSSDK(wrongCorpResponse, httptest.NewRequest(http.MethodGet, "/api/sidebar/jssdk-config?url=https%3A%2F%2Fcrm.example%2Fsidebar", nil), wrongCorp)
	if wrongCorpResponse.Code != http.StatusUnauthorized || signer.calls != 1 {
		t.Fatalf("wrong corp status=%d signer=%d", wrongCorpResponse.Code, signer.calls)
	}
	issue := httptest.NewRecorder()
	handler.ServeHTTP(issue, httptest.NewRequest(http.MethodPost, "/api/sidebar/context-token", strings.NewReader(`{"external_userid":"external-42"}`)))
	if issue.Code != http.StatusOK {
		t.Fatalf("issue status=%d body=%s", issue.Code, issue.Body.String())
	}
	var issued map[string]string
	if err := json.Unmarshal(issue.Body.Bytes(), &issued); err != nil {
		t.Fatal(err)
	}
	workbenchRequest := httptest.NewRequest(http.MethodGet, "/api/sidebar/v2/workbench", nil)
	workbenchRequest.Header.Set("Authorization", "Bearer "+issued["context_token"])
	workbench := httptest.NewRecorder()
	handler.ServeHTTP(workbench, workbenchRequest)
	if workbench.Code != http.StatusOK || !strings.Contains(workbench.Body.String(), `"customer_id":42`) {
		t.Fatalf("workbench status=%d body=%s", workbench.Code, workbench.Body.String())
	}
	disabled := httptest.NewRecorder()
	disabledHandler, err := NewHTTPHandler(HTTPHandlerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	disabledHandler.ServeHTTP(disabled, httptest.NewRequest(http.MethodGet, "/api/sidebar/jssdk-config", nil))
	if disabled.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled status=%d", disabled.Code)
	}
	unknown := httptest.NewRecorder()
	handler.ServeHTTP(unknown, httptest.NewRequest(http.MethodPost, "/api/sidebar/context-token", strings.NewReader(`{"external_userid":"unknown"}`)))
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("unknown identity status=%d", unknown.Code)
	}
	trailing := httptest.NewRecorder()
	handler.ServeHTTP(trailing, httptest.NewRequest(http.MethodPost, "/api/sidebar/context-token", strings.NewReader(`{"external_userid":"external-42"} {}`)))
	if trailing.Code != http.StatusBadRequest {
		t.Fatalf("trailing body status=%d", trailing.Code)
	}
}

func TestOAuthCallbackWritesSecureCookies(t *testing.T) {
	states := &memoryOAuthStates{states: map[[32]byte]storedOAuthState{}}
	service := OAuthService{Enabled: true, CorpID: "wx-corp", StateStore: states, UOW: directUOW{}, Client: fakeOAuthClient{}, AllowedPaths: map[string]struct{}{"/admin": {}}, Now: func() time.Time { return fixedNow }, Random: func(value []byte) error {
		for i := range value {
			value[i] = byte(i + 1)
		}
		return nil
	}}
	start, err := service.Start(context.Background(), OAuthAdmin, "/admin")
	if err != nil {
		t.Fatal(err)
	}
	relationships := &memoryRelationships{active: map[string]bool{relationshipKey("wx-corp", "employee", 42): true}}
	handler, err := NewHTTPHandler(HTTPHandlerOptions{OAuth: service, ContextTokens: ContextTokenService{CorpID: "wx-corp", SigningKey: bytes32(), Relationships: relationships, UOW: directUOW{}}, JSSDKSigner: &fakeJSSDKSigner{}, JSSDKOrigin: "https://crm.example", PrincipalResolver: fakePrincipal{}, CustomerViewer: fakeCustomerViewer{}, ExistingIdentity: fakeExistingIdentity{}, SessionIssuer: fakeSessionIssuer{}, CookieSecure: true})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/auth/wecom/callback?state="+start.State+"&code=provider-code", nil))
	if response.Code != http.StatusFound || strings.Contains(response.Body.String(), "session-token") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 2 || cookies[0].Name != "aicrm_admin_session" || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode || cookies[0].Path != "/" {
		t.Fatalf("bad session cookie: %+v", cookies)
	}
	if cookies[1].Name != "aicrm_admin_csrf" || !cookies[1].Secure || cookies[1].HttpOnly {
		t.Fatalf("bad csrf cookie: %+v", cookies[1])
	}
}

func encryptForTest(t *testing.T, crypto *CallbackCrypto, message []byte) string {
	t.Helper()
	return encryptRawForTest(t, crypto.key, message, crypto.corpID)
}

func encryptRawForTest(t *testing.T, key, message []byte, corpID string) string {
	t.Helper()
	plain := make([]byte, 20+len(message)+len(corpID))
	for i := 0; i < 16; i++ {
		plain[i] = byte(i + 1)
	}
	binary.BigEndian.PutUint32(plain[16:20], uint32(len(message)))
	copy(plain[20:], message)
	copy(plain[20+len(message):], corpID)
	padding := 32 - len(plain)%32
	plain = append(plain, bytesRepeat(byte(padding), padding)...)
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext := make([]byte, len(plain))
	cipher.NewCBCEncrypter(block, key[:aes.BlockSize]).CryptBlocks(ciphertext, plain)
	return base64.StdEncoding.EncodeToString(ciphertext)
}

func bytesRepeat(value byte, count int) []byte {
	result := make([]byte, count)
	for i := range result {
		result[i] = value
	}
	return result
}
func bytes32() []byte { return bytesRepeat(7, 32) }

type directUOW struct{}

func (directUOW) Within(ctx context.Context, callback func(context.Context) error) error {
	return callback(ctx)
}

type memoryWebhookStore struct{ deliveries []webhook.Delivery }

func (store *memoryWebhookStore) PutIfAbsent(_ context.Context, delivery webhook.Delivery) (webhook.Delivery, bool, error) {
	for _, existing := range store.deliveries {
		if existing.Provider == delivery.Provider && existing.IdempotencyKey == delivery.IdempotencyKey {
			return existing, false, nil
		}
	}
	delivery.ID = int64(len(store.deliveries) + 1)
	store.deliveries = append(store.deliveries, delivery)
	return delivery, true, nil
}
func (store *memoryWebhookStore) Claim(_ context.Context, claim webhook.Claim) ([]webhook.Delivery, error) {
	var claimed []webhook.Delivery
	for i := range store.deliveries {
		if store.deliveries[i].Provider == claim.Provider && (store.deliveries[i].Status == webhook.StatusReceived || store.deliveries[i].Status == webhook.StatusRetryable) {
			store.deliveries[i].Status = webhook.StatusProcessing
			store.deliveries[i].AttemptCount++
			claimed = append(claimed, store.deliveries[i])
		}
	}
	return claimed, nil
}
func (store *memoryWebhookStore) Complete(_ context.Context, completion webhook.Completion) (webhook.Delivery, error) {
	for i := range store.deliveries {
		if store.deliveries[i].ID == completion.ID {
			if store.deliveries[i].AttemptCount != completion.ExpectedAttempt {
				return webhook.Delivery{}, webhook.ErrConcurrentUpdate
			}
			store.deliveries[i].Status = completion.Status
			return store.deliveries[i], nil
		}
	}
	return webhook.Delivery{}, errors.New("missing delivery")
}

type fakeIdentity struct {
	calls int
	find  bool
}

func (fake *fakeIdentity) ProvisionVerifiedWeComIdentity(_ context.Context, fact identitydomain.VerifiedFact) (customerdomain.CustomerID, error) {
	if !fact.Valid() {
		return 0, errors.New("invalid fact")
	}
	fake.calls++
	return 42, nil
}
func (fake *fakeIdentity) FindVerifiedWeComIdentity(_ context.Context, fact identitydomain.VerifiedFact) (customerdomain.CustomerID, bool, error) {
	return 42, fake.find && fact.Valid(), nil
}

type memoryRelationships struct{ active map[string]bool }

func (store *memoryRelationships) Upsert(_ context.Context, relationship FollowRelationship) error {
	if store.active == nil {
		store.active = map[string]bool{}
	}
	store.active[relationshipKey(relationship.CorpID, relationship.EmployeeID, relationship.CustomerID)] = relationship.Active
	return nil
}
func (store *memoryRelationships) IsActive(_ context.Context, corp, employee string, customer customerdomain.CustomerID) (bool, error) {
	return store.active[relationshipKey(corp, employee, customer)], nil
}
func relationshipKey(corp, employee string, customer customerdomain.CustomerID) string {
	return corp + ":" + employee + ":" + string(rune(customer))
}

type memoryAuditStore struct{ events int }

func (store *memoryAuditStore) Append(_ context.Context, event audit.Event) (audit.Event, error) {
	store.events++
	return event, nil
}

type storedOAuthState struct {
	state OAuthState
	nonce [32]byte
}
type memoryOAuthStates struct{ states map[[32]byte]storedOAuthState }

func (store *memoryOAuthStates) Create(_ context.Context, state OAuthState, digest, nonce [32]byte) error {
	store.states[digest] = storedOAuthState{state: state, nonce: nonce}
	return nil
}
func (store *memoryOAuthStates) Consume(_ context.Context, purpose OAuthPurpose, stateDigest, nonce [32]byte, now time.Time) (OAuthState, error) {
	stored, found := store.states[stateDigest]
	if !found || stored.nonce != nonce || stored.state.Purpose != purpose || stored.state.ExpiresAt.Before(now) {
		return OAuthState{}, ErrInvalidOAuth
	}
	delete(store.states, stateDigest)
	return stored.state, nil
}

type fakeOAuthClient struct{}

func (fakeOAuthClient) AuthorizationURL(_ context.Context, _ OAuthPurpose, state, _ string) (string, error) {
	return "https://wecom.example/auth?state=" + state, nil
}
func (fakeOAuthClient) ExchangeCode(_ context.Context, _ OAuthPurpose, _ string) (OAuthIdentity, error) {
	return OAuthIdentity{CorpID: "wx-corp", EmployeeID: "employee"}, nil
}

type fakeJSSDKSigner struct{ calls int }

func (signer *fakeJSSDKSigner) ConfigForURL(_ context.Context, value string) (map[string]string, error) {
	signer.calls++
	return map[string]string{"url": value, "signature": "signed"}, nil
}

type fakePrincipal struct{}

func (fakePrincipal) SidebarPrincipal(context.Context) (SidebarPrincipal, error) {
	return SidebarPrincipal{CorpID: "wx-corp", EmployeeID: "employee"}, nil
}

type failingPrincipal struct{}

func (failingPrincipal) SidebarPrincipal(context.Context) (SidebarPrincipal, error) {
	return SidebarPrincipal{}, errors.New("no sidebar session")
}

type wrongCorpPrincipal struct{}

func (wrongCorpPrincipal) SidebarPrincipal(context.Context) (SidebarPrincipal, error) {
	return SidebarPrincipal{CorpID: "wrong-corp", EmployeeID: "employee"}, nil
}

type fakeCustomerViewer struct{}

func (fakeCustomerViewer) SidebarCustomer(_ context.Context, customerID customerdomain.CustomerID) (SidebarCustomerView, error) {
	return SidebarCustomerView{CustomerID: customerID, Status: "active"}, nil
}

type fakeExistingIdentity struct{}

func (fakeExistingIdentity) ResolveExistingWeComIdentity(_ context.Context, _, externalUserID string) (customerdomain.CustomerID, bool, error) {
	return 42, externalUserID == "external-42", nil
}

type fakeSessionIssuer struct{}

func (fakeSessionIssuer) IssueWeComSession(_ context.Context, _ OAuthPurpose, _ OAuthIdentity) (BrowserCredentials, error) {
	return BrowserCredentials{SessionToken: "session-token", CSRFToken: "csrf-token", ExpiresAt: time.Now().UTC().Add(time.Hour)}, nil
}

var _ = sha256.Size
