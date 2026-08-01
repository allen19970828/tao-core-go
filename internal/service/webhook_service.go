package service

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"tao-core-go/internal/domain/models"
	"tao-core-go/internal/security"
)

type WebhookEvent struct {
	EventName string      `json:"event_name"`
	Payload   interface{} `json:"payload"`
}

type WebhookService interface {
	Dispatch(eventName string, payload interface{})
	RegisterConfig(config *models.WebhookConfig) (string, error)
	GetWebhookLogs() ([]models.WebhookLog, error)
}

type webhookService struct {
	db         *gorm.DB
	logger     *zap.Logger
	eventChan  chan WebhookEvent
	httpClient *http.Client
	policy     *security.OutboundPolicy
	cipher     *security.SecretCipher
}

func NewWebhookService(db *gorm.DB, logger *zap.Logger, workerPoolSize int, cipher *security.SecretCipher, allowedHosts []string) (WebhookService, error) {
	if workerPoolSize <= 0 || workerPoolSize > 100 {
		workerPoolSize = 5
	}
	policy, err := security.NewOutboundPolicy(allowedHosts)
	if err != nil {
		return nil, fmt.Errorf("webhook 外連政策無效: %w", err)
	}
	s := &webhookService{
		db: db, logger: logger, eventChan: make(chan WebhookEvent, 100),
		httpClient: policy.HTTPClient(10 * time.Second), policy: policy, cipher: cipher,
	}
	for i := 0; i < workerPoolSize; i++ {
		go s.workerLoop()
	}
	return s, nil
}

func (s *webhookService) Dispatch(eventName string, payload interface{}) {
	select {
	case s.eventChan <- WebhookEvent{EventName: eventName, Payload: payload}:
	default:
		s.logger.Warn("Webhook 事件佇列已滿，丟棄事件", zap.String("event", eventName))
	}
}

func (s *webhookService) RegisterConfig(config *models.WebhookConfig) (string, error) {
	if config.Event != "session.completed" {
		return "", errors.New("不支援的 webhook event")
	}
	validated, err := s.policy.ValidateURL(config.TargetURL)
	if err != nil {
		return "", err
	}
	config.TargetURL = validated.String()
	config.HTTPMethod = http.MethodPost
	if config.ID == "" {
		config.ID = uuid.NewString()
	}
	config.CreatedAt = time.Now()
	secret := config.SecretToken
	if secret == "" {
		secretBytes := make([]byte, 32)
		if _, err := rand.Read(secretBytes); err != nil {
			return "", err
		}
		secret = hex.EncodeToString(secretBytes)
	}
	if len(secret) < 32 {
		return "", errors.New("webhook signing secret 至少需要 32 bytes")
	}
	encrypted, err := s.cipher.Encrypt(secret)
	if err != nil {
		return "", err
	}
	config.SecretToken = encrypted
	if err := s.db.Create(config).Error; err != nil {
		return "", err
	}
	return secret, nil
}

func (s *webhookService) GetWebhookLogs() ([]models.WebhookLog, error) {
	var logs []models.WebhookLog
	err := s.db.Order("created_at DESC").Limit(100).Find(&logs).Error
	return logs, err
}

func (s *webhookService) workerLoop() {
	for event := range s.eventChan {
		var configs []models.WebhookConfig
		if err := s.db.Where("event = ? AND is_active = ?", event.EventName, true).Find(&configs).Error; err != nil {
			continue
		}
		body, err := json.Marshal(event)
		if err != nil {
			continue
		}
		for _, config := range configs {
			s.sendWebhook(config, event.EventName, body)
		}
	}
}

func (s *webhookService) sendWebhook(config models.WebhookConfig, eventName string, payload []byte) {
	validated, err := s.policy.ValidateURL(config.TargetURL)
	if err != nil {
		s.logWebhookResult(config.ID, eventName, 0, err.Error(), false)
		return
	}
	secret, err := s.cipher.Decrypt(config.SecretToken)
	if err != nil {
		s.logWebhookResult(config.ID, eventName, 0, err.Error(), false)
		return
	}
	timestamp := fmt.Sprintf("%d", time.Now().Unix())
	deliveryID := uuid.NewString()
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp + "." + deliveryID + "." + string(payload)))
	signature := "v1=" + hex.EncodeToString(mac.Sum(nil))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, validated.String(), bytes.NewReader(payload))
	if err != nil {
		s.logWebhookResult(config.ID, eventName, 0, err.Error(), false)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tao-Delivery", deliveryID)
	req.Header.Set("X-Tao-Timestamp", timestamp)
	req.Header.Set("X-Tao-Signature", signature)

	response, err := s.httpClient.Do(req)
	if err != nil {
		s.logWebhookResult(config.ID, eventName, 0, err.Error(), false)
		return
	}
	defer response.Body.Close()
	responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	isSuccess := response.StatusCode >= 200 && response.StatusCode < 300
	s.logWebhookResult(config.ID, eventName, response.StatusCode, strings.TrimSpace(string(responseBody)), isSuccess)
}

func (s *webhookService) logWebhookResult(configID, eventName string, statusCode int, responseBody string, isSuccess bool) {
	if len(responseBody) > 4096 {
		responseBody = responseBody[:4096]
	}
	entry := models.WebhookLog{
		ID: uuid.NewString(), ConfigID: configID, Event: eventName, StatusCode: statusCode,
		ResponseBody: responseBody, IsSuccess: isSuccess, CreatedAt: time.Now(),
	}
	if err := s.db.Create(&entry).Error; err != nil {
		s.logger.Warn("Webhook log 寫入失敗", zap.Error(err))
	}
}
