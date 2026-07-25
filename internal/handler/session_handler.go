package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"tao-core-go/internal/domain/models"
	"tao-core-go/internal/service"
)

// SessionHandler 處理學生應試測驗會話 (TestSession) 相關的 HTTP 請求。
type SessionHandler struct {
	sessionService service.SessionService
	webhookService service.WebhookService
}

// NewSessionHandler 建立並回傳 SessionHandler 控制器實體。
func NewSessionHandler(sessionService service.SessionService, webhookService service.WebhookService) *SessionHandler {
	return &SessionHandler{
		sessionService: sessionService,
		webhookService: webhookService,
	}
}

// StartSessionRequest 定義開啟測驗會話的請求體結構。
type StartSessionRequest struct {
	DeliveryID string `json:"delivery_id" binding:"required"`
	UserID     string `json:"user_id" binding:"required"`
}

// StartSession 處理 POST /api/v1/sessions/start
// 學生開始測驗會話：查詢或建立會話，並將狀態切換為 IN_PROGRESS。
func (h *SessionHandler) StartSession(c *gin.Context) {
	var req StartSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	session, err := h.sessionService.StartSession(req.DeliveryID, req.UserID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, session)
}

// SaveResponseRequest 定義暫存單題答案的請求體結構。
type SaveResponseRequest struct {
	ItemID       string `json:"item_id" binding:"required"`
	ResponseData string `json:"response_data" binding:"required"`
}

// SaveResponse 處理 POST /api/v1/sessions/:id/response
// 學生作答過程中暫存答案：寫入或更新 ItemResponse。
func (h *SessionHandler) SaveResponse(c *gin.Context) {
	sessionID := c.Param("id")

	var req SaveResponseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	resp, err := h.sessionService.SaveResponse(sessionID, req.ItemID, req.ResponseData)
	if err != nil {
		if err == service.ErrSessionCompleted {
			c.JSON(http.StatusForbidden, gin.H{"error": "測驗已經結束交卷，不可再修改答案"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, resp)
}

// SubmitSession 處理 POST /api/v1/sessions/:id/submit
// 學生終止交卷：觸發自動計分、計算總分、切換狀態為 COMPLETED，並異步觸發 Webhook 與 LTI 成績回傳。
func (h *SessionHandler) SubmitSession(c *gin.Context) {
	sessionID := c.Param("id")

	session, err := h.sessionService.SubmitSession(sessionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, session)
}

// GetSession 處理 GET /api/v1/sessions/:id
// 查詢指定測驗會話狀態與學生答題紀錄。
func (h *SessionHandler) GetSession(c *gin.Context) {
	sessionID := c.Param("id")

	session, err := h.sessionService.GetSession(sessionID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, session)
}

// RegisterWebhookRequest 定義註冊 Webhook 的請求體結構。
type RegisterWebhookRequest struct {
	Event string `json:"event" binding:"required"`
	URL   string `json:"url" binding:"required"`
}

// RegisterWebhook 處理 POST /api/v1/webhooks/configs
// 註冊異步 Webhook 事件通知。
func (h *SessionHandler) RegisterWebhook(c *gin.Context) {
	var req RegisterWebhookRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	cfg := &models.WebhookConfig{
		Event:     req.Event,
		TargetURL: req.URL,
		IsActive:  true,
	}

	if err := h.webhookService.RegisterConfig(cfg); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, cfg)
}

// GetWebhookLogs 處理 GET /api/v1/webhooks/logs
// 查詢 Webhook 發送執行日誌。
func (h *SessionHandler) GetWebhookLogs(c *gin.Context) {
	logs, err := h.webhookService.GetWebhookLogs()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, logs)
}
