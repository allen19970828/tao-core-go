package service

import (
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestEventBus(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	bus := NewEventBus(logger)

	var wg sync.WaitGroup
	wg.Add(1)

	var receivedData string

	bus.Subscribe("user.created", func(payload interface{}) {
		defer wg.Done()
		receivedData = payload.(string)
	})

	bus.Publish("user.created", "john_doe")

	// Wait with timeout
	c := make(chan struct{})
	go func() {
		wg.Wait()
		close(c)
	}()

	select {
	case <-c:
		if receivedData != "john_doe" {
			t.Errorf("Expected payload 'john_doe', got '%s'", receivedData)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("EventBus handler timed out")
	}
}
