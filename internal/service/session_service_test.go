package service

import (
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"tao-core-go/internal/domain/models"
	"tao-core-go/internal/security"
)

type recordingWebhook struct {
	mu       sync.Mutex
	dispatch int
}

func (w *recordingWebhook) Dispatch(string, interface{}) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.dispatch++
}
func (*recordingWebhook) RegisterConfig(*models.WebhookConfig) (string, error) { return "", nil }
func (*recordingWebhook) GetWebhookLogs() ([]models.WebhookLog, error)         { return nil, nil }
func (w *recordingWebhook) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.dispatch
}

func testSecretCipher(t *testing.T) *security.SecretCipher {
	t.Helper()
	cipher, err := security.NewSecretCipher(base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32))))
	if err != nil {
		t.Fatalf("create test secret cipher: %v", err)
	}
	return cipher
}

func setupTestDB(t *testing.T) *gorm.DB {
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", uuid.New().String())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("Failed to open test in-memory SQLite: %v", err)
	}

	err = db.AutoMigrate(
		&models.User{},
		&models.Item{},
		&models.Test{},
		&models.TestSection{},
		&models.TestItem{},
		&models.Delivery{},
		&models.TestSession{},
		&models.ItemResponse{},
		&models.WebhookConfig{},
		&models.WebhookLog{},
		&models.LTIPlatform{},
		&models.LTILinkSession{},
		&models.LTIOIDCState{},
		&models.LTIResourceLink{},
		&models.ProctorEvent{},
		&models.Group{},
		&models.UserGroup{},
		&models.DeliveryGroup{},
	)
	if err != nil {
		t.Fatalf("AutoMigrate failed: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("retrieve test DB: %v", err)
	}
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func seedTestDelivery(t *testing.T, db *gorm.DB, deliveryID string, item models.Item) {
	t.Helper()
	testID := uuid.New().String()
	sectionID := uuid.New().String()
	if err := db.Create(&item).Error; err != nil {
		t.Fatalf("seed item: %v", err)
	}
	if err := db.Create(&models.Test{ID: testID, Title: "Test", QTIVersion: "3.0"}).Error; err != nil {
		t.Fatalf("seed test: %v", err)
	}
	if err := db.Create(&models.TestSection{ID: sectionID, TestID: testID, Title: "Section"}).Error; err != nil {
		t.Fatalf("seed section: %v", err)
	}
	if err := db.Create(&models.TestItem{ID: uuid.New().String(), SectionID: sectionID, ItemID: item.ID, Weight: 1}).Error; err != nil {
		t.Fatalf("seed test item: %v", err)
	}
	if err := db.Create(&models.Delivery{ID: deliveryID, TestID: testID, Title: "Delivery", IsActive: true, MaxAttempts: 1}).Error; err != nil {
		t.Fatalf("seed delivery: %v", err)
	}
}

func TestSessionLifecycle(t *testing.T) {
	db := setupTestDB(t)
	logger, _ := zap.NewDevelopment()

	scoringSvc := NewScoringService()
	webhookSvc, err := NewWebhookService(db, logger, 2, testSecretCipher(t), []string{"hooks.example.com"})
	if err != nil {
		t.Fatalf("create webhook service: %v", err)
	}
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
	seedTestDelivery(t, db, "delivery-test-01", item)

	// 1. Start Session
	session, err := sessionSvc.StartSession("delivery-test-01", "user-student-01")
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	if session.Status != models.SessionStatusInProgress {
		t.Errorf("Expected status IN_PROGRESS, got %s", session.Status)
	}

	// 2. Save Response
	resp, err := sessionSvc.SaveResponse(session.ID, "user-student-01", "item-test-101", "2")
	if err != nil {
		t.Fatalf("SaveResponse failed: %v", err)
	}
	if resp.ResponseData != "2" {
		t.Errorf("Expected response data '2', got %s", resp.ResponseData)
	}

	// 3. Submit Session
	submitted, err := sessionSvc.SubmitSession(session.ID, "user-student-01")
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
	_, err = sessionSvc.SaveResponse(session.ID, "user-student-01", "item-test-101", "3")
	if err != ErrSessionCompleted {
		t.Errorf("Expected ErrSessionCompleted, got %v", err)
	}
}

func TestSessionEnforcesOwnershipAndDeliveryItems(t *testing.T) {
	db := setupTestDB(t)
	item := models.Item{ID: "owned-item", Title: "Owned", Prompt: "?", ItemType: models.ItemTypeSingleChoice, CorrectAnswer: "A", MaxScore: 2}
	seedTestDelivery(t, db, "owned-delivery", item)
	if err := db.Create(&models.Item{ID: "rogue-item", Title: "Rogue", Prompt: "?", ItemType: models.ItemTypeSingleChoice, CorrectAnswer: "A", MaxScore: 999}).Error; err != nil {
		t.Fatalf("seed rogue item: %v", err)
	}
	service := NewSessionService(db, NewScoringService(), nil)
	session, err := service.StartSession("owned-delivery", "owner")
	if err != nil {
		t.Fatalf("start session: %v", err)
	}

	if _, err := service.GetSession(session.ID, "attacker"); err != ErrSessionNotFound {
		t.Fatalf("expected foreign session to be hidden, got %v", err)
	}
	if _, err := service.SaveResponse(session.ID, "attacker", item.ID, "A"); err != ErrSessionNotFound {
		t.Fatalf("expected foreign response write to fail, got %v", err)
	}
	if _, err := service.SubmitSession(session.ID, "attacker"); err != ErrSessionNotFound {
		t.Fatalf("expected foreign submission to fail, got %v", err)
	}
	if _, err := service.SaveResponse(session.ID, "owner", "rogue-item", "A"); err != ErrItemNotInDelivery {
		t.Fatalf("expected unrelated item to be rejected, got %v", err)
	}
}

func TestSessionEligibilityAndAttemptLimit(t *testing.T) {
	t.Run("closed delivery", func(t *testing.T) {
		db := setupTestDB(t)
		seedTestDelivery(t, db, "closed-delivery", models.Item{ID: "closed-item", Title: "Closed", Prompt: "?", ItemType: models.ItemTypeShortAnswer, CorrectAnswer: "x", MaxScore: 1})
		past := time.Now().Add(-time.Minute)
		if err := db.Model(&models.Delivery{}).Where("id = ?", "closed-delivery").Update("end_time", past).Error; err != nil {
			t.Fatalf("close delivery: %v", err)
		}
		if _, err := NewSessionService(db, NewScoringService(), nil).StartSession("closed-delivery", "student"); err != ErrDeliveryClosed {
			t.Fatalf("expected closed delivery error, got %v", err)
		}
	})

	t.Run("group restriction", func(t *testing.T) {
		db := setupTestDB(t)
		seedTestDelivery(t, db, "group-delivery", models.Item{ID: "group-item", Title: "Group", Prompt: "?", ItemType: models.ItemTypeShortAnswer, CorrectAnswer: "x", MaxScore: 1})
		group := models.Group{ID: "allowed-group", GroupCode: "ALLOWED", GroupName: "Allowed"}
		if err := db.Create(&group).Error; err != nil {
			t.Fatalf("seed group: %v", err)
		}
		if err := db.Create(&models.DeliveryGroup{DeliveryID: "group-delivery", GroupID: group.ID}).Error; err != nil {
			t.Fatalf("restrict delivery: %v", err)
		}
		service := NewSessionService(db, NewScoringService(), nil)
		if _, err := service.StartSession("group-delivery", "outsider"); err != ErrDeliveryForbidden {
			t.Fatalf("expected group restriction, got %v", err)
		}
		if err := db.Create(&models.UserGroup{UserID: "member", GroupID: group.ID}).Error; err != nil {
			t.Fatalf("seed membership: %v", err)
		}
		if _, err := service.StartSession("group-delivery", "member"); err != nil {
			t.Fatalf("expected member to start: %v", err)
		}
	})

	t.Run("attempt limit", func(t *testing.T) {
		db := setupTestDB(t)
		item := models.Item{ID: "limit-item", Title: "Limit", Prompt: "?", ItemType: models.ItemTypeShortAnswer, CorrectAnswer: "x", MaxScore: 1}
		seedTestDelivery(t, db, "limit-delivery", item)
		service := NewSessionService(db, NewScoringService(), nil)
		session, err := service.StartSession("limit-delivery", "student")
		if err != nil {
			t.Fatalf("start session: %v", err)
		}
		if _, err := service.SubmitSession(session.ID, "student"); err != nil {
			t.Fatalf("submit session: %v", err)
		}
		if _, err := service.StartSession("limit-delivery", "student"); err != ErrMaxAttempts {
			t.Fatalf("expected attempt limit, got %v", err)
		}
	})
}

func TestSubmitIsIdempotentAndDispatchesOnce(t *testing.T) {
	db := setupTestDB(t)
	item := models.Item{ID: "weighted-item", Title: "Weighted", Prompt: "?", ItemType: models.ItemTypeSingleChoice, CorrectAnswer: "A", MaxScore: 2}
	seedTestDelivery(t, db, "weighted-delivery", item)
	if err := db.Model(&models.TestItem{}).Where("item_id = ?", item.ID).Update("weight", 1.5).Error; err != nil {
		t.Fatalf("set weight: %v", err)
	}
	recorder := &recordingWebhook{}
	service := NewSessionService(db, NewScoringService(), recorder)
	session, err := service.StartSession("weighted-delivery", "student")
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	if _, err := service.SaveResponse(session.ID, "student", item.ID, "A"); err != nil {
		t.Fatalf("save response: %v", err)
	}

	const submitters = 8
	errorsFound := make(chan error, submitters)
	var waitGroup sync.WaitGroup
	for range submitters {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			completed, err := service.SubmitSession(session.ID, "student")
			if err == nil && (completed.Status != models.SessionStatusCompleted || completed.TotalScore != 3) {
				err = fmt.Errorf("unexpected completion: status=%s score=%v", completed.Status, completed.TotalScore)
			}
			errorsFound <- err
		}()
	}
	waitGroup.Wait()
	close(errorsFound)
	for err := range errorsFound {
		if err != nil {
			t.Fatalf("concurrent submit failed: %v", err)
		}
	}
	if recorder.count() != 1 {
		t.Fatalf("expected exactly one webhook dispatch, got %d", recorder.count())
	}
}

func TestSaveResponseRejectsExpiredExamTimeLimit(t *testing.T) {
	db := setupTestDB(t)
	item := models.Item{ID: "timed-item", Title: "Timed", Prompt: "?", ItemType: models.ItemTypeSingleChoice, CorrectAnswer: "A", MaxScore: 1}
	seedTestDelivery(t, db, "timed-delivery", item)
	var delivery models.Delivery
	if err := db.First(&delivery, "id = ?", "timed-delivery").Error; err != nil {
		t.Fatalf("load delivery: %v", err)
	}
	if err := db.Model(&models.Test{}).Where("id = ?", delivery.TestID).Update("time_limit_seconds", 1).Error; err != nil {
		t.Fatalf("set time limit: %v", err)
	}
	service := NewSessionService(db, NewScoringService(), nil)
	session, err := service.StartSession(delivery.ID, "student")
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	past := time.Now().Add(-time.Minute)
	if err := db.Model(&models.TestSession{}).Where("id = ?", session.ID).Update("started_at", past).Error; err != nil {
		t.Fatalf("age session: %v", err)
	}
	if _, err := service.SaveResponse(session.ID, "student", item.ID, "A"); err != ErrSessionExpired {
		t.Fatalf("expected expired session response to be rejected, got %v", err)
	}
	if _, err := service.SubmitSession(session.ID, "student"); err != nil {
		t.Fatalf("expected expired session to remain submittable: %v", err)
	}
}
