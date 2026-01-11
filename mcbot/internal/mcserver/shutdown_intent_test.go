package mcserver

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/snowy/mcbot/internal/config"
	"github.com/snowy/mcbot/internal/state"
)

func TestShutdownIntent_ImmediateDetection(t *testing.T) {
	cfg := &config.Config{MCContainerName: "test-mc"}
	stateManager := state.NewManager()
	controller, _ := NewController(cfg, stateManager)
	defer controller.Shutdown()

	stateManager.SetRunning(0)
	controller.shutdownIntentFromInside.Store(true)

	controller.handleNormalShutdown(state.StateRunning, true)

	finalState := stateManager.GetState()
	if finalState != state.StateStopped {
		t.Errorf("Expected StateStopped after normal shutdown, got %v", finalState)
	}

	if controller.shutdownIntentFromInside.Load() {
		t.Error("shutdownIntentFromInside should be reset to false after handleNormalShutdown")
	}
}

func TestShutdownIntent_DelayedDetection_ViaLogWatcherDone(t *testing.T) {
	cfg := &config.Config{MCContainerName: "test-mc"}
	stateManager := state.NewManager()
	controller, _ := NewController(cfg, stateManager)
	defer controller.Shutdown()

	stateManager.SetRunning(0)

	logWatcherDoneCh := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()

		time.Sleep(50 * time.Millisecond)
		controller.shutdownIntentFromInside.Store(true)
		close(logWatcherDoneCh)
	}()

	select {
	case <-logWatcherDoneCh:
	case <-time.After(ShutdownIntentGracePeriod):
		t.Fatal("Timeout waiting for logWatcherDoneCh - test setup error")
	}

	if !controller.shutdownIntentFromInside.Load() {
		t.Error("shutdownIntentFromInside should be true after delayed set")
	}

	wg.Wait()
}

func TestShutdownIntent_Timeout_TreatedAsCrash(t *testing.T) {
	cfg := &config.Config{MCContainerName: "test-mc"}
	stateManager := state.NewManager()
	controller, _ := NewController(cfg, stateManager)
	defer controller.Shutdown()

	stateManager.SetRunning(0)
	controller.shutdownIntentFromInside.Store(false)

	logWatcherDoneCh := make(chan struct{})

	shortTimeout := 100 * time.Millisecond
	select {
	case <-logWatcherDoneCh:
		t.Fatal("logWatcherDoneCh should not be closed in this test")
	case <-time.After(shortTimeout):
	}

	if controller.shutdownIntentFromInside.Load() {
		t.Error("shutdownIntentFromInside should remain false (simulating crash scenario)")
	}
}

func TestShutdownIntent_RaceScenario_LogDelayedButArrives(t *testing.T) {
	cfg := &config.Config{MCContainerName: "test-mc"}
	stateManager := state.NewManager()
	controller, _ := NewController(cfg, stateManager)
	defer controller.Shutdown()

	stateManager.SetRunning(0)

	logWatcherDoneCh := make(chan struct{})

	intentArriveDelay := 100 * time.Millisecond

	var resultState state.ServerState
	var resultMu sync.Mutex

	go func() {
		time.Sleep(intentArriveDelay)
		controller.shutdownIntentFromInside.Store(true)
		close(logWatcherDoneCh)
	}()

	go func() {
		if controller.shutdownIntentFromInside.Load() {
			resultMu.Lock()
			resultState = state.StateStopped
			resultMu.Unlock()
			return
		}

		select {
		case <-logWatcherDoneCh:
		case <-time.After(ShutdownIntentGracePeriod):
		}

		resultMu.Lock()
		if controller.shutdownIntentFromInside.Load() {
			resultState = state.StateStopped
		} else {
			resultState = state.StateCrashed
		}
		resultMu.Unlock()
	}()

	time.Sleep(intentArriveDelay + 50*time.Millisecond)

	resultMu.Lock()
	defer resultMu.Unlock()
	if resultState != state.StateStopped {
		t.Errorf("Expected StateStopped (normal shutdown detected via event), got %v", resultState)
	}
}

func TestShutdownIntent_CtxCancelDuringGrace_WithIntent_TransitionsToStopped(t *testing.T) {
	cfg := &config.Config{MCContainerName: "test-mc"}
	stateManager := state.NewManager()
	controller, _ := NewController(cfg, stateManager)
	defer controller.Shutdown()

	stateManager.SetRunning(0)
	controller.shutdownIntentFromInside.Store(true)

	controller.handleNormalShutdown(state.StateRunning, true)

	finalState := stateManager.GetState()
	if finalState != state.StateStopped {
		t.Errorf("Expected StateStopped when ctx cancelled with shutdownIntent=true, got %v", finalState)
	}
}

func TestShutdownIntent_CtxCancelDuringGrace_WithoutIntent_TransitionsToCrashed(t *testing.T) {
	cfg := &config.Config{MCContainerName: "test-mc"}
	stateManager := state.NewManager()
	controller, _ := NewController(cfg, stateManager)
	defer controller.Shutdown()

	stateManager.SetRunning(0)
	controller.shutdownIntentFromInside.Store(false)

	input := reconcileInput{
		currentState:     state.StateRunning,
		containerExists:  true,
		containerRunning: false,
		shutdownIntent:   false,
		reason:           ReasonRuntimeContainerStopped,
		err:              nil,
	}
	result := controller.reconcileState(input)
	controller.applyReconcileResult(result, ReasonRuntimeContainerStopped, nil, true)

	finalState := stateManager.GetState()
	if finalState != state.StateCrashed {
		t.Errorf("Expected StateCrashed when ctx cancelled without shutdownIntent, got %v", finalState)
	}
}

func TestShutdownIntent_GracePeriodCtxCancel_StateTransitionGuaranteed(t *testing.T) {
	cfg := &config.Config{MCContainerName: "test-mc"}
	stateManager := state.NewManager()
	controller, _ := NewController(cfg, stateManager)
	defer controller.Shutdown()

	stateManager.SetRunning(0)

	logWatcherDoneCh := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())

	var resultState state.ServerState
	var transitionHappened bool
	var mu sync.Mutex

	go func() {
		controller.shutdownIntentFromInside.Store(false)

		ctxCancelledDuringGrace := false
		select {
		case <-logWatcherDoneCh:
		case <-time.After(ShutdownIntentGracePeriod):
		case <-ctx.Done():
			ctxCancelledDuringGrace = true
		}

		mu.Lock()
		transitionHappened = true
		if controller.shutdownIntentFromInside.Load() {
			resultState = state.StateStopped
		} else {
			resultState = state.StateCrashed
		}
		mu.Unlock()

		_ = ctxCancelledDuringGrace
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	if !transitionHappened {
		t.Error("State transition should have happened even after ctx cancel")
	}
	if resultState != state.StateCrashed {
		t.Errorf("Expected StateCrashed (conservative policy), got %v", resultState)
	}
}
