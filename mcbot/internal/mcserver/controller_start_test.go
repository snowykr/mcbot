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

func TestStartErrorMessage_StateMapping(t *testing.T) {
	tests := []struct {
		name          string
		state         state.ServerState
		expectedMsg   string
		expectedError bool
	}{
		{
			name:          "StateStarting returns appropriate error",
			state:         state.StateStarting,
			expectedMsg:   "서버가 이미 시작 중입니다.",
			expectedError: true,
		},
		{
			name:          "StateRunning returns appropriate error",
			state:         state.StateRunning,
			expectedMsg:   "서버가 이미 실행 중입니다.",
			expectedError: true,
		},
		{
			name:          "StateStopping returns appropriate error",
			state:         state.StateStopping,
			expectedMsg:   "서버가 종료 중입니다. 종료가 완료된 후 다시 시도해주세요.",
			expectedError: true,
		},
		{
			name:          "StateStopped allows start (no error)",
			state:         state.StateStopped,
			expectedMsg:   "",
			expectedError: false,
		},
		{
			name:          "StateError allows start (no error)",
			state:         state.StateError,
			expectedMsg:   "",
			expectedError: false,
		},
		{
			name:          "Unknown state returns generic error",
			state:         state.ServerState(999),
			expectedMsg:   "서버 상태가 변경되었습니다. 다시 시도해주세요.",
			expectedError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, hasError := startErrorMessage(tt.state)

			if hasError != tt.expectedError {
				t.Errorf("Expected hasError=%v, got %v", tt.expectedError, hasError)
			}

			if msg != tt.expectedMsg {
				t.Errorf("Expected message %q, got %q", tt.expectedMsg, msg)
			}
		})
	}
}
