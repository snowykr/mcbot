package mcserver

import (
	"context"
	"sync"
	"time"

	"github.com/snowy/mcbot/internal/dockerctl"
)

type LogMultiplexer struct {
	containerName string
	subscribers   []chan dockerctl.LogLine
	mu            sync.RWMutex
	ctx           context.Context
	cancel        context.CancelFunc
	started       bool
	stopped       bool
	startOnce     sync.Once
	wg            sync.WaitGroup
}

func NewLogMultiplexer(containerName string) *LogMultiplexer {
	ctx, cancel := context.WithCancel(context.Background())
	return &LogMultiplexer{
		containerName: containerName,
		subscribers:   make([]chan dockerctl.LogLine, 0),
		ctx:           ctx,
		cancel:        cancel,
	}
}

func (m *LogMultiplexer) Start(since time.Time) {
	m.startOnce.Do(func() {
		m.mu.Lock()
		if m.stopped {
			m.mu.Unlock()
			return
		}
		m.started = true
		m.mu.Unlock()

		m.wg.Add(1)
		go m.run(since)
	})
}

func (m *LogMultiplexer) run(since time.Time) {
	defer m.wg.Done()
	defer m.closeAllSubscribers()

	logCh := dockerctl.FollowLogs(m.ctx, m.containerName, since)

	for logLine := range logCh {
		m.mu.RLock()
		subs := make([]chan dockerctl.LogLine, len(m.subscribers))
		copy(subs, m.subscribers)
		m.mu.RUnlock()

		for _, sub := range subs {
			select {
			case sub <- logLine:
			case <-m.ctx.Done():
				return
			default:
			}
		}
	}
}

func (m *LogMultiplexer) Subscribe() <-chan dockerctl.LogLine {
	m.mu.Lock()
	defer m.mu.Unlock()

	ch := make(chan dockerctl.LogLine, 100)

	if m.stopped {
		close(ch)
		return ch
	}

	m.subscribers = append(m.subscribers, ch)
	return ch
}

func (m *LogMultiplexer) Stop() {
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return
	}
	m.stopped = true
	m.mu.Unlock()

	m.cancel()
	m.wg.Wait()
}

func (m *LogMultiplexer) closeAllSubscribers() {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, sub := range m.subscribers {
		close(sub)
	}
	m.subscribers = nil
}
