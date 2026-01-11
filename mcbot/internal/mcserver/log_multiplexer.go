package mcserver

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/snowy/mcbot/internal/dockerctl"
)

type subscriber struct {
	ch        chan dockerctl.LogLine
	mu        sync.Mutex
	closed    bool
	closeOnce sync.Once
}

type LogMultiplexer struct {
	containerName string
	subscribers   map[uint64]*subscriber
	nextSubID     uint64
	mu            sync.RWMutex
	ctx           context.Context
	cancel        context.CancelFunc
	running       bool
	closed        bool
	wg            sync.WaitGroup
}

func NewLogMultiplexer(containerName string) *LogMultiplexer {
	return &LogMultiplexer{
		containerName: containerName,
		subscribers:   make(map[uint64]*subscriber),
	}
}

func (m *LogMultiplexer) Start(since time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		log.Printf("LogMultiplexer: 이미 종료되었습니다. Start 호출 무시")
		return
	}

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
		m.mu.Unlock()
	}()

	logCh := dockerctl.FollowLogs(ctx, m.containerName, since)

	for logLine := range logCh {
		m.mu.RLock()
		subs := make([]*subscriber, 0, len(m.subscribers))
		for _, sub := range m.subscribers {
			subs = append(subs, sub)
		}
		m.mu.RUnlock()

		for _, sub := range subs {
			sub.mu.Lock()
			if sub.closed {
				sub.mu.Unlock()
				continue
			}
			select {
			case sub.ch <- logLine:
			case <-ctx.Done():
				sub.mu.Unlock()
				return
			default:
			}
			sub.mu.Unlock()
		}
	}
}

type Subscription struct {
	Ch          <-chan dockerctl.LogLine
	Unsubscribe func()
}

func (m *LogMultiplexer) Subscribe() Subscription {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		ch := make(chan dockerctl.LogLine)
		close(ch)
		return Subscription{Ch: ch, Unsubscribe: func() {}}
	}

	s := &subscriber{ch: make(chan dockerctl.LogLine, 100)}
	subID := m.nextSubID
	m.nextSubID++
	m.subscribers[subID] = s

	var once sync.Once

	unsubscribe := func() {
		once.Do(func() {
			m.mu.Lock()
			if m.subscribers != nil {
				delete(m.subscribers, subID)
			}
			m.mu.Unlock()

			s.closeOnce.Do(func() {
				s.mu.Lock()
				defer s.mu.Unlock()
				if s.closed {
					return
				}
				s.closed = true
				close(s.ch)
			})
		})
	}

	return Subscription{Ch: s.ch, Unsubscribe: unsubscribe}
}

func (m *LogMultiplexer) Stop() {
	m.mu.Lock()
	if m.closed || !m.running {
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

func (m *LogMultiplexer) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	cancel := m.cancel
	isRunning := m.running
	subs := make([]*subscriber, 0, len(m.subscribers))
	for _, sub := range m.subscribers {
		subs = append(subs, sub)
	}
	m.subscribers = nil
	m.mu.Unlock()

	if isRunning && cancel != nil {
		cancel()
		m.wg.Wait()
	}

	for _, sub := range subs {
		sub.closeOnce.Do(func() {
			sub.mu.Lock()
			defer sub.mu.Unlock()
			if sub.closed {
				return
			}
			sub.closed = true
			close(sub.ch)
		})
	}
}
