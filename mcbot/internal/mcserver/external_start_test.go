package mcserver

import (
	"context"
	"sync"
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

func TestContainerWatchLoopHonorsExternalStopWhileStarting(t *testing.T) {
	stateManager := state.NewManager()
	controller, err := NewController(&config.Config{
		MCContainerName:        "test-mc",
		CrashDetectionInterval: 5 * time.Millisecond,
	}, stateManager)
	if err != nil {
		t.Fatalf("NewController failed: %v", err)
	}
	defer controller.Shutdown()

	stateManager.SetState(state.StateStarting)
	store := &singleUseExternalStopIntent{matched: true}
	controller.SetExternalStopIntentStore(store)
	controller.inspectContainer = func(context.Context, string) (*dockerctl.ContainerState, error) {
		return &dockerctl.ContainerState{Exists: true, Running: false, Status: "exited"}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	controller.watchersWg.Add(1)
	go controller.containerWatchLoop(ctx, make(chan struct{}))

	for stateManager.GetState() == state.StateStarting && ctx.Err() == nil {
		time.Sleep(time.Millisecond)
	}
	if got := stateManager.GetState(); got != state.StateStopped {
		t.Fatalf("state after external stop while starting = %v, want Stopped", got)
	}
	if store.calls != 1 {
		t.Fatalf("external stop intent calls = %d, want 1", store.calls)
	}
	cancel()
	controller.watchersWg.Wait()
}

func TestSyncWatcherHonorsExternalStopWhileStarting(t *testing.T) {
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

	stateManager.SetState(state.StateStarting)
	store := &singleUseExternalStopIntent{matched: true}
	controller.SetExternalStopIntentStore(store)
	controller.inspectContainer = func(context.Context, string) (*dockerctl.ContainerState, error) {
		return &dockerctl.ContainerState{Exists: true, Running: false, Status: "exited"}, nil
	}

	controller.startSyncWatcher(time.Now())
	deadline := time.After(250 * time.Millisecond)
	for stateManager.GetState() == state.StateStarting {
		select {
		case <-deadline:
			t.Fatal("sync watcher did not handle external stop")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if got := stateManager.GetState(); got != state.StateStopped {
		t.Fatalf("state after sync watcher external stop = %v, want Stopped", got)
	}
}

func TestObserveRunningLifecycleUsesDockerStartedAt(t *testing.T) {
	controller, err := NewController(&config.Config{MCContainerName: "test-mc"}, state.NewManager())
	if err != nil {
		t.Fatalf("NewController failed: %v", err)
	}
	defer controller.Shutdown()

	want := time.Now().Add(-time.Second).Round(0)
	controller.inspectContainer = func(context.Context, string) (*dockerctl.ContainerState, error) {
		return &dockerctl.ContainerState{Exists: true, Running: true, StartedAt: want}, nil
	}

	if got := controller.observeRunningLifecycle(context.Background()); !got.Equal(want) {
		t.Fatalf("observed lifecycle = %v, want Docker StartedAt %v", got, want)
	}
}

func TestContainerWatchLoopRecordsFirstObservedLifecycle(t *testing.T) {
	stateManager := state.NewManager()
	controller, err := NewController(&config.Config{
		MCContainerName:        "test-mc",
		CrashDetectionInterval: 5 * time.Millisecond,
	}, stateManager)
	if err != nil {
		t.Fatalf("NewController failed: %v", err)
	}
	defer controller.Shutdown()

	want := time.Now().Add(-time.Second).Round(0)
	stateManager.SetRunning(0)
	controller.inspectContainer = func(context.Context, string) (*dockerctl.ContainerState, error) {
		return &dockerctl.ContainerState{Exists: true, Running: true, StartedAt: want}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	controller.watchersWg.Add(1)
	go controller.containerWatchLoop(ctx, make(chan struct{}))

	for controller.runningLifecycleStartedAt().IsZero() && ctx.Err() == nil {
		time.Sleep(time.Millisecond)
	}
	if got := controller.runningLifecycleStartedAt(); !got.Equal(want) {
		t.Fatalf("recorded lifecycle = %v, want first observed Docker StartedAt %v", got, want)
	}
	cancel()
	controller.watchersWg.Wait()
}

func TestStartupSuccessWithoutTimestampKeepsObservedLifecycle(t *testing.T) {
	controller, err := NewController(&config.Config{MCContainerName: "test-mc"}, state.NewManager())
	if err != nil {
		t.Fatalf("NewController failed: %v", err)
	}
	defer controller.Shutdown()

	want := time.Now().Add(-time.Second).Round(0)
	controller.setRunningLifecycleStartedAt(want)
	controller.applyStartupSuccess(time.Second, 1, time.Time{})

	if got := controller.runningLifecycleStartedAt(); !got.Equal(want) {
		t.Fatalf("lifecycle after timestamp-less startup success = %v, want observed %v", got, want)
	}
}

func TestConcurrentStartupStopObserversEndStopped(t *testing.T) {
	controller, err := NewController(&config.Config{MCContainerName: "test-mc"}, state.NewManager())
	if err != nil {
		t.Fatalf("NewController failed: %v", err)
	}
	defer controller.Shutdown()
	controller.stateManager.SetState(state.StateStarting)
	controller.SetExternalStopIntentStore(&singleUseExternalStopIntent{matched: true})

	var done sync.WaitGroup
	done.Add(2)
	for range 2 {
		go func() {
			defer done.Done()
			controller.handleStoppedDuringStartup(context.Background(), true, ReasonSyncContainerStopped)
		}()
	}
	done.Wait()

	if got := controller.stateManager.GetState(); got != state.StateStopped {
		t.Fatalf("state after concurrent startup stop observers = %v, want Stopped", got)
	}
}

func TestStartupStopWinsAgainstReadyAndFailure(t *testing.T) {
	for _, terminal := range []string{"ready", "failure"} {
		t.Run(terminal, func(t *testing.T) {
			for range 100 {
				controller, err := NewController(&config.Config{MCContainerName: "test-mc"}, state.NewManager())
				if err != nil {
					t.Fatalf("NewController failed: %v", err)
				}
				controller.stateManager.SetState(state.StateStarting)
				controller.SetExternalStopIntentStore(&singleUseExternalStopIntent{matched: true})

				var done sync.WaitGroup
				done.Add(2)
				go func() {
					defer done.Done()
					controller.runStartupTransition(func() {
						if terminal == "ready" {
							controller.applyStartupSuccess(time.Second, 1, time.Now())
							return
						}
						controller.applyStartupFailure("test", context.Canceled)
					})
				}()
				go func() {
					defer done.Done()
					controller.handleStoppedDuringStartup(context.Background(), true, ReasonSyncContainerStopped)
				}()
				done.Wait()

				if got := controller.stateManager.GetState(); got != state.StateStopped {
					t.Fatalf("state after stop raced with %s = %v, want Stopped", terminal, got)
				}
				controller.Shutdown()
			}
		})
	}
}

func TestDiscordStartDoesNotReportSuccessAfterExternalStop(t *testing.T) {
	controller, err := NewController(&config.Config{
		MCContainerName: "test-mc",
		ReadyTimeout:    time.Second,
	}, state.NewManager())
	if err != nil {
		t.Fatalf("NewController failed: %v", err)
	}
	defer controller.Shutdown()
	controller.SetExternalStopIntentStore(&singleUseExternalStopIntent{matched: true})
	var inspectCalls atomic.Int32
	controller.inspectContainer = func(context.Context, string) (*dockerctl.ContainerState, error) {
		if inspectCalls.Add(1) == 1 {
			return &dockerctl.ContainerState{Exists: true, Running: false}, nil
		}
		return &dockerctl.ContainerState{Exists: true, Running: true, StartedAt: time.Now()}, nil
	}
	controller.startContainer = func(context.Context, string) error { return nil }

	resultCh := controller.Start(context.Background())
	var startSubscriber *subscriber
	deadline := time.Now().Add(250 * time.Millisecond)
	for startSubscriber == nil && time.Now().Before(deadline) {
		controller.logMux.mu.RLock()
		if len(controller.logMux.subscribers) >= 2 {
			var newestID uint64
			for id, candidate := range controller.logMux.subscribers {
				if startSubscriber == nil || id > newestID {
					newestID = id
					startSubscriber = candidate
				}
			}
		}
		controller.logMux.mu.RUnlock()
		if startSubscriber == nil {
			time.Sleep(time.Millisecond)
		}
	}
	if startSubscriber == nil {
		t.Fatal("Start did not subscribe to logs")
	}

	controller.handleStoppedDuringStartup(context.Background(), true, ReasonSyncContainerStopped)
	startSubscriber.ch <- dockerctl.LogLine{Text: `[12:34:56] [Server thread/INFO]: Done (1.234s)!`}

	result := <-resultCh
	if result.Success {
		t.Fatal("Start reported success after external stop won the terminal transition")
	}
	if got := controller.stateManager.GetState(); got != state.StateStopped {
		t.Fatalf("state after external stop and ready log = %v, want Stopped", got)
	}
}

func TestSyncStateWithZeroStartedAtDoesNotRemainStarting(t *testing.T) {
	stateManager := state.NewManager()
	controller, err := NewController(&config.Config{
		MCContainerName:        "test-mc",
		CrashDetectionInterval: 5 * time.Millisecond,
		ReadyTimeout:           10 * time.Millisecond,
	}, stateManager)
	if err != nil {
		t.Fatalf("NewController failed: %v", err)
	}
	defer controller.Shutdown()
	controller.inspectContainer = func(context.Context, string) (*dockerctl.ContainerState, error) {
		return &dockerctl.ContainerState{Exists: true, Running: true}, nil
	}

	if err := controller.SyncState(context.Background()); err != nil {
		t.Fatalf("SyncState failed: %v", err)
	}
	deadline := time.After(250 * time.Millisecond)
	for stateManager.GetState() == state.StateStarting {
		select {
		case <-deadline:
			t.Fatal("state remained Starting after zero StartedAt timeout fallback")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	if got := stateManager.GetState(); got != state.StateRunning {
		t.Fatalf("state after zero StartedAt fallback = %v, want Running", got)
	}
}

func TestShutdownCancelsTimeoutFallbackInspect(t *testing.T) {
	controller, err := NewController(&config.Config{
		MCContainerName:        "test-mc",
		CrashDetectionInterval: 10 * time.Second,
		ReadyTimeout:           5 * time.Millisecond,
	}, state.NewManager())
	if err != nil {
		t.Fatalf("NewController failed: %v", err)
	}
	controller.stateManager.SetState(state.StateStarting)
	inspectStarted := make(chan struct{})
	controller.inspectContainer = func(ctx context.Context, _ string) (*dockerctl.ContainerState, error) {
		close(inspectStarted)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	controller.startSyncWatcher(time.Now())

	select {
	case <-inspectStarted:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("timeout fallback inspect did not start")
	}
	shutdownDone := make(chan struct{})
	go func() {
		controller.Shutdown()
		close(shutdownDone)
	}()
	select {
	case <-shutdownDone:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("Shutdown blocked on timeout fallback inspect")
	}
}

func TestStopSyncWatcherInvalidatesGenerationBeforeCancel(t *testing.T) {
	controller, err := NewController(&config.Config{MCContainerName: "test-mc"}, state.NewManager())
	if err != nil {
		t.Fatalf("NewController failed: %v", err)
	}
	initialGeneration := controller.syncWatcherGeneration.Load()
	canceledAt := make(chan uint64, 1)
	done := make(chan struct{})
	close(done)
	controller.syncWatcherMu.Lock()
	controller.syncWatcherCancel = func() {
		canceledAt <- controller.syncWatcherGeneration.Load()
	}
	controller.syncWatcherDone = done
	controller.syncWatcherMu.Unlock()

	controller.stopSyncWatcher()

	if got := <-canceledAt; got <= initialGeneration {
		t.Fatalf("generation at cancel = %d, want greater than %d", got, initialGeneration)
	}
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
