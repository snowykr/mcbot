package mcserver

import (
	"testing"
	"time"

	"github.com/snowy/mcbot/internal/state"
)

func TestReconcileState_StartingWithContainerStopped_TransitionsToCrashed(t *testing.T) {
	stateManager := state.NewManager()
	stateManager.SetState(state.StateStarting)

	ctrl := &Controller{
		stateManager: stateManager,
	}

	input := reconcileInput{
		currentState:     state.StateStarting,
		containerExists:  true,
		containerRunning: false,
		shutdownIntent:   false,
		reason:           "test_container_stopped",
		err:              nil,
	}

	result := ctrl.reconcileState(input)

	if result.action != reconcileTransitionToCrashed {
		t.Errorf("Expected action to be reconcileTransitionToCrashed, got %v", result.action)
	}

	if !result.shouldStopMux {
		t.Error("Expected shouldStopMux to be true")
	}

	if !result.shouldClearPlayers {
		t.Error("Expected shouldClearPlayers to be true")
	}
}

func TestReconcileState_RunningWithContainerStoppedNoShutdownIntent_TransitionsToCrashed(t *testing.T) {
	stateManager := state.NewManager()
	stateManager.SetRunning(5 * time.Second)

	ctrl := &Controller{
		stateManager: stateManager,
	}

	input := reconcileInput{
		currentState:     state.StateRunning,
		containerExists:  true,
		containerRunning: false,
		shutdownIntent:   false,
		reason:           "unexpected_stop",
		err:              nil,
	}

	result := ctrl.reconcileState(input)

	if result.action != reconcileTransitionToCrashed {
		t.Errorf("Expected action to be reconcileTransitionToCrashed, got %v", result.action)
	}
}

func TestReconcileState_StoppingWithContainerStopped_TransitionsToStopped(t *testing.T) {
	stateManager := state.NewManager()
	stateManager.SetState(state.StateStopping)

	ctrl := &Controller{
		stateManager: stateManager,
	}

	input := reconcileInput{
		currentState:     state.StateStopping,
		containerExists:  true,
		containerRunning: false,
		shutdownIntent:   true,
		reason:           "normal_stop",
		err:              nil,
	}

	result := ctrl.reconcileState(input)

	if result.action != reconcileTransitionToStopped {
		t.Errorf("Expected action to be reconcileTransitionToStopped, got %v", result.action)
	}

	if !result.shouldStopMux {
		t.Error("Expected shouldStopMux to be true")
	}

	if !result.shouldClearPlayers {
		t.Error("Expected shouldClearPlayers to be true")
	}
}

func TestReconcileState_CrashedState_RemainsUnchanged(t *testing.T) {
	stateManager := state.NewManager()
	stateManager.SetCrashed(nil)

	ctrl := &Controller{
		stateManager: stateManager,
	}

	input := reconcileInput{
		currentState:     state.StateCrashed,
		containerExists:  false,
		containerRunning: false,
		shutdownIntent:   false,
		reason:           "test",
		err:              nil,
	}

	result := ctrl.reconcileState(input)

	if result.action != reconcileNoAction {
		t.Errorf("Expected action to be reconcileNoAction for Crashed state, got %v", result.action)
	}
}

func TestReconcileState_StartingWithContainerRunningAndStartedAt_TransitionsToRunning(t *testing.T) {
	stateManager := state.NewManager()
	stateManager.SetState(state.StateStarting)

	ctrl := &Controller{
		stateManager: stateManager,
	}

	containerStartedAt := time.Now().Add(-10 * time.Second)
	input := reconcileInput{
		currentState:       state.StateStarting,
		containerExists:    true,
		containerRunning:   true,
		containerStartedAt: containerStartedAt,
		shutdownIntent:     false,
		reason:             "timeout_fallback",
		err:                nil,
	}

	result := ctrl.reconcileState(input)

	if result.action != reconcileTransitionToRunning {
		t.Errorf("Expected action to be reconcileTransitionToRunning, got %v", result.action)
	}

	if result.readyDuration == 0 {
		t.Error("Expected readyDuration to be set")
	}
}

func TestReconcileState_RunningWithContainerStoppedAndShutdownIntent_TransitionsToStopped(t *testing.T) {
	stateManager := state.NewManager()
	stateManager.SetRunning(5 * time.Second)

	ctrl := &Controller{
		stateManager: stateManager,
	}

	input := reconcileInput{
		currentState:     state.StateRunning,
		containerExists:  true,
		containerRunning: false,
		shutdownIntent:   true,
		reason:           "intentional_stop",
		err:              nil,
	}

	result := ctrl.reconcileState(input)

	if result.action != reconcileTransitionToStopped {
		t.Errorf("Expected action to be reconcileTransitionToStopped, got %v", result.action)
	}
}
