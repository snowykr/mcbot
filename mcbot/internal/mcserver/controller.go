package mcserver

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/snowy/mcbot/internal/config"
	"github.com/snowy/mcbot/internal/dockerctl"
	"github.com/snowy/mcbot/internal/logutil"
	"github.com/snowy/mcbot/internal/serverops"
	"github.com/snowy/mcbot/internal/state"
)

type StartResult struct {
	Success       bool
	ReadyDuration time.Duration
	LoadSeconds   float64
	ErrorMessage  string
}

type StopResult struct {
	Success      bool
	ErrorMessage string
}

type StatusResult struct {
	State             state.ServerState
	ContainerExists   bool
	ContainerRunning  bool
	LastStartTime     time.Time
	LastReadyDuration time.Duration
	LastError         error
	Players           []string
}

type StateChangeCallback func(newState state.ServerState)

type ExternalStopIntentStore interface {
	ClearStaleStopIntent(ctx context.Context, container string, now, lifecycleStartedAt time.Time) (bool, error)
	ConsumeUnexpiredStopIntent(ctx context.Context, container string, now time.Time) (bool, error)
}

// ShutdownIntentGracePeriod is the maximum time to wait for the log watcher
// to process shutdown intent after container stop is detected.
// This allows the log pipeline (docker logs -> multiplexer -> watcher) to deliver
// the "Stopping server" log line before we declare a crash.
//
// Constraints: 1s <= value <= 5s
//   - Below 1s: Log pipeline may not deliver shutdown intent in time, causing false crash detection.
//   - Above 5s: Excessive delay before crash notification to users.
const ShutdownIntentGracePeriod = 2 * time.Second

type Controller struct {
	cfg                      *config.Config
	stateManager             *state.Manager
	readyPatterns            []readyPattern
	logMux                   *LogMultiplexer
	playerTracker            *PlayerTracker
	lifecycleLogger          LifecycleLogger // never nil; initialized to NoopLifecycleLogger if not provided
	onStateChange            StateChangeCallback
	syncWatcherCancel        context.CancelFunc
	syncWatcherMu            sync.Mutex
	syncWatcherReplaceMu     sync.Mutex
	syncStateMu              sync.Mutex
	syncWatcherGeneration    atomic.Uint64
	syncWatcherDone          chan struct{}
	containerWatcherCancel   context.CancelFunc
	runtimeLogSub            *Subscription
	shutdownIntentFromInside atomic.Bool
	watchersRunning          bool
	watchersWg               sync.WaitGroup
	watchersMu               sync.Mutex
	// logWatcherDoneCh is closed when runtimeLogWatcherLoop finishes processing.
	// containerWatchLoop uses this to wait for log watcher to drain before deciding crash vs normal shutdown.
	// Created fresh on each StartRuntimeWatchers call; nil when watchers not running.
	logWatcherDoneCh chan struct{}
	// watchersSupervisorDoneCh is closed when watchersSupervisor completes cleanup.
	// StopRuntimeWatchers waits on this to guarantee all cleanup is done before returning.
	// Created fresh on each StartRuntimeWatchers call; nil when watchers not running.
	watchersSupervisorDoneCh chan struct{}
	stateChangeWorkerCtx     context.Context
	stateChangeWorkerCancel  context.CancelFunc
	stateChangeWorkerWg      sync.WaitGroup
	stateChangeMu            sync.RWMutex
	// stateChangeEventCh: best-effort notification channel for state changes.
	// Non-blocking send; drops events when buffer is full (logged via stateChangeDropCount).
	// Listeners should NOT treat these as source of truth; query stateManager for authoritative state.
	// Buffer size 10: absorbs normal bursts during crash/recover cycles; drop is acceptable.
	//
	// Resource lifecycle note:
	// - This channel is intentionally never closed to avoid send-on-closed-channel panics.
	// - The worker goroutine is stopped via context cancellation in stopStateChangeWorker().
	// - When the Controller is GC'd, the channel becomes unreachable and is garbage collected.
	// - This is NOT a memory leak: Go's GC handles unreferenced channels correctly.
	// - After Shutdown(), notifyStateChange becomes a no-op (enforced by shutdownComplete flag).
	stateChangeEventCh    chan state.ServerState
	stateChangeDropCount  atomic.Uint64
	shutdownComplete      atomic.Bool
	runningLifecycleNanos atomic.Int64
	externalStopIntentMu  sync.RWMutex
	externalStopIntent    ExternalStopIntentStore
	inspectContainer      func(context.Context, string) (*dockerctl.ContainerState, error)
}

func NewController(cfg *config.Config, stateManager *state.Manager) (*Controller, error) {
	return NewControllerWithLogger(cfg, stateManager, nil)
}

func NewControllerWithLogger(cfg *config.Config, stateManager *state.Manager, logger LifecycleLogger) (*Controller, error) {
	patterns, err := newReadyMatchers()
	if err != nil {
		return nil, fmt.Errorf("failed to init ready patterns: %w", err)
	}

	logMux := NewLogMultiplexer(cfg.MCContainerName)

	playerTracker, err := NewPlayerTracker(logMux, cfg.MCJoinLogPattern, cfg.MCLeaveLogPattern)
	if err != nil {
		return nil, fmt.Errorf("failed to create player tracker: %w", err)
	}

	if logger == nil {
		logger = &NoopLifecycleLogger{}
	}

	return &Controller{
		cfg:                cfg,
		stateManager:       stateManager,
		readyPatterns:      patterns,
		logMux:             logMux,
		playerTracker:      playerTracker,
		lifecycleLogger:    logger,
		externalStopIntent: serverops.NewFileIntentStore(serverops.BotDataDir()),
		inspectContainer:   dockerctl.InspectContainer,
	}, nil
}

func (c *Controller) SetExternalStopIntentStore(store ExternalStopIntentStore) {
	c.externalStopIntentMu.Lock()
	defer c.externalStopIntentMu.Unlock()
	c.externalStopIntent = store
}

func (c *Controller) getExternalStopIntentStore() ExternalStopIntentStore {
	c.externalStopIntentMu.RLock()
	defer c.externalStopIntentMu.RUnlock()
	return c.externalStopIntent
}

func (c *Controller) SetOnStateChange(callback StateChangeCallback) {
	c.stateChangeMu.Lock()
	c.onStateChange = callback
	c.stateChangeMu.Unlock()
	c.ensureStateChangeWorkerStarted()
}

func (c *Controller) ensureStateChangeWorkerStarted() {
	c.stateChangeMu.Lock()
	defer c.stateChangeMu.Unlock()

	if c.stateChangeEventCh == nil {
		c.stateChangeEventCh = make(chan state.ServerState, 10)
	}

	if c.stateChangeWorkerCtx == nil || c.stateChangeWorkerCtx.Err() != nil {
		c.stateChangeWorkerCtx, c.stateChangeWorkerCancel = context.WithCancel(context.Background())
		c.startStateChangeWorkerInternalLocked()
	}
}

func (c *Controller) notifyStateChange(newState state.ServerState) {
	if c.shutdownComplete.Load() {
		return
	}

	c.stateChangeMu.RLock()
	cb := c.onStateChange
	ch := c.stateChangeEventCh
	c.stateChangeMu.RUnlock()

	if cb != nil && ch != nil {
		select {
		case ch <- newState:
		default:
			dropCount := c.stateChangeDropCount.Add(1)
			logutil.Infof("[STATE_CHANGE] WARNING: Event channel full, dropped notification for %s (total drops: %d)", newState.Korean(), dropCount)
		}
	}
}

func (c *Controller) StartStateChangeWorker(_ context.Context) {
	c.ensureStateChangeWorkerStarted()
}

func (c *Controller) startStateChangeWorkerInternalLocked() {
	c.stateChangeWorkerWg.Add(1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logutil.Infof("[STATE_CHANGE] Worker recovered from panic: %v", r)
			}
			c.stateChangeWorkerWg.Done()
			logutil.Debugf("[STATE_CHANGE] Worker stopped")
		}()

		logutil.Debugf("[STATE_CHANGE] Worker started")
		for {
			select {
			case <-c.stateChangeWorkerCtx.Done():
				return
			case newState := <-c.stateChangeEventCh:
				c.stateChangeMu.RLock()
				cb := c.onStateChange
				c.stateChangeMu.RUnlock()

				if cb != nil {
					func() {
						defer func() {
							if r := recover(); r != nil {
								logutil.Infof("[STATE_CHANGE] Callback panic recovered: %v", r)
							}
						}()
						cb(newState)
					}()
				}
			}
		}
	}()
}

func (c *Controller) stopStateChangeWorker() {
	c.stateChangeMu.Lock()
	cancel := c.stateChangeWorkerCancel
	c.onStateChange = nil
	c.stateChangeWorkerCancel = nil
	c.stateChangeMu.Unlock()

	if cancel != nil {
		cancel()
		c.stateChangeWorkerWg.Wait()
	}
}

func (c *Controller) Start(_ context.Context) <-chan StartResult {
	resultCh := make(chan StartResult, 1)

	if err := c.stateManager.TryStartTransition(); err != nil {
		resultCh <- StartResult{
			Success:      false,
			ErrorMessage: err.Error(),
		}
		close(resultCh)
		return resultCh
	}

	c.lifecycleLogger.OnServerStartRequested()

	go func() {
		defer close(resultCh)

		opCtx := context.Background()

		containerState, err := dockerctl.InspectContainer(opCtx, c.cfg.MCContainerName)
		if err != nil {
			c.applyTransitionError("docker_inspect_failed", err)
			resultCh <- StartResult{
				Success:      false,
				ErrorMessage: fmt.Sprintf("컨테이너 상태 확인 실패: %v", err),
			}
			return
		}

		if !containerState.Exists {
			c.applyStoppedWithError("container_not_found", fmt.Errorf("container not found"))
			resultCh <- StartResult{
				Success: false,
				ErrorMessage: fmt.Sprintf(
					"컨테이너 '%s'를 찾을 수 없습니다.\n\n"+
						"[권장] canonical CLI로 서버를 시작해 컨테이너를 생성/시작해주세요:\n"+
						"  mcbot server start\n\n"+
						"[저수준 점검용] Docker Compose를 직접 사용하는 경우 canonical CLI/TOML bridge를 우회합니다:\n"+
						"  • Docker Compose v2: docker compose create mc-server\n"+
						"                      또는 docker compose up --no-start mc-server\n"+
						"  • Docker Compose v1: docker-compose create mc-server\n"+
						"                      또는 docker-compose up --no-start mc-server",
					c.cfg.MCContainerName,
				),
			}
			return
		}

		if containerState.Running {
			result := c.reconcileState(reconcileInput{
				currentState:     c.stateManager.GetState(),
				containerExists:  true,
				containerRunning: true,
				shutdownIntent:   false,
				reason:           "already_running",
				err:              nil,
			})
			c.applyReconcileResult(result, "already_running", nil, true)
			c.lifecycleLogger.OnServerStartFailed("already_running", nil)
			resultCh <- StartResult{
				Success:      false,
				ErrorMessage: "서버가 이미 실행 중입니다.",
			}
			return
		}

		startTime := time.Now()

		if err := dockerctl.StartContainer(opCtx, c.cfg.MCContainerName); err != nil {
			c.applyTransitionError("docker_start_failed", err)
			resultCh <- StartResult{
				Success:      false,
				ErrorMessage: fmt.Sprintf("컨테이너 시작 실패: %v", err),
			}
			return
		}
		c.setRunningLifecycleStartedAt(startTime)

		c.playerTracker.Clear()

		c.logMux.Start(startTime)

		logCtx, logCancel := context.WithTimeout(context.Background(), c.cfg.ReadyTimeout)
		defer logCancel()

		sub := c.logMux.Subscribe()
		defer sub.Unsubscribe()

		for {
			select {
			case logLine, ok := <-sub.Ch:
				if !ok {
					c.applyTransitionError(ReasonLogStreamEndedUnexpectedly, fmt.Errorf("log stream ended unexpectedly"))
					resultCh <- StartResult{
						Success:      false,
						ErrorMessage: "로그 스트림이 예기치 않게 종료되었습니다.",
					}
					return
				}

				if logLine.Err != nil {
					logutil.Debugf("로그 읽기 오류: %v", logLine.Err)
					continue
				}

				if failure := checkFailurePatterns(logLine.Text); failure != nil {
					failureErr := fmt.Errorf("startup failure: %s", failure.Message)
					c.applyStartupFailure(failure.PatternName, failureErr)
					resultCh <- StartResult{
						Success:      false,
						ErrorMessage: failure.Message,
					}
					return
				}

				for _, pattern := range c.readyPatterns {
					matches := pattern.re.FindStringSubmatch(logLine.Text)
					if len(matches) >= 2 {
						loadSeconds, parseErr := parseLoadSeconds(matches[1])
						if parseErr != nil {
							logutil.Debugf("패턴 '%s' 매칭되었으나 로딩 시간 파싱 실패: %v", pattern.name, parseErr)
							continue
						}

						readyDuration := time.Since(startTime)
						c.applyStartupSuccess(readyDuration, loadSeconds, startTime)

						logCancel()

						resultCh <- StartResult{
							Success:       true,
							ReadyDuration: readyDuration,
							LoadSeconds:   loadSeconds,
						}
						return
					}
				}

			case <-logCtx.Done():
				c.applyTransitionError("ready_timeout", fmt.Errorf("ready timeout exceeded"))
				resultCh <- StartResult{
					Success:      false,
					ErrorMessage: fmt.Sprintf("서버 시작 시간이 %v을 초과했습니다. 서버 로그를 확인해주세요.", c.cfg.ReadyTimeout),
				}
				return
			}
		}
	}()

	return resultCh
}

func (c *Controller) Stop(_ context.Context) <-chan StopResult {
	resultCh := make(chan StopResult, 1)

	if err := c.stateManager.TryStopTransition(); err != nil {
		resultCh <- StopResult{
			Success:      false,
			ErrorMessage: err.Error(),
		}
		close(resultCh)
		return resultCh
	}

	c.lifecycleLogger.OnServerStopRequested()

	go func() {
		defer close(resultCh)

		opCtx := context.Background()

		containerState, err := dockerctl.InspectContainer(opCtx, c.cfg.MCContainerName)
		if err != nil {
			c.applyTransitionError("docker_inspect_failed", err)
			resultCh <- StopResult{
				Success:      false,
				ErrorMessage: fmt.Sprintf("컨테이너 상태 확인 실패: %v", err),
			}
			return
		}

		if !containerState.Exists || !containerState.Running {
			currentState := c.stateManager.GetState()
			result := c.reconcileState(reconcileInput{
				currentState:     currentState,
				containerExists:  containerState.Exists,
				containerRunning: false,
				shutdownIntent:   true,
				reason:           "already_stopped",
				err:              nil,
			})
			c.applyReconcileResult(result, "already_stopped", nil, containerState.Exists)
			c.lifecycleLogger.OnServerStopFailed("already_stopped", nil)
			resultCh <- StopResult{
				Success:      false,
				ErrorMessage: "서버가 이미 종료되어 있습니다.",
			}
			return
		}

		if err := dockerctl.StopContainer(opCtx, c.cfg.MCContainerName, c.cfg.StopTimeoutSeconds); err != nil {
			c.applyTransitionError("docker_stop_failed", err)
			resultCh <- StopResult{
				Success:      false,
				ErrorMessage: fmt.Sprintf("컨테이너 종료 실패: %v", err),
			}
			return
		}

		currentState := c.stateManager.GetState()
		result := c.reconcileState(reconcileInput{
			currentState:     currentState,
			containerExists:  true,
			containerRunning: false,
			shutdownIntent:   true,
			reason:           "stop_requested",
			err:              nil,
		})
		c.applyReconcileResult(result, "stop_requested", nil, true)

		resultCh <- StopResult{
			Success: true,
		}
	}()

	return resultCh
}

func (c *Controller) Status(ctx context.Context) StatusResult {
	info := c.stateManager.GetInfo()

	containerState, err := dockerctl.InspectContainer(ctx, c.cfg.MCContainerName)
	if err != nil {
		return StatusResult{
			State:             info.State,
			ContainerExists:   false,
			ContainerRunning:  false,
			LastStartTime:     info.LastStartTime,
			LastReadyDuration: info.LastReadyDuration,
			LastError:         err,
			Players:           []string{},
		}
	}

	return StatusResult{
		State:             info.State,
		ContainerExists:   containerState.Exists,
		ContainerRunning:  containerState.Running,
		LastStartTime:     info.LastStartTime,
		LastReadyDuration: info.LastReadyDuration,
		LastError:         info.LastError,
		Players:           c.playerTracker.GetPlayers(),
	}
}

func (c *Controller) Presence(ctx context.Context) PresenceState {
	info := c.stateManager.GetInfo()

	containerRunning := false
	containerState, err := dockerctl.InspectContainer(ctx, c.cfg.MCContainerName)
	if err == nil && containerState.Exists {
		containerRunning = containerState.Running
	}

	return PresenceState{
		ServerState:       info.State,
		ContainerRunning:  containerRunning,
		Players:           c.playerTracker.GetPlayers(),
		LastStartTime:     info.LastStartTime,
		LastReadyDuration: info.LastReadyDuration,
	}
}

func (c *Controller) GetPlayerTracker() *PlayerTracker {
	return c.playerTracker
}

func (c *Controller) SyncState(ctx context.Context) error {
	c.syncStateMu.Lock()
	defer c.syncStateMu.Unlock()

	containerState, err := c.inspectContainer(ctx, c.cfg.MCContainerName)
	if err != nil {
		return err
	}

	currentState := c.stateManager.GetState()

	if containerState.Exists && containerState.Running {
		lifecycleChanged := c.hasDifferentRunningLifecycle(containerState.StartedAt)
		if lifecycleChanged {
			c.stopSyncWatcher()
		}
		c.setRunningLifecycleStartedAt(containerState.StartedAt)
		needsStartupSync := currentState == state.StateStopped ||
			currentState == state.StateError ||
			currentState == state.StateCrashed ||
			lifecycleChanged

		if needsStartupSync {
			c.applyStateTransition(state.StateStarting)
		}

		if lifecycleChanged {
			c.logMux.Stop()
		}
		c.logMux.Start(containerState.StartedAt)

		if needsStartupSync {
			c.startSyncWatcher(containerState.StartedAt)
		}
	} else {
		result := c.reconcileState(reconcileInput{
			currentState:     currentState,
			containerExists:  containerState.Exists,
			containerRunning: containerState.Running,
			shutdownIntent:   false,
			reason:           ReasonSyncDetectedUnexpectedStop,
			err:              fmt.Errorf("server stopped unexpectedly during sync"),
		})
		c.applyReconcileResult(result, ReasonSyncDetectedUnexpectedStop, fmt.Errorf("server stopped unexpectedly during sync"), containerState.Exists)
	}

	return nil
}

func (c *Controller) hasDifferentRunningLifecycle(startedAt time.Time) bool {
	currentStartedAt := c.runningLifecycleStartedAt()
	return !startedAt.IsZero() && !currentStartedAt.IsZero() && !startedAt.Equal(currentStartedAt)
}

func (c *Controller) startSyncWatcher(containerStartedAt time.Time) {
	c.syncWatcherReplaceMu.Lock()
	defer c.syncWatcherReplaceMu.Unlock()
	c.stopSyncWatcherLocked()

	watchCtx, cancel := context.WithTimeout(context.Background(), c.cfg.ReadyTimeout)
	generation := c.syncWatcherGeneration.Add(1)
	done := make(chan struct{})
	c.syncWatcherMu.Lock()
	c.syncWatcherCancel = cancel
	c.syncWatcherDone = done
	c.syncWatcherMu.Unlock()

	go c.syncWatcherLoop(watchCtx, containerStartedAt, generation, done)
}

func (c *Controller) stopSyncWatcher() {
	c.syncWatcherReplaceMu.Lock()
	defer c.syncWatcherReplaceMu.Unlock()
	c.stopSyncWatcherLocked()
}

func (c *Controller) stopSyncWatcherLocked() {
	c.syncWatcherMu.Lock()
	previousCancel := c.syncWatcherCancel
	previousDone := c.syncWatcherDone
	c.syncWatcherGeneration.Add(1)
	c.syncWatcherMu.Unlock()
	if previousCancel != nil {
		previousCancel()
	}
	if previousDone != nil {
		<-previousDone
	}
	c.syncWatcherMu.Lock()
	if c.syncWatcherDone == previousDone {
		c.syncWatcherCancel = nil
		c.syncWatcherDone = nil
	}
	c.syncWatcherMu.Unlock()
}

func (c *Controller) syncWatcherLoop(ctx context.Context, containerStartedAt time.Time, generation uint64, done chan struct{}) {
	defer func() {
		c.syncWatcherMu.Lock()
		if c.syncWatcherGeneration.Load() == generation && c.syncWatcherCancel != nil {
			c.syncWatcherCancel()
			c.syncWatcherCancel = nil
			c.syncWatcherDone = nil
		}
		c.syncWatcherMu.Unlock()
		close(done)
	}()

	sub := c.logMux.Subscribe()
	defer sub.Unsubscribe()
	logutil.Debugf("[SYNC_WATCHER] 외부 시작 서버 감시 시작 (타임아웃: %v)", c.cfg.ReadyTimeout)

	containerCheckTicker := time.NewTicker(c.cfg.CrashDetectionInterval)
	defer containerCheckTicker.Stop()

	for {
		select {
		case logLine, ok := <-sub.Ch:
			if c.syncWatcherGeneration.Load() != generation {
				return
			}
			if !ok {
				currentState := c.stateManager.GetState()
				if currentState == state.StateStarting {
					c.applyStartupFailure(ReasonSyncLogStreamEnded, fmt.Errorf("log stream ended during startup"))
					logutil.Infof("[SYNC_WATCHER] 로그 스트림 종료 - 시작 실패로 처리")
				}
				return
			}

			if logLine.Err != nil {
				logutil.Debugf("[SYNC_WATCHER] 로그 읽기 오류: %v", logLine.Err)
				continue
			}

			if failure := checkFailurePatterns(logLine.Text); failure != nil {
				failureErr := fmt.Errorf("startup failure: %s", failure.Message)
				c.applyStartupFailure(failure.PatternName, failureErr)
				logutil.Infof("[SYNC_WATCHER] 실패 패턴 감지: %s - %s", failure.PatternName, failure.Message)
				return
			}

			for _, pattern := range c.readyPatterns {
				matches := pattern.re.FindStringSubmatch(logLine.Text)
				if len(matches) >= 2 {
					loadSeconds, parseErr := parseLoadSeconds(matches[1])
					if parseErr != nil {
						logutil.Debugf("[SYNC_WATCHER] 패턴 '%s' 매칭되었으나 로딩 시간 파싱 실패: %v", pattern.name, parseErr)
						continue
					}

					readyDuration := time.Since(containerStartedAt)
					c.applyStartupSuccess(readyDuration, loadSeconds, containerStartedAt)
					logutil.Infof("[SYNC_WATCHER] 서버 준비 완료 (로딩: %.2fs, 총 소요: %v)", loadSeconds, readyDuration)
					return
				}
			}

		case <-containerCheckTicker.C:
			if c.syncWatcherGeneration.Load() != generation {
				return
			}
			currentState := c.stateManager.GetState()
			if currentState != state.StateStarting {
				continue
			}

			containerState, err := dockerctl.InspectContainer(ctx, c.cfg.MCContainerName)
			if err != nil {
				logutil.Debugf("[SYNC_WATCHER] 컨테이너 상태 확인 실패: %v", err)
				continue
			}

			if !containerState.Running {
				logutil.Infof("[SYNC_WATCHER] Starting 중 컨테이너 종료 감지 - Crashed로 전환")
				result := c.reconcileState(reconcileInput{
					currentState:     currentState,
					containerExists:  containerState.Exists,
					containerRunning: containerState.Running,
					shutdownIntent:   false,
					reason:           ReasonSyncContainerStopped,
					err:              fmt.Errorf("container stopped during startup"),
				})
				c.applyReconcileResult(result, ReasonSyncContainerStopped, fmt.Errorf("container stopped during startup"), containerState.Exists)
				return
			}

		case <-ctx.Done():
			if c.syncWatcherGeneration.Load() != generation {
				return
			}
			currentState := c.stateManager.GetState()
			if currentState == state.StateStarting {
				c.handleReadyTimeoutFallback(containerStartedAt)
			}
			return
		}
	}
}

func (c *Controller) handleReadyTimeoutFallback(containerStartedAt time.Time) {
	containerState, err := dockerctl.InspectContainer(context.Background(), c.cfg.MCContainerName)
	if err != nil {
		c.applyStartupFailure(ReasonSyncContainerInspectFailed, fmt.Errorf("container inspect failed: %w", err))
		logutil.Infof("[SYNC_WATCHER] 컨테이너 상태 확인 실패: %v", err)
		return
	}

	currentState := c.stateManager.GetState()
	result := c.reconcileState(reconcileInput{
		currentState:       currentState,
		containerExists:    containerState.Exists,
		containerRunning:   containerState.Running,
		containerStartedAt: containerStartedAt,
		shutdownIntent:     false,
		reason:             ReasonSyncTimeout,
		err:                fmt.Errorf("ready timeout exceeded"),
	})
	c.applyReconcileResult(result, ReasonSyncTimeout, fmt.Errorf("ready timeout exceeded"), containerState.Exists)
}

type reconcileInput struct {
	currentState       state.ServerState
	containerExists    bool
	containerRunning   bool
	containerStartedAt time.Time
	shutdownIntent     bool
	reason             string
	err                error
}

type reconcileAction int

const (
	reconcileNoAction reconcileAction = iota
	reconcileTransitionToCrashed
	reconcileTransitionToStopped
	reconcileTransitionToRunning
)

type reconcileResult struct {
	action             reconcileAction
	newState           state.ServerState
	readyDuration      time.Duration
	shouldStopMux      bool
	shouldClearPlayers bool
}

func (c *Controller) reconcileState(input reconcileInput) reconcileResult {
	logutil.Debugf("[RECONCILE] currentState=%s containerExists=%v containerRunning=%v shutdownIntent=%v reason=%s",
		input.currentState.Korean(), input.containerExists, input.containerRunning, input.shutdownIntent, input.reason)

	if input.currentState == state.StateCrashed {
		logutil.Debugf("[RECONCILE] Already crashed, no action")
		return reconcileResult{action: reconcileNoAction}
	}

	if !input.containerRunning {
		switch input.currentState {
		case state.StateRunning:
			if input.shutdownIntent {
				logutil.Infof("[RECONCILE] Running -> Stopped (normal shutdown)")
				return reconcileResult{
					action:             reconcileTransitionToStopped,
					newState:           state.StateStopped,
					shouldStopMux:      true,
					shouldClearPlayers: true,
				}
			}
			logutil.Infof("[RECONCILE] Running -> Crashed (unexpected stop)")
			return reconcileResult{
				action:             reconcileTransitionToCrashed,
				newState:           state.StateCrashed,
				shouldStopMux:      true,
				shouldClearPlayers: true,
			}

		case state.StateStarting:
			logutil.Infof("[RECONCILE] Starting -> Crashed (container stopped during startup)")
			return reconcileResult{
				action:             reconcileTransitionToCrashed,
				newState:           state.StateCrashed,
				shouldStopMux:      true,
				shouldClearPlayers: true,
			}

		case state.StateStopping:
			logutil.Infof("[RECONCILE] Stopping -> Stopped (normal)")
			return reconcileResult{
				action:             reconcileTransitionToStopped,
				newState:           state.StateStopped,
				shouldStopMux:      true,
				shouldClearPlayers: true,
			}

		default:
			logutil.Debugf("[RECONCILE] Container not running but state=%s, no action", input.currentState.Korean())
			return reconcileResult{action: reconcileNoAction}
		}
	}

	if input.containerRunning && input.currentState == state.StateStarting && !input.containerStartedAt.IsZero() {
		readyDuration := time.Since(input.containerStartedAt)
		logutil.Infof("[RECONCILE] Starting -> Running (timeout fallback, duration=%v)", readyDuration)
		return reconcileResult{
			action:        reconcileTransitionToRunning,
			newState:      state.StateRunning,
			readyDuration: readyDuration,
		}
	}

	logutil.Debugf("[RECONCILE] No state change needed")
	return reconcileResult{action: reconcileNoAction}
}

func (c *Controller) applyStartupSuccess(readyDuration time.Duration, loadSeconds float64, lifecycleStartedAt time.Time) {
	c.setRunningLifecycleStartedAt(lifecycleStartedAt)
	c.clearStaleExternalStopIntent(context.Background(), lifecycleStartedAt)
	c.stateManager.ClearFailureCandidate()
	c.stateManager.SetRunning(readyDuration)
	c.notifyStateChange(state.StateRunning)
	c.lifecycleLogger.OnServerStarted(readyDuration, loadSeconds)
}

func (c *Controller) setRunningLifecycleStartedAt(startedAt time.Time) {
	if startedAt.IsZero() {
		c.runningLifecycleNanos.Store(0)
		return
	}
	c.runningLifecycleNanos.Store(startedAt.UnixNano())
}

func (c *Controller) runningLifecycleStartedAt() time.Time {
	nanos := c.runningLifecycleNanos.Load()
	if nanos == 0 {
		return time.Time{}
	}
	return time.Unix(0, nanos)
}

func (c *Controller) clearStaleExternalStopIntent(ctx context.Context, lifecycleStartedAt time.Time) {
	store := c.getExternalStopIntentStore()
	if store == nil || lifecycleStartedAt.IsZero() {
		return
	}
	cleared, err := store.ClearStaleStopIntent(ctx, c.cfg.MCContainerName, time.Now(), lifecycleStartedAt)
	if err != nil {
		logutil.Debugf("[RUNTIME] stale external stop intent clear unavailable: %v", err)
		return
	}
	if cleared {
		logutil.Infof("[RUNTIME] 이전 외부 CLI 종료 의도를 무효화했습니다")
	}
}

func (c *Controller) applyStartupFailure(reason string, err error) {
	c.stateManager.SetCrashed(err)
	c.notifyStateChange(state.StateCrashed)
	c.lifecycleLogger.OnServerStartFailed(reason, err)
}

func (c *Controller) applyTransitionError(reason string, err error) {
	c.stateManager.SetError(err)
	c.lifecycleLogger.OnServerStartFailed(reason, err)
}

func (c *Controller) applyStateTransition(newState state.ServerState) {
	c.stateManager.SetState(newState)
	c.notifyStateChange(newState)
}

func (c *Controller) applyStoppedWithError(reason string, err error) {
	c.stateManager.SetStoppedWithError(err)
	c.lifecycleLogger.OnServerStartFailed(reason, err)
}

func (c *Controller) applyReconcileResult(result reconcileResult, reason string, err error, containerExists bool) {
	if result.action == reconcileNoAction {
		return
	}

	if result.shouldStopMux {
		c.logMux.Stop()
	}
	if result.shouldClearPlayers {
		c.playerTracker.Clear()
	}

	switch result.action {
	case reconcileNoAction:

	case reconcileTransitionToCrashed:
		failureCandidate := c.stateManager.ConsumeFailureCandidate()
		finalReason := reason
		finalErr := err
		if failureCandidate != nil {
			finalReason = ReasonRuntimeFailurePrefix + failureCandidate.PatternName
			if err != nil {
				finalErr = fmt.Errorf("%s (detected failure: %s)", err.Error(), failureCandidate.Message)
			} else {
				finalErr = fmt.Errorf("detected failure: %s", failureCandidate.Message)
			}
			logutil.Infof("[RECONCILE] 크래시 원인 후보 첨부: %s - %s", failureCandidate.PatternName, failureCandidate.Message)
		}
		c.stateManager.SetCrashed(finalErr)
		c.notifyStateChange(state.StateCrashed)
		info := c.stateManager.GetInfo()
		c.lifecycleLogger.OnServerCrashed(finalReason, info, containerExists)
		logutil.Infof("[RECONCILE] Transitioned to Crashed: reason=%s container_exists=%v", finalReason, containerExists)

	case reconcileTransitionToStopped:
		c.stateManager.ClearFailureCandidate()
		c.stateManager.SetStopped()
		c.notifyStateChange(state.StateStopped)
		c.lifecycleLogger.OnServerStopped()
		logutil.Infof("[RECONCILE] Transitioned to Stopped")

	case reconcileTransitionToRunning:
		c.clearStaleExternalStopIntent(context.Background(), c.runningLifecycleStartedAt())
		c.stateManager.ClearFailureCandidate()
		c.stateManager.SetRunning(result.readyDuration)
		c.notifyStateChange(state.StateRunning)
		logutil.Infof("[RECONCILE] Transitioned to Running (duration=%v)", result.readyDuration)
	}
}

func (c *Controller) StartRuntimeWatchers(ctx context.Context) {
	c.watchersMu.Lock()

	if c.watchersRunning {
		c.watchersMu.Unlock()
		logutil.Debugf("[RUNTIME] 런타임 감시가 이미 실행 중입니다")
		return
	}

	logutil.Infof("[RUNTIME] 런타임 감시 시작 (컨테이너: %s)", c.cfg.MCContainerName)

	sub := c.logMux.Subscribe()
	c.runtimeLogSub = &sub

	watchCtx, cancel := context.WithCancel(ctx)
	c.containerWatcherCancel = cancel
	c.watchersRunning = true
	c.shutdownIntentFromInside.Store(false)
	c.logWatcherDoneCh = make(chan struct{})
	c.watchersSupervisorDoneCh = make(chan struct{})

	c.watchersWg.Add(2)
	go c.runtimeLogWatcherLoop(watchCtx, c.logWatcherDoneCh)
	go c.containerWatchLoop(watchCtx, c.logWatcherDoneCh)

	supervisorDoneCh := c.watchersSupervisorDoneCh
	go c.watchersSupervisor(supervisorDoneCh)

	c.watchersMu.Unlock()
	logutil.Debugf("[RUNTIME] 런타임 감시 시작 완료 (로그 워처 + 컨테이너 워처)")
}

func (c *Controller) watchersSupervisor(doneCh chan struct{}) {
	defer close(doneCh)
	defer func() {
		if r := recover(); r != nil {
			logutil.Infof("[RUNTIME] watchersSupervisor recovered from panic: %v\n%s", r, debug.Stack())
		}
	}()

	c.watchersWg.Wait()

	c.watchersMu.Lock()
	defer c.watchersMu.Unlock()

	if c.runtimeLogSub != nil {
		c.runtimeLogSub.Unsubscribe()
		c.runtimeLogSub = nil
	}

	c.watchersRunning = false
	c.containerWatcherCancel = nil
	c.logWatcherDoneCh = nil
	c.watchersSupervisorDoneCh = nil
	c.shutdownIntentFromInside.Store(false)

	logutil.Debugf("[RUNTIME] 워처 supervisor: 모든 워처 종료 완료, 상태 정리됨")
}

func (c *Controller) StopRuntimeWatchers() {
	c.watchersMu.Lock()

	if !c.watchersRunning {
		c.watchersMu.Unlock()
		return
	}

	cancel := c.containerWatcherCancel
	supervisorDoneCh := c.watchersSupervisorDoneCh
	c.watchersMu.Unlock()

	if cancel != nil {
		cancel()
	}

	c.watchersWg.Wait()

	if supervisorDoneCh != nil {
		<-supervisorDoneCh
	}

	logutil.Infof("[RUNTIME] 런타임 감시 중지 완료 (cleanup 완료 보장됨)")
}

func (c *Controller) runtimeLogWatcherLoop(ctx context.Context, doneCh chan struct{}) {
	defer close(doneCh)
	defer c.watchersWg.Done()
	defer func() {
		if r := recover(); r != nil {
			logutil.Infof("[LOG_WATCHER] Recovered from panic: %v", r)
		}
		logutil.Debugf("[LOG_WATCHER] 런타임 로그 감시 종료")
	}()

	logutil.Debugf("[LOG_WATCHER] 런타임 로그 감시 시작")

	for {
		select {
		case <-ctx.Done():
			logutil.Debugf("[LOG_WATCHER] 런타임 로그 감시 종료 (context done)")
			return

		case logLine, ok := <-c.runtimeLogSub.Ch:
			if !ok {
				logutil.Debugf("[LOG_WATCHER] 로그 채널 닫힘")
				return
			}

			if logLine.Err != nil {
				continue
			}

			currentState := c.stateManager.GetState()
			if currentState != state.StateRunning {
				continue
			}

			if failure := checkFailurePatterns(logLine.Text); failure != nil {
				c.stateManager.SetFailureCandidate(&state.FailureCandidate{
					PatternName: failure.PatternName,
					Message:     failure.Message,
					DetectedAt:  time.Now(),
					RawLog:      logLine.Text,
				})
				logutil.Infof("[LOG_WATCHER] 실패 패턴 감지 (원인 후보로 저장): %s - %s", failure.PatternName, failure.Message)
			}

			if isShutdownLog(logLine.Text) {
				c.shutdownIntentFromInside.Store(true)
				logutil.Infof("[LOG_WATCHER] 서버 내부 종료 시작 감지")
			}
		}
	}
}

func (c *Controller) handleNormalShutdown(currentState state.ServerState, containerExists bool) {
	result := c.reconcileState(reconcileInput{
		currentState:     currentState,
		containerExists:  containerExists,
		containerRunning: false,
		shutdownIntent:   true,
		reason:           ReasonRuntimeNormalShutdown,
		err:              nil,
	})
	c.applyReconcileResult(result, ReasonRuntimeNormalShutdown, nil, containerExists)
	c.shutdownIntentFromInside.Store(false)
}

func (c *Controller) hasShutdownIntent(ctx context.Context) bool {
	if c.shutdownIntentFromInside.Load() {
		return true
	}
	store := c.getExternalStopIntentStore()
	if store == nil {
		return false
	}
	matched, err := store.ConsumeUnexpiredStopIntent(ctx, c.cfg.MCContainerName, time.Now())
	if err != nil {
		logutil.Debugf("[CONTAINER_WATCHER] external stop intent unavailable: %v", err)
		return false
	}
	if matched {
		logutil.Infof("[CONTAINER_WATCHER] 외부 CLI 종료 의도 감지")
	}
	return matched
}

func (c *Controller) containerWatchLoop(ctx context.Context, logWatcherDoneCh <-chan struct{}) {
	defer func() {
		if r := recover(); r != nil {
			logutil.Infof("[CONTAINER_WATCHER] Recovered from panic: %v", r)
		}
		c.watchersWg.Done()
		logutil.Debugf("[CONTAINER_WATCHER] 컨테이너 상태 감시 종료")
	}()

	ticker := time.NewTicker(c.cfg.CrashDetectionInterval)
	defer ticker.Stop()

	logutil.Debugf("[CONTAINER_WATCHER] 컨테이너 상태 감시 시작 (주기: %v)", c.cfg.CrashDetectionInterval)

	consecutiveInspectFailures := 0
	maxConsecutiveFailures := c.cfg.MaxInspectFailureAttempts

	for {
		select {
		case <-ctx.Done():
			logutil.Debugf("[CONTAINER_WATCHER] 컨테이너 상태 감시 종료 (context done)")
			return

		case <-ticker.C:
			currentState := c.stateManager.GetState()

			containerState, err := c.inspectContainer(ctx, c.cfg.MCContainerName)
			if err != nil {
				consecutiveInspectFailures++
				logutil.Debugf("[CONTAINER_WATCHER] 컨테이너 상태 확인 실패 (%d/%d): %v",
					consecutiveInspectFailures, maxConsecutiveFailures, err)

				if consecutiveInspectFailures >= maxConsecutiveFailures && currentState == state.StateRunning {
					result := c.reconcileState(reconcileInput{
						currentState:     currentState,
						containerExists:  false,
						containerRunning: false,
						shutdownIntent:   false,
						reason:           ReasonRuntimeInspectFailedRepeatedly,
						err:              fmt.Errorf("container inspect failed %d times: %w", consecutiveInspectFailures, err),
					})
					c.applyReconcileResult(result, ReasonRuntimeInspectFailedRepeatedly,
						fmt.Errorf("container inspect failed %d times: %w", consecutiveInspectFailures, err), false)
					return
				}
				continue
			}

			consecutiveInspectFailures = 0

			shutdownIntent := c.shutdownIntentFromInside.Load()
			logutil.Debugf("[CONTAINER_WATCHER] tick - state=%s exists=%v running=%v shutdownIntent=%v",
				currentState.Korean(), containerState.Exists, containerState.Running, shutdownIntent)

			if containerState.Running && c.hasDifferentRunningLifecycle(containerState.StartedAt) {
				if err := c.SyncState(ctx); err != nil {
					logutil.Debugf("[CONTAINER_WATCHER] 새 컨테이너 lifecycle 동기화 실패: %v", err)
				}
				continue
			}

			if currentState != state.StateRunning {
				if containerState.Running && (currentState == state.StateStopped || currentState == state.StateError || currentState == state.StateCrashed) {
					if err := c.SyncState(ctx); err != nil {
						logutil.Debugf("[CONTAINER_WATCHER] 외부 시작 상태 동기화 실패: %v", err)
					}
				}
				continue
			}

			if !containerState.Running {
				if c.hasShutdownIntent(ctx) {
					logutil.Infof("[CONTAINER_WATCHER] 종료 의도 감지 (즉시) - 정상 종료로 처리")
					c.handleNormalShutdown(currentState, containerState.Exists)
					continue
				}

				logutil.Debugf("[CONTAINER_WATCHER] 컨테이너 종료 감지, 로그 워처 종료 또는 타임아웃 대기 (최대 %v)", ShutdownIntentGracePeriod)
				ctxCancelledDuringGrace := false
				select {
				case <-logWatcherDoneCh:
					logutil.Debugf("[CONTAINER_WATCHER] 로그 워처 종료됨, intent 재확인")
				case <-time.After(ShutdownIntentGracePeriod):
					logutil.Debugf("[CONTAINER_WATCHER] grace period 타임아웃")
				case <-ctx.Done():
					// ctx가 취소되어도 상태 전이는 수행해야 함 (정책: 정확한 상태 전이 보장)
					// 이 시점에 이미 컨테이너 stop을 감지했으므로, 최종 상태를 확정해야 함
					logutil.Debugf("[CONTAINER_WATCHER] grace period 중 context 취소됨, 상태 전이는 계속 진행")
					ctxCancelledDuringGrace = true
				}

				// ctx가 취소되었어도 상태 전이를 수행 (정책: 상태 일관성 보장)
				// shutdownIntent 여부로 정상/비정상 종료 판별
				if c.hasShutdownIntent(ctx) {
					logutil.Infof("[CONTAINER_WATCHER] 종료 의도 감지 (대기 후) - 정상 종료로 처리")
					c.handleNormalShutdown(currentState, containerState.Exists)
					continue
				}

				// ctx 취소 + shutdownIntent=false: 보수적으로 비정상 종료(crash)로 처리
				// 이유: 거짓 정상종료보다 거짓 crash가 운영상 더 안전함
				if ctxCancelledDuringGrace {
					logutil.Infof("[CONTAINER_WATCHER] context 취소 중 비정상 종료로 처리 (보수적 정책) - exists=%v", containerState.Exists)
				} else {
					logutil.Infof("[CONTAINER_WATCHER] 비정상 종료 감지 - exists=%v", containerState.Exists)
				}
				result := c.reconcileState(reconcileInput{
					currentState:     currentState,
					containerExists:  containerState.Exists,
					containerRunning: containerState.Running,
					shutdownIntent:   false,
					reason:           ReasonRuntimeContainerStopped,
					err:              fmt.Errorf("container stopped unexpectedly"),
				})
				c.applyReconcileResult(result, ReasonRuntimeContainerStopped, fmt.Errorf("container stopped unexpectedly"), containerState.Exists)
				return
			}
		}
	}
}

func (c *Controller) Shutdown() {
	c.shutdownComplete.Store(true)

	c.StopRuntimeWatchers()
	c.syncStateMu.Lock()
	c.stopSyncWatcher()
	c.syncStateMu.Unlock()

	c.stopStateChangeWorker()
	c.playerTracker.Close()
	c.logMux.Close()
}
