package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/snowy/mcbot/internal/config"
	"github.com/snowy/mcbot/internal/discord"
	"github.com/snowy/mcbot/internal/mcserver"
	"github.com/snowy/mcbot/internal/state"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("마크봇을 시작합니다...")

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("설정 로드 실패: %v", err)
	}
	log.Printf("설정 로드 완료 (컨테이너: %s, 역할: %s)", cfg.MCContainerName, cfg.McbotRoleName)

	session, err := discordgo.New("Bot " + cfg.DiscordToken)
	if err != nil {
		log.Fatalf("Discord 세션 생성 실패: %v", err)
	}

	session.Identify.Intents = discordgo.IntentsGuilds | discordgo.IntentsGuildMessages

	stateManager := state.NewManager()

	lifecycleLogger := NewStdLifecycleLogger()

	controller, err := mcserver.NewControllerWithLogger(cfg, stateManager, lifecycleLogger)
	if err != nil {
		log.Fatalf("MC 서버 컨트롤러 생성 실패: %v", err)
	}

	statusEmbed := discord.NewStatusEmbedManager(session, cfg, controller)

	handler := discord.NewHandler(cfg, controller, statusEmbed)

	controller.SetOnStateChange(func(newState state.ServerState) {
		log.Printf("[STATE_CHANGE] 상태 변경 감지: %s", newState.Korean())
		if err := statusEmbed.Update(context.Background()); err != nil {
			log.Printf("상태 변경 시 임베드 업데이트 실패: %v", err)
		}
	})

	playerTracker := controller.GetPlayerTracker()
	playerTracker.SetOnChange(func(players []string) {
		if err := statusEmbed.Update(context.Background()); err != nil {
			log.Printf("플레이어 변경 시 상태 임베드 업데이트 실패: %v", err)
		}
	})

	session.AddHandler(handler.HandleInteraction)

	session.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
		log.Printf("봇이 준비되었습니다: %s#%s", r.User.Username, r.User.Discriminator)

		ctx := context.Background()
		if err := controller.SyncState(ctx); err != nil {
			log.Printf("초기 상태 동기화 실패: %v", err)
		} else {
			log.Printf("초기 상태 동기화 완료: %s", stateManager.GetState().Korean())
		}

		if err := statusEmbed.Init(ctx); err != nil {
			log.Printf("상시 임베드 초기화 실패: %v", err)
		} else {
			log.Printf("상시 임베드 초기화 완료")
		}
	})

	if err := session.Open(); err != nil {
		log.Fatalf("Discord 연결 실패: %v", err)
	}
	defer func() {
		if err := session.Close(); err != nil {
			log.Printf("Discord 세션 종료 실패: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	monitorCtx, monitorCancel := context.WithCancel(context.Background())
	monitorDone := make(chan struct{})

	go serverMonitorLoop(monitorCtx, monitorDone, cfg, controller, statusEmbed)

	log.Println("마크봇이 실행 중입니다. 종료하려면 Ctrl+C를 누르세요.")

	<-stop

	log.Println("마크봇을 종료합니다...")
	monitorCancel()
	<-monitorDone
	controller.Shutdown()
	log.Println("마크봇이 정상적으로 종료되었습니다.")
}

func serverMonitorLoop(
	ctx context.Context,
	done chan<- struct{},
	cfg *config.Config,
	controller *mcserver.Controller,
	statusEmbed *discord.StatusEmbedManager,
) {
	defer close(done)

	if !cfg.AutoRecoverEnabled {
		log.Println("[MONITOR] 자동 복구가 비활성화되어 있습니다")
		<-ctx.Done()
		return
	}

	log.Printf("[MONITOR] 서버 모니터링 시작 (주기: %v, 최대 시도: %d회)",
		cfg.AutoRecoverInterval, cfg.MaxAutoRecoverAttempts)

	ticker := time.NewTicker(cfg.AutoRecoverInterval)
	defer ticker.Stop()

	var autoRestartInProgress atomic.Bool
	var consecutiveFailures int

	for {
		select {
		case <-ctx.Done():
			log.Println("[MONITOR] 서버 모니터링 종료")
			return
		case <-ticker.C:
			if autoRestartInProgress.Load() {
				continue
			}

			status := controller.Status(ctx)

			if status.State != state.StateCrashed {
				if consecutiveFailures > 0 {
					consecutiveFailures = 0
				}
				continue
			}

			if cfg.MaxAutoRecoverAttempts > 0 && consecutiveFailures >= cfg.MaxAutoRecoverAttempts {
				log.Printf("[MONITOR] 최대 자동 복구 시도 횟수(%d회) 초과, 자동 복구 중단",
					cfg.MaxAutoRecoverAttempts)
				continue
			}

			autoRestartInProgress.Store(true)
			consecutiveFailures++

			log.Printf("[LIFECYCLE] event=auto_restart_scheduled attempt=%d/%d",
				consecutiveFailures, cfg.MaxAutoRecoverAttempts)

			if err := statusEmbed.Update(ctx); err != nil {
				log.Printf("[MONITOR] 크래시 상태 임베드 업데이트 실패: %v", err)
			}

			go func(attempt int) {
				defer autoRestartInProgress.Store(false)

				resultCh := controller.Start(context.Background())

				waitCtx, waitCancel := context.WithTimeout(context.Background(), cfg.ServerOperationTimeout)
				defer waitCancel()

				select {
				case result, ok := <-resultCh:
					if !ok {
						log.Printf("[LIFECYCLE] event=auto_restart_failed attempt=%d reason=channel_closed",
							attempt)
						return
					}

					if result.Success {
						log.Printf("[LIFECYCLE] event=auto_restart_succeeded attempt=%d ready_duration=%.2fs",
							attempt, result.ReadyDuration.Seconds())
						consecutiveFailures = 0
					} else {
						log.Printf("[LIFECYCLE] event=auto_restart_failed attempt=%d reason=%s",
							attempt, result.ErrorMessage)
					}

				case <-waitCtx.Done():
					log.Printf("[LIFECYCLE] event=auto_restart_failed attempt=%d reason=timeout",
						attempt)
				}

				if err := statusEmbed.Update(context.Background()); err != nil {
					log.Printf("[MONITOR] 재시작 후 임베드 업데이트 실패: %v", err)
				}
			}(consecutiveFailures)
		}
	}
}
