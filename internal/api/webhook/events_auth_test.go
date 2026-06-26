package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
)

func sign(token, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func TestAuthorizeEventRequest(t *testing.T) {
	const token = "internal-secret"
	body := []byte(`{"event":"recharge","event_id":"x1"}`)
	ts := "1700000000"

	t.Run("valid bearer", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/internal/webhooks/events", nil)
		r.Header.Set("Authorization", "Bearer "+token)
		if !authorizeEventRequest(r, body, token) {
			t.Fatal("valid bearer should authorize")
		}
	})

	t.Run("valid hmac signature", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/internal/webhooks/events", nil)
		r.Header.Set("X-APayShop-Timestamp", ts)
		r.Header.Set("X-APayShop-Signature", sign(token, ts, body))
		if !authorizeEventRequest(r, body, token) {
			t.Fatal("valid hmac should authorize")
		}
	})

	t.Run("tampered body fails hmac", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/internal/webhooks/events", nil)
		r.Header.Set("X-APayShop-Timestamp", ts)
		r.Header.Set("X-APayShop-Signature", sign(token, ts, body))
		if authorizeEventRequest(r, []byte(`{"event":"recharge","event_id":"TAMPERED"}`), token) {
			t.Fatal("tampered body must not authorize")
		}
	})

	t.Run("wrong token fails", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/internal/webhooks/events", nil)
		r.Header.Set("Authorization", "Bearer wrong")
		if authorizeEventRequest(r, body, token) {
			t.Fatal("wrong bearer must not authorize")
		}
	})

	t.Run("no credentials fails", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodPost, "/internal/webhooks/events", nil)
		if authorizeEventRequest(r, body, token) {
			t.Fatal("missing credentials must not authorize")
		}
	})
}
