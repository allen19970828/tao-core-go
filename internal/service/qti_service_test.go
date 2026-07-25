package service

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestImportQTIPackage(t *testing.T) {
	db := setupTestDB(t)

	scoringSvc := NewScoringService()
	qtiSvc := NewQTIService(db, scoringSvc)

	// Create test ZIP in memory
	buf := new(bytes.Buffer)
	zipWriter := zip.NewWriter(buf)

	// Add sample QTI 3.0 XML
	xmlContent := `<?xml version="1.0" encoding="UTF-8"?>
<qti-assessment-item xmlns="http://www.imsglobal.org/xsd/imsqti_v3p0" identifier="item-qti-01" title="幾何面積試題">
    <qti-response-declaration identifier="RESPONSE" cardinality="single" base-type="identifier">
        <qti-correct-response>
            <qti-value>ChoiceB</qti-value>
        </qti-correct-response>
    </qti-response-declaration>
    <qti-outcome-declaration identifier="SCORE" cardinality="single" base-type="float">
        <qti-default-value><qti-value>10.0</qti-value></qti-default-value>
    </qti-outcome-declaration>
    <qti-item-body>
        <qti-choice-interaction response-identifier="RESPONSE" shuffle="true" max-choices="1">
            <qti-prompt>請計算下圖正方形的面積 (參見 diagram.png)：</qti-prompt>
            <qti-simple-choice identifier="ChoiceA">16 平方公分</qti-simple-choice>
            <qti-simple-choice identifier="ChoiceB">25 平方公分</qti-simple-choice>
            <qti-simple-choice identifier="ChoiceC">36 平方公分</qti-simple-choice>
            <qti-simple-choice identifier="ChoiceD">49 平方公分</qti-simple-choice>
        </qti-choice-interaction>
    </qti-item-body>
</qti-assessment-item>`

	xmlFile, err := zipWriter.Create("items/question_01.xml")
	if err != nil {
		t.Fatalf("Failed to create xml file in zip: %v", err)
	}
	xmlFile.Write([]byte(xmlContent))

	// Add sample media file
	imgFile, err := zipWriter.Create("media/diagram.png")
	if err != nil {
		t.Fatalf("Failed to create image file in zip: %v", err)
	}
	imgFile.Write([]byte("fake png image bytes"))

	zipWriter.Close()

	// Temp upload dir and temp zip file
	tempDir := filepath.Join(os.TempDir(), "tao_qti_test_uploads")
	tempZipFile := filepath.Join(os.TempDir(), "test_qti_package.zip")
	defer os.RemoveAll(tempDir)
	defer os.Remove(tempZipFile)

	if err := os.WriteFile(tempZipFile, buf.Bytes(), 0644); err != nil {
		t.Fatalf("Failed to write temp zip file: %v", err)
	}

	importedItems, err := qtiSvc.ImportQTIPackage(tempZipFile, tempDir)
	if err != nil {
		t.Fatalf("ImportQTIPackage failed: %v", err)
	}

	if len(importedItems) != 1 {
		t.Fatalf("Expected 1 imported item, got %d", len(importedItems))
	}

	item := importedItems[0]
	if item.Title != "幾何面積試題" {
		t.Errorf("Expected title '幾何面積試題', got '%s'", item.Title)
	}
}
