package http

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestVerifyIntegrationAuthenticatesTheExactRequest(t *testing.T) {
	now := time.Date(2026, 9, 4, 1, 2, 3, 0, time.UTC)
	secret := strings.Repeat("s", 32)
	handler := &Handler{integration: IntegrationConfig{Enabled: true, Key: "automation", Secret: secret, ActorID: 7, MaxSkew: 5 * time.Minute}, now: func() time.Time { return now }}
	body := []byte(`{"name":"review"}`)
	request := signedIntegrationRequest(now, secret, body)
	got, timestamp, nonce, key, err := handler.verifyIntegration(request)
	if err != nil || string(got) != string(body) || !timestamp.Equal(now) || nonce != "1234567890abcdef" || key != "automation" {
		t.Fatalf("body=%q timestamp=%s nonce=%q key=%q err=%v", got, timestamp, nonce, key, err)
	}

	tampered := signedIntegrationRequest(now, secret, body)
	tampered.Body = io.NopCloser(strings.NewReader(`{"name":"changed"}`))
	if _, _, _, _, err = handler.verifyIntegration(tampered); err == nil {
		t.Fatal("tampered body passed signature verification")
	}

	stale := signedIntegrationRequest(now.Add(-6*time.Minute), secret, body)
	if _, _, _, _, err = handler.verifyIntegration(stale); err == nil {
		t.Fatal("stale request passed timestamp verification")
	}
}

func signedIntegrationRequest(at time.Time, secret string, body []byte) *http.Request {
	req := httptest.NewRequest("POST", "/api/integrations/ai-assistant/review-plans", strings.NewReader(string(body)))
	timestamp := strconv.FormatInt(at.Unix(), 10)
	nonce, idempotency := "1234567890abcdef", "integration-replay-1"
	digest := sha256.Sum256(body)
	message := timestamp + "\n" + nonce + "\n" + idempotency + "\n" + hex.EncodeToString(digest[:])
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(message))
	req.Header.Set("X-AICRM-Integration-Key", "automation")
	req.Header.Set("X-AICRM-Nonce", nonce)
	req.Header.Set("X-AICRM-Timestamp", timestamp)
	req.Header.Set("X-AICRM-Signature", hex.EncodeToString(mac.Sum(nil)))
	req.Header.Set("Idempotency-Key", idempotency)
	return req
}
