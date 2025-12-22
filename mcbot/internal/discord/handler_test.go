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
		EmbedUpdateTimeout: 5 * time.Second,
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
		EmbedUpdateTimeout: 5 * time.Second,
	}

	controller := &testServerController{
		startCh: make(chan mcserver.StartResult, 1),
		presenceVal: mcserver.PresenceState{
			ServerState: state.StateStopped,
		},
	}

	updater := &testStatusEmbedUpdater{}

	handler := NewHandler(cfg, controller, updater)

	ctx, cancel := context.WithCancel(context.Background())
	mockSession := &discordgo.Session{}
	mockInteraction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:    "test-interaction-id",
			Token: "test-token",
			Member: &discordgo.Member{
				User: &discordgo.User{
					Username: "testuser",
				},
			},
		},
	}

	done := make(chan bool, 1)

	testHandler := func(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) {
		resultCh := handler.controller.Start(ctx)

		if err := handler.statusEmbed.Update(ctx); err != nil {
			t.Logf("상태 임베드 업데이트 실패: %v", err)
		}

		go func() {
			defer func() { done <- true }()
			result := <-resultCh

			updateCtx, updateCancel := context.WithTimeout(context.Background(), handler.cfg.EmbedUpdateTimeout)
			defer updateCancel()

			if err := handler.statusEmbed.Update(updateCtx); err != nil {
				t.Logf("서버 시작 후 상태 임베드 업데이트 실패: %v", err)
			}

			_ = result
		}()
	}

	testHandler(ctx, mockSession, mockInteraction)

	cancel()

	controller.startCh <- mcserver.StartResult{
		Success:       true,
		ReadyDuration: 10 * time.Second,
	}

	<-done

	calls := updater.getUpdateCalls()
	if len(calls) != 2 {
		t.Fatalf("Expected 2 Update calls total, got %d", len(calls))
	}

	secondCall := calls[1]

	if secondCall.ctx == ctx {
		t.Error("Second Update call should NOT use the cancelled parent context")
	}

	if secondCall.isCancelled {
		t.Error("Second Update context should not be cancelled immediately after parent cancellation")
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
		EmbedUpdateTimeout: 5 * time.Second,
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
		EmbedUpdateTimeout: 5 * time.Second,
	}

	controller := &testServerController{
		stopCh: make(chan mcserver.StopResult, 1),
		presenceVal: mcserver.PresenceState{
			ServerState: state.StateRunning,
		},
	}

	updater := &testStatusEmbedUpdater{}

	handler := NewHandler(cfg, controller, updater)

	ctx, cancel := context.WithCancel(context.Background())
	mockSession := &discordgo.Session{}
	mockInteraction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:    "test-interaction-id",
			Token: "test-token",
			Member: &discordgo.Member{
				User: &discordgo.User{
					Username: "testuser",
				},
			},
		},
	}

	done := make(chan bool, 1)

	testHandler := func(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) {
		resultCh := handler.controller.Stop(ctx)

		if err := handler.statusEmbed.Update(ctx); err != nil {
			t.Logf("상태 임베드 업데이트 실패: %v", err)
		}

		go func() {
			defer func() { done <- true }()
			result := <-resultCh

			updateCtx, updateCancel := context.WithTimeout(context.Background(), handler.cfg.EmbedUpdateTimeout)
			defer updateCancel()

			if err := handler.statusEmbed.Update(updateCtx); err != nil {
				t.Logf("서버 종료 후 상태 임베드 업데이트 실패: %v", err)
			}

			_ = result
		}()
	}

	testHandler(ctx, mockSession, mockInteraction)

	cancel()

	controller.stopCh <- mcserver.StopResult{
		Success: true,
	}

	<-done

	calls := updater.getUpdateCalls()
	if len(calls) != 2 {
		t.Fatalf("Expected 2 Update calls total, got %d", len(calls))
	}

	secondCall := calls[1]

	if secondCall.ctx == ctx {
		t.Error("Second Update call should NOT use the cancelled parent context")
	}

	if secondCall.isCancelled {
		t.Error("Second Update context should not be cancelled immediately after parent cancellation")
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
		EmbedUpdateTimeout: 50 * time.Millisecond,
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
	mockSession := &discordgo.Session{}
	mockInteraction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:    "test-interaction-id",
			Token: "test-token",
			Member: &discordgo.Member{
				User: &discordgo.User{
					Username: "testuser",
				},
			},
		},
	}

	done := make(chan bool, 1)

	testHandler := func(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) {
		resultCh := handler.controller.Start(ctx)

		if err := handler.statusEmbed.Update(ctx); err != nil {
			t.Logf("상태 임베드 업데이트 실패: %v", err)
		}

		go func() {
			defer func() { done <- true }()
			result := <-resultCh

			updateCtx, updateCancel := context.WithTimeout(context.Background(), handler.cfg.EmbedUpdateTimeout)
			defer updateCancel()

			if err := handler.statusEmbed.Update(updateCtx); err != nil {
				t.Logf("서버 시작 후 상태 임베드 업데이트 실패: %v", err)
			}

			_ = result
		}()
	}

	testHandler(ctx, mockSession, mockInteraction)

	controller.startCh <- mcserver.StartResult{
		Success:       true,
		ReadyDuration: 10 * time.Second,
	}

	<-done

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
