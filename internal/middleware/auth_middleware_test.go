package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

func TestJWTAuthAndRequireRole(t *testing.T) {
	gin.SetMode(gin.TestMode)
	secretKey := "test-secret-key"

	// Create valid JWT token for ADMIN role
	claims := &Claims{
		UserID: "admin-01",
		Roles:  []string{"ADMIN", "TEACHER"},
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(secretKey))
	if err != nil {
		t.Fatalf("Failed to generate test token: %v", err)
	}

	r := gin.New()
	r.Use(JWTAuthMiddleware(secretKey))
	r.Use(RequireRole("ADMIN"))
	r.GET("/protected", func(c *gin.Context) {
		c.String(200, "OK")
	})

	// Test 1: Valid token
	reqValid := httptest.NewRequest("GET", "/protected", nil)
	reqValid.Header.Set("Authorization", "Bearer "+tokenString)
	wValid := httptest.NewRecorder()
	r.ServeHTTP(wValid, reqValid)

	if wValid.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", wValid.Code)
	}

	// Test 2: Missing token
	reqMissing := httptest.NewRequest("GET", "/protected", nil)
	wMissing := httptest.NewRecorder()
	r.ServeHTTP(wMissing, reqMissing)

	if wMissing.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401 for missing token, got %d", wMissing.Code)
	}
}
