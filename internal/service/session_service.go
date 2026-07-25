package service

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"tao-core-go/internal/domain/models"
)

var (
	ErrSessionNotFound  = errors.New("找不到指定的測驗會話 (TestSession)")
	ErrSessionCompleted = errors.New("測驗會話已經結束交卷，不可重複提交或修改答案")
)

// SessionService 提供測驗會話生命週期管理 (狀態機) 介面。
type SessionService interface {
	StartSession(deliveryID, userID string) (*models.TestSession, error)
	SaveResponse(sessionID, itemID, responseData string) (*models.ItemResponse, error)
	SubmitSession(sessionID string) (*models.TestSession, error)
	GetSession(sessionID string) (*models.TestSession, error)
}

type sessionService struct {
	db             *gorm.DB
	scoringService ScoringService
	webhookService WebhookService
	ltiService     LTIService
	eventBus       EventBus
}

// NewSessionService 建立並回傳 SessionService 實體。
func NewSessionService(db *gorm.DB, scoringService ScoringService, webhookService WebhookService) SessionService {
	return &sessionService{
		db:             db,
		scoringService: scoringService,
		webhookService: webhookService,
	}
}

// SetLTIService 注入 LTI 1.3 服務依賴，供交卷時觸發 AGS 成績自動回傳。
func (s *sessionService) SetLTIService(ltiService LTIService) {
	s.ltiService = ltiService
}

// SetEventBus 注入解耦 EventBus 服務依賴。
func (s *sessionService) SetEventBus(eventBus EventBus) {
	s.eventBus = eventBus
}

// StartSession 啟動學生的測驗會話。
// 若會話已存在但狀態為 NOT_STARTED，將狀態切換為 IN_PROGRESS 並寫入開考時間 StartedAt。
func (s *sessionService) StartSession(deliveryID, userID string) (*models.TestSession, error) {
	var existing models.TestSession
	err := s.db.Where("delivery_id = ? AND user_id = ?", deliveryID, userID).First(&existing).Error
	if err == nil {
		if existing.Status == models.SessionStatusNotStarted {
			now := time.Now()
			existing.Status = models.SessionStatusInProgress
			existing.StartedAt = &now
			s.db.Save(&existing)
		}
		return &existing, nil
	}

	// 建立全新的 TestSession 紀錄
	now := time.Now()
	session := &models.TestSession{
		ID:         uuid.New().String(),
		DeliveryID: deliveryID,
		UserID:     userID,
		Status:     models.SessionStatusInProgress,
		StartedAt:  &now,
	}

	if err := s.db.Create(session).Error; err != nil {
		return nil, err
	}

	return session, nil
}

// SaveResponse 暫存考生對單一題目的答案。
// 若會話狀態已為 COMPLETED，拒絕修改並回傳 ErrSessionCompleted。
func (s *sessionService) SaveResponse(sessionID, itemID, responseData string) (*models.ItemResponse, error) {
	var session models.TestSession
	if err := s.db.First(&session, "id = ?", sessionID).Error; err != nil {
		return nil, ErrSessionNotFound
	}

	if session.Status == models.SessionStatusCompleted {
		return nil, ErrSessionCompleted
	}

	var itemResponse models.ItemResponse
	err := s.db.Where("session_id = ? AND item_id = ?", sessionID, itemID).First(&itemResponse).Error
	now := time.Now()

	if err == nil {
		// 更新現有答案
		itemResponse.ResponseData = responseData
		itemResponse.RespondedAt = now
		if err := s.db.Save(&itemResponse).Error; err != nil {
			return nil, err
		}
		return &itemResponse, nil
	}

	// 建立新答題紀錄
	itemResponse = models.ItemResponse{
		ID:           uuid.New().String(),
		SessionID:    sessionID,
		ItemID:       itemID,
		ResponseData: responseData,
		RespondedAt:  now,
	}

	if err := s.db.Create(&itemResponse).Error; err != nil {
		return nil, err
	}

	return &itemResponse, nil
}

// SubmitSession 終止並提交測驗會話。
// 流程：
// 1. 載入會話與所有答題明細 (ItemResponses)
// 2. 呼叫 ScoringService 計算各題得分與整張試卷總分 TotalScore
// 3. 計算作答總耗時 TimeSpentSeconds，狀態切換為 COMPLETED
// 4. 發布 "session.completed" 事件至 EventBus
// 5. 異步觸發 Webhook 派送與 LTI 1.3 AGS 成績自動回寫
func (s *sessionService) SubmitSession(sessionID string) (*models.TestSession, error) {
	var session models.TestSession
	if err := s.db.Preload("Responses").First(&session, "id = ?", sessionID).Error; err != nil {
		return nil, ErrSessionNotFound
	}

	if session.Status == models.SessionStatusCompleted {
		return &session, nil // 已交卷者直接回傳
	}

	now := time.Now()
	var totalScore float64

	// 遍歷所有答案並進行自動計分
	for i := range session.Responses {
		resp := &session.Responses[i]
		var item models.Item
		if err := s.db.First(&item, "id = ?", resp.ItemID).Error; err == nil {
			score, isCorrect := s.scoringService.ScoreItem(&item, resp.ResponseData)
			resp.ScoreGiven = score
			resp.IsCorrect = isCorrect
			s.db.Save(resp)
			totalScore += score
		}
	}

	session.Status = models.SessionStatusCompleted
	session.TotalScore = totalScore
	session.FinishedAt = &now

	if session.StartedAt != nil {
		session.TimeSpentSeconds = int(now.Sub(*session.StartedAt).Seconds())
	}

	if err := s.db.Save(&session).Error; err != nil {
		return nil, err
	}

	// 1. 發布事件至內部解耦 EventBus
	if s.eventBus != nil {
		s.eventBus.Publish("session.completed", &session)
	}

	// 2. 觸發異步 Webhook 派送 Worker
	s.webhookService.Dispatch("session.completed", map[string]interface{}{
		"session_id":          session.ID,
		"delivery_id":         session.DeliveryID,
		"user_id":             session.UserID,
		"total_score":         totalScore,
		"time_spent_seconds": session.TimeSpentSeconds,
		"finished_at":         now,
	})

	// 3. 若為 LTI 單點登入連線，異步自動 POST 回寫成績至 Moodle/Canvas LMS 成績單冊 (Gradebook)
	if s.ltiService != nil {
		go s.ltiService.SubmitGradeToLMS(&session)
	}

	return &session, nil
}

// GetSession 查詢指定 SessionID 的會話資料與答題明細。
func (s *sessionService) GetSession(sessionID string) (*models.TestSession, error) {
	var session models.TestSession
	if err := s.db.Preload("Responses").First(&session, "id = ?", sessionID).Error; err != nil {
		return nil, ErrSessionNotFound
	}
	return &session, nil
}
