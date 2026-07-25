package service

import (
	"testing"

	"go.uber.org/zap"

	"tao-core-go/internal/domain/models"
)

func TestLTILifecycle(t *testing.T) {
	db := setupTestDB(t)
	logger, _ := zap.NewDevelopment()

	scoringSvc := NewScoringService()
	webhookSvc := NewWebhookService(db, logger, 2)
	sessionSvc := NewSessionService(db, scoringSvc, webhookSvc)
	ltiSvc := NewLTIService(db, logger, sessionSvc)

	// 1. Register LTI Platform (e.g. Moodle)
	platform := &models.LTIPlatform{
		Issuer:       "https://moodle.example.com",
		ClientID:     "moodle-client-123",
		KeySetURL:    "https://moodle.example.com/mod/lti/certs.php",
		AuthTokenURL: "https://moodle.example.com/mod/lti/token.php",
		AuthLoginURL: "https://moodle.example.com/mod/lti/auth.php",
	}

	if err := ltiSvc.RegisterPlatform(platform); err != nil {
		t.Fatalf("RegisterPlatform failed: %v", err)
	}

	// 2. Initiate Login
	loginURL, err := ltiSvc.InitiateLogin("https://moodle.example.com", "moodle-client-123", "http://localhost:8080/api/v1/lti/launch")
	if err != nil {
		t.Fatalf("InitiateLogin failed: %v", err)
	}

	if loginURL == "" {
		t.Errorf("Expected non-empty login redirect URL")
	}

	// 3. Test non-existent platform login
	_, errNotFound := ltiSvc.InitiateLogin("https://unknown.com", "client-999", "http://localhost/launch")
	if errNotFound != ErrPlatformNotFound {
		t.Errorf("Expected ErrPlatformNotFound, got %v", errNotFound)
	}
}
