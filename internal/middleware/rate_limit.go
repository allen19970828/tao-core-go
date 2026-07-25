package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type clientVisitor struct {
	lastSeen time.Time
	count    int
}

type RateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*clientVisitor
	limit    int
	window   time.Duration
}

func NewRateLimiter(maxRequests int, windowDuration time.Duration) *RateLimiter {
	rl := &RateLimiter{
		visitors: make(map[string]*clientVisitor),
		limit:    maxRequests,
		window:   windowDuration,
	}

	// Cleanup old visitors every minute
	go func() {
		for {
			time.Sleep(1 * time.Minute)
			rl.mu.Lock()
			for ip, v := range rl.visitors {
				if time.Since(v.lastSeen) > rl.window {
					delete(rl.visitors, ip)
				}
			}
			rl.mu.Unlock()
		}
	}()

	return rl
}

func (rl *RateLimiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		clientIP := c.ClientIP()

		rl.mu.Lock()
		v, exists := rl.visitors[clientIP]
		now := time.Now()

		if !exists || now.Sub(v.lastSeen) > rl.window {
			rl.visitors[clientIP] = &clientVisitor{
				lastSeen: now,
				count:    1,
			}
			rl.mu.Unlock()
			c.Next()
			return
		}

		v.count++
		if v.count > rl.limit {
			rl.mu.Unlock()
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":       "Too Many Requests: Rate limit exceeded (Max 10 req/sec)",
				"retry_after": "1s",
			})
			c.Abort()
			return
		}

		rl.mu.Unlock()
		c.Next()
	}
}
