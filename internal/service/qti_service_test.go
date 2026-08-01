package service

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tao-core-go/internal/domain/models"
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
	imgFile.Write([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0, 'I', 'H', 'D', 'R'})

	zipWriter.Close()

	// Temp upload dir and temp zip file
	tempRoot := t.TempDir()
	tempDir := filepath.Join(tempRoot, "uploads")
	tempZipFile := filepath.Join(tempRoot, "test_qti_package.zip")

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
	var options []models.Option
	if err := json.Unmarshal([]byte(item.OptionsJSON), &options); err != nil {
		t.Fatalf("decode shuffled options: %v", err)
	}
	correctText := ""
	for _, option := range options {
		if option.Identifier == item.CorrectAnswer {
			correctText = option.Text
		}
	}
	if correctText != "25 平方公分" {
		t.Fatalf("shuffle corrupted the correct answer mapping: answer=%q text=%q options=%s", item.CorrectAnswer, correctText, item.OptionsJSON)
	}
}

func TestImportQTIPackageRejectsSVGAndCompressionBomb(t *testing.T) {
	for name, createEntry := range map[string]func(*testing.T, *zip.Writer){
		"SVG": func(t *testing.T, writer *zip.Writer) {
			entry, err := writer.Create("media/payload.svg")
			if err != nil {
				t.Fatalf("create SVG: %v", err)
			}
			_, _ = entry.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`))
		},
		"compression bomb": func(t *testing.T, writer *zip.Writer) {
			entry, err := writer.Create("items/bomb.xml")
			if err != nil {
				t.Fatalf("create compressed entry: %v", err)
			}
			_, _ = entry.Write([]byte(strings.Repeat("A", 1<<20)))
		},
	} {
		t.Run(name, func(t *testing.T) {
			buffer := new(bytes.Buffer)
			writer := zip.NewWriter(buffer)
			createEntry(t, writer)
			if err := writer.Close(); err != nil {
				t.Fatalf("close ZIP: %v", err)
			}
			zipPath := filepath.Join(t.TempDir(), "package.zip")
			if err := os.WriteFile(zipPath, buffer.Bytes(), 0600); err != nil {
				t.Fatalf("write ZIP: %v", err)
			}
			if _, err := NewQTIService(setupTestDB(t), NewScoringService()).ImportQTIPackage(zipPath, filepath.Join(t.TempDir(), "media")); err == nil {
				t.Fatal("expected dangerous QTI package to be rejected")
			}
		})
	}
}

func TestImportQTIPackageRemovesMediaWhenDatabaseImportFails(t *testing.T) {
	buffer := new(bytes.Buffer)
	writer := zip.NewWriter(buffer)
	image, err := writer.Create("media/image.png")
	if err != nil {
		t.Fatalf("create image: %v", err)
	}
	_, _ = image.Write([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0, 0, 0, 0, 'I', 'H', 'D', 'R'})
	invalidXML, err := writer.Create("items/invalid.xml")
	if err != nil {
		t.Fatalf("create XML: %v", err)
	}
	_, _ = invalidXML.Write([]byte(`<manifest/>`))
	if err := writer.Close(); err != nil {
		t.Fatalf("close ZIP: %v", err)
	}
	root := t.TempDir()
	zipPath, mediaDir := filepath.Join(root, "package.zip"), filepath.Join(root, "media")
	if err := os.WriteFile(zipPath, buffer.Bytes(), 0600); err != nil {
		t.Fatalf("write ZIP: %v", err)
	}
	if _, err := NewQTIService(setupTestDB(t), NewScoringService()).ImportQTIPackage(zipPath, mediaDir); err == nil {
		t.Fatal("expected invalid package import to fail")
	}
	entries, err := os.ReadDir(mediaDir)
	if err != nil {
		t.Fatalf("read media directory: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected rolled-back media files to be removed, found %d", len(entries))
	}
}
