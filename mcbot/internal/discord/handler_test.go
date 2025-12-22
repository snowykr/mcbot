package discord

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/snowy/mcbot/internal/config"
	"github.com/snowy/mcbot/internal/mcserver"
	"github.com/snowy/mcbot/internal/state"
)

type testServerController struct {
	startCh     chan mcserver.StartResult
	stopCh      chan mcserver.StopResult
	presenceVal mcserver.PresenceState
}

func (t *testServerController) Start(_ context.Context) <-chan mcserver.StartResult {
	return t.startCh
}

func (t *testServerController) Stop(_ context.Context) <-chan mcserver.StopResult {
	return t.stopCh
}

func (t *testServerController) Presence(_ context.Context) mcserver.PresenceState {
	return t.presenceVal
}

type testStatusEmbedUpdater struct {
	mu          sync.Mutex
	updateCalls []contextInfo
}

type contextInfo struct {
	ctx              context.Context
	hasDeadline      bool
	deadlineDuration time.Duration
	isCancelled      bool
}

func (t *testStatusEmbedUpdater) Update(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	info := contextInfo{
		ctx: ctx,
	}

	if deadline, ok := ctx.Deadline(); ok {
		info.hasDeadline = true
		info.deadlineDuration = time.Until(deadline)
	}

	select {
	case <-ctx.Done():
		info.isCancelled = true
	default:
	}

	t.updateCalls = append(t.updateCalls, info)
	return nil
}

func (t *testStatusEmbedUpdater) getUpdateCalls() []contextInfo {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]contextInfo{}, t.updateCalls...)
}

func TestHandleButtonStart_FirstUpdateUsesProvidedContext(t *testing.T) {
	cfg := &config.Config{
		EmbedUpdateTimeout:     5 * time.Second,
		ServerOperationTimeout: 10 * time.Second,
	}

	controller := &testServerController{
		startCh: make(chan mcserver.StartResult, 1),
		presenceVal: mcserver.PresenceState{
			ServerState: state.StateStopped,
		},
	}

	updater := &testStatusEmbedUpdater{}

	handler := NewHandler(cfg, controller, updater)

	ctx := context.Background()
	mockInteraction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			Member: &discordgo.Member{
				User: &discordgo.User{
					Username: "testuser",
				},
			},
		},
	}

	handler.handleButtonStart(ctx, nil, mockInteraction)

	calls := updater.getUpdateCalls()
	if len(calls) != 1 {
		t.Fatalf("Expected 1 Update call immediately, got %d", len(calls))
	}

	firstCall := calls[0]
	if firstCall.ctx != ctx {
		t.Error("First Update call should use the provided context")
	}

	if firstCall.isCancelled {
		t.Error("First Update context should not be cancelled")
	}
}

func TestHandleButtonStart_GoroutineUpdateUsesIndependentContext(t *testing.T) {
	cfg := &config.Config{
		EmbedUpdateTimeout:     5 * time.Second,
		ServerOperationTimeout: 10 * time.Second,
	}

	controller := &testServerController{
		startCh: make(chan mcserver.StartResult, 1),
		presenceVal: mcserver.PresenceState{
			ServerState: state.StateStopped,
		},
	}

	updater := &testStatusEmbedUpdater{}

	handler := NewHandler(cfg, controller, updater)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mockInteraction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:    "test-interaction-id",
			Token: "test-token",
			Member: &discordgo.Member{
				User: &discordgo.User{
					Username: "testuser",
					ID:       "test-user-id",
				},
			},
		},
	}

	goroutineDone := make(chan bool, 1)

	handler.handleButtonStart(ctx, nil, mockInteraction)

	time.Sleep(10 * time.Millisecond)

	controller.startCh <- mcserver.StartResult{
		Success:       true,
		ReadyDuration: 10 * time.Second,
	}

	go func() {
		for {
			if len(updater.getUpdateCalls()) >= 2 {
				goroutineDone <- true
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	select {
	case <-goroutineDone:
	case <-time.After(1 * time.Second):
		t.Fatal("Goroutine did not complete second update within timeout")
	}

	calls := updater.getUpdateCalls()
	if len(calls) != 2 {
		t.Fatalf("Expected 2 Update calls total, got %d", len(calls))
	}

	secondCall := calls[1]

	if secondCall.ctx == ctx {
		t.Error("Second Update call should NOT use the parent context")
	}

	if secondCall.isCancelled {
		t.Error("Second Update context should not be cancelled")
	}

	if !secondCall.hasDeadline {
		t.Error("Second Update context should have a deadline (timeout)")
	}

	if secondCall.deadlineDuration > cfg.EmbedUpdateTimeout {
		t.Errorf("Second Update context deadline is too far: %v (expected <= %v)",
			secondCall.deadlineDuration, cfg.EmbedUpdateTimeout)
	}
}

func TestHandleButtonStop_FirstUpdateUsesProvidedContext(t *testing.T) {
	cfg := &config.Config{
		EmbedUpdateTimeout:     5 * time.Second,
		ServerOperationTimeout: 10 * time.Second,
	}

	controller := &testServerController{
		stopCh: make(chan mcserver.StopResult, 1),
		presenceVal: mcserver.PresenceState{
			ServerState: state.StateRunning,
		},
	}

	updater := &testStatusEmbedUpdater{}

	handler := NewHandler(cfg, controller, updater)

	ctx := context.Background()
	mockInteraction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			Member: &discordgo.Member{
				User: &discordgo.User{
					Username: "testuser",
				},
			},
		},
	}

	handler.handleButtonStop(ctx, nil, mockInteraction)

	calls := updater.getUpdateCalls()
	if len(calls) != 1 {
		t.Fatalf("Expected 1 Update call immediately, got %d", len(calls))
	}

	firstCall := calls[0]
	if firstCall.ctx != ctx {
		t.Error("First Update call should use the provided context")
	}

	if firstCall.isCancelled {
		t.Error("First Update context should not be cancelled")
	}
}

func TestHandleButtonStop_GoroutineUpdateUsesIndependentContext(t *testing.T) {
	cfg := &config.Config{
		EmbedUpdateTimeout:     5 * time.Second,
		ServerOperationTimeout: 10 * time.Second,
	}

	controller := &testServerController{
		stopCh: make(chan mcserver.StopResult, 1),
		presenceVal: mcserver.PresenceState{
			ServerState: state.StateRunning,
		},
	}

	updater := &testStatusEmbedUpdater{}

	handler := NewHandler(cfg, controller, updater)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mockInteraction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:    "test-interaction-id",
			Token: "test-token",
			Member: &discordgo.Member{
				User: &discordgo.User{
					Username: "testuser",
					ID:       "test-user-id",
				},
			},
		},
	}

	goroutineDone := make(chan bool, 1)

	handler.handleButtonStop(ctx, nil, mockInteraction)

	time.Sleep(10 * time.Millisecond)

	controller.stopCh <- mcserver.StopResult{
		Success: true,
	}

	go func() {
		for {
			if len(updater.getUpdateCalls()) >= 2 {
				goroutineDone <- true
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	select {
	case <-goroutineDone:
	case <-time.After(1 * time.Second):
		t.Fatal("Goroutine did not complete second update within timeout")
	}

	calls := updater.getUpdateCalls()
	if len(calls) != 2 {
		t.Fatalf("Expected 2 Update calls total, got %d", len(calls))
	}

	secondCall := calls[1]

	if secondCall.ctx == ctx {
		t.Error("Second Update call should NOT use the parent context")
	}

	if secondCall.isCancelled {
		t.Error("Second Update context should not be cancelled")
	}

	if !secondCall.hasDeadline {
		t.Error("Second Update context should have a deadline (timeout)")
	}

	if secondCall.deadlineDuration > cfg.EmbedUpdateTimeout {
		t.Errorf("Second Update context deadline is too far: %v (expected <= %v)",
			secondCall.deadlineDuration, cfg.EmbedUpdateTimeout)
	}
}

func TestHandleButtonStart_GoroutineContextEventuallyTimesOut(t *testing.T) {
	cfg := &config.Config{
		EmbedUpdateTimeout:     50 * time.Millisecond,
		ServerOperationTimeout: 10 * time.Second,
	}

	controller := &testServerController{
		startCh: make(chan mcserver.StartResult, 1),
		presenceVal: mcserver.PresenceState{
			ServerState: state.StateStopped,
		},
	}

	updater := &testStatusEmbedUpdater{}

	handler := NewHandler(cfg, controller, updater)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mockInteraction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:    "test-interaction-id",
			Token: "test-token",
			Member: &discordgo.Member{
				User: &discordgo.User{
					Username: "testuser",
					ID:       "test-user-id",
				},
			},
		},
	}

	goroutineDone := make(chan bool, 1)

	handler.handleButtonStart(ctx, nil, mockInteraction)

	time.Sleep(10 * time.Millisecond)

	controller.startCh <- mcserver.StartResult{
		Success:       true,
		ReadyDuration: 10 * time.Second,
	}

	go func() {
		for {
			if len(updater.getUpdateCalls()) >= 2 {
				goroutineDone <- true
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	select {
	case <-goroutineDone:
	case <-time.After(1 * time.Second):
		t.Fatal("Goroutine did not complete within timeout")
	}

	calls := updater.getUpdateCalls()
	if len(calls) != 2 {
		t.Fatalf("Expected 2 Update calls, got %d", len(calls))
	}

	goroutineCtx := calls[1].ctx

	time.Sleep(100 * time.Millisecond)

	select {
	case <-goroutineCtx.Done():
	default:
		t.Error("Goroutine context should be cancelled after timeout")
	}

	if goroutineCtx.Err() == nil {
		t.Error("Goroutine context should have an error after timeout")
	}
}

func TestHandleButtonStart_TimeoutWhenChannelNeverSends(t *testing.T) {
	cfg := &config.Config{
		EmbedUpdateTimeout:     5 * time.Second,
		ServerOperationTimeout: 100 * time.Millisecond,
	}

	controller := &testServerController{
		startCh: make(chan mcserver.StartResult),
		presenceVal: mcserver.PresenceState{
			ServerState: state.StateStopped,
		},
	}

	updater := &testStatusEmbedUpdater{}
	handler := NewHandler(cfg, controller, updater)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	mockInteraction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:    "test-interaction-id",
			Token: "test-token",
			Member: &discordgo.Member{
				User: &discordgo.User{
					Username: "testuser",
					ID:       "test-user-id",
				},
			},
		},
	}

	goroutineDone := make(chan bool, 1)

	handler.handleButtonStart(ctx, nil, mockInteraction)

	go func() {
		for {
			if len(updater.getUpdateCalls()) >= 2 {
				goroutineDone <- true
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	select {
	case <-goroutineDone:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("handleButtonStart goroutine did not complete within timeout")
	}

	calls := updater.getUpdateCalls()
	if len(calls) != 2 {
		t.Fatalf("Expected 2 Update calls (initial + after timeout), got %d", len(calls))
	}
}

func TestHandleButtonStop_TimeoutWhenChannelNeverSends(t *testing.T) {
	cfg := &config.Config{
		EmbedUpdateTimeout:     5 * time.Second,
		ServerOperationTimeout: 100 * time.Millisecond,
	}

	controller := &testServerController{
		stopCh: make(chan mcserver.StopResult),
		presenceVal: mcserver.PresenceState{
			ServerState: state.StateRunning,
		},
	}

	updater := &testStatusEmbedUpdater{}
	handler := NewHandler(cfg, controller, updater)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	mockInteraction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:    "test-interaction-id",
			Token: "test-token",
			Member: &discordgo.Member{
				User: &discordgo.User{
					Username: "testuser",
					ID:       "test-user-id",
				},
			},
		},
	}

	goroutineDone := make(chan bool, 1)

	handler.handleButtonStop(ctx, nil, mockInteraction)

	go func() {
		for {
			if len(updater.getUpdateCalls()) >= 2 {
				goroutineDone <- true
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	select {
	case <-goroutineDone:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("handleButtonStop goroutine did not complete within timeout")
	}

	calls := updater.getUpdateCalls()
	if len(calls) != 2 {
		t.Fatalf("Expected 2 Update calls (initial + after timeout), got %d", len(calls))
	}
}

func TestHandleButtonStart_ChannelClosedWithoutValue(t *testing.T) {
	cfg := &config.Config{
		EmbedUpdateTimeout:     5 * time.Second,
		ServerOperationTimeout: 10 * time.Second,
	}

	controller := &testServerController{
		startCh: make(chan mcserver.StartResult),
		presenceVal: mcserver.PresenceState{
			ServerState: state.StateStopped,
		},
	}

	updater := &testStatusEmbedUpdater{}
	handler := NewHandler(cfg, controller, updater)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mockInteraction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:    "test-interaction-id",
			Token: "test-token",
			Member: &discordgo.Member{
				User: &discordgo.User{
					Username: "testuser",
					ID:       "test-user-id",
				},
			},
		},
	}

	goroutineDone := make(chan bool, 1)

	handler.handleButtonStart(ctx, nil, mockInteraction)

	time.Sleep(10 * time.Millisecond)
	close(controller.startCh)

	go func() {
		for {
			if len(updater.getUpdateCalls()) >= 2 {
				goroutineDone <- true
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	select {
	case <-goroutineDone:
	case <-time.After(1 * time.Second):
		t.Fatal("handleButtonStart goroutine did not complete after channel close")
	}

	calls := updater.getUpdateCalls()
	if len(calls) != 2 {
		t.Fatalf("Expected 2 Update calls, got %d", len(calls))
	}
}

func TestHandleButtonStop_ChannelClosedWithoutValue(t *testing.T) {
	cfg := &config.Config{
		EmbedUpdateTimeout:     5 * time.Second,
		ServerOperationTimeout: 10 * time.Second,
	}

	controller := &testServerController{
		stopCh: make(chan mcserver.StopResult),
		presenceVal: mcserver.PresenceState{
			ServerState: state.StateRunning,
		},
	}

	updater := &testStatusEmbedUpdater{}
	handler := NewHandler(cfg, controller, updater)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mockInteraction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:    "test-interaction-id",
			Token: "test-token",
			Member: &discordgo.Member{
				User: &discordgo.User{
					Username: "testuser",
					ID:       "test-user-id",
				},
			},
		},
	}

	goroutineDone := make(chan bool, 1)

	handler.handleButtonStop(ctx, nil, mockInteraction)

	time.Sleep(10 * time.Millisecond)
	close(controller.stopCh)

	go func() {
		for {
			if len(updater.getUpdateCalls()) >= 2 {
				goroutineDone <- true
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	select {
	case <-goroutineDone:
	case <-time.After(1 * time.Second):
		t.Fatal("handleButtonStop goroutine did not complete after channel close")
	}

	calls := updater.getUpdateCalls()
	if len(calls) != 2 {
		t.Fatalf("Expected 2 Update calls, got %d", len(calls))
	}
}
