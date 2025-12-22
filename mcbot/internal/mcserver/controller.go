package mcserver

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strconv"
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
	cfg           *config.Config
	stateManager  *state.Manager
	readyPattern  *regexp.Regexp
	logMux        *LogMultiplexer
	playerTracker *PlayerTracker
}

func NewController(cfg *config.Config, stateManager *state.Manager) (*Controller, error) {
	pattern := regexp.MustCompile(cfg.ReadyLogPattern)

	logMux := NewLogMultiplexer(cfg.MCContainerName)

	playerTracker, err := NewPlayerTracker(logMux, cfg.MCJoinLogPattern, cfg.MCLeaveLogPattern)
	if err != nil {
		return nil, fmt.Errorf("failed to create player tracker: %w", err)
	}

	return &Controller{
		cfg:           cfg,
		stateManager:  stateManager,
		readyPattern:  pattern,
		logMux:        logMux,
		playerTracker: playerTracker,
	}, nil
}

func (c *Controller) Start(ctx context.Context) <-chan StartResult {
	resultCh := make(chan StartResult, 1)

	if !c.stateManager.SetStarting() {
		currentState := c.stateManager.GetState()
		resultCh <- StartResult{
			Success:      false,
			ErrorMessage: fmt.Sprintf("서버를 시작할 수 없습니다. 현재 상태: %s", currentState.Korean()),
		}
		close(resultCh)
		return resultCh
	}

	go func() {
		defer close(resultCh)

		containerState, err := dockerctl.InspectContainer(ctx, c.cfg.MCContainerName)
		if err != nil {
			c.stateManager.SetError(err)
			resultCh <- StartResult{
				Success:      false,
				ErrorMessage: fmt.Sprintf("컨테이너 상태 확인 실패: %v", err),
			}
			return
		}

		if !containerState.Exists {
			c.stateManager.SetError(fmt.Errorf("container not found"))
			resultCh <- StartResult{
				Success: false,
				ErrorMessage: fmt.Sprintf(
					"컨테이너 '%s'를 찾을 수 없습니다. 'docker compose create mc-server' 또는 'docker compose up --no-start mc-server' 명령으로 먼저 컨테이너를 생성해주세요.",
					c.cfg.MCContainerName,
				),
			}
			return
		}

		if containerState.Running {
			c.stateManager.SetState(state.StateRunning)
			resultCh <- StartResult{
				Success:      false,
				ErrorMessage: "서버가 이미 실행 중입니다.",
			}
			return
		}

		startTime := time.Now()

		if err := dockerctl.StartContainer(ctx, c.cfg.MCContainerName); err != nil {
			c.stateManager.SetError(err)
			resultCh <- StartResult{
				Success:      false,
				ErrorMessage: fmt.Sprintf("컨테이너 시작 실패: %v", err),
			}
			return
		}

		c.playerTracker.Clear()

		c.logMux.Start(startTime)

		logCtx, logCancel := context.WithTimeout(ctx, c.cfg.ReadyTimeout)
		defer logCancel()

		logCh := c.logMux.Subscribe()

		for logLine := range logCh {
			if logLine.Err != nil {
				log.Printf("로그 읽기 오류: %v", logLine.Err)
				continue
			}

			matches := c.readyPattern.FindStringSubmatch(logLine.Text)
			if len(matches) >= 2 {
				loadSeconds, _ := strconv.ParseFloat(matches[1], 64)
				readyDuration := time.Since(startTime)
				c.stateManager.SetRunning(readyDuration)

				logCancel()

				resultCh <- StartResult{
					Success:       true,
					ReadyDuration: readyDuration,
					LoadSeconds:   loadSeconds,
				}
				return
			}

			select {
			case <-logCtx.Done():
				if logCtx.Err() == context.DeadlineExceeded {
					c.stateManager.SetError(fmt.Errorf("ready timeout exceeded"))
					resultCh <- StartResult{
						Success:      false,
						ErrorMessage: fmt.Sprintf("서버 시작 시간이 %v을 초과했습니다. 서버 로그를 확인해주세요.", c.cfg.ReadyTimeout),
					}
					return
				}
			case <-ctx.Done():
				c.stateManager.SetError(ctx.Err())
				resultCh <- StartResult{
					Success:      false,
					ErrorMessage: "서버 시작이 취소되었습니다.",
				}
				return
			default:
			}
		}

		c.stateManager.SetError(fmt.Errorf("log stream ended unexpectedly"))
		resultCh <- StartResult{
			Success:      false,
			ErrorMessage: "로그 스트림이 예기치 않게 종료되었습니다.",
		}
	}()

	return resultCh
}

func (c *Controller) Stop(ctx context.Context) <-chan StopResult {
	resultCh := make(chan StopResult, 1)

	currentState := c.stateManager.GetState()

	if currentState == state.StateStopping {
		resultCh <- StopResult{
			Success:      false,
			ErrorMessage: "서버가 이미 종료 중입니다.",
		}
		close(resultCh)
		return resultCh
	}

	if currentState == state.StateStopped {
		resultCh <- StopResult{
			Success:      false,
			ErrorMessage: "서버가 이미 종료되어 있습니다.",
		}
		close(resultCh)
		return resultCh
	}

	if currentState == state.StateStarting {
		resultCh <- StopResult{
			Success:      false,
			ErrorMessage: "서버가 시작 중입니다. 시작이 완료된 후 다시 시도해주세요.",
		}
		close(resultCh)
		return resultCh
	}

	if !c.stateManager.SetStopping() {
		resultCh <- StopResult{
			Success:      false,
			ErrorMessage: fmt.Sprintf("서버를 종료할 수 없습니다. 현재 상태: %s", currentState.Korean()),
		}
		close(resultCh)
		return resultCh
	}

	go func() {
		defer close(resultCh)

		containerState, err := dockerctl.InspectContainer(ctx, c.cfg.MCContainerName)
		if err != nil {
			c.stateManager.SetError(err)
			resultCh <- StopResult{
				Success:      false,
				ErrorMessage: fmt.Sprintf("컨테이너 상태 확인 실패: %v", err),
			}
			return
		}

		if !containerState.Exists || !containerState.Running {
			c.playerTracker.Clear()
			c.stateManager.SetStopped()
			resultCh <- StopResult{
				Success:      false,
				ErrorMessage: "서버가 이미 종료되어 있습니다.",
			}
			return
		}

		if err := dockerctl.StopContainer(ctx, c.cfg.MCContainerName, c.cfg.StopTimeoutSeconds); err != nil {
			c.stateManager.SetError(err)
			resultCh <- StopResult{
				Success:      false,
				ErrorMessage: fmt.Sprintf("컨테이너 종료 실패: %v", err),
			}
			return
		}

		c.playerTracker.Clear()
		c.stateManager.SetStopped()
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

	if containerState.Running && info.State == state.StateStopped {
		c.stateManager.SetState(state.StateRunning)
		info.State = state.StateRunning
	} else if !containerState.Running && info.State == state.StateRunning {
		c.playerTracker.Clear()
		c.stateManager.SetStopped()
		info.State = state.StateStopped
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
		if currentState == state.StateStopped || currentState == state.StateError {
			c.stateManager.SetState(state.StateRunning)
		}

		c.logMux.Start(containerState.StartedAt)
	} else {
		if currentState == state.StateRunning {
			c.playerTracker.Clear()
			c.stateManager.SetStopped()
		}
	}

	return nil
}

func (c *Controller) Shutdown() {
	c.logMux.Stop()
}
