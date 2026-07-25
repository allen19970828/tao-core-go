package handler

import (
	"net/http"
	"os"
	"path/filepath"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"tao-core-go/internal/service"
)

// QTIHandler 處理 QTI 3.0 試題包匯入的 HTTP 請求。
type QTIHandler struct {
	qtiService service.QTIService
	uploadDir  string
}

// NewQTIHandler 建立並回傳 QTIHandler 實體。
func NewQTIHandler(qtiService service.QTIService, uploadDir string) *QTIHandler {
	return &QTIHandler{
		qtiService: qtiService,
		uploadDir:  uploadDir,
	}
}

// ImportQTIPackage 處理 POST /api/v1/items/import-qti
// 接收 multipart/form-data 上傳的 QTI 3.0 .zip 試題包檔：
// 1. 暫存上傳的 ZIP 檔
// 2. 呼叫 QTIService 解壓、解析 XML 與儲存多媒體圖片
// 3. 回傳匯入成功的題目列表
func (h *QTIHandler) ImportQTIPackage(c *gin.Context) {
	fileHeader, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 multipart/form-data 'file' 上傳參數"})
		return
	}

	// 暫存上傳的 ZIP 檔案
	tempZipPath := filepath.Join(os.TempDir(), uuid.New().String()+"_qti.zip")
	if err := c.SaveUploadedFile(fileHeader, tempZipPath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "無法暫存上傳的 ZIP 檔案"})
		return
	}
	defer os.Remove(tempZipPath)

	importedItems, err := h.qtiService.ImportQTIPackage(tempZipPath, h.uploadDir)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"message":        "QTI 3.0 試題包匯入成功！",
		"imported_count": len(importedItems),
		"items":          importedItems,
	})
}
