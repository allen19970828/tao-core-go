package service

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"tao-core-go/internal/domain/models"
)

const (
	MaxQTIPackageSize      int64 = 50 << 20
	maxQTIEntries                = 1000
	maxQTIExpandedSize     int64 = 200 << 20
	maxQTIFileSize         int64 = 20 << 20
	maxQTIXMLSize          int64 = 5 << 20
	maxQTICompressionRatio       = 100
)

// QTIService 提供 QTI 3.0 試題包 (.zip) 解構、圖片媒體提取與資料庫匯入介面。
type QTIService interface {
	ImportQTIPackage(zipFilePath string, mediaTargetDir string) ([]*models.Item, error)
}

type qtiService struct {
	db             *gorm.DB
	scoringService ScoringService
}

// NewQTIService 建立並回傳 QTIService 實體。
func NewQTIService(db *gorm.DB, scoringService ScoringService) QTIService {
	return &qtiService{
		db:             db,
		scoringService: scoringService,
	}
}

// QTI 3.0 XML 標籤反序列化映射結構體
type QTIAssessmentItem struct {
	XMLName             xml.Name               `xml:"qti-assessment-item"`
	Identifier          string                 `xml:"identifier,attr"`
	Title               string                 `xml:"title,attr"`
	ResponseDeclaration QTIResponseDeclaration `xml:"qti-response-declaration"`
	OutcomeDeclaration  QTIOutcomeDeclaration  `xml:"qti-outcome-declaration"`
	ItemBody            QTIItemBody            `xml:"qti-item-body"`
}

type QTIResponseDeclaration struct {
	Identifier      string   `xml:"identifier,attr"`
	Cardinality     string   `xml:"cardinality,attr"`
	BaseType        string   `xml:"baseType,attr"`
	CorrectResponse []string `xml:"qti-correct-response>qti-value"`
}

type QTIOutcomeDeclaration struct {
	Identifier   string  `xml:"identifier,attr"`
	Cardinality  string  `xml:"cardinality,attr"`
	BaseType     string  `xml:"baseType,attr"`
	DefaultValue float64 `xml:"qti-default-value>qti-value"`
}

type QTIItemBody struct {
	ChoiceInteraction QTIChoiceInteraction `xml:"qti-choice-interaction"`
}

type QTIChoiceInteraction struct {
	ResponseIdentifier string            `xml:"response-identifier,attr"`
	Shuffle            bool              `xml:"shuffle,attr"`
	MaxChoices         int               `xml:"max-choices,attr"`
	Prompt             string            `xml:"qti-prompt"`
	SimpleChoices      []QTISimpleChoice `xml:"qti-simple-choice"`
}

type QTISimpleChoice struct {
	Identifier string `xml:"identifier,attr"`
	Text       string `xml:",chardata"`
}

// ImportQTIPackage 讀取並解壓 QTI 3.0 .zip 試題包：
// 1. 遍歷 ZIP 內的媒體檔案 (PNG, JPG, MP4)，解壓寫入多媒體目錄 (/uploads/media/)
// 2. 遍歷 XML 試題檔，解析 qti-assessment-item 與 qti-choice-interaction
// 3. 自動替代題目內容中的相對圖片路徑為伺服器 URL
// 4. 寫入 DB 並傳回成功匯入的題目實體陣列
func (s *qtiService) ImportQTIPackage(zipFilePath string, mediaTargetDir string) ([]*models.Item, error) {
	info, err := os.Stat(zipFilePath)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > MaxQTIPackageSize {
		return nil, errors.New("QTI ZIP 檔案大小或類型無效")
	}
	r, err := zip.OpenReader(zipFilePath)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	if len(r.File) > maxQTIEntries {
		return nil, errors.New("QTI ZIP entry 數量超過限制")
	}
	var totalExpanded uint64
	for _, file := range r.File {
		totalExpanded += file.UncompressedSize64
		if file.UncompressedSize64 > uint64(maxQTIFileSize) || totalExpanded > uint64(maxQTIExpandedSize) {
			return nil, errors.New("QTI ZIP 解壓大小超過限制")
		}
		if file.CompressedSize64 > 0 && file.UncompressedSize64/file.CompressedSize64 > maxQTICompressionRatio {
			return nil, errors.New("QTI ZIP 壓縮比例異常")
		}
	}

	if err := os.MkdirAll(mediaTargetDir, 0750); err != nil {
		return nil, err
	}

	mediaMap := make(map[string]string)
	var xmlFiles []*zip.File
	var createdMedia []string
	committed := false
	defer func() {
		if !committed {
			for _, path := range createdMedia {
				_ = os.Remove(path)
			}
		}
	}()

	// 1. 第一趟掃描：提取圖片與影音多媒體檔案
	for _, f := range r.File {
		ext := strings.ToLower(filepath.Ext(f.Name))
		if ext == ".svg" {
			return nil, errors.New("基於同源腳本風險，QTI 套件不允許 SVG")
		}
		if ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".gif" || ext == ".mp3" || ext == ".mp4" {
			mediaURL, diskPath, err := s.saveMediaFile(f, mediaTargetDir)
			if err != nil {
				return nil, err
			}
			createdMedia = append(createdMedia, diskPath)
			mediaMap[f.Name] = mediaURL
			mediaMap[filepath.Base(f.Name)] = mediaURL
		} else if ext == ".xml" {
			if f.UncompressedSize64 > uint64(maxQTIXMLSize) {
				return nil, errors.New("QTI XML 大小超過限制")
			}
			xmlFiles = append(xmlFiles, f)
		}
	}

	var importedItems []*models.Item

	tx := s.db.Begin()
	if tx.Error != nil {
		return nil, tx.Error
	}
	defer tx.Rollback()

	// 2. 第二趟掃描：解構 QTI 3.0 XML 試題並以交易寫入資料庫
	for _, xmlFile := range xmlFiles {
		item, err := s.parseQTIXMLFile(xmlFile, mediaMap)
		if err != nil {
			continue // 跳過非 QTI 試題 XML 檔 (例如 manifest)
		}

		if item != nil {
			if err := tx.Create(item).Error; err != nil {
				return nil, err
			}
			importedItems = append(importedItems, item)
		}
	}

	if len(importedItems) == 0 {
		return nil, errors.New("無法從 ZIP 試題包中解構任何有效的 QTI 3.0 題目")
	}
	if err := tx.Commit().Error; err != nil {
		return nil, err
	}
	committed = true

	return importedItems, nil
}

// saveMediaFile 將 ZIP 內的多媒體檔案寫入硬碟並傳回 HTTP 存取 URL。
func (s *qtiService) saveMediaFile(file *zip.File, targetDir string) (string, string, error) {
	rc, err := file.Open()
	if err != nil {
		return "", "", err
	}
	defer rc.Close()

	ext := filepath.Ext(file.Name)
	newFileName := uuid.New().String() + ext
	targetRoot, err := filepath.Abs(targetDir)
	if err != nil {
		return "", "", err
	}
	root, err := os.OpenRoot(targetRoot)
	if err != nil {
		return "", "", err
	}
	defer root.Close()

	outFile, err := root.OpenFile(newFileName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", "", err
	}
	targetPath := filepath.Join(targetRoot, newFileName)
	cleanup := true
	defer func() {
		_ = outFile.Close()
		if cleanup {
			_ = root.Remove(newFileName)
		}
	}()

	buffer := make([]byte, 512)
	n, readErr := io.ReadFull(rc, buffer)
	if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		return "", "", readErr
	}
	if err := validateMediaType(ext, http.DetectContentType(buffer[:n])); err != nil {
		return "", "", err
	}
	written, err := io.Copy(outFile, io.MultiReader(bytes.NewReader(buffer[:n]), io.LimitReader(rc, maxQTIFileSize-int64(n)+1)))
	if err != nil {
		return "", "", err
	}
	if written > maxQTIFileSize {
		return "", "", errors.New("QTI 媒體檔案超過大小限制")
	}
	if err := outFile.Close(); err != nil {
		return "", "", err
	}
	cleanup = false
	return "/uploads/media/" + newFileName, targetPath, nil
}

// parseQTIXMLFile 解析單一 QTI 3.0 XML 試題檔案。
func (s *qtiService) parseQTIXMLFile(file *zip.File, mediaMap map[string]string) (*models.Item, error) {
	rc, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	buf := new(bytes.Buffer)
	written, err := io.Copy(buf, io.LimitReader(rc, maxQTIXMLSize+1))
	if err != nil {
		return nil, err
	}
	if written > maxQTIXMLSize {
		return nil, errors.New("QTI XML 大小超過限制")
	}

	var qtiItem QTIAssessmentItem
	if err := xml.Unmarshal(buf.Bytes(), &qtiItem); err != nil {
		return nil, err
	}

	if qtiItem.Identifier == "" && qtiItem.ItemBody.ChoiceInteraction.ResponseIdentifier == "" {
		return nil, errors.New("非有效 QTI 3.0 試題 XML")
	}
	if len(qtiItem.Identifier) > 36 || len(qtiItem.Title) > 255 || len(qtiItem.ItemBody.ChoiceInteraction.Prompt) > 1<<20 {
		return nil, errors.New("QTI 試題識別碼、標題或題幹超過長度限制")
	}

	// 提取題幹描述
	prompt := qtiItem.ItemBody.ChoiceInteraction.Prompt
	if prompt == "" {
		prompt = qtiItem.Title
	}

	// 自動取代多媒體圖片路徑為伺服器 URL
	for originalPath, serverURL := range mediaMap {
		prompt = strings.ReplaceAll(prompt, originalPath, serverURL)
	}

	// 提取正確答案
	correctAnswer := ""
	if len(qtiItem.ResponseDeclaration.CorrectResponse) > 0 {
		correctAnswer = strings.Join(qtiItem.ResponseDeclaration.CorrectResponse, ",")
	}

	// 解析選項
	var options []models.Option
	for _, choice := range qtiItem.ItemBody.ChoiceInteraction.SimpleChoices {
		if len(choice.Identifier) > 64 || len(choice.Text) > 4096 {
			return nil, errors.New("QTI 選項識別碼或內容超過長度限制")
		}
		options = append(options, models.Option{
			Identifier: choice.Identifier,
			Text:       strings.TrimSpace(choice.Text),
		})
	}

	// 洗牌後同步重映射標準答案，避免選項內容與答案代號失去對應。
	options, identifierMap, err := s.scoringService.BalanceOptionsKey(options)
	if err != nil {
		return nil, err
	}
	if correctAnswer != "" {
		answerParts := strings.Split(correctAnswer, ",")
		for i, original := range answerParts {
			mapped, ok := identifierMap[strings.TrimSpace(original)]
			if !ok {
				return nil, errors.New("QTI 標準答案未對應到有效選項")
			}
			answerParts[i] = mapped
		}
		correctAnswer = strings.Join(answerParts, ",")
		if len(correctAnswer) > 255 {
			return nil, errors.New("QTI 標準答案超過長度限制")
		}
	}

	optionsJSON, _ := json.Marshal(options)

	maxScore := qtiItem.OutcomeDeclaration.DefaultValue
	if maxScore == 0 {
		maxScore = 1.0
	}
	if maxScore < 0 || maxScore > 1_000_000 || math.IsNaN(maxScore) || math.IsInf(maxScore, 0) {
		return nil, errors.New("QTI 題目配分無效")
	}

	itemType := models.ItemTypeSingleChoice
	if qtiItem.ItemBody.ChoiceInteraction.MaxChoices > 1 {
		itemType = models.ItemTypeMultipleChoice
	}

	itemID := qtiItem.Identifier
	if itemID == "" {
		itemID = uuid.New().String()
	}

	item := &models.Item{
		ID:            itemID,
		Title:         qtiItem.Title,
		Prompt:        prompt,
		ItemType:      itemType,
		OptionsJSON:   string(optionsJSON),
		CorrectAnswer: correctAnswer,
		MaxScore:      maxScore,
	}

	return item, nil
}

func validateMediaType(ext, contentType string) error {
	allowed := map[string][]string{
		".png":  {"image/png"},
		".jpg":  {"image/jpeg"},
		".jpeg": {"image/jpeg"},
		".gif":  {"image/gif"},
		".mp3":  {"audio/mpeg", "application/octet-stream"},
		".mp4":  {"video/mp4", "application/octet-stream"},
	}
	for _, expected := range allowed[strings.ToLower(ext)] {
		if contentType == expected {
			return nil
		}
	}
	return fmt.Errorf("媒體內容類型 %s 與副檔名 %s 不符", contentType, ext)
}
