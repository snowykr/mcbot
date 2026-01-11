package mcserver

import (
	"sync"
	"testing"
	"time"

	"github.com/snowy/mcbot/internal/config"
	"github.com/snowy/mcbot/internal/state"
)

func TestController_StateChangeWorker_Restart(t *testing.T) {
	cfg := &config.Config{MCContainerName: "test-mc"}
	stateManager := state.NewManager()
	controller, _ := NewController(cfg, stateManager)
	defer controller.Shutdown()

	var wg sync.WaitGroup
	var mu sync.Mutex
	var lastState state.ServerState

	callback := func(s state.ServerState) {
		mu.Lock()
		lastState = s
		mu.Unlock()
		wg.Done()
	}

	// 1. Initial run
	wg.Add(1)
	controller.SetOnStateChange(callback)
	controller.notifyStateChange(state.StateRunning)

	if !waitWithTimeout(&wg, 1*time.Second) {
		t.Fatal("Timed out waiting for first state change")
	}

	// 2. Shutdown and Restart
	controller.Shutdown()

	wg.Add(1)
	controller.SetOnStateChange(callback) // Should restart the worker
	controller.notifyStateChange(state.StateStopped)

	if !waitWithTimeout(&wg, 1*time.Second) {
		t.Fatal("Timed out waiting for second state change (Restart failed)")
	}

	mu.Lock()
	if lastState != state.StateStopped {
		t.Errorf("Expected StateStopped, got %v", lastState)
	}
	mu.Unlock()
}

func TestController_StateChange_Race(t *testing.T) {
	cfg := &config.Config{MCContainerName: "test-mc"}
	stateManager := state.NewManager()
	controller, _ := NewController(cfg, stateManager)
	defer controller.Shutdown()

	done := make(chan bool)
	go func() {
		for i := 0; i < 100; i++ {
			controller.SetOnStateChange(func(s state.ServerState) {})
		}
		done <- true
	}()

	for i := 0; i < 100; i++ {
		controller.notifyStateChange(state.StateRunning)
	}
	<-done
}

func waitWithTimeout(wg *sync.WaitGroup, timeout time.Duration) bool {
	c := make(chan struct{})
	go func() {
		defer close(c)
		wg.Wait()
	}()
	select {
	case <-c:
		return true
	case <-time.After(timeout):
		return false
	}
}
