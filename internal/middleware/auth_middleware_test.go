package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

func TestJWTAuthAndRequireRole(t *testing.T) {
	gin.SetMode(gin.TestMode)
	config := JWTConfig{Secret: "test-secret-key-with-at-least-32-bytes", Issuer: "test-issuer", Audience: "test-audience"}

	// Create valid JWT token for ADMIN role
	claims := &Claims{
		UserID: "admin-01",
		Roles:  []string{"ADMIN", "TEACHER"},
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    config.Issuer,
			Audience:  jwt.ClaimStrings{config.Audience},
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString([]byte(config.Secret))
	if err != nil {
		t.Fatalf("Failed to generate test token: %v", err)
	}

	r := gin.New()
	r.Use(JWTAuthMiddleware(config))
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

func TestValidateJWTSecret(t *testing.T) {
	if err := ValidateJWTSecret(""); err == nil {
		t.Fatal("expected an empty JWT secret to be rejected")
	}
	if err := ValidateJWTSecret("too-short"); err == nil {
		t.Fatal("expected a short JWT secret to be rejected")
	}
	if err := ValidateJWTSecret("a-secure-test-secret-with-32-bytes"); err != nil {
		t.Fatalf("expected a sufficiently long JWT secret to be accepted: %v", err)
	}
}

func TestJWTAuthRejectsUnexpectedSigningMethod(t *testing.T) {
	config := JWTConfig{Secret: "test-secret-key-with-at-least-32-bytes", Issuer: "test-issuer", Audience: "test-audience"}
	claims := &Claims{
		UserID: "admin-01",
		Roles:  []string{"ADMIN"},
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    config.Issuer,
			Audience:  jwt.ClaimStrings{config.Audience},
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS512, claims)
	tokenString, err := token.SignedString([]byte(config.Secret))
	if err != nil {
		t.Fatalf("sign test JWT: %v", err)
	}

	r := gin.New()
	r.Use(JWTAuthMiddleware(config))
	r.GET("/protected", func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+tokenString)
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected HS512 token to be rejected with 401, got %d", resp.Code)
	}
}

func TestJWTAuthRejectsInvalidRegisteredClaimsAndIdentity(t *testing.T) {
	config := JWTConfig{Secret: "test-secret-key-with-at-least-32-bytes", Issuer: "test-issuer", Audience: "test-audience"}
	now := time.Now()
	tests := map[string]*Claims{
		"wrong issuer": {
			UserID: "student", RegisteredClaims: jwt.RegisteredClaims{Issuer: "other", Audience: jwt.ClaimStrings{config.Audience},
				ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)), IssuedAt: jwt.NewNumericDate(now)},
		},
		"wrong audience": {
			UserID: "student", RegisteredClaims: jwt.RegisteredClaims{Issuer: config.Issuer, Audience: jwt.ClaimStrings{"other"},
				ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)), IssuedAt: jwt.NewNumericDate(now)},
		},
		"missing expiry": {
			UserID: "student", RegisteredClaims: jwt.RegisteredClaims{Issuer: config.Issuer, Audience: jwt.ClaimStrings{config.Audience}, IssuedAt: jwt.NewNumericDate(now)},
		},
		"missing issued at": {
			UserID: "student", RegisteredClaims: jwt.RegisteredClaims{Issuer: config.Issuer, Audience: jwt.ClaimStrings{config.Audience}, ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour))},
		},
		"empty user": {
			UserID: " ", RegisteredClaims: jwt.RegisteredClaims{Issuer: config.Issuer, Audience: jwt.ClaimStrings{config.Audience},
				ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)), IssuedAt: jwt.NewNumericDate(now)},
		},
	}
	for name, claims := range tests {
		t.Run(name, func(t *testing.T) {
			token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
			tokenString, err := token.SignedString([]byte(config.Secret))
			if err != nil {
				t.Fatalf("sign token: %v", err)
			}
			router := gin.New()
			router.Use(JWTAuthMiddleware(config))
			router.GET("/", func(c *gin.Context) { c.Status(http.StatusNoContent) })
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.Header.Set("Authorization", "Bearer "+tokenString)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("expected 401, got %d", response.Code)
			}
		})
	}
}

func TestGenerateJWTRejectsInvalidIdentityOrDuration(t *testing.T) {
	config := JWTConfig{Secret: "test-secret-key-with-at-least-32-bytes", Issuer: "test-issuer", Audience: "test-audience"}
	if _, err := GenerateJWT("", nil, config, time.Hour); err == nil {
		t.Fatal("expected empty user ID to be rejected")
	}
	if _, err := GenerateJWT("student", nil, config, 0); err == nil {
		t.Fatal("expected non-positive duration to be rejected")
	}
	if _, err := GenerateJWT(strings.Repeat("u", 256), nil, config, time.Hour); err == nil {
		t.Fatal("expected oversized user ID to be rejected")
	}
}
