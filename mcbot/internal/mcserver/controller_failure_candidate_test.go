package mcserver

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/snowy/mcbot/internal/config"
	"github.com/snowy/mcbot/internal/state"
)

func TestApplyReconcileResult_CrashedWithFailureCandidate(t *testing.T) {
	stateManager := state.NewManager()
	stateManager.SetRunning(5 * time.Second)

	candidate := &state.FailureCandidate{
		PatternName: "jvm_insufficient_memory",
		Message:     "Java 런타임에 할당할 메모리가 부족합니다.",
		DetectedAt:  time.Now(),
		RawLog:      "There is insufficient memory for the Java Runtime Environment",
	}
	stateManager.SetFailureCandidate(candidate)

	var capturedReason string
	logger := &mockLifecycleLogger{
		onServerCrashed: func(reason string, info state.Info, containerExists bool) {
			capturedReason = reason
		},
	}

	ctrl := &Controller{
		stateManager:    stateManager,
		lifecycleLogger: logger,
		logMux:          NewLogMultiplexer("test"),
		playerTracker:   &PlayerTracker{},
	}

	result := reconcileResult{
		action:             reconcileTransitionToCrashed,
		newState:           state.StateCrashed,
		shouldStopMux:      true,
		shouldClearPlayers: true,
	}

	ctrl.applyReconcileResult(result, "runtime_container_stopped", fmt.Errorf("container stopped unexpectedly"), true)

	if stateManager.GetState() != state.StateCrashed {
		t.Errorf("Expected state to be Crashed, got %s", stateManager.GetState())
	}

	if !strings.Contains(capturedReason, "jvm_insufficient_memory") {
		t.Errorf("Expected reason to contain failure pattern name, got %s", capturedReason)
	}

	if stateManager.GetFailureCandidate() != nil {
		t.Error("Expected failure candidate to be consumed after crash")
	}
}

func TestApplyReconcileResult_CrashedWithoutFailureCandidate(t *testing.T) {
	stateManager := state.NewManager()
	stateManager.SetRunning(5 * time.Second)

	var capturedReason string
	logger := &mockLifecycleLogger{
		onServerCrashed: func(reason string, info state.Info, containerExists bool) {
			capturedReason = reason
		},
	}

	ctrl := &Controller{
		stateManager:    stateManager,
		lifecycleLogger: logger,
		logMux:          NewLogMultiplexer("test"),
		playerTracker:   &PlayerTracker{},
	}

	result := reconcileResult{
		action:             reconcileTransitionToCrashed,
		newState:           state.StateCrashed,
		shouldStopMux:      true,
		shouldClearPlayers: true,
	}

	ctrl.applyReconcileResult(result, "runtime_container_stopped", fmt.Errorf("container stopped unexpectedly"), true)

	if capturedReason != "runtime_container_stopped" {
		t.Errorf("Expected reason to be 'runtime_container_stopped', got %s", capturedReason)
	}
}

func TestApplyReconcileResult_StoppedClearsFailureCandidate(t *testing.T) {
	stateManager := state.NewManager()
	stateManager.SetRunning(5 * time.Second)

	candidate := &state.FailureCandidate{
		PatternName: "test_pattern",
		Message:     "test message",
		DetectedAt:  time.Now(),
	}
	stateManager.SetFailureCandidate(candidate)

	logger := &mockLifecycleLogger{
		onServerStopped: func() {},
	}

	ctrl := &Controller{
		stateManager:    stateManager,
		lifecycleLogger: logger,
		logMux:          NewLogMultiplexer("test"),
		playerTracker:   &PlayerTracker{},
	}

	result := reconcileResult{
		action:             reconcileTransitionToStopped,
		newState:           state.StateStopped,
		shouldStopMux:      true,
		shouldClearPlayers: true,
	}

	ctrl.applyReconcileResult(result, "normal_stop", nil, true)

	if stateManager.GetState() != state.StateStopped {
		t.Errorf("Expected state to be Stopped, got %s", stateManager.GetState())
	}

	if stateManager.GetFailureCandidate() != nil {
		t.Error("Expected failure candidate to be cleared after normal stop")
	}
}

func TestApplyReconcileResult_CrashedWithExpiredFailureCandidate(t *testing.T) {
	stateManager := state.NewManager()
	stateManager.SetRunning(5 * time.Second)
	stateManager.SetFailureCandidateTTL(50 * time.Millisecond)

	candidate := &state.FailureCandidate{
		PatternName: "expired_pattern",
		Message:     "expired message",
		DetectedAt:  time.Now(),
	}
	stateManager.SetFailureCandidate(candidate)

	time.Sleep(60 * time.Millisecond)

	var capturedReason string
	logger := &mockLifecycleLogger{
		onServerCrashed: func(reason string, info state.Info, containerExists bool) {
			capturedReason = reason
		},
	}

	ctrl := &Controller{
		stateManager:    stateManager,
		lifecycleLogger: logger,
		logMux:          NewLogMultiplexer("test"),
		playerTracker:   &PlayerTracker{},
	}

	result := reconcileResult{
		action:             reconcileTransitionToCrashed,
		newState:           state.StateCrashed,
		shouldStopMux:      true,
		shouldClearPlayers: true,
	}

	ctrl.applyReconcileResult(result, "runtime_container_stopped", fmt.Errorf("container stopped unexpectedly"), true)

	if strings.Contains(capturedReason, "expired_pattern") {
		t.Errorf("Expected expired failure candidate not to be included in reason, got %s", capturedReason)
	}

	if capturedReason != "runtime_container_stopped" {
		t.Errorf("Expected reason to be 'runtime_container_stopped', got %s", capturedReason)
	}
}

func TestApplyStartupSuccess_ClearsFailureCandidate(t *testing.T) {
	cfg := &config.Config{
		MCContainerName:   "test-container",
		ReadyTimeout:      5 * time.Second,
		MCJoinLogPattern:  `(\w+) joined`,
		MCLeaveLogPattern: `(\w+) left`,
	}

	stateManager := state.NewManager()
	stateManager.SetState(state.StateStarting)

	candidate := &state.FailureCandidate{
		PatternName: "test_pattern",
		Message:     "test message",
		DetectedAt:  time.Now(),
	}
	stateManager.SetFailureCandidate(candidate)

	ctrl, err := NewController(cfg, stateManager)
	if err != nil {
		t.Fatalf("Failed to create controller: %v", err)
	}
	defer ctrl.Shutdown()

	ctrl.applyStartupSuccess(10*time.Second, 5.0)

	if stateManager.GetState() != state.StateRunning {
		t.Errorf("Expected state to be Running, got %s", stateManager.GetState())
	}

	if stateManager.GetFailureCandidate() != nil {
		t.Error("Expected failure candidate to be cleared after startup success")
	}
}

func TestApplyReconcileResult_RunningClearsFailureCandidate(t *testing.T) {
	stateManager := state.NewManager()
	stateManager.SetState(state.StateStarting)

	candidate := &state.FailureCandidate{
		PatternName: "test_pattern",
		Message:     "test message",
		DetectedAt:  time.Now(),
	}
	stateManager.SetFailureCandidate(candidate)

	logger := &mockLifecycleLogger{}

	ctrl := &Controller{
		stateManager:    stateManager,
		lifecycleLogger: logger,
		logMux:          NewLogMultiplexer("test"),
		playerTracker:   &PlayerTracker{},
	}

	result := reconcileResult{
		action:        reconcileTransitionToRunning,
		newState:      state.StateRunning,
		readyDuration: 10 * time.Second,
	}

	ctrl.applyReconcileResult(result, "timeout_fallback", nil, true)

	if stateManager.GetState() != state.StateRunning {
		t.Errorf("Expected state to be Running, got %s", stateManager.GetState())
	}

	if stateManager.GetFailureCandidate() != nil {
		t.Error("Expected failure candidate to be cleared after transition to Running")
	}
}

type mockLifecycleLogger struct {
	onServerStartRequested func()
	onServerStarted        func(readyDuration time.Duration, loadSeconds float64)
	onServerStartFailed    func(reason string, err error)
	onServerStopRequested  func()
	onServerStopped        func()
	onServerStopFailed     func(reason string, err error)
	onServerCrashed        func(reason string, info state.Info, containerExists bool)
}

func (m *mockLifecycleLogger) OnServerStartRequested() {
	if m.onServerStartRequested != nil {
		m.onServerStartRequested()
	}
}

func (m *mockLifecycleLogger) OnServerStarted(readyDuration time.Duration, loadSeconds float64) {
	if m.onServerStarted != nil {
		m.onServerStarted(readyDuration, loadSeconds)
	}
}

func (m *mockLifecycleLogger) OnServerStartFailed(reason string, err error) {
	if m.onServerStartFailed != nil {
		m.onServerStartFailed(reason, err)
	}
}

func (m *mockLifecycleLogger) OnServerStopRequested() {
	if m.onServerStopRequested != nil {
		m.onServerStopRequested()
	}
}

func (m *mockLifecycleLogger) OnServerStopped() {
	if m.onServerStopped != nil {
		m.onServerStopped()
	}
}

func (m *mockLifecycleLogger) OnServerStopFailed(reason string, err error) {
	if m.onServerStopFailed != nil {
		m.onServerStopFailed(reason, err)
	}
}

func (m *mockLifecycleLogger) OnServerCrashed(reason string, info state.Info, containerExists bool) {
	if m.onServerCrashed != nil {
		m.onServerCrashed(reason, info, containerExists)
	}
}
