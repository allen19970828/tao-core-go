package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"strings"
	"testing"

	"go.uber.org/zap"

	"tao-core-go/internal/domain/models"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestWebhookRegistrationValidatesURLAndEncryptsSecret(t *testing.T) {
	db := setupTestDB(t)
	service, err := NewWebhookService(db, zap.NewNop(), 1, testSecretCipher(t), []string{"hooks.example.com", "localhost"})
	if err != nil {
		t.Fatalf("new webhook service: %v", err)
	}
	for name, config := range map[string]*models.WebhookConfig{
		"unsupported event": {Event: "user.created", TargetURL: "https://hooks.example.com/events"},
		"plain HTTP":        {Event: "session.completed", TargetURL: "http://hooks.example.com/events"},
		"localhost":         {Event: "session.completed", TargetURL: "https://localhost/events"},
		"disallowed host":   {Event: "session.completed", TargetURL: "https://evil.example/events"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := service.RegisterConfig(config); err == nil {
				t.Fatal("expected webhook registration to be rejected")
			}
		})
	}

	config := &models.WebhookConfig{Event: "session.completed", TargetURL: "https://hooks.example.com/events", IsActive: true}
	rawSecret, err := service.RegisterConfig(config)
	if err != nil {
		t.Fatalf("register webhook: %v", err)
	}
	if len(rawSecret) < 32 || rawSecret == config.SecretToken || !strings.HasPrefix(config.SecretToken, "enc:v1:") {
		t.Fatal("expected a generated one-time secret and encrypted database value")
	}
	var stored models.WebhookConfig
	if err := db.First(&stored, "id = ?", config.ID).Error; err != nil {
		t.Fatalf("load webhook: %v", err)
	}
	if stored.SecretToken != config.SecretToken || !strings.HasPrefix(stored.SecretToken, "enc:v1:") {
		t.Fatal("webhook secret was not encrypted at rest")
	}
}

func TestWebhookHMACSignature(t *testing.T) {
	db := setupTestDB(t)
	interfaceService, err := NewWebhookService(db, zap.NewNop(), 1, testSecretCipher(t), []string{"hooks.example.com"})
	if err != nil {
		t.Fatalf("new webhook service: %v", err)
	}
	service := interfaceService.(*webhookService)
	config := &models.WebhookConfig{
		Event: "session.completed", TargetURL: "https://hooks.example.com/events", IsActive: true,
		SecretToken: "0123456789abcdef0123456789abcdef",
	}
	rawSecret, err := service.RegisterConfig(config)
	if err != nil {
		t.Fatalf("register webhook: %v", err)
	}
	payload := []byte(`{"event_name":"session.completed","payload":{"session_id":"one"}}`)
	service.httpClient = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		body, readErr := io.ReadAll(request.Body)
		if readErr != nil {
			t.Fatalf("read webhook body: %v", readErr)
		}
		if string(body) != string(payload) {
			t.Fatalf("unexpected webhook body: %s", body)
		}
		timestamp := request.Header.Get("X-Tao-Timestamp")
		deliveryID := request.Header.Get("X-Tao-Delivery")
		if timestamp == "" || deliveryID == "" {
			t.Fatal("missing webhook anti-replay headers")
		}
		mac := hmac.New(sha256.New, []byte(rawSecret))
		_, _ = mac.Write([]byte(timestamp + "." + deliveryID + "." + string(payload)))
		expected := "v1=" + hex.EncodeToString(mac.Sum(nil))
		if !hmac.Equal([]byte(expected), []byte(request.Header.Get("X-Tao-Signature"))) {
			t.Fatalf("invalid webhook signature: got %s want %s", request.Header.Get("X-Tao-Signature"), expected)
		}
		return &http.Response{
			StatusCode: http.StatusNoContent,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    request,
		}, nil
	})}

	service.sendWebhook(*config, config.Event, payload)
	var log models.WebhookLog
	if err := db.First(&log, "config_id = ?", config.ID).Error; err != nil {
		t.Fatalf("load webhook log: %v", err)
	}
	if !log.IsSuccess || log.StatusCode != http.StatusNoContent {
		t.Fatalf("unexpected webhook log: %#v", log)
	}
}
