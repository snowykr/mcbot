package mcserver

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/snowy/mcbot/internal/config"
	"github.com/snowy/mcbot/internal/dockerctl"
	"github.com/snowy/mcbot/internal/state"
)

func TestContainerWatchLoopSyncsExternalStart(t *testing.T) {
	stateManager := state.NewManager()
	controller, err := NewController(&config.Config{
		MCContainerName:        "test-mc",
		CrashDetectionInterval: 5 * time.Millisecond,
		ReadyTimeout:           time.Second,
	}, stateManager)
	if err != nil {
		t.Fatalf("NewController failed: %v", err)
	}
	defer controller.Shutdown()
	stateManager.SetRunning(0)
	store := &singleUseExternalStopIntent{matched: true}
	controller.SetExternalStopIntentStore(store)

	startedAt := time.Now()
	var inspectCalls atomic.Int32
	controller.inspectContainer = func(context.Context, string) (*dockerctl.ContainerState, error) {
		if inspectCalls.Add(1) == 1 {
			return &dockerctl.ContainerState{Exists: true, Running: false, Status: "exited"}, nil
		}
		return &dockerctl.ContainerState{Exists: true, Running: true, Status: "running", StartedAt: startedAt}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	controller.watchersWg.Add(1)
	go controller.containerWatchLoop(ctx, make(chan struct{}))

	for (inspectCalls.Load() < 3 || (stateManager.GetState() != state.StateStarting && stateManager.GetState() != state.StateRunning)) && ctx.Err() == nil {
		time.Sleep(time.Millisecond)
	}
	if got := stateManager.GetState(); got != state.StateStarting && got != state.StateRunning {
		t.Fatalf("state after external start = %v, want Starting or Running", got)
	}
	if store.calls == 0 {
		t.Fatal("external stop intent was not consumed")
	}
	if got := inspectCalls.Load(); got < 3 {
		t.Fatalf("inspect calls = %d, want at least 3 for stop, start, and sync", got)
	}
	cancel()
	controller.watchersWg.Wait()
}

func TestContainerWatchLoopSyncsRapidExternalRestart(t *testing.T) {
	stateManager := state.NewManager()
	controller, err := NewController(&config.Config{
		MCContainerName:        "test-mc",
		CrashDetectionInterval: 5 * time.Millisecond,
		ReadyTimeout:           time.Second,
	}, stateManager)
	if err != nil {
		t.Fatalf("NewController failed: %v", err)
	}
	defer controller.Shutdown()

	oldStartedAt := time.Now().Add(-time.Minute)
	newStartedAt := time.Now()
	stateManager.SetRunning(0)
	controller.setRunningLifecycleStartedAt(oldStartedAt)
	controller.inspectContainer = func(context.Context, string) (*dockerctl.ContainerState, error) {
		return &dockerctl.ContainerState{Exists: true, Running: true, Status: "running", StartedAt: newStartedAt}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	controller.watchersWg.Add(1)
	go controller.containerWatchLoop(ctx, make(chan struct{}))

	for stateManager.GetState() == state.StateRunning && ctx.Err() == nil {
		time.Sleep(time.Millisecond)
	}
	if got := stateManager.GetState(); got != state.StateStarting {
		t.Fatalf("state after rapid external restart = %v, want Starting", got)
	}
	cancel()
	controller.watchersWg.Wait()
}

func TestSyncWatcherReplacementKeepsNewestWatcher(t *testing.T) {
	controller, err := NewController(&config.Config{
		MCContainerName:        "test-mc",
		CrashDetectionInterval: time.Second,
		ReadyTimeout:           time.Second,
	}, state.NewManager())
	if err != nil {
		t.Fatalf("NewController failed: %v", err)
	}
	defer controller.Shutdown()

	controller.startSyncWatcher(time.Now().Add(-time.Minute))
	controller.startSyncWatcher(time.Now())
	time.Sleep(20 * time.Millisecond)

	controller.syncWatcherMu.Lock()
	hasCurrentWatcher := controller.syncWatcherCancel != nil
	controller.syncWatcherMu.Unlock()
	if !hasCurrentWatcher {
		t.Fatal("newest sync watcher was cleared by replaced watcher cleanup")
	}
}
