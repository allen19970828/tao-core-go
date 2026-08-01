package handler

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

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
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.MaxQTIPackageSize)
	fileHeader, err := c.FormFile("file")
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "QTI ZIP 超過 50 MiB 限制"})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": "缺少 multipart/form-data 'file' 上傳參數"})
		return
	}
	if fileHeader.Size <= 0 || fileHeader.Size > service.MaxQTIPackageSize || !strings.EqualFold(filepath.Ext(fileHeader.Filename), ".zip") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "只允許不超過 50 MiB 的 ZIP 檔案"})
		return
	}

	source, err := fileHeader.Open()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "無法讀取上傳檔案"})
		return
	}
	defer source.Close()
	tempFile, err := os.CreateTemp("", "tao-qti-*.zip")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "無法暫存上傳的 ZIP 檔案"})
		return
	}
	tempZipPath := tempFile.Name()
	defer os.Remove(tempZipPath)
	written, copyErr := io.Copy(tempFile, io.LimitReader(source, service.MaxQTIPackageSize+1))
	closeErr := tempFile.Close()
	if copyErr != nil || closeErr != nil || written > service.MaxQTIPackageSize {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "QTI ZIP 超過大小限制或寫入失敗"})
		return
	}

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
