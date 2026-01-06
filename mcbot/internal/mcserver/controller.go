package mcserver

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/snowy/mcbot/internal/config"
	"github.com/snowy/mcbot/internal/dockerctl"
	"github.com/snowy/mcbot/internal/logutil"
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

type Controller struct {
	cfg                      *config.Config
	stateManager             *state.Manager
	readyPatterns            []readyPattern
	logMux                   *LogMultiplexer
	playerTracker            *PlayerTracker
	lifecycleLogger          LifecycleLogger
	onStateChange            StateChangeCallback
	syncWatcherCancel        context.CancelFunc
	syncWatcherMu            sync.Mutex
	containerWatcherCancel   context.CancelFunc
	runtimeLogSub            *Subscription
	shutdownIntentFromInside atomic.Bool
	watchersRunning          bool
	watchersWg               sync.WaitGroup
	watchersMu               sync.Mutex
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
		cfg:             cfg,
		stateManager:    stateManager,
		readyPatterns:   patterns,
		logMux:          logMux,
		playerTracker:   playerTracker,
		lifecycleLogger: logger,
	}, nil
}

func (c *Controller) SetOnStateChange(callback StateChangeCallback) {
	c.onStateChange = callback
}

func (c *Controller) notifyStateChange(newState state.ServerState) {
	if c.onStateChange != nil {
		go c.onStateChange(newState)
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
			c.stateManager.SetError(err)
			c.lifecycleLogger.OnServerStartFailed("docker_inspect_failed", err)
			resultCh <- StartResult{
				Success:      false,
				ErrorMessage: fmt.Sprintf("컨테이너 상태 확인 실패: %v", err),
			}
			return
		}

		if !containerState.Exists {
			c.stateManager.SetStoppedWithError(fmt.Errorf("container not found"))
			c.lifecycleLogger.OnServerStartFailed("container_not_found", nil)
			resultCh <- StartResult{
				Success: false,
				ErrorMessage: fmt.Sprintf(
					"컨테이너 '%s'를 찾을 수 없습니다.\n\n"+
						"[권장] Make를 사용하여 컨테이너를 생성해주세요:\n"+
						"  make ensure-mc\n\n"+
						"[대안] Docker Compose를 직접 사용하는 경우:\n"+
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
			c.stateManager.SetState(state.StateRunning)
			c.lifecycleLogger.OnServerStartFailed("already_running", nil)
			resultCh <- StartResult{
				Success:      false,
				ErrorMessage: "서버가 이미 실행 중입니다.",
			}
			return
		}

		startTime := time.Now()

		if err := dockerctl.StartContainer(opCtx, c.cfg.MCContainerName); err != nil {
			c.stateManager.SetError(err)
			c.lifecycleLogger.OnServerStartFailed("docker_start_failed", err)
			resultCh <- StartResult{
				Success:      false,
				ErrorMessage: fmt.Sprintf("컨테이너 시작 실패: %v", err),
			}
			return
		}

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
					c.stateManager.SetError(fmt.Errorf("log stream ended unexpectedly"))
					c.lifecycleLogger.OnServerStartFailed(ReasonLogStreamEndedUnexpectedly, nil)
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
					c.stateManager.SetCrashed(failureErr)
					c.notifyStateChange(state.StateCrashed)
					c.lifecycleLogger.OnServerStartFailed(failure.PatternName, failureErr)
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
						c.stateManager.SetRunning(readyDuration)
						c.notifyStateChange(state.StateRunning)
						c.lifecycleLogger.OnServerStarted(readyDuration, loadSeconds)

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
				c.stateManager.SetError(fmt.Errorf("ready timeout exceeded"))
				c.lifecycleLogger.OnServerStartFailed("ready_timeout", logCtx.Err())
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
			c.stateManager.SetError(err)
			c.lifecycleLogger.OnServerStopFailed("docker_inspect_failed", err)
			resultCh <- StopResult{
				Success:      false,
				ErrorMessage: fmt.Sprintf("컨테이너 상태 확인 실패: %v", err),
			}
			return
		}

		if !containerState.Exists || !containerState.Running {
			c.logMux.Stop()
			c.playerTracker.Clear()
			c.stateManager.SetStopped()
			c.lifecycleLogger.OnServerStopFailed("already_stopped", nil)
			resultCh <- StopResult{
				Success:      false,
				ErrorMessage: "서버가 이미 종료되어 있습니다.",
			}
			return
		}

		if err := dockerctl.StopContainer(opCtx, c.cfg.MCContainerName, c.cfg.StopTimeoutSeconds); err != nil {
			c.stateManager.SetError(err)
			c.lifecycleLogger.OnServerStopFailed("docker_stop_failed", err)
			resultCh <- StopResult{
				Success:      false,
				ErrorMessage: fmt.Sprintf("컨테이너 종료 실패: %v", err),
			}
			return
		}

		c.handleNormalStop()
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

	if !containerState.Running && info.State == state.StateRunning {
		c.handleCrash(ReasonStatusContainerNotRunning, fmt.Errorf("server stopped unexpectedly"), containerState.Exists)
		info = c.stateManager.GetInfo()
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
	containerState, err := dockerctl.InspectContainer(ctx, c.cfg.MCContainerName)
	if err != nil {
		return err
	}

	currentState := c.stateManager.GetState()

	if containerState.Exists && containerState.Running {
		needsStartupSync := currentState == state.StateStopped ||
			currentState == state.StateError ||
			currentState == state.StateCrashed

		if needsStartupSync {
			c.stateManager.SetState(state.StateStarting)
			c.notifyStateChange(state.StateStarting)
		}

		c.logMux.Start(containerState.StartedAt)

		if needsStartupSync {
			c.startSyncWatcher(containerState.StartedAt)
		}
	} else {
		if currentState == state.StateRunning {
			c.handleCrash(ReasonSyncDetectedUnexpectedStop, fmt.Errorf("server stopped unexpectedly during sync"), containerState.Exists)
		}
	}

	return nil
}

func (c *Controller) startSyncWatcher(containerStartedAt time.Time) {
	c.syncWatcherMu.Lock()
	if c.syncWatcherCancel != nil {
		c.syncWatcherCancel()
	}

	watchCtx, cancel := context.WithTimeout(context.Background(), c.cfg.ReadyTimeout)
	c.syncWatcherCancel = cancel
	c.syncWatcherMu.Unlock()

	go c.syncWatcherLoop(watchCtx, containerStartedAt)
}

func (c *Controller) syncWatcherLoop(ctx context.Context, containerStartedAt time.Time) {
	defer func() {
		c.syncWatcherMu.Lock()
		if c.syncWatcherCancel != nil {
			c.syncWatcherCancel()
			c.syncWatcherCancel = nil
		}
		c.syncWatcherMu.Unlock()
	}()

	sub := c.logMux.Subscribe()
	defer sub.Unsubscribe()
	logutil.Debugf("[SYNC_WATCHER] 외부 시작 서버 감시 시작 (타임아웃: %v)", c.cfg.ReadyTimeout)

	for {
		select {
		case logLine, ok := <-sub.Ch:
			if !ok {
				currentState := c.stateManager.GetState()
				if currentState == state.StateStarting {
					c.stateManager.SetCrashed(fmt.Errorf("log stream ended during startup"))
					c.notifyStateChange(state.StateCrashed)
					c.lifecycleLogger.OnServerStartFailed(ReasonSyncLogStreamEnded, nil)
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
				c.stateManager.SetCrashed(failureErr)
				c.notifyStateChange(state.StateCrashed)
				c.lifecycleLogger.OnServerStartFailed(failure.PatternName, failureErr)
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
					c.stateManager.SetRunning(readyDuration)
					c.notifyStateChange(state.StateRunning)
					c.lifecycleLogger.OnServerStarted(readyDuration, loadSeconds)
					logutil.Infof("[SYNC_WATCHER] 서버 준비 완료 (로딩: %.2fs, 총 소요: %v)", loadSeconds, readyDuration)
					return
				}
			}

		case <-ctx.Done():
			currentState := c.stateManager.GetState()
			if currentState == state.StateStarting {
				c.checkContainerAndDecideState()
			}
			return
		}
	}
}

func (c *Controller) checkContainerAndDecideState() {
	containerState, err := dockerctl.InspectContainer(context.Background(), c.cfg.MCContainerName)
	if err != nil {
		c.stateManager.SetCrashed(fmt.Errorf("container inspect failed: %w", err))
		c.notifyStateChange(state.StateCrashed)
		c.lifecycleLogger.OnServerStartFailed(ReasonSyncContainerInspectFailed, err)
		logutil.Infof("[SYNC_WATCHER] 컨테이너 상태 확인 실패: %v", err)
		return
	}

	if containerState.Running {
		readyDuration := time.Since(containerState.StartedAt)
		c.stateManager.SetRunning(readyDuration)
		c.notifyStateChange(state.StateRunning)
		logutil.Infof("[SYNC_WATCHER] 타임아웃 후 컨테이너 실행 중 확인 - Running으로 전환 (소요: %v)", readyDuration)
	} else {
		c.stateManager.SetCrashed(fmt.Errorf("container stopped during startup"))
		c.notifyStateChange(state.StateCrashed)
		c.lifecycleLogger.OnServerStartFailed(ReasonSyncContainerStopped, nil)
		logutil.Infof("[SYNC_WATCHER] 타임아웃 후 컨테이너 종료 확인 - Crashed로 전환")
	}
}

func (c *Controller) handleNormalStop() {
	c.logMux.Stop()
	c.playerTracker.Clear()
	c.stateManager.SetStopped()
	c.notifyStateChange(state.StateStopped)
	c.lifecycleLogger.OnServerStopped()
}

func (c *Controller) handleCrash(reason string, err error, containerExists bool) {
	currentState := c.stateManager.GetState()
	logutil.Debugf("[CRASH] handleCrash called - reason=%s currentState=%s err=%v containerExists=%v",
		reason, currentState.Korean(), err, containerExists)

	if currentState == state.StateCrashed {
		logutil.Debugf("[CRASH] handleCrash skipped - already crashed")
		return
	}

	if currentState != state.StateRunning {
		logutil.Infof("[CRASH][WARN] handleCrash called outside Running state: %s (invariant violation, skipping)", currentState.Korean())
		return
	}

	logutil.Debugf("[CRASH] Processing crash - transitioning from %s to Crashed", currentState.Korean())

	c.logMux.Stop()
	c.playerTracker.Clear()
	c.stateManager.SetCrashed(err)

	logutil.Debugf("[CRASH] State changed to Crashed, notifying listeners")
	c.notifyStateChange(state.StateCrashed)

	info := c.stateManager.GetInfo()
	c.lifecycleLogger.OnServerCrashed(reason, info, containerExists)
	logutil.Infof("[CRASH] 크래시 감지: reason=%s state_before=%s container_exists=%v", reason, currentState.Korean(), containerExists)
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

	c.watchersWg.Add(2)
	go c.runtimeLogWatcherLoop(watchCtx)
	go c.containerWatchLoop(watchCtx)

	go c.watchersSupervisor()

	c.watchersMu.Unlock()
	logutil.Debugf("[RUNTIME] 런타임 감시 시작 완료 (로그 워처 + 컨테이너 워처)")
}

func (c *Controller) watchersSupervisor() {
	c.watchersWg.Wait()

	c.watchersMu.Lock()
	defer c.watchersMu.Unlock()

	if c.runtimeLogSub != nil {
		c.runtimeLogSub.Unsubscribe()
		c.runtimeLogSub = nil
	}

	c.watchersRunning = false
	c.containerWatcherCancel = nil
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
	c.watchersMu.Unlock()

	if cancel != nil {
		cancel()
	}

	c.watchersWg.Wait()

	logutil.Infof("[RUNTIME] 런타임 감시 중지 완료 (supervisor가 정리 수행)")
}

func (c *Controller) runtimeLogWatcherLoop(ctx context.Context) {
	defer func() {
		c.watchersWg.Done()
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
				failureErr := fmt.Errorf("runtime failure: %s", failure.Message)
				c.handleCrash(ReasonRuntimeFailurePrefix+failure.PatternName, failureErr, true)
				return
			}

			if isShutdownLog(logLine.Text) {
				c.shutdownIntentFromInside.Store(true)
				logutil.Infof("[LOG_WATCHER] 서버 내부 종료 시작 감지")
			}
		}
	}
}

func (c *Controller) containerWatchLoop(ctx context.Context) {
	defer func() {
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

			containerState, err := dockerctl.InspectContainer(ctx, c.cfg.MCContainerName)
			if err != nil {
				consecutiveInspectFailures++
				logutil.Debugf("[CONTAINER_WATCHER] 컨테이너 상태 확인 실패 (%d/%d): %v",
					consecutiveInspectFailures, maxConsecutiveFailures, err)

				if consecutiveInspectFailures >= maxConsecutiveFailures && currentState == state.StateRunning {
					c.handleCrash(ReasonRuntimeInspectFailedRepeatedly,
						fmt.Errorf("container inspect failed %d times: %w", consecutiveInspectFailures, err), false)
					return
				}
				continue
			}

			consecutiveInspectFailures = 0

			shutdownIntent := c.shutdownIntentFromInside.Load()
			logutil.Debugf("[CONTAINER_WATCHER] tick - state=%s exists=%v running=%v shutdownIntent=%v",
				currentState.Korean(), containerState.Exists, containerState.Running, shutdownIntent)

			if currentState != state.StateRunning {
				continue
			}

			if !containerState.Running {
				if shutdownIntent {
					logutil.Infof("[CONTAINER_WATCHER] 서버 내부 종료로 인한 컨테이너 종료 - 정상 종료로 처리")
					c.handleNormalStop()
					c.shutdownIntentFromInside.Store(false)
					return
				}

				select {
				case <-time.After(200 * time.Millisecond):
				case <-ctx.Done():
					return
				}

				shutdownIntent = c.shutdownIntentFromInside.Load()
				if shutdownIntent {
					logutil.Infof("[CONTAINER_WATCHER] 서버 내부 종료 감지(지연 확인) - 정상 종료로 처리")
					c.handleNormalStop()
					c.shutdownIntentFromInside.Store(false)
					return
				}

				logutil.Infof("[CONTAINER_WATCHER] 비정상 종료 감지 - exists=%v", containerState.Exists)
				c.handleCrash(ReasonRuntimeContainerStopped, fmt.Errorf("container stopped unexpectedly"), containerState.Exists)
				return
			}
		}
	}
}

func (c *Controller) Shutdown() {
	c.StopRuntimeWatchers()
	if c.syncWatcherCancel != nil {
		c.syncWatcherCancel()
	}
	c.playerTracker.Close()
	c.logMux.Close()
}
