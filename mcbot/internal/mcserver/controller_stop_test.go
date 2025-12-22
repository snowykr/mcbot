package mcserver

import (
	"context"
	"testing"
	"time"

	"github.com/snowy/mcbot/internal/config"
	"github.com/snowy/mcbot/internal/state"
)

func TestControllerStop_FromRunningState(t *testing.T) {
	cfg := &config.Config{
		MCContainerName:    "test-container",
		ReadyLogPattern:    `Done \(([0-9.]+)s\)`,
		ReadyTimeout:       5 * time.Second,
		MCJoinLogPattern:   `(\w+) joined`,
		MCLeaveLogPattern:  `(\w+) left`,
		StopTimeoutSeconds: 10,
	}

	stateManager := state.NewManager()
	stateManager.SetState(state.StateRunning)

	controller, err := NewController(cfg, stateManager)
	if err != nil {
		t.Fatalf("Failed to create controller: %v", err)
	}
	defer controller.Shutdown()

	ctx := context.Background()
	resultCh := controller.Stop(ctx)

	select {
	case result := <-resultCh:
		if result.Success {
			t.Error("Expected Stop to fail (no container), but it succeeded")
		}
		if result.ErrorMessage == "" {
			t.Error("Expected error message, got empty string")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for Stop result")
	}
}

func TestControllerStop_FromStoppedState(t *testing.T) {
	cfg := &config.Config{
		MCContainerName:    "test-container",
		ReadyLogPattern:    `Done \(([0-9.]+)s\)`,
		ReadyTimeout:       5 * time.Second,
		MCJoinLogPattern:   `(\w+) joined`,
		MCLeaveLogPattern:  `(\w+) left`,
		StopTimeoutSeconds: 10,
	}

	stateManager := state.NewManager()

	controller, err := NewController(cfg, stateManager)
	if err != nil {
		t.Fatalf("Failed to create controller: %v", err)
	}
	defer controller.Shutdown()

	if stateManager.GetState() != state.StateStopped {
		t.Fatalf("Expected initial state to be Stopped, got %s", stateManager.GetState())
	}

	ctx := context.Background()
	resultCh := controller.Stop(ctx)

	select {
	case result := <-resultCh:
		if result.Success {
			t.Error("Expected Stop to fail from Stopped state")
		}
		expectedMsg := "서버가 이미 종료되어 있습니다."
		if result.ErrorMessage != expectedMsg {
			t.Errorf("Expected error message '%s', got '%s'", expectedMsg, result.ErrorMessage)
		}

		if stateManager.GetState() != state.StateStopped {
			t.Errorf("State should remain Stopped, got %s", stateManager.GetState())
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout waiting for Stop result")
	}
}

func TestControllerStop_FromStoppingState(t *testing.T) {
	cfg := &config.Config{
		MCContainerName:    "test-container",
		ReadyLogPattern:    `Done \(([0-9.]+)s\)`,
		ReadyTimeout:       5 * time.Second,
		MCJoinLogPattern:   `(\w+) joined`,
		MCLeaveLogPattern:  `(\w+) left`,
		StopTimeoutSeconds: 10,
	}

	stateManager := state.NewManager()
	stateManager.SetState(state.StateStopping)

	controller, err := NewController(cfg, stateManager)
	if err != nil {
		t.Fatalf("Failed to create controller: %v", err)
	}
	defer controller.Shutdown()

	ctx := context.Background()
	resultCh := controller.Stop(ctx)

	select {
	case result := <-resultCh:
		if result.Success {
			t.Error("Expected Stop to fail from Stopping state")
		}
		expectedMsg := "서버가 이미 종료 중입니다."
		if result.ErrorMessage != expectedMsg {
			t.Errorf("Expected error message '%s', got '%s'", expectedMsg, result.ErrorMessage)
		}

		if stateManager.GetState() != state.StateStopping {
			t.Errorf("State should remain Stopping, got %s", stateManager.GetState())
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout waiting for Stop result")
	}
}

func TestControllerStop_FromStartingState(t *testing.T) {
	cfg := &config.Config{
		MCContainerName:    "test-container",
		ReadyLogPattern:    `Done \(([0-9.]+)s\)`,
		ReadyTimeout:       5 * time.Second,
		MCJoinLogPattern:   `(\w+) joined`,
		MCLeaveLogPattern:  `(\w+) left`,
		StopTimeoutSeconds: 10,
	}

	stateManager := state.NewManager()
	stateManager.SetState(state.StateStarting)

	controller, err := NewController(cfg, stateManager)
	if err != nil {
		t.Fatalf("Failed to create controller: %v", err)
	}
	defer controller.Shutdown()

	ctx := context.Background()
	resultCh := controller.Stop(ctx)

	select {
	case result := <-resultCh:
		if result.Success {
			t.Error("Expected Stop to fail from Starting state")
		}
		expectedMsg := "서버가 시작 중입니다. 시작이 완료된 후 다시 시도해주세요."
		if result.ErrorMessage != expectedMsg {
			t.Errorf("Expected error message '%s', got '%s'", expectedMsg, result.ErrorMessage)
		}

		if stateManager.GetState() != state.StateStarting {
			t.Errorf("State should remain Starting, got %s", stateManager.GetState())
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout waiting for Stop result")
	}
}

func TestControllerStop_FromErrorState(t *testing.T) {
	cfg := &config.Config{
		MCContainerName:    "test-container",
		ReadyLogPattern:    `Done \(([0-9.]+)s\)`,
		ReadyTimeout:       5 * time.Second,
		MCJoinLogPattern:   `(\w+) joined`,
		MCLeaveLogPattern:  `(\w+) left`,
		StopTimeoutSeconds: 10,
	}

	stateManager := state.NewManager()
	stateManager.SetError(context.DeadlineExceeded)

	controller, err := NewController(cfg, stateManager)
	if err != nil {
		t.Fatalf("Failed to create controller: %v", err)
	}
	defer controller.Shutdown()

	if stateManager.GetState() != state.StateError {
		t.Fatalf("Expected initial state to be Error, got %s", stateManager.GetState())
	}

	ctx := context.Background()
	resultCh := controller.Stop(ctx)

	select {
	case result := <-resultCh:
		if result.Success {
			t.Error("Expected Stop to fail (no container), but it succeeded")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for Stop result")
	}
}
