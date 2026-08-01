package service

import (
	"bytes"
	"strings"
	"testing"

	"tao-core-go/internal/domain/models"
)

func TestExportDeliveryResultsCSV(t *testing.T) {
	db := setupTestDB(t)
	proctorSvc := NewProctorService(db)
	exportSvc := NewResultsExportService(db, proctorSvc)

	deliveryID := "delivery-csv-test-01"

	// Create test session
	sess := models.TestSession{
		ID:               "session-csv-01",
		DeliveryID:       deliveryID,
		UserID:           "=HYPERLINK(\"https://evil.example\",\"student\")",
		Status:           models.SessionStatusCompleted,
		TotalScore:       95.5,
		TimeSpentSeconds: 120,
	}
	db.Create(&sess)

	buf := new(bytes.Buffer)
	err := exportSvc.ExportDeliveryResultsCSV(deliveryID, buf)
	if err != nil {
		t.Fatalf("ExportDeliveryResultsCSV failed: %v", err)
	}

	csvOutput := buf.String()
	if !strings.Contains(csvOutput, "SessionID,UserID,Status,TotalScore") {
		t.Errorf("CSV header missing")
	}
	if !strings.Contains(csvOutput, "session-csv-01") || !strings.Contains(csvOutput, "95.50") {
		t.Errorf("CSV data row missing session details")
	}
	if strings.Contains(csvOutput, ",=HYPERLINK") || !strings.Contains(csvOutput, "'=HYPERLINK") {
		t.Errorf("CSV formula value was not neutralized: %q", csvOutput)
	}
}
