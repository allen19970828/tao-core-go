package service

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"tao-core-go/internal/domain/models"
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
	r, err := zip.OpenReader(zipFilePath)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	if err := os.MkdirAll(mediaTargetDir, 0755); err != nil {
		return nil, err
	}

	mediaMap := make(map[string]string)
	var xmlFiles []*zip.File

	// 1. 第一趟掃描：提取圖片與影音多媒體檔案
	for _, f := range r.File {
		ext := strings.ToLower(filepath.Ext(f.Name))
		if ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".gif" || ext == ".svg" || ext == ".mp3" || ext == ".mp4" {
			mediaURL, err := s.saveMediaFile(f, mediaTargetDir)
			if err == nil {
				mediaMap[f.Name] = mediaURL
				mediaMap[filepath.Base(f.Name)] = mediaURL
			}
		} else if ext == ".xml" {
			xmlFiles = append(xmlFiles, f)
		}
	}

	var importedItems []*models.Item

	// 2. 第二趟掃描：解構 QTI 3.0 XML 試題並寫入資料庫
	for _, xmlFile := range xmlFiles {
		item, err := s.parseQTIXMLFile(xmlFile, mediaMap)
		if err != nil {
			continue // 跳過非 QTI 試題 XML 檔 (例如 manifest)
		}

		if item != nil {
			if err := s.db.Create(item).Error; err == nil {
				importedItems = append(importedItems, item)
			}
		}
	}

	if len(importedItems) == 0 {
		return nil, errors.New("無法從 ZIP 試題包中解構任何有效的 QTI 3.0 題目")
	}

	return importedItems, nil
}

// saveMediaFile 將 ZIP 內的多媒體檔案寫入硬碟並傳回 HTTP 存取 URL。
func (s *qtiService) saveMediaFile(file *zip.File, targetDir string) (string, error) {
	rc, err := file.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()

	ext := filepath.Ext(file.Name)
	newFileName := uuid.New().String() + ext
	targetPath := filepath.Join(targetDir, newFileName)

	outFile, err := os.Create(targetPath)
	if err != nil {
		return "", err
	}
	defer outFile.Close()

	if _, err := io.Copy(outFile, rc); err != nil {
		return "", err
	}

	return "/uploads/media/" + newFileName, nil
}

// parseQTIXMLFile 解析單一 QTI 3.0 XML 試題檔案。
func (s *qtiService) parseQTIXMLFile(file *zip.File, mediaMap map[string]string) (*models.Item, error) {
	rc, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	buf := new(bytes.Buffer)
	if _, err := io.Copy(buf, rc); err != nil {
		return nil, err
	}

	var qtiItem QTIAssessmentItem
	if err := xml.Unmarshal(buf.Bytes(), &qtiItem); err != nil {
		return nil, err
	}

	if qtiItem.Identifier == "" && qtiItem.ItemBody.ChoiceInteraction.ResponseIdentifier == "" {
		return nil, errors.New("非有效 QTI 3.0 試題 XML")
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
		options = append(options, models.Option{
			Identifier: choice.Identifier,
			Text:       strings.TrimSpace(choice.Text),
		})
	}

	// 執行選項 25% 洗牌與識別碼重排
	options = s.scoringService.BalanceOptionsKey(options)

	optionsJSON, _ := json.Marshal(options)

	maxScore := qtiItem.OutcomeDeclaration.DefaultValue
	if maxScore == 0 {
		maxScore = 1.0
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
