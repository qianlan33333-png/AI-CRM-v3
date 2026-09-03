package wecom

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/webhook"
)

func assertEncryptedSuccessReply(t *testing.T, crypto *CallbackCrypto, body []byte) {
	t.Helper()
	var reply struct {
		Encrypt   string `xml:"Encrypt"`
		Signature string `xml:"MsgSignature"`
		Timestamp string `xml:"TimeStamp"`
		Nonce     string `xml:"Nonce"`
	}
	if err := xml.Unmarshal(body, &reply); err != nil {
		t.Fatalf("decode encrypted success reply: %v body=%q", err, body)
	}
	if reply.Encrypt == "" || reply.Signature == "" || reply.Timestamp == "" || reply.Nonce == "" {
		t.Fatalf("incomplete encrypted success reply: %+v", reply)
	}
	plain, err := crypto.VerifyAndDecrypt(reply.Signature, reply.Timestamp, reply.Nonce, reply.Encrypt)
	if err != nil || string(plain) != "success" {
		t.Fatalf("encrypted success reply plain=%q err=%v", plain, err)
	}
}

func TestCallbackEventExtractsTypedDigestOnlyFields(t *testing.T) {
	plain := []byte(`<xml><ToUserName>wx-corp</ToUserName><FromUserName>system</FromUserName><CreateTime>1788336000</CreateTime><MsgType>event</MsgType><Event>change_external_contact</Event><ChangeType>add_external_contact</ChangeType><ExternalUserID>external-1</ExternalUserID><UserID>employee-1</UserID><State><![CDATA[state-secret]]></State><MsgId>message-1</MsgId><WelcomeCode><![CDATA[welcome-secret]]></WelcomeCode><Source><![CDATA[source-secret]]></Source><FailReason><![CDATA[reason-secret]]></FailReason></xml>`)
	if _, err := parseCallbackEvent(plain, "wx-corp"); !errors.Is(err, ErrStateDigestUnavailable) {
		t.Fatalf("raw-state parser without digester err=%v", err)
	}
	digester, err := NewHMACStateDigester(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		t.Fatal(err)
	}
	event, err := parseCallbackEventWithStateDigester(plain, digester, "wx-corp")
	if err != nil {
		t.Fatal(err)
	}
	if event.CorpID != "wx-corp" || event.CreateTime != 1788336000 || event.MsgID != "message-1" || !event.MsgIDPresent || event.ExternalUserID != "external-1" || event.UserID != "employee-1" || !event.IsEntrant() {
		t.Fatalf("typed event = %+v", event)
	}
	stateDigest, err := digester.DigestState("wx-corp", "state-secret")
	if err != nil {
		t.Fatal(err)
	}
	if !event.StatePresent || event.StateDigest != formatStateDigest(stateDigest) || event.StateDigest == callbackValueDigest("state-secret") || !event.WelcomeCodePresent || event.WelcomeCodeDigest != callbackValueDigest("welcome-secret") || !event.SourcePresent || event.SourceDigest != callbackValueDigest("source-secret") || !event.FailReasonPresent || event.FailReasonDigest != callbackValueDigest("reason-secret") {
		t.Fatalf("digest fields = %+v", event)
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"state-secret", "welcome-secret", "source-secret", "reason-secret"} {
		if bytes.Contains(encoded, []byte(secret)) {
			t.Fatalf("typed callback JSON leaked %q: %s", secret, encoded)
		}
	}
}

func TestCallbackEventAcceptsLifecycleAndFutureChangeTypesWithoutRawSecrets(t *testing.T) {
	for _, changeType := range []string{"edit_external_contact", "del_follow_user", "del_external_contact", "future_change"} {
		t.Run(changeType, func(t *testing.T) {
			plain := []byte(`<xml><ToUserName>wx-corp</ToUserName><CreateTime>1788336000</CreateTime><MsgType>event</MsgType><Event>change_external_contact</Event><ChangeType>` + changeType + `</ChangeType><ExternalUserID>external-1</ExternalUserID><UserID>employee-1</UserID><Source>source-secret</Source><FailReason>reason-secret</FailReason></xml>`)
			event, err := parseCallbackEvent(plain, "wx-corp")
			if err != nil {
				t.Fatal(err)
			}
			if event.supported() != (changeType == "edit_external_contact" || changeType == "del_follow_user" || changeType == "del_external_contact") || event.IsEntrant() || event.UserID != "employee-1" {
				t.Fatalf("lifecycle event = %+v", event)
			}
			if event.SourceDigest != callbackValueDigest("source-secret") || event.FailReasonDigest != callbackValueDigest("reason-secret") {
				t.Fatalf("lifecycle digests = %+v", event)
			}
		})
	}
}

func TestCallbackEventRejectsSupportedLifecycleChangeWithoutEmployee(t *testing.T) {
	for _, changeType := range []string{
		ChangeAddExternalContact, ChangeAddHalfExternalContact, ChangeEditExternalContact,
		ChangeDelFollowUser, ChangeDelExternalContact,
	} {
		t.Run(changeType, func(t *testing.T) {
			plain := []byte(`<xml><ToUserName>wx-corp</ToUserName><CreateTime>1788336000</CreateTime><MsgType>event</MsgType><Event>change_external_contact</Event><ChangeType>` + changeType + `</ChangeType><ExternalUserID>external-1</ExternalUserID></xml>`)
			if _, err := parseCallbackEvent(plain, "wx-corp"); !errors.Is(err, ErrMalformedXML) {
				t.Fatalf("missing UserID err=%v", err)
			}
		})
	}
}

func TestCallbackEventSupportsExternalUserIDCompatibilityAlias(t *testing.T) {
	plain := []byte(`<xml><ToUserName>wx-corp</ToUserName><CreateTime>1788336000</CreateTime><MsgType>event</MsgType><Event>change_external_contact</Event><ChangeType>edit_external_contact</ChangeType><ExternalUserId>external-compat</ExternalUserId><UserID>employee-1</UserID></xml>`)
	event, err := parseCallbackEvent(plain, "wx-corp")
	if err != nil || event.ExternalUserID != "external-compat" {
		t.Fatalf("compatibility alias event=%+v err=%v", event, err)
	}
	duplicate := []byte(`<xml><ToUserName>wx-corp</ToUserName><CreateTime>1788336000</CreateTime><MsgType>event</MsgType><Event>change_external_contact</Event><ChangeType>edit_external_contact</ChangeType><ExternalUserID>external-a</ExternalUserID><ExternalUserId>external-a</ExternalUserId><UserID>employee-1</UserID></xml>`)
	if _, err := parseCallbackEvent(duplicate, "wx-corp"); !errors.Is(err, ErrMalformedXML) {
		t.Fatalf("dual external-user aliases err=%v", err)
	}
	half := []byte(`<xml><ToUserName>corp-a</ToUserName><CreateTime>1700000000</CreateTime><MsgType>event</MsgType><Event>change_external_contact</Event><ChangeType>add_half_external_contact</ChangeType><UserID>staff-1</UserID><ExternalUserId>external-1</ExternalUserId></xml>`)
	event, err = parseCallbackEvent(half, "corp-a")
	if err != nil || !event.IsEntrant() || event.ExternalUserID != "external-1" || event.UserID != "staff-1" {
		t.Fatalf("v2 half-contact fixture event=%+v err=%v", event, err)
	}
}

func TestCallbackParserIgnoresWellFormedProviderExtensions(t *testing.T) {
	plain := []byte(`<xml><ToUserName>wx-corp</ToUserName><FutureScalar>ignored</FutureScalar><FutureNested attr="ok"><Secret>not-persisted</Secret></FutureNested><CreateTime>1788336000</CreateTime><MsgType>event</MsgType><Event>change_external_contact</Event><ChangeType>edit_external_contact</ChangeType><ExternalUserID>external-1</ExternalUserID><UserID>employee-1</UserID></xml>`)
	if _, err := parseCallbackEvent(plain, "wx-corp"); err != nil {
		t.Fatalf("provider extension err=%v", err)
	}
	malformed := []byte(`<xml><ToUserName>wx-corp</ToUserName><FutureNested><Secret></FutureNested><CreateTime>1788336000</CreateTime><MsgType>event</MsgType><Event>change_external_contact</Event><ChangeType>edit_external_contact</ChangeType><ExternalUserID>external-1</ExternalUserID><UserID>employee-1</UserID></xml>`)
	if _, err := parseCallbackEvent(malformed, "wx-corp"); !errors.Is(err, ErrMalformedXML) {
		t.Fatalf("malformed provider extension err=%v", err)
	}
}

func TestCallbackEventPreservesPresenceForEmptyOptionalFields(t *testing.T) {
	plain := []byte(`<xml><ToUserName>wx-corp</ToUserName><CreateTime>1788336000</CreateTime><MsgType>event</MsgType><Event>change_external_contact</Event><ChangeType>edit_external_contact</ChangeType><ExternalUserID>external-1</ExternalUserID><UserID>employee-1</UserID><State></State><MsgId></MsgId><WelcomeCode></WelcomeCode><Source></Source><FailReason></FailReason></xml>`)
	digester, err := NewHMACStateDigester(bytes.Repeat([]byte{0x2a}, 32))
	if err != nil {
		t.Fatal(err)
	}
	event, err := parseCallbackEventWithStateDigester(plain, digester, "wx-corp")
	if err != nil {
		t.Fatal(err)
	}
	if !event.StatePresent || event.StateDigest == "" || !event.MsgIDPresent || !event.WelcomeCodePresent || event.WelcomeCodeDigest != callbackValueDigest("") || !event.SourcePresent || event.SourceDigest != callbackValueDigest("") || !event.FailReasonPresent || event.FailReasonDigest != callbackValueDigest("") {
		t.Fatalf("empty optional presence = %+v", event)
	}
}

func TestCallbackEventRejectsMalformedV2Fixtures(t *testing.T) {
	valid := `<xml><ToUserName>corp-a</ToUserName><CreateTime>1700000000</CreateTime><MsgType>event</MsgType><Event>change_external_contact</Event><ChangeType>add_external_contact</ChangeType><UserID>staff-1</UserID><ExternalUserID>external-1</ExternalUserID></xml>`
	for _, message := range []string{
		strings.Replace(valid, "corp-a", "corp-b", 1),
		strings.Replace(valid, "</xml>", "</xml>trailing", 1),
		strings.Replace(valid, "</ExternalUserID>", "</ExternalUserID><ExternalUserId>external-2</ExternalUserId>", 1),
		strings.Replace(valid, "<CreateTime>1700000000</CreateTime>", "<CreateTime>never</CreateTime>", 1),
	} {
		if _, err := parseCallbackEvent([]byte(message), "corp-a"); !errors.Is(err, ErrMalformedXML) && !errors.Is(err, ErrCorpMismatch) {
			t.Fatalf("malformed fixture=%q err=%v", message, err)
		}
	}
}

func TestCallbackIdempotencyDigestUsesFullPlaintextCorpAndVersion(t *testing.T) {
	plain := []byte(`<xml><ToUserName>wx-corp</ToUserName><CreateTime>1788336000</CreateTime><MsgType>event</MsgType><Event>change_external_contact</Event><ChangeType>add_external_contact</ChangeType><ExternalUserID>external-1</ExternalUserID><UserID>employee-1</UserID></xml>`)
	if got := CallbackIdempotencyDigest("wx-corp", plain); !strings.HasPrefix(got, "sha256:") || len(got) != len("sha256:")+64 {
		t.Fatalf("digest = %q", got)
	}
	if CallbackIdempotencyDigest("wx-corp", append(append([]byte(nil), plain...), []byte(" ")...)) == CallbackIdempotencyDigest("wx-corp", plain) {
		t.Fatal("trailing plaintext change was omitted from digest")
	}
	if CallbackIdempotencyDigest("other-corp", plain) == CallbackIdempotencyDigest("wx-corp", plain) {
		t.Fatal("corp was omitted from digest")
	}
	if stableCallbackKey("wx-corp", plain) == stableCallbackKey("wx-corp", append(append([]byte(nil), plain...), []byte(" ")...)) {
		t.Fatal("stable callback key omitted full plaintext")
	}
}

func TestHMACStateDigesterIsScopedAndFixedSize(t *testing.T) {
	digester, err := NewHMACStateDigester(bytes.Repeat([]byte{0x31}, 32))
	if err != nil {
		t.Fatal(err)
	}
	first, err := digester.DigestState("wx-corp", "state-secret")
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 32 {
		t.Fatalf("digest length=%d", len(first))
	}
	if first == [32]byte{} {
		t.Fatal("digest unexpectedly zero")
	}
	if otherCorp, _ := digester.DigestState("other-corp", "state-secret"); otherCorp == first {
		t.Fatal("corp scope omitted from State digest")
	}
	if otherState, _ := digester.DigestState("wx-corp", "other-state"); otherState == first {
		t.Fatal("State omitted from digest")
	}
	encoded := formatStateDigest(first)
	decoded, err := ParseStateDigest(encoded)
	if err != nil || decoded != first {
		t.Fatalf("state digest round trip=%v err=%v", decoded, err)
	}
	if _, err := NewHMACStateDigester(bytes.Repeat([]byte{0x31}, 31)); !errors.Is(err, ErrInvalidStateDigester) {
		t.Fatalf("short key err=%v", err)
	}
}

func TestEncryptedSuccessReplyEscapesReplyNonce(t *testing.T) {
	crypto, err := NewCallbackCryptoWithOptions("callback-token", "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG", "wx-corp", CallbackCryptoOptions{
		Now:   func() time.Time { return fixedNow },
		Nonce: func() string { return `reply&<nonce>` },
	})
	if err != nil {
		t.Fatal(err)
	}
	reply, err := crypto.EncryptedSuccessReply()
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Encrypt   string `xml:"Encrypt"`
		Signature string `xml:"MsgSignature"`
		Timestamp string `xml:"TimeStamp"`
		Nonce     string `xml:"Nonce"`
	}
	if err := xml.Unmarshal(reply, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Nonce != `reply&<nonce>` {
		t.Fatalf("unescaped nonce=%q", envelope.Nonce)
	}
	plain, err := crypto.VerifyAndDecrypt(envelope.Signature, envelope.Timestamp, envelope.Nonce, envelope.Encrypt)
	if err != nil || string(plain) != "success" {
		t.Fatalf("escaped ACK plaintext=%q err=%v", plain, err)
	}
}

func TestCallbackHandlerRequiresStateDigesterBeforeDurablePayload(t *testing.T) {
	crypto, err := NewCallbackCryptoWithOptions("callback-token", "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG", "wx-corp", CallbackCryptoOptions{
		Now:   func() time.Time { return fixedNow },
		Nonce: func() string { return "reply-nonce" },
	})
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte(`<xml><ToUserName>wx-corp</ToUserName><CreateTime>1788336000</CreateTime><MsgType>event</MsgType><Event>change_external_contact</Event><ChangeType>add_external_contact</ChangeType><ExternalUserID>external-1</ExternalUserID><UserID>employee-1</UserID><State><![CDATA[state-secret]]></State><WelcomeCode><![CDATA[welcome-secret]]></WelcomeCode><Source><![CDATA[source-secret]]></Source><FailReason><![CDATA[reason-secret]]></FailReason></xml>`)
	encrypted := encryptForTest(t, crypto, plain)
	timestamp, nonce := "1788336000", "nonce"
	query := url.Values{"msg_signature": {callbackSignature("callback-token", timestamp, nonce, encrypted)}, "timestamp": {timestamp}, "nonce": {nonce}}
	body := "<xml><ToUserName>wx-corp</ToUserName><Encrypt><![CDATA[" + encrypted + "]]></Encrypt></xml>"
	store := &memoryWebhookStore{}
	inbox, err := webhook.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	handler := CallbackHandler{Enabled: true, Crypto: crypto, Inbox: inbox, UOW: directUOW{}}
	withoutDigester := httptest.NewRecorder()
	handler.ServeHTTP(withoutDigester, httptest.NewRequest(http.MethodPost, "/wecom/external-contact/callback?"+query.Encode(), strings.NewReader(body)))
	if withoutDigester.Code != http.StatusServiceUnavailable || len(store.deliveries) != 0 {
		t.Fatalf("missing digester response=%d body=%q deliveries=%d", withoutDigester.Code, withoutDigester.Body.String(), len(store.deliveries))
	}

	digester, err := NewHMACStateDigester(bytes.Repeat([]byte{0x5a}, 32))
	if err != nil {
		t.Fatal(err)
	}
	handler.StateDigester = digester
	handler.WelcomeGrants = callbackWelcomeGrantStub{}
	withDigester := httptest.NewRecorder()
	handler.ServeHTTP(withDigester, httptest.NewRequest(http.MethodPost, "/wecom/external-contact/callback?"+query.Encode(), strings.NewReader(body)))
	if withDigester.Code != http.StatusOK || len(store.deliveries) != 1 {
		t.Fatalf("configured digester response=%d body=%q deliveries=%d", withDigester.Code, withDigester.Body.String(), len(store.deliveries))
	}
	assertEncryptedSuccessReply(t, crypto, withDigester.Body.Bytes())
	var event CallbackEvent
	if err := json.Unmarshal(store.deliveries[0].Payload, &event); err != nil {
		t.Fatal(err)
	}
	stateDigest, err := digester.DigestState("wx-corp", "state-secret")
	if err != nil || !event.StatePresent || event.StateDigest != formatStateDigest(stateDigest) {
		t.Fatalf("durable state digest event=%+v err=%v", event, err)
	}
	for _, secret := range []string{"state-secret", "welcome-secret", "source-secret", "reason-secret", "\"state\""} {
		if bytes.Contains(store.deliveries[0].Payload, []byte(secret)) {
			t.Fatalf("durable callback payload leaked %q: %s", secret, store.deliveries[0].Payload)
		}
	}
}

type callbackWelcomeGrantStub struct{}

func (callbackWelcomeGrantStub) Seal(_ context.Context, _ string, value string, _ time.Time) (string, error) {
	if value == "" {
		return "", ErrWelcomeGrantUnavailable
	}
	return "wgrant_1", nil
}

func TestCallbackHandlerRejectsDuplicateQueryAndTrailingOuterXML(t *testing.T) {
	crypto, err := NewCallbackCryptoWithOptions("callback-token", "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG", "wx-corp", CallbackCryptoOptions{
		Now:   func() time.Time { return fixedNow },
		Nonce: func() string { return "reply-nonce" },
	})
	if err != nil {
		t.Fatal(err)
	}
	store := &memoryWebhookStore{}
	inbox, err := webhook.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	handler := CallbackHandler{Enabled: true, Crypto: crypto, Inbox: inbox, UOW: directUOW{}}
	plain := []byte(`<xml><ToUserName>wx-corp</ToUserName><CreateTime>1788336000</CreateTime><MsgType>event</MsgType><Event>change_external_contact</Event><ChangeType>add_external_contact</ChangeType><ExternalUserID>external-1</ExternalUserID><UserID>employee-1</UserID></xml>`)
	encrypted := encryptForTest(t, crypto, plain)
	timestamp, nonce := "1788336000", "nonce"
	base := url.Values{"msg_signature": {callbackSignature("callback-token", timestamp, nonce, encrypted)}, "timestamp": {timestamp}, "nonce": {nonce}}
	base.Add("msg_signature", base.Get("msg_signature"))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/wecom/external-contact/callback?"+base.Encode(), strings.NewReader("<xml><ToUserName>wx-corp</ToUserName><Encrypt><![CDATA["+encrypted+"]]></Encrypt></xml>")))
	if response.Code != http.StatusBadRequest || len(store.deliveries) != 0 {
		t.Fatalf("duplicate query response=%d body=%q deliveries=%d", response.Code, response.Body.String(), len(store.deliveries))
	}
	base = url.Values{"msg_signature": {callbackSignature("callback-token", timestamp, nonce, encrypted)}, "timestamp": {timestamp}, "nonce": {nonce}}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/wecom/external-contact/callback?"+base.Encode(), strings.NewReader("<xml><ToUserName>wx-corp</ToUserName><Encrypt><![CDATA["+encrypted+"]]></Encrypt></xml><xml/>")))
	if response.Code != http.StatusBadRequest || len(store.deliveries) != 0 {
		t.Fatalf("trailing XML response=%d body=%q deliveries=%d", response.Code, response.Body.String(), len(store.deliveries))
	}
}

func TestCallbackHandlerChecksOuterCorpAndKeepsGETVerification(t *testing.T) {
	crypto, err := NewCallbackCryptoWithOptions("callback-token", "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG", "wx-corp", CallbackCryptoOptions{Now: func() time.Time { return fixedNow }, Nonce: func() string { return "reply-nonce" }})
	if err != nil {
		t.Fatal(err)
	}
	store := &memoryWebhookStore{}
	inbox, err := webhook.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	handler := CallbackHandler{Enabled: true, Crypto: crypto, Inbox: inbox, UOW: directUOW{}}
	plain := []byte(`<xml><ToUserName>wx-corp</ToUserName><CreateTime>1788336000</CreateTime><MsgType>event</MsgType><Event>change_external_contact</Event><ChangeType>add_external_contact</ChangeType><ExternalUserID>external-1</ExternalUserID><UserID>employee-1</UserID></xml>`)
	encrypted := encryptForTest(t, crypto, plain)
	timestamp, nonce := "1788336000", "nonce"
	signature := callbackSignature("callback-token", timestamp, nonce, encrypted)
	outerWrongCorp := httptest.NewRecorder()
	handler.ServeHTTP(outerWrongCorp, httptest.NewRequest(http.MethodPost, "/wecom/external-contact/callback?msg_signature="+signature+"&timestamp="+timestamp+"&nonce="+nonce, strings.NewReader("<xml><ToUserName>other-corp</ToUserName><Encrypt><![CDATA["+encrypted+"]]></Encrypt></xml>")))
	if outerWrongCorp.Code != http.StatusForbidden || len(store.deliveries) != 0 {
		t.Fatalf("outer corp response=%d body=%q deliveries=%d", outerWrongCorp.Code, outerWrongCorp.Body.String(), len(store.deliveries))
	}
	get := httptest.NewRecorder()
	handler.ServeHTTP(get, httptest.NewRequest(http.MethodGet, "/wecom/external-contact/callback?"+url.Values{"msg_signature": {callbackSignature("callback-token", timestamp, nonce, encrypted)}, "timestamp": {timestamp}, "nonce": {nonce}, "echostr": {encrypted}}.Encode(), nil))
	if get.Code != http.StatusOK || get.Body.String() != string(plain) || get.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatalf("GET response=%d body=%q content-type=%q", get.Code, get.Body.String(), get.Header().Get("Content-Type"))
	}
}

func TestCallbackHandlerKeepsMethodContractForUnsupportedMethods(t *testing.T) {
	response := httptest.NewRecorder()
	crypto, err := NewCallbackCrypto("callback-token", "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG", "wx-corp")
	if err != nil {
		t.Fatal(err)
	}
	store := &memoryWebhookStore{}
	inbox, err := webhook.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	CallbackHandler{Enabled: true, Crypto: crypto, Inbox: inbox, UOW: directUOW{}}.ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/wecom/external-contact/callback", nil))
	if response.Code != http.StatusMethodNotAllowed || response.Header().Get("Allow") != "GET, POST" {
		t.Fatalf("unsupported method response=%d allow=%q", response.Code, response.Header().Get("Allow"))
	}
}

func TestHTTPHandlerKeepsV2CallbackRouteAlias(t *testing.T) {
	crypto, err := NewCallbackCryptoWithOptions("callback-token", "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG", "wx-corp", CallbackCryptoOptions{Now: func() time.Time { return fixedNow }})
	if err != nil {
		t.Fatal(err)
	}
	store := &memoryWebhookStore{}
	inbox, err := webhook.NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	callback := CallbackHandler{Enabled: true, Crypto: crypto, Inbox: inbox, UOW: directUOW{}}
	handler, err := NewHTTPHandler(HTTPHandlerOptions{Callback: callback})
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("compatibility-echo")
	encrypted := encryptForTest(t, crypto, plain)
	timestamp, nonce := "1788336000", "nonce"
	request := httptest.NewRequest(http.MethodGet, "/api/wecom/events?"+url.Values{
		"msg_signature": {callbackSignature("callback-token", timestamp, nonce, encrypted)},
		"timestamp":     {timestamp},
		"nonce":         {nonce},
		"echostr":       {encrypted},
	}.Encode(), nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != string(plain) {
		t.Fatalf("v2 GET alias response=%d body=%q", response.Code, response.Body.String())
	}
}
