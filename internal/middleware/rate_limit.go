package middleware

import (
	"fmt"
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

const maxTrackedRateLimitClients = 10000

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
			if !exists && len(rl.visitors) >= maxTrackedRateLimitClients {
				// Evict one arbitrary entry in constant expected time. The periodic
				// cleanup handles age ordering; this branch only bounds memory under attack.
				for ip := range rl.visitors {
					delete(rl.visitors, ip)
					break
				}
			}
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
			c.Header("Retry-After", fmt.Sprintf("%.0f", max(1, rl.window.Seconds())))
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":       fmt.Sprintf("Too Many Requests: rate limit exceeded (max %d per %s)", rl.limit, rl.window),
				"retry_after": rl.window.String(),
			})
			c.Abort()
			return
		}

		rl.mu.Unlock()
		c.Next()
	}
}
