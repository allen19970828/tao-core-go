package handler

import (
	"fmt"
	"net/http"
	"strings"
	"unicode"

	"github.com/gin-gonic/gin"

	"tao-core-go/internal/service"
)

// ResultsHandler 處理成績與 Raw Data CSV 匯出的 HTTP 控制器。
type ResultsHandler struct {
	exportService service.ResultsExportService
}

// NewResultsHandler 建立並回傳 ResultsHandler 實體。
func NewResultsHandler(exportService service.ResultsExportService) *ResultsHandler {
	return &ResultsHandler{
		exportService: exportService,
	}
}

// ExportResultsCSV 處理 GET /api/v1/deliveries/:id/results/csv
// 將指定 Delivery 測驗發布場次的所有學生會話、總分、作答耗時、監考風險等級 (RiskLevel) 匯出為 CSV 檔案串流。
func (h *ResultsHandler) ExportResultsCSV(c *gin.Context) {
	deliveryID := c.Param("id")
	if deliveryID == "" || len(deliveryID) > 36 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "delivery id 無效"})
		return
	}

	filename := fmt.Sprintf("delivery_%s_results.csv", safeFilenamePart(deliveryID))
	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filename))

	if err := h.exportService.ExportDeliveryResultsCSV(deliveryID, c.Writer); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
}

func safeFilenamePart(value string) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' {
			return r
		}
		return '_'
	}, value)
	if value == "" {
		return "unknown"
	}
	return value
}
