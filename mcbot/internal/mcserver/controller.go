package mcserver

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/snowy/mcbot/internal/config"
	"github.com/snowy/mcbot/internal/dockerctl"
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

type Controller struct {
	cfg             *config.Config
	stateManager    *state.Manager
	readyPatterns   []readyPattern
	logMux          *LogMultiplexer
	playerTracker   *PlayerTracker
	lifecycleLogger LifecycleLogger
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

		logCh := c.logMux.Subscribe()

		for {
			select {
			case logLine, ok := <-logCh:
				if !ok {
					c.stateManager.SetError(fmt.Errorf("log stream ended unexpectedly"))
					c.lifecycleLogger.OnServerStartFailed("log_stream_ended_unexpectedly", nil)
					resultCh <- StartResult{
						Success:      false,
						ErrorMessage: "로그 스트림이 예기치 않게 종료되었습니다.",
					}
					return
				}

				if logLine.Err != nil {
					log.Printf("로그 읽기 오류: %v", logLine.Err)
					continue
				}

				for _, pattern := range c.readyPatterns {
					matches := pattern.re.FindStringSubmatch(logLine.Text)
					if len(matches) >= 2 {
						loadSeconds, parseErr := parseLoadSeconds(matches[1])
						if parseErr != nil {
							log.Printf("패턴 '%s' 매칭되었으나 로딩 시간 파싱 실패: %v", pattern.name, parseErr)
							continue
						}

						readyDuration := time.Since(startTime)
						c.stateManager.SetRunning(readyDuration)
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

		c.logMux.Stop()
		c.playerTracker.Clear()
		c.stateManager.SetStopped()
		c.lifecycleLogger.OnServerStopped()
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
		c.logMux.Stop()
		c.playerTracker.Clear()
		c.stateManager.SetCrashed(fmt.Errorf("server stopped unexpectedly"))
		info.State = state.StateCrashed
		info.LastError = fmt.Errorf("server stopped unexpectedly")
		c.lifecycleLogger.OnServerCrashed("container_not_running_while_state_running", info, containerState.Exists)
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
	status := c.Status(ctx)

	return PresenceState{
		ServerState:       status.State,
		ContainerRunning:  status.ContainerRunning,
		Players:           status.Players,
		LastStartTime:     status.LastStartTime,
		LastReadyDuration: status.LastReadyDuration,
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
		if currentState == state.StateStopped || currentState == state.StateError || currentState == state.StateCrashed {
			c.stateManager.SetState(state.StateRunning)
		}

		c.logMux.Start(containerState.StartedAt)
	} else {
		if currentState == state.StateRunning {
			c.logMux.Stop()
			c.playerTracker.Clear()
			c.stateManager.SetCrashed(fmt.Errorf("server stopped unexpectedly during sync"))
			info := c.stateManager.GetInfo()
			c.lifecycleLogger.OnServerCrashed("sync_detected_unexpected_stop", info, containerState.Exists)
		}
	}

	return nil
}

func (c *Controller) Shutdown() {
	c.logMux.Close()
}
