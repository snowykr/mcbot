package state

import (
	"fmt"
	"sync"
	"time"

	"github.com/snowy/mcbot/internal/logutil"
)

type ServerState int

const (
	StateStopped ServerState = iota
	StateStarting
	StateRunning
	StateStopping
	StateError
	StateCrashed
)

func (s ServerState) String() string {
	switch s {
	case StateStopped:
		return "stopped"
	case StateStarting:
		return "starting"
	case StateRunning:
		return "running"
	case StateStopping:
		return "stopping"
	case StateError:
		return "error"
	case StateCrashed:
		return "crashed"
	default:
		return "unknown"
	}
}

func (s ServerState) Korean() string {
	switch s {
	case StateStopped:
		return "종료됨"
	case StateStarting:
		return "시작 중"
	case StateRunning:
		return "실행 중"
	case StateStopping:
		return "종료 중"
	case StateError:
		return "오류"
	case StateCrashed:
		return "종료됨(크래시)"
	default:
		return "알 수 없음"
	}
}

// FailureCandidate represents a detected failure pattern that may explain a crash.
// This is stored as a "candidate" until the container actually stops, at which point
// it becomes the crash reason. TTL-based expiration prevents stale candidates from
// being incorrectly attributed to later crashes.
type FailureCandidate struct {
	PatternName string
	Message     string
	DetectedAt  time.Time
	RawLog      string
}

// DefaultFailureCandidateTTL is the default time-to-live for failure candidates.
// If the container doesn't crash within this duration, the candidate is considered stale.
const DefaultFailureCandidateTTL = 2 * time.Minute

type Manager struct {
	mu                  sync.RWMutex
	state               ServerState
	lastStartTime       time.Time
	lastReadyDuration   time.Duration
	lastError           error
	failureCandidate    *FailureCandidate
	failureCandidateTTL time.Duration
}

func NewManager() *Manager {
	return &Manager{
		state:               StateStopped,
		failureCandidateTTL: DefaultFailureCandidateTTL,
	}
}

func (m *Manager) GetState() ServerState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state
}

func (m *Manager) SetState(s ServerState) {
	m.mu.Lock()
	defer m.mu.Unlock()
	oldState := m.state
	m.state = s
	logutil.Debugf("[STATE] SetState: %s -> %s", oldState.Korean(), m.state.Korean())
}

func (m *Manager) TryTransition(from, to ServerState) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state != from {
		return false
	}
	m.state = to
	return true
}

type TransitionError struct {
	CurrentState ServerState
	Message      string
}

func (e *TransitionError) Error() string {
	return e.Message
}

func (m *Manager) TryStartTransition() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	switch m.state {
	case StateStarting:
		return &TransitionError{
			CurrentState: m.state,
			Message:      "서버가 이미 시작 중입니다.",
		}
	case StateRunning:
		return &TransitionError{
			CurrentState: m.state,
			Message:      "서버가 이미 실행 중입니다.",
		}
	case StateStopping:
		return &TransitionError{
			CurrentState: m.state,
			Message:      "서버가 종료 중입니다. 종료가 완료된 후 다시 시도해주세요.",
		}
	case StateStopped, StateError, StateCrashed:
		m.state = StateStarting
		m.lastStartTime = time.Now()
		m.lastError = nil
		return nil
	default:
		return &TransitionError{
			CurrentState: m.state,
			Message:      "서버 상태가 변경되었습니다. 다시 시도해주세요.",
		}
	}
}

func (m *Manager) SetStarting() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state != StateStopped && m.state != StateError && m.state != StateCrashed {
		return false
	}
	m.state = StateStarting
	m.lastStartTime = time.Now()
	m.lastError = nil
	return true
}

func (m *Manager) SetRunning(readyDuration time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	oldState := m.state
	m.state = StateRunning
	m.lastReadyDuration = readyDuration
	logutil.Debugf("[STATE] SetRunning: %s -> %s (duration: %v)", oldState.Korean(), m.state.Korean(), readyDuration)
}

func (m *Manager) TryStopTransition() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	switch m.state {
	case StateStopping:
		return &TransitionError{
			CurrentState: m.state,
			Message:      "서버가 이미 종료 중입니다.",
		}
	case StateStopped:
		return &TransitionError{
			CurrentState: m.state,
			Message:      "서버가 이미 종료되어 있습니다.",
		}
	case StateStarting:
		return &TransitionError{
			CurrentState: m.state,
			Message:      "서버가 시작 중입니다. 시작이 완료된 후 다시 시도해주세요.",
		}
	case StateRunning, StateError, StateCrashed:
		m.state = StateStopping
		return nil
	default:
		return &TransitionError{
			CurrentState: m.state,
			Message:      fmt.Sprintf("서버를 종료할 수 없습니다. 현재 상태: %s", m.state.Korean()),
		}
	}
}

func (m *Manager) SetStopping() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state != StateRunning {
		return false
	}
	m.state = StateStopping
	return true
}

func (m *Manager) SetStopped() {
	m.mu.Lock()
	defer m.mu.Unlock()
	oldState := m.state
	m.state = StateStopped
	logutil.Debugf("[STATE] SetStopped: %s -> %s", oldState.Korean(), m.state.Korean())
}

func (m *Manager) SetStoppedWithError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = StateStopped
	m.lastError = err
}

func (m *Manager) SetError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = StateError
	m.lastError = err
}

func (m *Manager) SetCrashed(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	oldState := m.state
	m.state = StateCrashed
	m.lastError = err
	logutil.Infof("[STATE] SetCrashed: %s -> %s (err: %v)", oldState.Korean(), m.state.Korean(), err)
}

func (m *Manager) GetLastStartTime() time.Time {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastStartTime
}

func (m *Manager) GetLastReadyDuration() time.Duration {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastReadyDuration
}

func (m *Manager) GetLastError() error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.lastError
}

type Info struct {
	State             ServerState
	LastStartTime     time.Time
	LastReadyDuration time.Duration
	LastError         error
	FailureCandidate  *FailureCandidate
}

func (m *Manager) GetInfo() Info {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return Info{
		State:             m.state,
		LastStartTime:     m.lastStartTime,
		LastReadyDuration: m.lastReadyDuration,
		LastError:         m.lastError,
		FailureCandidate:  m.getValidFailureCandidateLocked(),
	}
}

func (m *Manager) SetFailureCandidate(candidate *FailureCandidate) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failureCandidate = candidate
	logutil.Debugf("[STATE] SetFailureCandidate: pattern=%s message=%s", candidate.PatternName, candidate.Message)
}

func (m *Manager) GetFailureCandidate() *FailureCandidate {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.getValidFailureCandidateLocked()
}

func (m *Manager) getValidFailureCandidateLocked() *FailureCandidate {
	if m.failureCandidate == nil {
		return nil
	}
	if time.Since(m.failureCandidate.DetectedAt) > m.failureCandidateTTL {
		return nil
	}
	return m.failureCandidate
}

func (m *Manager) ClearFailureCandidate() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failureCandidate != nil {
		logutil.Debugf("[STATE] ClearFailureCandidate: cleared pattern=%s", m.failureCandidate.PatternName)
	}
	m.failureCandidate = nil
}

func (m *Manager) ConsumeFailureCandidate() *FailureCandidate {
	m.mu.Lock()
	defer m.mu.Unlock()
	candidate := m.getValidFailureCandidateLocked()
	m.failureCandidate = nil
	return candidate
}

func (m *Manager) SetFailureCandidateTTL(ttl time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.failureCandidateTTL = ttl
}
