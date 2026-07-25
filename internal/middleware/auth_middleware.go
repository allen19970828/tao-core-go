package middleware

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

// Claims 定義 JWT Token 攜帶的 Payload 結構體。
type Claims struct {
	UserID string   `json:"user_id"`
	Roles  []string `json:"roles"`
	jwt.RegisteredClaims
}

// GenerateJWT 為指定使用者簽發有效期為 tokenDuration 的 JWT Token。
func GenerateJWT(userID string, roles []string, secret string, tokenDuration time.Duration) (string, error) {
	claims := &Claims{
		UserID: userID,
		Roles:  roles,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(tokenDuration)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// JWTAuthMiddleware 驗證 Authorization Header 中的 Bearer JWT Token。
func JWTAuthMiddleware(secret string) gin.HandlerFunc {
	return func(c *gin.Context) {
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

		token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
			return []byte(secret), nil
		})

		if err != nil || !token.Valid {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Token 無效或已過期"})
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
