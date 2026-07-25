package service

import (
	"sync"

	"go.uber.org/zap"
)

// EventHandler 定義事件訂閱處理函式型態。
type EventHandler func(payload interface{})

// EventBus 提供系統解耦內部事件總線 (In-Memory Pub/Sub) 介面。
type EventBus interface {
	Subscribe(eventName string, handler EventHandler)
	Publish(eventName string, payload interface{})
}

type eventBus struct {
	mu          sync.RWMutex
	subscribers map[string][]EventHandler
	logger      *zap.Logger
}

// NewEventBus 建立並回傳 EventBus 實體。
func NewEventBus(logger *zap.Logger) EventBus {
	return &eventBus{
		subscribers: make(map[string][]EventHandler),
		logger:      logger,
	}
}

// Subscribe 註冊特定事件名稱的監聽處理函式。
func (b *eventBus) Subscribe(eventName string, handler EventHandler) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subscribers[eventName] = append(b.subscribers[eventName], handler)
}

// Publish 廣播事件至所有訂閱者，並以獨立 Goroutine 非阻塞執行，支援 Panic Recovery 防護。
func (b *eventBus) Publish(eventName string, payload interface{}) {
	b.mu.RLock()
	handlers, exists := b.subscribers[eventName]
	b.mu.RUnlock()

	if !exists || len(handlers) == 0 {
		return
	}

	for _, handler := range handlers {
		h := handler
		go func() {
			defer func() {
				if r := recover(); r != nil {
					b.logger.Error("EventBus 訂閱者處理函式觸發 Panic", zap.String("event", eventName), zap.Any("panic", r))
				}
			}()
			h(payload)
		}()
	}
}
