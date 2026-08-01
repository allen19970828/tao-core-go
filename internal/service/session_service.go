package service

import (
	"errors"
	"math"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"tao-core-go/internal/domain/models"
)

var (
	ErrSessionNotFound   = errors.New("找不到指定的測驗會話")
	ErrSessionCompleted  = errors.New("測驗會話已經結束，不可重複提交或修改答案")
	ErrSessionNotActive  = errors.New("測驗會話目前不可作答")
	ErrSessionExpired    = errors.New("測驗作答時間已截止，請直接交卷")
	ErrDeliveryNotFound  = errors.New("找不到指定的測驗發布")
	ErrDeliveryClosed    = errors.New("測驗發布目前未開放")
	ErrDeliveryForbidden = errors.New("使用者不屬於此測驗允許的群組")
	ErrMaxAttempts       = errors.New("已達測驗允許的最大作答次數")
	ErrItemNotInDelivery = errors.New("題目不屬於此測驗")
)

// SessionService 提供測驗會話生命週期與資源所有權管理。
type SessionService interface {
	StartSession(deliveryID, userID string) (*models.TestSession, error)
	SaveResponse(sessionID, userID, itemID, responseData string) (*models.ItemResponse, error)
	SubmitSession(sessionID, userID string) (*models.TestSession, error)
	GetSession(sessionID, userID string) (*models.TestSession, error)
}

type sessionService struct {
	db             *gorm.DB
	scoringService ScoringService
	webhookService WebhookService
	ltiService     LTIService
	eventBus       EventBus
}

func NewSessionService(db *gorm.DB, scoringService ScoringService, webhookService WebhookService) SessionService {
	return &sessionService{db: db, scoringService: scoringService, webhookService: webhookService}
}

func (s *sessionService) SetLTIService(ltiService LTIService) { s.ltiService = ltiService }
func (s *sessionService) SetEventBus(eventBus EventBus)       { s.eventBus = eventBus }

func (s *sessionService) StartSession(deliveryID, userID string) (*models.TestSession, error) {
	deliveryID = strings.TrimSpace(deliveryID)
	userID = strings.TrimSpace(userID)
	if deliveryID == "" || userID == "" {
		return nil, ErrDeliveryNotFound
	}

	var session models.TestSession
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var delivery models.Delivery
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&delivery, "id = ?", deliveryID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrDeliveryNotFound
			}
			return err
		}

		now := time.Now()
		if !delivery.IsActive || (delivery.StartTime != nil && now.Before(*delivery.StartTime)) || (delivery.EndTime != nil && now.After(*delivery.EndTime)) {
			return ErrDeliveryClosed
		}

		var restrictedGroupCount int64
		if err := tx.Model(&models.DeliveryGroup{}).Where("delivery_id = ?", deliveryID).Count(&restrictedGroupCount).Error; err != nil {
			return err
		}
		if restrictedGroupCount > 0 {
			var membershipCount int64
			if err := tx.Model(&models.UserGroup{}).
				Joins("JOIN delivery_groups ON delivery_groups.group_id = user_groups.group_id").
				Where("delivery_groups.delivery_id = ? AND user_groups.user_id = ?", deliveryID, userID).
				Count(&membershipCount).Error; err != nil {
				return err
			}
			if membershipCount == 0 {
				return ErrDeliveryForbidden
			}
		}

		err := tx.Where("delivery_id = ? AND user_id = ? AND status IN ?", deliveryID, userID,
			[]models.SessionStatus{models.SessionStatusNotStarted, models.SessionStatusInProgress, models.SessionStatusPaused}).
			Order("attempt DESC").First(&session).Error
		if err == nil {
			if session.Status == models.SessionStatusNotStarted {
				session.Status = models.SessionStatusInProgress
				session.StartedAt = &now
				return tx.Save(&session).Error
			}
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		var attempts int64
		if err := tx.Model(&models.TestSession{}).Where("delivery_id = ? AND user_id = ?", deliveryID, userID).Count(&attempts).Error; err != nil {
			return err
		}
		maxAttempts := delivery.MaxAttempts
		if maxAttempts <= 0 {
			maxAttempts = 1
		}
		if attempts >= int64(maxAttempts) {
			return ErrMaxAttempts
		}

		session = models.TestSession{
			ID:         uuid.New().String(),
			DeliveryID: deliveryID,
			UserID:     userID,
			Attempt:    int(attempts) + 1,
			Status:     models.SessionStatusInProgress,
			StartedAt:  &now,
		}
		return tx.Create(&session).Error
	})
	if err != nil {
		return nil, err
	}
	return &session, nil
}

func (s *sessionService) SaveResponse(sessionID, userID, itemID, responseData string) (*models.ItemResponse, error) {
	var saved models.ItemResponse
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var session models.TestSession
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND user_id = ?", sessionID, userID).First(&session).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrSessionNotFound
			}
			return err
		}
		if session.Status == models.SessionStatusCompleted {
			return ErrSessionCompleted
		}
		if session.Status != models.SessionStatusInProgress {
			return ErrSessionNotActive
		}
		if err := ensureSessionWithinTimeLimit(tx, &session); err != nil {
			return err
		}
		if err := ensureItemBelongsToDelivery(tx, session.DeliveryID, itemID); err != nil {
			return err
		}

		now := time.Now()
		saved = models.ItemResponse{
			ID:           uuid.New().String(),
			SessionID:    sessionID,
			ItemID:       itemID,
			ResponseData: responseData,
			RespondedAt:  now,
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{{Name: "session_id"}, {Name: "item_id"}},
			DoUpdates: clause.Assignments(map[string]interface{}{
				"response_data": responseData,
				"responded_at":  now,
				"score_given":   0,
				"is_correct":    false,
			}),
		}).Create(&saved).Error; err != nil {
			return err
		}
		return tx.Where("session_id = ? AND item_id = ?", sessionID, itemID).First(&saved).Error
	})
	if err != nil {
		return nil, err
	}
	return &saved, nil
}

func (s *sessionService) SubmitSession(sessionID, userID string) (*models.TestSession, error) {
	var session models.TestSession
	didComplete := false
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Preload("Responses").
			Where("id = ? AND user_id = ?", sessionID, userID).First(&session).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrSessionNotFound
			}
			return err
		}
		if session.Status == models.SessionStatusCompleted {
			return nil
		}
		if session.Status != models.SessionStatusInProgress {
			return ErrSessionNotActive
		}

		var totalScore float64
		for i := range session.Responses {
			resp := &session.Responses[i]
			testItem, err := findDeliveryTestItem(tx, session.DeliveryID, resp.ItemID)
			if err != nil {
				return err
			}
			if testItem.Weight <= 0 || testItem.Weight > 1_000_000 || math.IsNaN(testItem.Weight) || math.IsInf(testItem.Weight, 0) ||
				testItem.Item.MaxScore <= 0 || testItem.Item.MaxScore > 1_000_000 || math.IsNaN(testItem.Item.MaxScore) || math.IsInf(testItem.Item.MaxScore, 0) {
				return errors.New("測驗題目配分設定無效")
			}
			score, isCorrect := s.scoringService.ScoreItem(&testItem.Item, resp.ResponseData)
			score *= testItem.Weight
			resp.ScoreGiven = score
			resp.IsCorrect = isCorrect
			if err := tx.Model(resp).Select("score_given", "is_correct").Updates(resp).Error; err != nil {
				return err
			}
			totalScore += score
			if math.IsNaN(totalScore) || math.IsInf(totalScore, 0) {
				return errors.New("測驗總分計算溢位")
			}
		}

		now := time.Now()
		updates := map[string]interface{}{
			"status":      models.SessionStatusCompleted,
			"total_score": totalScore,
			"finished_at": now,
		}
		if session.StartedAt != nil {
			updates["time_spent_seconds"] = max(0, int(now.Sub(*session.StartedAt).Seconds()))
		}
		result := tx.Model(&models.TestSession{}).
			Where("id = ? AND user_id = ? AND status = ?", session.ID, userID, models.SessionStatusInProgress).
			Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrSessionCompleted
		}
		didComplete = true
		return tx.Preload("Responses").First(&session, "id = ?", session.ID).Error
	})
	if err != nil {
		return nil, err
	}

	if didComplete {
		if s.eventBus != nil {
			s.eventBus.Publish("session.completed", &session)
		}
		if s.webhookService != nil {
			s.webhookService.Dispatch("session.completed", map[string]interface{}{
				"session_id": session.ID, "delivery_id": session.DeliveryID, "user_id": session.UserID,
				"total_score": session.TotalScore, "time_spent_seconds": session.TimeSpentSeconds,
				"finished_at": session.FinishedAt,
			})
		}
		if s.ltiService != nil {
			go func(completed models.TestSession) { _ = s.ltiService.SubmitGradeToLMS(&completed) }(session)
		}
	}
	return &session, nil
}

func (s *sessionService) GetSession(sessionID, userID string) (*models.TestSession, error) {
	var session models.TestSession
	if err := s.db.Preload("Responses").Where("id = ? AND user_id = ?", sessionID, userID).First(&session).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrSessionNotFound
		}
		return nil, err
	}
	return &session, nil
}

func ensureItemBelongsToDelivery(tx *gorm.DB, deliveryID, itemID string) error {
	_, err := findDeliveryTestItem(tx, deliveryID, itemID)
	return err
}

func ensureSessionWithinTimeLimit(tx *gorm.DB, session *models.TestSession) error {
	if session.StartedAt == nil {
		return ErrSessionNotActive
	}
	var result struct{ TimeLimitSeconds int }
	err := tx.Model(&models.Delivery{}).
		Select("tests.time_limit_seconds").
		Joins("JOIN tests ON tests.id = deliveries.test_id").
		Where("deliveries.id = ?", session.DeliveryID).
		Scan(&result).Error
	if err != nil {
		return err
	}
	if result.TimeLimitSeconds > 0 && time.Now().After(session.StartedAt.Add(time.Duration(result.TimeLimitSeconds)*time.Second)) {
		return ErrSessionExpired
	}
	return nil
}

func findDeliveryTestItem(tx *gorm.DB, deliveryID, itemID string) (*models.TestItem, error) {
	var testItem models.TestItem
	err := tx.Preload("Item").
		Joins("JOIN test_sections ON test_sections.id = test_items.section_id").
		Joins("JOIN deliveries ON deliveries.test_id = test_sections.test_id").
		Where("deliveries.id = ? AND test_items.item_id = ?", deliveryID, itemID).
		First(&testItem).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrItemNotInDelivery
	}
	if err != nil {
		return nil, err
	}
	return &testItem, nil
}
