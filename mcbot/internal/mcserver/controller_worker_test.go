package mcserver

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/snowy/mcbot/internal/config"
	"github.com/snowy/mcbot/internal/state"
)

func TestController_StateChangeWorker_Restart(t *testing.T) {
	cfg := &config.Config{
		MCContainerName:        "test-mc",
		CrashDetectionInterval: 5 * time.Second,
	}
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
	cfg := &config.Config{
		MCContainerName:        "test-mc",
		CrashDetectionInterval: 5 * time.Second,
	}
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

func TestRuntimeWatchers_StopThenImmediateStart(t *testing.T) {
	cfg := &config.Config{
		MCContainerName:        "test-mc-watchers",
		CrashDetectionInterval: 5 * time.Second,
	}
	stateManager := state.NewManager()
	controller, err := NewController(cfg, stateManager)
	if err != nil {
		t.Fatalf("Failed to create controller: %v", err)
	}
	defer controller.Shutdown()

	ctx := context.Background()

	controller.StartRuntimeWatchers(ctx)

	controller.watchersMu.Lock()
	running1 := controller.watchersRunning
	controller.watchersMu.Unlock()

	if !running1 {
		t.Fatal("Watchers should be running after StartRuntimeWatchers")
	}

	controller.StopRuntimeWatchers()

	controller.watchersMu.Lock()
	runningAfterStop := controller.watchersRunning
	supervisorDoneCh := controller.watchersSupervisorDoneCh
	runtimeLogSub := controller.runtimeLogSub
	controller.watchersMu.Unlock()

	if runningAfterStop {
		t.Error("watchersRunning should be false after StopRuntimeWatchers returns")
	}
	if supervisorDoneCh != nil {
		t.Error("watchersSupervisorDoneCh should be nil after StopRuntimeWatchers returns")
	}
	if runtimeLogSub != nil {
		t.Error("runtimeLogSub should be nil after StopRuntimeWatchers returns")
	}

	controller.StartRuntimeWatchers(ctx)

	controller.watchersMu.Lock()
	running2 := controller.watchersRunning
	controller.watchersMu.Unlock()

	if !running2 {
		t.Fatal("Watchers should be running after immediate restart")
	}

	controller.StopRuntimeWatchers()

	controller.watchersMu.Lock()
	runningFinal := controller.watchersRunning
	controller.watchersMu.Unlock()

	if runningFinal {
		t.Error("watchersRunning should be false after final StopRuntimeWatchers")
	}
}

func TestRuntimeWatchers_StopIdempotent(t *testing.T) {
	cfg := &config.Config{
		MCContainerName:        "test-mc-idempotent",
		CrashDetectionInterval: 5 * time.Second,
	}
	stateManager := state.NewManager()
	controller, err := NewController(cfg, stateManager)
	if err != nil {
		t.Fatalf("Failed to create controller: %v", err)
	}
	defer controller.Shutdown()

	controller.StopRuntimeWatchers()
	controller.StopRuntimeWatchers()

	controller.StartRuntimeWatchers(context.Background())
	controller.StopRuntimeWatchers()
	controller.StopRuntimeWatchers()
}

func TestStateChange_StopDisablesNotifications(t *testing.T) {
	cfg := &config.Config{
		MCContainerName:        "test-mc-statechange",
		CrashDetectionInterval: 5 * time.Second,
	}
	stateManager := state.NewManager()
	controller, err := NewController(cfg, stateManager)
	if err != nil {
		t.Fatalf("Failed to create controller: %v", err)
	}
	defer controller.Shutdown()

	var callCount int
	var mu sync.Mutex

	controller.SetOnStateChange(func(s state.ServerState) {
		mu.Lock()
		callCount++
		mu.Unlock()
	})

	controller.notifyStateChange(state.StateRunning)
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	countBeforeStop := callCount
	mu.Unlock()

	if countBeforeStop != 1 {
		t.Errorf("Expected 1 callback before stop, got %d", countBeforeStop)
	}

	controller.stopStateChangeWorker()

	controller.notifyStateChange(state.StateCrashed)
	controller.notifyStateChange(state.StateStopped)
	controller.notifyStateChange(state.StateRunning)
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	countAfterStop := callCount
	mu.Unlock()

	if countAfterStop != countBeforeStop {
		t.Errorf("Callbacks should not increase after stopStateChangeWorker: before=%d, after=%d",
			countBeforeStop, countAfterStop)
	}
}
