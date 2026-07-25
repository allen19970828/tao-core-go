package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"tao-core-go/internal/domain/models"
	"tao-core-go/internal/service"
)

// ProctorHandler 處理防作弊監考事件與數據分析報告的 HTTP 控制器。
type ProctorHandler struct {
	proctorService service.ProctorService
}

// NewProctorHandler 建立並回傳 ProctorHandler 實體。
func NewProctorHandler(proctorService service.ProctorService) *ProctorHandler {
	return &ProctorHandler{
		proctorService: proctorService,
	}
}

// RecordProctorEventRequest 定義上報監考違規事件的請求體結構。
type RecordProctorEventRequest struct {
	EventType       models.ProctorEventType `json:"event_type" binding:"required"`
	DurationSeconds int                     `json:"duration_seconds"`
	Details         string                  `json:"details"`
}

// RecordEvent 處理 POST /api/v1/sessions/:id/proctor/event
// 上報切頁、跳出視窗、複製文字或 Web 端黑屏防截圖觸發事件。
func (h *ProctorHandler) RecordEvent(c *gin.Context) {
	sessionID := c.Param("id")

	var req RecordProctorEventRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	event := &models.ProctorEvent{
		SessionID:       sessionID,
		EventType:       req.EventType,
		DurationSeconds: req.DurationSeconds,
		Details:         req.Details,
	}

	if err := h.proctorService.RecordEvent(event); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, event)
}

// GetProctorLog 處理 GET /api/v1/sessions/:id/proctor/log
// 查詢特定 SessionID 的監考歷史事件串。
func (h *ProctorHandler) GetProctorLog(c *gin.Context) {
	sessionID := c.Param("id")

	events, err := h.proctorService.GetSessionProctorLog(sessionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"session_id": sessionID,
		"total_logs": len(events),
		"events":     events,
	})
}

// GetProctorAnalytics 處理 GET /api/v1/sessions/:id/proctor/analytics
// 取得指定測驗會話的作弊風險等級評估報告 (LOW / MEDIUM / HIGH)。
func (h *ProctorHandler) GetProctorAnalytics(c *gin.Context) {
	sessionID := c.Param("id")

	summary, err := h.proctorService.GetProctorAnalytics(sessionID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, summary)
}
