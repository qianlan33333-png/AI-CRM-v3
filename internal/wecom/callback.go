package wecom

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/idempotency"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/webhook"
)

const maxCallbackBody = 1 << 20

type CallbackEvent struct {
	ToUserName     string `xml:"ToUserName" json:"to_user_name"`
	Event          string `xml:"Event" json:"event"`
	ChangeType     string `xml:"ChangeType" json:"change_type"`
	ExternalUserID string `xml:"ExternalUserID" json:"external_userid"`
	UserID         string `xml:"UserID" json:"userid"`
	WelcomeCode    string `xml:"WelcomeCode" json:"-"`
	MsgID          string `xml:"MsgId" json:"msg_id"`
	CreateTime     int64  `xml:"CreateTime" json:"create_time"`
}

func (event CallbackEvent) supported() bool {
	return event.Event == "change_external_contact" && (event.ChangeType == "add_external_contact" || event.ChangeType == "add_half_external_contact" || event.ChangeType == "edit_external_contact" || event.ChangeType == "del_follow_user")
}

func parseCallbackEvent(value []byte) (CallbackEvent, error) {
	decoder := xml.NewDecoder(strings.NewReader(string(value)))
	decoder.Strict = true
	var event CallbackEvent
	if err := decoder.Decode(&event); err != nil {
		return CallbackEvent{}, ErrMalformedXML
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return CallbackEvent{}, ErrMalformedXML
	}
	if strings.TrimSpace(event.ToUserName) == "" || !event.supported() || strings.TrimSpace(event.ExternalUserID) == "" || strings.TrimSpace(event.UserID) == "" {
		return CallbackEvent{}, ErrMalformedXML
	}
	return event, nil
}

func parseEncryptedEnvelope(value []byte) (string, error) {
	decoder := xml.NewDecoder(strings.NewReader(string(value)))
	decoder.Strict = true
	var envelope struct {
		Encrypt string `xml:"Encrypt"`
	}
	if err := decoder.Decode(&envelope); err != nil || strings.TrimSpace(envelope.Encrypt) == "" {
		return "", ErrMalformedXML
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return "", ErrMalformedXML
	}
	return envelope.Encrypt, nil
}

type CallbackHandler struct {
	Enabled bool
	Crypto  *CallbackCrypto
	Inbox   *webhook.Service
	UOW     port.UnitOfWork
}

func (handler CallbackHandler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if !handler.Enabled {
		writeWeComError(writer, http.StatusServiceUnavailable, "provider_unavailable")
		return
	}
	if handler.Crypto == nil || handler.Inbox == nil || handler.UOW == nil {
		writeWeComError(writer, http.StatusServiceUnavailable, "provider_unavailable")
		return
	}
	signature := request.URL.Query().Get("msg_signature")
	timestamp := request.URL.Query().Get("timestamp")
	nonce := request.URL.Query().Get("nonce")
	switch request.Method {
	case http.MethodGet:
		plain, err := handler.Crypto.VerifyAndDecrypt(signature, timestamp, nonce, request.URL.Query().Get("echostr"))
		if err != nil {
			writeCallbackFailure(writer, err)
			return
		}
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = writer.Write(plain)
	case http.MethodPost:
		request.Body = http.MaxBytesReader(writer, request.Body, maxCallbackBody)
		body, err := io.ReadAll(request.Body)
		if err != nil {
			writeWeComError(writer, http.StatusRequestEntityTooLarge, "invalid_request")
			return
		}
		encrypted, err := parseEncryptedEnvelope(body)
		if err != nil {
			writeCallbackFailure(writer, err)
			return
		}
		plain, err := handler.Crypto.VerifyAndDecrypt(signature, timestamp, nonce, encrypted)
		if err != nil {
			writeCallbackFailure(writer, err)
			return
		}
		event, err := parseCallbackEvent(plain)
		if err != nil {
			writeCallbackFailure(writer, err)
			return
		}
		if event.ToUserName != handler.Crypto.corpID {
			writeCallbackFailure(writer, ErrCorpMismatch)
			return
		}
		payload, err := json.Marshal(event)
		if err != nil {
			writeWeComError(writer, http.StatusBadRequest, "invalid_request")
			return
		}
		key, _ := idempotency.Parse("wecom:external-contact:" + stableEventKey(event))
		err = handler.UOW.Within(request.Context(), func(txContext context.Context) error {
			_, ingestErr := handler.Inbox.Ingest(txContext, webhook.Ingest{Provider: "wecom.external_contact", IdempotencyKey: key, Payload: payload})
			return ingestErr
		})
		if err != nil {
			writeWeComError(writer, http.StatusServiceUnavailable, "provider_unavailable")
			return
		}
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = writer.Write([]byte("success"))
	default:
		writer.Header().Set("Allow", "GET, POST")
		writeWeComError(writer, http.StatusMethodNotAllowed, "invalid_request")
	}
}

func writeCallbackFailure(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrSignature):
		writeWeComError(writer, http.StatusForbidden, "wecom_signature_invalid")
	case errors.Is(err, ErrCallbackExpired):
		writeWeComError(writer, http.StatusForbidden, "wecom_callback_expired")
	case errors.Is(err, ErrCorpMismatch):
		writeWeComError(writer, http.StatusForbidden, "wecom_corp_mismatch")
	default:
		writeWeComError(writer, http.StatusBadRequest, "invalid_request")
	}
}

func writeWeComError(writer http.ResponseWriter, status int, code string) {
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]string{"code": code})
}

// stableEventKey is intentionally based on authenticated plaintext. WeCom
// does not promise every callback event has a message id, so exact delivery
// replays still converge without treating a provider id as universally present.
func stableEventKey(event CallbackEvent) string {
	value := strings.Join([]string{event.ToUserName, event.Event, event.ChangeType, event.ExternalUserID, event.UserID, event.MsgID, time.Unix(event.CreateTime, 0).UTC().Format(time.RFC3339)}, "\x00")
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
