package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestRateLimiterRejectsExcessAndBoundsClientMap(t *testing.T) {
	gin.SetMode(gin.TestMode)
	limiter := &RateLimiter{visitors: make(map[string]*clientVisitor), limit: 1, window: time.Second}
	router := gin.New()
	router.Use(limiter.Middleware())
	router.GET("/", func(context *gin.Context) { context.Status(http.StatusNoContent) })

	for requestNumber := 1; requestNumber <= 2; requestNumber++ {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.RemoteAddr = "203.0.114.1:1234"
		router.ServeHTTP(response, request)
		if requestNumber == 1 && response.Code != http.StatusNoContent {
			t.Fatalf("first request unexpectedly rejected: %d", response.Code)
		}
		if requestNumber == 2 && (response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") != "1") {
			t.Fatalf("expected second request to be rate limited with Retry-After: %d %#v", response.Code, response.Header())
		}
	}

	limiter = &RateLimiter{visitors: make(map[string]*clientVisitor), limit: 1, window: time.Second}
	for index := 0; index < maxTrackedRateLimitClients; index++ {
		limiter.visitors[fmt.Sprintf("198.18.%d.%d", index/256, index%256)] = &clientVisitor{lastSeen: time.Unix(int64(index), 0), count: 1}
	}
	router = gin.New()
	router.Use(limiter.Middleware())
	router.GET("/", func(context *gin.Context) { context.Status(http.StatusNoContent) })
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "203.0.114.2:1234"
	router.ServeHTTP(httptest.NewRecorder(), request)
	if len(limiter.visitors) != maxTrackedRateLimitClients {
		t.Fatalf("rate limiter client map exceeded bound: %d", len(limiter.visitors))
	}
}
