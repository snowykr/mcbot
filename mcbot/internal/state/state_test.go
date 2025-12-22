package state

import (
	"testing"
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
