package service

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"tao-core-go/internal/domain/models"
)

// WebhookEvent 定義管道派送的 Webhook 事件結構體。
type WebhookEvent struct {
	EventName string      `json:"event_name"`
	Payload   interface{} `json:"payload"`
}

// WebhookService 提供異步 Webhook 事件分發與日誌管理介面。
type WebhookService interface {
	Dispatch(eventName string, payload interface{})
	RegisterConfig(config *models.WebhookConfig) error
	GetWebhookLogs() ([]models.WebhookLog, error)
}

type webhookService struct {
	db          *gorm.DB
	logger      *zap.Logger
	eventChan   chan WebhookEvent
	workerCount int
	httpClient  *http.Client
}

// NewWebhookService 建立並啟動 Goroutine Worker 併發池處理解耦的 Webhook 派送任務。
func NewWebhookService(db *gorm.DB, logger *zap.Logger, workerPoolSize int) WebhookService {
	if workerPoolSize <= 0 {
		workerPoolSize = 5
	}

	s := &webhookService{
		db:          db,
		logger:      logger,
		eventChan:   make(chan WebhookEvent, 100),
		workerCount: workerPoolSize,
		httpClient:  &http.Client{Timeout: 10 * time.Second},
	}

	// 啟動指定數量的 Goroutine 背景 Worker 池
	for i := 0; i < workerPoolSize; i++ {
		go s.workerLoop()
	}

	return s
}

// Dispatch 將 Webhook 事件非阻塞推入 Channel 緩衝管道中。
func (s *webhookService) Dispatch(eventName string, payload interface{}) {
	select {
	case s.eventChan <- WebhookEvent{EventName: eventName, Payload: payload}:
	default:
		s.logger.Warn("Webhook 事件佇列已滿，丟棄事件", zap.String("event", eventName))
	}
}

// RegisterConfig 註冊新的 Webhook 事件訂閱點。
func (s *webhookService) RegisterConfig(config *models.WebhookConfig) error {
	if config.ID == "" {
		config.ID = uuid.New().String()
	}
	if config.CreatedAt.IsZero() {
		config.CreatedAt = time.Now()
	}
	return s.db.Create(config).Error
}

// GetWebhookLogs 查詢歷次 Webhook 派送執行紀錄與 HTTP 回應碼。
func (s *webhookService) GetWebhookLogs() ([]models.WebhookLog, error) {
	var logs []models.WebhookLog
	err := s.db.Order("created_at DESC").Limit(100).Find(&logs).Error
	return logs, err
}

// workerLoop 是 Goroutine 背景 Worker 的常駐執行迴圈。
func (s *webhookService) workerLoop() {
	for event := range s.eventChan {
		var configs []models.WebhookConfig
		if err := s.db.Where("event = ? AND is_active = ?", event.EventName, true).Find(&configs).Error; err != nil {
			continue
		}

		bodyBytes, err := json.Marshal(event.Payload)
		if err != nil {
			continue
		}

		for _, cfg := range configs {
			s.sendWebhook(cfg, event.EventName, bodyBytes)
		}
	}
}

// sendWebhook 執行對外部 Target URL 的 HTTP POST 發送並記錄執行日誌。
func (s *webhookService) sendWebhook(cfg models.WebhookConfig, eventName string, payload []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", cfg.TargetURL, bytes.NewBuffer(payload))
	if err != nil {
		s.logWebhookResult(cfg.ID, eventName, 0, err.Error(), false)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	if cfg.SecretToken != "" {
		req.Header.Set("X-Tao-Signature", cfg.SecretToken)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		s.logWebhookResult(cfg.ID, eventName, 0, err.Error(), false)
		return
	}
	defer resp.Body.Close()

	isSuccess := resp.StatusCode >= 200 && resp.StatusCode < 300
	s.logWebhookResult(cfg.ID, eventName, resp.StatusCode, resp.Status, isSuccess)
}

// logWebhookResult 記錄 Webhook 發送日誌至資料庫。
func (s *webhookService) logWebhookResult(configID, eventName string, statusCode int, responseBody string, isSuccess bool) {
	logEntry := models.WebhookLog{
		ID:           uuid.New().String(),
		ConfigID:     configID,
		Event:        eventName,
		StatusCode:   statusCode,
		ResponseBody: responseBody,
		IsSuccess:    isSuccess,
		CreatedAt:    time.Now(),
	}
	s.db.Create(&logEntry)
}
