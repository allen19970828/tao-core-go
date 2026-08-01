package service

import (
	"encoding/csv"
	"fmt"
	"io"
	"strings"

	"gorm.io/gorm"

	"tao-core-go/internal/domain/models"
)

// ResultsExportService 提供將測驗成績與 Raw Data 答題紀錄匯出為 CSV 串流的介面。
type ResultsExportService interface {
	ExportDeliveryResultsCSV(deliveryID string, writer io.Writer) error
}

type resultsExportService struct {
	db             *gorm.DB
	proctorService ProctorService
}

// NewResultsExportService 建立並回傳 ResultsExportService 實體。
func NewResultsExportService(db *gorm.DB, proctorService ProctorService) ResultsExportService {
	return &resultsExportService{
		db:             db,
		proctorService: proctorService,
	}
}

// ExportDeliveryResultsCSV 將指定 Delivery 測驗發布場次的所有學生會話、總分、作答耗時、監考風險等級 (RiskLevel) 匯出為標準 CSV 串流。
func (s *resultsExportService) ExportDeliveryResultsCSV(deliveryID string, writer io.Writer) error {
	var sessions []models.TestSession
	if err := s.db.Preload("Responses").Where("delivery_id = ?", deliveryID).Find(&sessions).Error; err != nil {
		return err
	}

	csvWriter := csv.NewWriter(writer)
	defer csvWriter.Flush()

	// 寫入 CSV 標題列 (Header)
	header := []string{
		"SessionID",
		"UserID",
		"Status",
		"TotalScore",
		"TimeSpentSeconds",
		"RiskLevel",
		"TotalTabSwitches",
		"TotalResponsesCount",
		"StartedAt",
		"FinishedAt",
	}
	if err := csvWriter.Write(header); err != nil {
		return err
	}

	// 寫入資料列 (Data Rows)
	for _, sess := range sessions {
		analytics, _ := s.proctorService.GetProctorAnalytics(sess.ID)
		riskLevel := "LOW"
		tabSwitches := "0"
		if analytics != nil {
			riskLevel = analytics.RiskLevel
			tabSwitches = fmt.Sprintf("%d", analytics.TotalTabSwitches)
		}

		startedAtStr := ""
		if sess.StartedAt != nil {
			startedAtStr = sess.StartedAt.Format("2006-01-02 15:04:05")
		}

		finishedAtStr := ""
		if sess.FinishedAt != nil {
			finishedAtStr = sess.FinishedAt.Format("2006-01-02 15:04:05")
		}

		row := []string{
			sanitizeCSVCell(sess.ID),
			sanitizeCSVCell(sess.UserID),
			string(sess.Status),
			fmt.Sprintf("%.2f", sess.TotalScore),
			fmt.Sprintf("%d", sess.TimeSpentSeconds),
			sanitizeCSVCell(riskLevel),
			tabSwitches,
			fmt.Sprintf("%d", len(sess.Responses)),
			startedAtStr,
			finishedAtStr,
		}
		if err := csvWriter.Write(row); err != nil {
			return err
		}
	}

	return nil
}

// sanitizeCSVCell prevents spreadsheet programs from interpreting exported,
// user-controlled values as formulas when an administrator opens the CSV.
func sanitizeCSVCell(value string) string {
	trimmed := strings.TrimLeft(value, " \t\r\n")
	if trimmed == "" {
		return value
	}
	switch trimmed[0] {
	case '=', '+', '-', '@':
		return "'" + value
	default:
		return value
	}
}
