package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"tao-core-go/internal/handler"
	"tao-core-go/internal/middleware"
)

var routeTestJWTConfig = middleware.JWTConfig{
	Secret:   "route-test-secret-with-at-least-32-bytes",
	Issuer:   "route-test-issuer",
	Audience: "route-test-audience",
}

func newRouteTestEngine() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	registerProtectedRoutes(r, routeTestJWTConfig, middleware.NewRateLimiter(10, time.Second), apiRouteHandlers{
		session: handler.NewSessionHandler(nil, nil),
		qti:     handler.NewQTIHandler(nil, ""),
		lti:     handler.NewLTIHandler(nil, routeTestJWTConfig, time.Hour),
		proctor: handler.NewProctorHandler(nil),
		results: handler.NewResultsHandler(nil),
	})
	return r
}

func TestProtectedRoutesRejectAnonymousRequests(t *testing.T) {
	r := newRouteTestEngine()
	tests := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/metrics"},
		{http.MethodGet, "/uploads/media/question.png"},
		{http.MethodPost, "/api/v1/sessions/start"},
		{http.MethodGet, "/api/v1/sessions/session-1"},
		{http.MethodPost, "/api/v1/sessions/session-1/response"},
		{http.MethodPost, "/api/v1/sessions/session-1/submit"},
		{http.MethodPost, "/api/v1/sessions/session-1/proctor/event"},
		{http.MethodGet, "/api/v1/sessions/session-1/proctor/log"},
		{http.MethodGet, "/api/v1/sessions/session-1/proctor/analytics"},
		{http.MethodGet, "/api/v1/deliveries/delivery-1/results/csv"},
		{http.MethodPost, "/api/v1/items/import-qti"},
		{http.MethodPost, "/api/v1/lti/platforms"},
		{http.MethodPost, "/api/v1/lti/resource-links"},
		{http.MethodPost, "/api/v1/webhooks/configs"},
		{http.MethodGet, "/api/v1/webhooks/logs"},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			resp := httptest.NewRecorder()
			r.ServeHTTP(resp, req)
			if resp.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d", resp.Code)
			}
		})
	}
}

func TestDatabaseSecurityValidation(t *testing.T) {
	if err := validateDatabaseSecurity("release", "postgres", "host=db sslmode=disable", false); err == nil {
		t.Fatal("expected insecure production PostgreSQL DSN to be rejected")
	}
	if err := validateDatabaseSecurity("release", "postgres", "host=db sslmode=verify-full", false); err != nil {
		t.Fatalf("expected verified TLS DSN to pass: %v", err)
	}
	if err := validateDatabaseSecurity("release", "postgres", "host=postgres sslmode=disable", true); err != nil {
		t.Fatalf("expected explicit isolated-network exception to pass: %v", err)
	}
	if _, err := openDatabase("unsupported", "ignored"); err == nil {
		t.Fatal("expected unknown database driver to fail")
	}
}

func TestAdminRoutesRejectNonAdminToken(t *testing.T) {
	r := newRouteTestEngine()
	token, err := middleware.GenerateJWT("student-1", []string{"STUDENT"}, routeTestJWTConfig, time.Hour)
	if err != nil {
		t.Fatalf("generate test JWT: %v", err)
	}

	tests := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/metrics"},
		{http.MethodGet, "/api/v1/sessions/session-1/proctor/log"},
		{http.MethodGet, "/api/v1/sessions/session-1/proctor/analytics"},
		{http.MethodGet, "/api/v1/deliveries/delivery-1/results/csv"},
		{http.MethodPost, "/api/v1/items/import-qti"},
		{http.MethodPost, "/api/v1/lti/platforms"},
		{http.MethodPost, "/api/v1/lti/resource-links"},
		{http.MethodPost, "/api/v1/webhooks/configs"},
		{http.MethodGet, "/api/v1/webhooks/logs"},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			resp := httptest.NewRecorder()
			r.ServeHTTP(resp, req)
			if resp.Code != http.StatusForbidden {
				t.Fatalf("expected 403, got %d", resp.Code)
			}
		})
	}
}

func TestPublicLTIEndpointsRequireProtocolInputs(t *testing.T) {
	r := newRouteTestEngine()
	tests := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/lti/login"},
		{http.MethodPost, "/api/v1/lti/login"},
		{http.MethodPost, "/api/v1/lti/launch"},
	}

	for _, tt := range tests {
		req := httptest.NewRequest(tt.method, tt.path, nil)
		resp := httptest.NewRecorder()
		r.ServeHTTP(resp, req)
		if resp.Code != http.StatusBadRequest {
			t.Fatalf("expected incomplete LTI request %s %s to return 400, got %d", tt.method, tt.path, resp.Code)
		}
	}
}
