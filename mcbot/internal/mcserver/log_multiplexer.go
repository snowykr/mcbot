package mcserver

import (
	"context"
	"log"
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
	running       bool
	wg            sync.WaitGroup
}

func NewLogMultiplexer(containerName string) *LogMultiplexer {
	return &LogMultiplexer{
		containerName: containerName,
		subscribers:   make([]chan dockerctl.LogLine, 0),
	}
}

func (m *LogMultiplexer) Start(since time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.running {
		log.Printf("LogMultiplexer: 이미 실행 중입니다. Start 호출 무시")
		return
	}

	m.ctx, m.cancel = context.WithCancel(context.Background())
	m.running = true

	m.wg.Add(1)
	go m.run(m.ctx, since)
}

func (m *LogMultiplexer) run(ctx context.Context, since time.Time) {
	defer m.wg.Done()
	defer func() {
		m.mu.Lock()
		m.running = false
		for _, sub := range m.subscribers {
			close(sub)
		}
		m.subscribers = nil
		m.mu.Unlock()
	}()

	logCh := dockerctl.FollowLogs(ctx, m.containerName, since)

	for logLine := range logCh {
		m.mu.RLock()
		subs := make([]chan dockerctl.LogLine, len(m.subscribers))
		copy(subs, m.subscribers)
		m.mu.RUnlock()

		for _, sub := range subs {
			select {
			case sub <- logLine:
			case <-ctx.Done():
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
	m.subscribers = append(m.subscribers, ch)
	return ch
}

func (m *LogMultiplexer) Stop() {
	m.mu.Lock()
	if !m.running {
		m.mu.Unlock()
		return
	}
	cancel := m.cancel
	m.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	m.wg.Wait()
}
