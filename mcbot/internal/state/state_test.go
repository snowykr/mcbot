package state

import (
	"fmt"
	"testing"
	"time"
)

func TestTryStartTransition(t *testing.T) {
	tests := []struct {
		name          string
		initialState  ServerState
		expectError   bool
		expectedMsg   string
		expectedState ServerState
	}{
		{
			name:          "Start from Stopped succeeds",
			initialState:  StateStopped,
			expectError:   false,
			expectedState: StateStarting,
		},
		{
			name:          "Start from Error succeeds",
			initialState:  StateError,
			expectError:   false,
			expectedState: StateStarting,
		},
		{
			name:          "Start from Starting fails",
			initialState:  StateStarting,
			expectError:   true,
			expectedMsg:   "서버가 이미 시작 중입니다.",
			expectedState: StateStarting,
		},
		{
			name:          "Start from Running fails",
			initialState:  StateRunning,
			expectError:   true,
			expectedMsg:   "서버가 이미 실행 중입니다.",
			expectedState: StateRunning,
		},
		{
			name:          "Start from Stopping fails",
			initialState:  StateStopping,
			expectError:   true,
			expectedMsg:   "서버가 종료 중입니다. 종료가 완료된 후 다시 시도해주세요.",
			expectedState: StateStopping,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewManager()
			m.SetState(tt.initialState)

			err := m.TryStartTransition()

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error, got nil")
				} else if err.Error() != tt.expectedMsg {
					t.Errorf("Expected error message %q, got %q", tt.expectedMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error, got %v", err)
				}
			}

			finalState := m.GetState()
			if finalState != tt.expectedState {
				t.Errorf("Expected final state %s, got %s", tt.expectedState, finalState)
			}
		})
	}
}

func TestTryStopTransition(t *testing.T) {
	tests := []struct {
		name          string
		initialState  ServerState
		expectError   bool
		expectedMsg   string
		expectedState ServerState
	}{
		{
			name:          "Stop from Running succeeds",
			initialState:  StateRunning,
			expectError:   false,
			expectedState: StateStopping,
		},
		{
			name:          "Stop from Error succeeds",
			initialState:  StateError,
			expectError:   false,
			expectedState: StateStopping,
		},
		{
			name:          "Stop from Stopping fails",
			initialState:  StateStopping,
			expectError:   true,
			expectedMsg:   "서버가 이미 종료 중입니다.",
			expectedState: StateStopping,
		},
		{
			name:          "Stop from Stopped fails",
			initialState:  StateStopped,
			expectError:   true,
			expectedMsg:   "서버가 이미 종료되어 있습니다.",
			expectedState: StateStopped,
		},
		{
			name:          "Stop from Starting fails",
			initialState:  StateStarting,
			expectError:   true,
			expectedMsg:   "서버가 시작 중입니다. 시작이 완료된 후 다시 시도해주세요.",
			expectedState: StateStarting,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := NewManager()
			m.SetState(tt.initialState)

			err := m.TryStopTransition()

			if tt.expectError {
				if err == nil {
					t.Errorf("Expected error, got nil")
				} else if err.Error() != tt.expectedMsg {
					t.Errorf("Expected error message %q, got %q", tt.expectedMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error, got %v", err)
				}
			}

			finalState := m.GetState()
			if finalState != tt.expectedState {
				t.Errorf("Expected final state %s, got %s", tt.expectedState, finalState)
			}
		})
	}
}

func TestConcurrentStartTransitions(t *testing.T) {
	m := NewManager()

	const numGoroutines = 10
	resultCh := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			err := m.TryStartTransition()
			resultCh <- err
		}()
	}

	successCount := 0
	errorCount := 0
	for i := 0; i < numGoroutines; i++ {
		err := <-resultCh
		if err == nil {
			successCount++
		} else {
			errorCount++
		}
	}

	if successCount != 1 {
		t.Errorf("Expected exactly 1 successful transition, got %d", successCount)
	}

	if errorCount != 9 {
		t.Errorf("Expected 9 failed transitions, got %d", errorCount)
	}

	finalState := m.GetState()
	if finalState != StateStarting {
		t.Errorf("Expected final state Starting, got %s", finalState)
	}
}

func TestConcurrentStopTransitions(t *testing.T) {
	m := NewManager()
	m.SetState(StateRunning)

	const numGoroutines = 10
	resultCh := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			err := m.TryStopTransition()
			resultCh <- err
		}()
	}

	successCount := 0
	errorCount := 0
	for i := 0; i < numGoroutines; i++ {
		err := <-resultCh
		if err == nil {
			successCount++
		} else {
			errorCount++
		}
	}

	if successCount != 1 {
		t.Errorf("Expected exactly 1 successful transition, got %d", successCount)
	}

	if errorCount != 9 {
		t.Errorf("Expected 9 failed transitions, got %d", errorCount)
	}

	finalState := m.GetState()
	if finalState != StateStopping {
		t.Errorf("Expected final state Stopping, got %s", finalState)
	}
}

func TestSetStoppedWithError(t *testing.T) {
	m := NewManager()
	m.SetState(StateRunning)

	testErr := fmt.Errorf("container not found")
	m.SetStoppedWithError(testErr)

	if m.GetState() != StateStopped {
		t.Errorf("Expected state Stopped, got %s", m.GetState())
	}

	if m.GetLastError() != testErr {
		t.Errorf("Expected lastError to be %v, got %v", testErr, m.GetLastError())
	}
}

func TestSetStoppedWithErrorFromError(t *testing.T) {
	m := NewManager()
	m.SetError(fmt.Errorf("previous error"))

	if m.GetState() != StateError {
		t.Errorf("Expected initial state Error, got %s", m.GetState())
	}

	newErr := fmt.Errorf("container not found")
	m.SetStoppedWithError(newErr)

	if m.GetState() != StateStopped {
		t.Errorf("Expected state Stopped after SetStoppedWithError, got %s", m.GetState())
	}

	if m.GetLastError() != newErr {
		t.Errorf("Expected lastError to be updated to %v, got %v", newErr, m.GetLastError())
	}
}

func TestFailureCandidate_SetAndGet(t *testing.T) {
	m := NewManager()

	if m.GetFailureCandidate() != nil {
		t.Error("Expected nil failure candidate initially")
	}

	candidate := &FailureCandidate{
		PatternName: "jvm_insufficient_memory",
		Message:     "Java 런타임에 할당할 메모리가 부족합니다.",
		DetectedAt:  time.Now(),
		RawLog:      "There is insufficient memory for the Java Runtime Environment",
	}
	m.SetFailureCandidate(candidate)

	got := m.GetFailureCandidate()
	if got == nil {
		t.Fatal("Expected non-nil failure candidate")
	}
	if got.PatternName != candidate.PatternName {
		t.Errorf("Expected PatternName %s, got %s", candidate.PatternName, got.PatternName)
	}
	if got.Message != candidate.Message {
		t.Errorf("Expected Message %s, got %s", candidate.Message, got.Message)
	}
}

func TestFailureCandidate_TTLExpiration(t *testing.T) {
	m := NewManager()
	m.SetFailureCandidateTTL(50 * time.Millisecond)

	candidate := &FailureCandidate{
		PatternName: "test_pattern",
		Message:     "test message",
		DetectedAt:  time.Now(),
	}
	m.SetFailureCandidate(candidate)

	if m.GetFailureCandidate() == nil {
		t.Error("Expected non-nil failure candidate before TTL expiration")
	}

	time.Sleep(60 * time.Millisecond)

	if m.GetFailureCandidate() != nil {
		t.Error("Expected nil failure candidate after TTL expiration")
	}
}

func TestFailureCandidate_NewestOverwrite(t *testing.T) {
	m := NewManager()

	first := &FailureCandidate{
		PatternName: "first_pattern",
		Message:     "first message",
		DetectedAt:  time.Now(),
	}
	m.SetFailureCandidate(first)

	second := &FailureCandidate{
		PatternName: "second_pattern",
		Message:     "second message",
		DetectedAt:  time.Now(),
	}
	m.SetFailureCandidate(second)

	got := m.GetFailureCandidate()
	if got == nil {
		t.Fatal("Expected non-nil failure candidate")
	}
	if got.PatternName != "second_pattern" {
		t.Errorf("Expected newest pattern 'second_pattern', got %s", got.PatternName)
	}
}

func TestFailureCandidate_ClearFailureCandidate(t *testing.T) {
	m := NewManager()

	candidate := &FailureCandidate{
		PatternName: "test_pattern",
		Message:     "test message",
		DetectedAt:  time.Now(),
	}
	m.SetFailureCandidate(candidate)

	if m.GetFailureCandidate() == nil {
		t.Error("Expected non-nil failure candidate before clear")
	}

	m.ClearFailureCandidate()

	if m.GetFailureCandidate() != nil {
		t.Error("Expected nil failure candidate after clear")
	}
}

func TestFailureCandidate_ConsumeFailureCandidate(t *testing.T) {
	m := NewManager()

	candidate := &FailureCandidate{
		PatternName: "test_pattern",
		Message:     "test message",
		DetectedAt:  time.Now(),
	}
	m.SetFailureCandidate(candidate)

	consumed := m.ConsumeFailureCandidate()
	if consumed == nil {
		t.Fatal("Expected non-nil consumed failure candidate")
	}
	if consumed.PatternName != "test_pattern" {
		t.Errorf("Expected PatternName 'test_pattern', got %s", consumed.PatternName)
	}

	if m.GetFailureCandidate() != nil {
		t.Error("Expected nil failure candidate after consume")
	}
}

func TestFailureCandidate_ConsumeAfterTTLExpiration(t *testing.T) {
	m := NewManager()
	m.SetFailureCandidateTTL(50 * time.Millisecond)

	candidate := &FailureCandidate{
		PatternName: "test_pattern",
		Message:     "test message",
		DetectedAt:  time.Now(),
	}
	m.SetFailureCandidate(candidate)

	time.Sleep(60 * time.Millisecond)

	consumed := m.ConsumeFailureCandidate()
	if consumed != nil {
		t.Error("Expected nil consumed failure candidate after TTL expiration")
	}
}

func TestFailureCandidate_IncludedInInfo(t *testing.T) {
	m := NewManager()

	candidate := &FailureCandidate{
		PatternName: "test_pattern",
		Message:     "test message",
		DetectedAt:  time.Now(),
	}
	m.SetFailureCandidate(candidate)

	info := m.GetInfo()
	if info.FailureCandidate == nil {
		t.Fatal("Expected FailureCandidate in Info")
	}
	if info.FailureCandidate.PatternName != "test_pattern" {
		t.Errorf("Expected PatternName 'test_pattern' in Info, got %s", info.FailureCandidate.PatternName)
	}
}
