package middleware

import (
	"bytes"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		c.Header("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		c.Next()
	}
}

func JSONBodyLimit(maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		contentType := strings.ToLower(c.GetHeader("Content-Type"))
		if !strings.HasPrefix(contentType, "application/json") || c.Request.Body == nil {
			c.Next()
			return
		}
		if c.Request.ContentLength > maxBytes {
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{"error": "JSON request body 過大"})
			return
		}
		body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxBytes+1))
		_ = c.Request.Body.Close()
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "無法讀取 JSON request body"})
			return
		}
		if int64(len(body)) > maxBytes {
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{"error": "JSON request body 過大"})
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(body))
		c.Request.ContentLength = int64(len(body))
		c.Next()
	}
}
