package middleware

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

const minJWTSecretLength = 32

type JWTConfig struct {
	Secret   string
	Issuer   string
	Audience string
}

// ValidateJWTSecret 防止服務使用可猜測或誤留白的 HMAC 金鑰啟動。
func ValidateJWTSecret(secret string) error {
	if secret != strings.TrimSpace(secret) || len([]byte(secret)) < minJWTSecretLength {
		return errors.New("JWT_SECRET 必須至少包含 32 bytes")
	}
	return nil
}

func ValidateJWTConfig(config JWTConfig) error {
	if err := ValidateJWTSecret(config.Secret); err != nil {
		return err
	}
	if strings.TrimSpace(config.Issuer) == "" || strings.TrimSpace(config.Audience) == "" ||
		config.Issuer != strings.TrimSpace(config.Issuer) || config.Audience != strings.TrimSpace(config.Audience) {
		return errors.New("JWT_ISSUER 與 JWT_AUDIENCE 不可留白")
	}
	return nil
}

// Claims 定義 JWT Token 攜帶的 Payload 結構體。
type Claims struct {
	UserID string   `json:"user_id"`
	Roles  []string `json:"roles"`
	jwt.RegisteredClaims
}

// GenerateJWT 為指定使用者簽發有效期為 tokenDuration 的 JWT Token。
func GenerateJWT(userID string, roles []string, config JWTConfig, tokenDuration time.Duration) (string, error) {
	if err := ValidateJWTConfig(config); err != nil {
		return "", err
	}
	if strings.TrimSpace(userID) == "" || len(userID) > 255 {
		return "", errors.New("JWT user_id 不可留白或超過 255 bytes")
	}
	if tokenDuration <= 0 {
		return "", errors.New("JWT 有效期間必須大於零")
	}

	claims := &Claims{
		UserID: userID,
		Roles:  roles,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(tokenDuration)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    config.Issuer,
			Audience:  jwt.ClaimStrings{config.Audience},
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(config.Secret))
}

// JWTAuthMiddleware 驗證 Authorization Header 中的 Bearer JWT Token。
func JWTAuthMiddleware(config JWTConfig) gin.HandlerFunc {
	configErr := ValidateJWTConfig(config)

	return func(c *gin.Context) {
		if configErr != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "伺服器認證設定無效"})
			c.Abort()
			return
		}

		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "未提供 Authorization 請求標頭"})
			c.Abort()
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Authorization 格式無效，必須為 Bearer <token>"})
			c.Abort()
			return
		}

		tokenString := parts[1]
		claims := &Claims{}

		token, err := jwt.ParseWithClaims(
			tokenString,
			claims,
			func(token *jwt.Token) (interface{}, error) {
				return []byte(config.Secret), nil
			},
			jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
			jwt.WithIssuer(config.Issuer),
			jwt.WithAudience(config.Audience),
			jwt.WithExpirationRequired(),
			jwt.WithIssuedAt(),
		)

		if err != nil || !token.Valid {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Token 無效或已過期"})
			c.Abort()
			return
		}
		if strings.TrimSpace(claims.UserID) == "" || len(claims.UserID) > 255 {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Token 缺少有效的 user_id"})
			c.Abort()
			return
		}
		if claims.IssuedAt == nil {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Token 缺少簽發時間"})
			c.Abort()
			return
		}

		// 將解析後的 UserID 與 Roles 寫入 Gin Context 供後續 Handler 使用
		c.Set("user_id", claims.UserID)
		c.Set("user_roles", claims.Roles)

		c.Next()
	}
}

// RequireRole 實作 RBAC 權限檢查中間件，驗證請求者是否擁有特定角色 (例如 "ADMIN")。
func RequireRole(requiredRole string) gin.HandlerFunc {
	return func(c *gin.Context) {
		rolesVal, exists := c.Get("user_roles")
		if !exists {
			c.JSON(http.StatusForbidden, gin.H{"error": "存取被拒絕: 未找到使用者角色權限"})
			c.Abort()
			return
		}

		roles, ok := rolesVal.([]string)
		if !ok {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "內部錯誤: 無法解析使用者角色"})
			c.Abort()
			return
		}

		hasRole := false
		for _, role := range roles {
			if role == requiredRole {
				hasRole = true
				break
			}
		}

		if !hasRole {
			c.JSON(http.StatusForbidden, gin.H{"error": "權限不足: 需要 " + requiredRole + " 角色"})
			c.Abort()
			return
		}

		c.Next()
	}
}
