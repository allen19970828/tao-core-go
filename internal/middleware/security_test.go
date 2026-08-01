package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestSecurityHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(SecurityHeaders())
	router.GET("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Header().Get("X-Content-Type-Options") != "nosniff" || response.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatalf("expected security headers, got %#v", response.Header())
	}
}

func TestJSONBodyLimitRejectsKnownAndChunkedOversizeBodies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(JSONBodyLimit(8))
	router.POST("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })

	for _, contentLength := range []int64{9, -1} {
		request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"value":1}`))
		request.Header.Set("Content-Type", "application/json")
		request.ContentLength = contentLength
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("content length %d: expected 413, got %d", contentLength, response.Code)
		}
	}
}

func TestJSONBodyLimitRestoresAcceptedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(JSONBodyLimit(32))
	router.POST("/", func(c *gin.Context) {
		var payload map[string]int
		if err := c.ShouldBindJSON(&payload); err != nil || payload["ok"] != 1 {
			c.Status(http.StatusBadRequest)
			return
		}
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"ok":1}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("expected accepted JSON body, got %d", response.Code)
	}
}
