package service

import (
	"testing"

	"tao-core-go/internal/domain/models"
)

func TestProctorService(t *testing.T) {
	db := setupTestDB(t)
	proctorSvc := NewProctorService(db)

	sessionID := "session-proctor-test-01"
	userID := "student-proctor-01"
	testID := "test-proctor-01"
	if err := db.Create(&models.Test{ID: testID, Title: "Proctor test"}).Error; err != nil {
		t.Fatalf("seed test: %v", err)
	}
	if err := db.Create(&models.Delivery{ID: "delivery-proctor-01", TestID: testID, Title: "Proctor delivery", IsActive: true}).Error; err != nil {
		t.Fatalf("seed delivery: %v", err)
	}
	if err := db.Create(&models.TestSession{ID: sessionID, DeliveryID: "delivery-proctor-01", UserID: userID, Attempt: 1, Status: models.SessionStatusInProgress}).Error; err != nil {
		t.Fatalf("seed session: %v", err)
	}

	// 1. Record normal tab switch
	err := proctorSvc.RecordEvent(&models.ProctorEvent{
		SessionID:       sessionID,
		EventType:       models.ProctorEventTabSwitch,
		DurationSeconds: 15,
		Details:         "Tab switched to another app",
	}, userID)
	if err != nil {
		t.Fatalf("RecordEvent failed: %v", err)
	}

	// 2. Record screenshot blackout attempt
	err = proctorSvc.RecordEvent(&models.ProctorEvent{
		SessionID:       sessionID,
		EventType:       models.ProctorEventScreenshotAttempt,
		DurationSeconds: 0,
		Details:         "Blur blackout triggered by window.onblur",
	}, userID)
	if err != nil {
		t.Fatalf("RecordEvent failed: %v", err)
	}

	// 3. Fetch logs
	logs, err := proctorSvc.GetSessionProctorLog(sessionID)
	if err != nil {
		t.Fatalf("GetSessionProctorLog failed: %v", err)
	}
	if len(logs) != 2 {
		t.Fatalf("Expected 2 proctor logs, got %d", len(logs))
	}

	// 4. Check Analytics RiskLevel
	analytics, err := proctorSvc.GetProctorAnalytics(sessionID)
	if err != nil {
		t.Fatalf("GetProctorAnalytics failed: %v", err)
	}

	if analytics.TotalTabSwitches != 1 {
		t.Errorf("Expected 1 tab switch, got %d", analytics.TotalTabSwitches)
	}
	if analytics.TotalScreenshotAttempts != 1 {
		t.Errorf("Expected 1 screenshot attempt, got %d", analytics.TotalScreenshotAttempts)
	}
	if analytics.RiskLevel != "MEDIUM" {
		t.Errorf("Expected RiskLevel 'MEDIUM', got %s", analytics.RiskLevel)
	}

	if err := proctorSvc.RecordEvent(&models.ProctorEvent{
		SessionID: sessionID, EventType: models.ProctorEventCopyAttempt,
	}, "another-student"); err != ErrSessionNotFound {
		t.Fatalf("expected a foreign proctor event to be rejected, got %v", err)
	}
	if err := proctorSvc.RecordEvent(&models.ProctorEvent{
		SessionID: sessionID, EventType: models.ProctorEventType("ARBITRARY"),
	}, userID); err == nil {
		t.Fatal("expected an unknown proctor event type to be rejected")
	}
}
