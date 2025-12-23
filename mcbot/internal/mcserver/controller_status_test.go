package mcserver

import (
	"context"
	"testing"
	"time"

	"github.com/snowy/mcbot/internal/config"
	"github.com/snowy/mcbot/internal/state"
)

func TestControllerStatus_StateStartingPreservedWhenContainerNotRunning(t *testing.T) {
	cfg := &config.Config{
		MCContainerName:    "test-container",
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

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	status := controller.Status(ctx)

	if status.State != state.StateStarting {
		t.Errorf("Expected state to remain StateStarting when container is not running, got %s", status.State)
	}

	if stateManager.GetState() != state.StateStarting {
		t.Errorf("Expected stateManager to remain StateStarting, got %s", stateManager.GetState())
	}
}

func TestControllerStatus_StateStartingPreservedEvenWhenContainerRunning(t *testing.T) {
	cfg := &config.Config{
		MCContainerName:    "test-container",
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

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	status := controller.Status(ctx)

	if status.State != state.StateStarting {
		t.Errorf("Expected state to remain StateStarting even when container is running (ready log not yet detected), got %s", status.State)
	}

	if stateManager.GetState() != state.StateStarting {
		t.Errorf("Expected stateManager to remain StateStarting, got %s", stateManager.GetState())
	}
}

func TestControllerStatus_StateRunningToStoppedWhenContainerNotRunning(t *testing.T) {
	cfg := &config.Config{
		MCContainerName:    "test-container",
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

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	status := controller.Status(ctx)

	if !status.ContainerRunning {
		if status.State != state.StateStopped {
			t.Errorf("Expected state to transition to StateStopped when container is not running, got %s", status.State)
		}

		if stateManager.GetState() != state.StateStopped {
			t.Errorf("Expected stateManager to transition to StateStopped, got %s", stateManager.GetState())
		}
	}
}

func TestControllerStatus_StateStoppingPreservedWhenContainerNotRunning(t *testing.T) {
	cfg := &config.Config{
		MCContainerName:    "test-container",
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

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	status := controller.Status(ctx)

	if status.State != state.StateStopping {
		t.Errorf("Expected state to remain StateStopping when container is not running, got %s", status.State)
	}

	if stateManager.GetState() != state.StateStopping {
		t.Errorf("Expected stateManager to remain StateStopping, got %s", stateManager.GetState())
	}
}
