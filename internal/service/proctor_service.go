package service

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"tao-core-go/internal/domain/models"
)

// ProctorService 提供切頁監控事件記錄與作弊風險評估介面。
type ProctorService interface {
	RecordEvent(event *models.ProctorEvent) error
	GetSessionProctorLog(sessionID string) ([]models.ProctorEvent, error)
	GetProctorAnalytics(sessionID string) (*models.ProctorAnalyticsSummary, error)
}

type proctorService struct {
	db *gorm.DB
}

// NewProctorService 建立並回傳 ProctorService 實體。
func NewProctorService(db *gorm.DB) ProctorService {
	return &proctorService{
		db: db,
	}
}

// RecordEvent 寫入考生切頁、跳出視窗、複製文字或觸發黑屏防截圖的事件日誌。
// （註：切頁不觸發強制斷考交卷，而是完整保存於資料庫供後續監考數據分析）。
func (s *proctorService) RecordEvent(event *models.ProctorEvent) error {
	if event.ID == "" {
		event.ID = uuid.New().String()
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now()
	}
	return s.db.Create(event).Error
}

// GetSessionProctorLog 查詢指定會話的所有監考事件紀錄。
func (s *proctorService) GetSessionProctorLog(sessionID string) ([]models.ProctorEvent, error) {
	var events []models.ProctorEvent
	err := s.db.Where("session_id = ?", sessionID).Order("created_at ASC").Find(&events).Error
	return events, err
}

// GetProctorAnalytics 自動統計考生的切頁次數、總離頁時間與防截圖觸發次數，輸出 RiskLevel (LOW / MEDIUM / HIGH) 分析報告。
func (s *proctorService) GetProctorAnalytics(sessionID string) (*models.ProctorAnalyticsSummary, error) {
	events, err := s.GetSessionProctorLog(sessionID)
	if err != nil {
		return nil, err
	}

	summary := &models.ProctorAnalyticsSummary{
		SessionID: sessionID,
		RiskLevel: "LOW",
	}

	for _, e := range events {
		switch e.EventType {
		case models.ProctorEventTabSwitch, models.ProctorEventFocusLost:
			summary.TotalTabSwitches++
			summary.TotalFocusLostSeconds += e.DurationSeconds
		case models.ProctorEventScreenshotAttempt:
			summary.TotalScreenshotAttempts++
		case models.ProctorEventCopyAttempt:
			summary.TotalCopyAttempts++
		}
	}

	// 根據違規數據評估風險等級
	if summary.TotalTabSwitches >= 5 || summary.TotalFocusLostSeconds >= 120 || summary.TotalCopyAttempts >= 3 || summary.TotalScreenshotAttempts >= 3 {
		summary.RiskLevel = "HIGH"
	} else if summary.TotalTabSwitches >= 2 || summary.TotalFocusLostSeconds >= 30 || summary.TotalCopyAttempts >= 1 || summary.TotalScreenshotAttempts >= 1 {
		summary.RiskLevel = "MEDIUM"
	}

	return summary, nil
}
