package mcserver

import (
	"context"
	"testing"
	"time"

	"github.com/snowy/mcbot/internal/config"
	"github.com/snowy/mcbot/internal/state"
)

func TestControllerStart_FromStoppedState(t *testing.T) {
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
	resultCh := controller.Start(ctx)

	select {
	case result := <-resultCh:
		if result.Success {
			t.Error("Expected Start to fail (no container), but it succeeded")
		}
		if result.ErrorMessage == "" {
			t.Error("Expected error message, got empty string")
		}

		if stateManager.GetState() != state.StateStopped {
			t.Errorf("Expected state to be Stopped after container not found, got %s", stateManager.GetState())
		}

		if stateManager.GetLastError() == nil {
			t.Error("Expected lastError to be set after container not found")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for Start result")
	}
}

func TestControllerStart_FromErrorState(t *testing.T) {
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
	resultCh := controller.Start(ctx)

	select {
	case result := <-resultCh:
		if result.Success {
			t.Error("Expected Start to fail (no container), but it succeeded")
		}

		if stateManager.GetState() != state.StateStopped {
			t.Errorf("Expected state to be Stopped after container not found, got %s", stateManager.GetState())
		}

		if stateManager.GetLastError() == nil {
			t.Error("Expected lastError to be set after container not found")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for Start result")
	}
}

func TestControllerStart_FromRunningState(t *testing.T) {
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
	resultCh := controller.Start(ctx)

	select {
	case result := <-resultCh:
		if result.Success {
			t.Error("Expected Start to fail from Running state")
		}
		expectedMsg := "서버가 이미 실행 중입니다."
		if result.ErrorMessage != expectedMsg {
			t.Errorf("Expected error message '%s', got '%s'", expectedMsg, result.ErrorMessage)
		}

		if stateManager.GetState() != state.StateRunning {
			t.Errorf("State should remain Running, got %s", stateManager.GetState())
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout waiting for Start result")
	}
}

func TestControllerStart_FromStartingState(t *testing.T) {
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
	resultCh := controller.Start(ctx)

	select {
	case result := <-resultCh:
		if result.Success {
			t.Error("Expected Start to fail from Starting state")
		}
		expectedMsg := "서버가 이미 시작 중입니다."
		if result.ErrorMessage != expectedMsg {
			t.Errorf("Expected error message '%s', got '%s'", expectedMsg, result.ErrorMessage)
		}

		if stateManager.GetState() != state.StateStarting {
			t.Errorf("State should remain Starting, got %s", stateManager.GetState())
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout waiting for Start result")
	}
}

func TestControllerStart_FromStoppingState(t *testing.T) {
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
	resultCh := controller.Start(ctx)

	select {
	case result := <-resultCh:
		if result.Success {
			t.Error("Expected Start to fail from Stopping state")
		}
		expectedMsg := "서버가 종료 중입니다. 종료가 완료된 후 다시 시도해주세요."
		if result.ErrorMessage != expectedMsg {
			t.Errorf("Expected error message '%s', got '%s'", expectedMsg, result.ErrorMessage)
		}

		if stateManager.GetState() != state.StateStopping {
			t.Errorf("State should remain Stopping, got %s", stateManager.GetState())
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout waiting for Start result")
	}
}

func TestControllerStart_ContextCancelledImmediately(t *testing.T) {
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

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	resultCh := controller.Start(ctx)

	select {
	case result := <-resultCh:
		if result.Success {
			t.Error("Expected Start to fail due to cancelled context")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Timeout waiting for Start result with cancelled context")
	}

	finalState := stateManager.GetState()
	if finalState != state.StateError && finalState != state.StateStopped {
		t.Errorf("Expected state to be Error or Stopped after cancellation, got %s", finalState)
	}
}

func TestControllerStart_ContextCancelledDuringOperation(t *testing.T) {
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

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	resultCh := controller.Start(ctx)

	select {
	case result := <-resultCh:
		if result.Success {
			t.Error("Expected Start to fail due to timeout or cancellation")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Timeout waiting for Start result")
	}

	finalState := stateManager.GetState()
	if finalState != state.StateError && finalState != state.StateStopped {
		t.Errorf("Expected state to be Error or Stopped after timeout, got %s", finalState)
	}
}
