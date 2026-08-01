package middleware

import (
	"runtime"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
)

var (
	TotalRequests     uint64
	ActiveSessions    int64
	CompletedSessions int64
)

type MetricsSummary struct {
	Engine            string    `json:"engine"`
	Status            string    `json:"status"`
	Timestamp         time.Time `json:"timestamp"`
	TotalRequests     uint64    `json:"total_requests"`
	ActiveSessions    int64     `json:"active_sessions"`
	CompletedSessions int64     `json:"completed_sessions"`
	NumGoroutines     int       `json:"num_goroutines"`
	MemoryAllocatedMB float64   `json:"memory_allocated_mb"`
}

func MetricsCollector() gin.HandlerFunc {
	return func(c *gin.Context) {
		atomic.AddUint64(&TotalRequests, 1)
		c.Next()
	}
}

func GetMetricsSummary() MetricsSummary {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	return MetricsSummary{
		Engine:            "tao-core-go",
		Status:            "HEALTHY",
		Timestamp:         time.Now(),
		TotalRequests:     atomic.LoadUint64(&TotalRequests),
		ActiveSessions:    atomic.LoadInt64(&ActiveSessions),
		CompletedSessions: atomic.LoadInt64(&CompletedSessions),
		NumGoroutines:     runtime.NumGoroutine(),
		MemoryAllocatedMB: float64(m.Alloc) / 1024 / 1024,
	}
}
