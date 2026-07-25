package service

import (
	"testing"

	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"tao-core-go/internal/domain/models"
)

func setupTestDB(t *testing.T) *gorm.DB {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open test in-memory SQLite: %v", err)
	}

	err = db.AutoMigrate(
		&models.User{},
		&models.Item{},
		&models.Delivery{},
		&models.TestSession{},
		&models.ItemResponse{},
		&models.WebhookConfig{},
		&models.WebhookLog{},
		&models.LTIPlatform{},
		&models.LTILinkSession{},
		&models.ProctorEvent{},
	)
	if err != nil {
		t.Fatalf("AutoMigrate failed: %v", err)
	}
	return db
}

func TestSessionLifecycle(t *testing.T) {
	db := setupTestDB(t)
	logger, _ := zap.NewDevelopment()

	scoringSvc := NewScoringService()
	webhookSvc := NewWebhookService(db, logger, 2)
	sessionSvc := NewSessionService(db, scoringSvc, webhookSvc)

	// Seed item
	item := models.Item{
		ID:            "item-test-101",
		Title:         "Test Item",
		Prompt:        "1 + 1 = ?",
		ItemType:      models.ItemTypeSingleChoice,
		CorrectAnswer: "2",
		MaxScore:      5.0,
	}
	db.Create(&item)

	// 1. Start Session
	session, err := sessionSvc.StartSession("delivery-test-01", "user-student-01")
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	if session.Status != models.SessionStatusInProgress {
		t.Errorf("Expected status IN_PROGRESS, got %s", session.Status)
	}

	// 2. Save Response
	resp, err := sessionSvc.SaveResponse(session.ID, "item-test-101", "2")
	if err != nil {
		t.Fatalf("SaveResponse failed: %v", err)
	}
	if resp.ResponseData != "2" {
		t.Errorf("Expected response data '2', got %s", resp.ResponseData)
	}

	// 3. Submit Session
	submitted, err := sessionSvc.SubmitSession(session.ID)
	if err != nil {
		t.Fatalf("SubmitSession failed: %v", err)
	}
	if submitted.Status != models.SessionStatusCompleted {
		t.Errorf("Expected status COMPLETED, got %s", submitted.Status)
	}
	if submitted.TotalScore != 5.0 {
		t.Errorf("Expected total score 5.0, got %f", submitted.TotalScore)
	}

	// 4. Try saving response after completion (should fail)
	_, err = sessionSvc.SaveResponse(session.ID, "item-test-101", "3")
	if err != ErrSessionCompleted {
		t.Errorf("Expected ErrSessionCompleted, got %v", err)
	}
}
