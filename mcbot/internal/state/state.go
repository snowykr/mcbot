package state

import (
	"sync"
	"time"
)

type ServerState int

const (
	StateStopped ServerState = iota
	StateStarting
	StateRunning
	StateStopping
	StateError
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
	default:
		return "알 수 없음"
	}
}

type Manager struct {
	mu                sync.RWMutex
	state             ServerState
	lastStartTime     time.Time
	lastReadyDuration time.Duration
	lastError         error
}

func NewManager() *Manager {
	return &Manager{
		state: StateStopped,
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
	m.state = s
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

func (m *Manager) SetStarting() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state != StateStopped && m.state != StateError {
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
	m.state = StateRunning
	m.lastReadyDuration = readyDuration
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
	m.state = StateStopped
}

func (m *Manager) SetError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = StateError
	m.lastError = err
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
}

func (m *Manager) GetInfo() Info {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return Info{
		State:             m.state,
		LastStartTime:     m.lastStartTime,
		LastReadyDuration: m.lastReadyDuration,
		LastError:         m.lastError,
	}
}
