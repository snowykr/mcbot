package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/snowy/mcbot/internal/config"
	"github.com/snowy/mcbot/internal/discord"
	"github.com/snowy/mcbot/internal/mcserver"
	"github.com/snowy/mcbot/internal/rcon"
	"github.com/snowy/mcbot/internal/state"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	log.Println("마크봇을 시작합니다...")

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("설정 로드 실패: %v", err)
	}
	log.Printf("설정 로드 완료 (컨테이너: %s, 역할: %s, RCON: %v)", cfg.MCContainerName, cfg.McbotRoleName, cfg.RCONEnabled())

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

	var rconClient discord.RCONExecutor
	if cfg.RCONEnabled() {
		rconClient = rcon.NewClient(cfg.RCONHost, cfg.RCONPort, cfg.RCONPassword, cfg.RCONTimeout)
	}

	handler := discord.NewHandler(cfg, controller, statusEmbed, rconClient)

	controller.SetOnStateChange(func(newState state.ServerState) {
		log.Printf("[STATE_CHANGE] 상태 변경 감지: %s", newState.Korean())
		ctx, cancel := context.WithTimeout(context.Background(), cfg.EmbedUpdateTimeout)
		defer cancel()
		if err := statusEmbed.Update(ctx); err != nil {
			log.Printf("상태 변경 시 임베드 업데이트 실패: %v", err)
		}
	})

	playerTracker := controller.GetPlayerTracker()
	playerTracker.SetOnChange(func(players []string) {
		ctx, cancel := context.WithTimeout(context.Background(), cfg.EmbedUpdateTimeout)
		defer cancel()
		if err := statusEmbed.Update(ctx); err != nil {
			log.Printf("플레이어 변경 시 상태 임베드 업데이트 실패: %v", err)
		}
	})

	session.AddHandler(handler.HandleInteraction)

	session.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
		log.Printf("봇이 준비되었습니다: %s#%s", r.User.Username, r.User.Discriminator)

		ctx := context.Background()
		controller.StartStateChangeWorker(ctx)

		if err := controller.SyncState(ctx); err != nil {
			log.Printf("초기 상태 동기화 실패: %v", err)
		} else {
			log.Printf("초기 상태 동기화 완료: %s", stateManager.GetState().Korean())
		}

		controller.StartRuntimeWatchers(context.Background())

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

	registeredCommands := registerSlashCommands(session)
	defer cleanupSlashCommands(session, registeredCommands)

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
		performFinalUpdate(statusEmbed)
		return
	}

	log.Printf("[MONITOR] 서버 모니터링 시작 (주기: %v, 최대 시도: %d회)",
		cfg.AutoRecoverInterval, cfg.MaxAutoRecoverAttempts)

	ticker := time.NewTicker(cfg.AutoRecoverInterval)
	defer ticker.Stop()

	outcomeCh := make(chan restartOutcome, 1)

	restartSem := make(chan struct{}, 1)

	var consecutiveAttempts int

	for {
		select {
		case <-ctx.Done():
			log.Println("[MONITOR] 서버 모니터링 종료 중...")
			drainRestartGoroutine(restartSem, outcomeCh, 500*time.Millisecond)
			performFinalUpdate(statusEmbed)
			log.Println("[MONITOR] 서버 모니터링 종료 완료")
			return

		case outcome := <-outcomeCh:
			<-restartSem

			if outcome.success {
				consecutiveAttempts = 0
				log.Printf("[LIFECYCLE] event=auto_restart_succeeded attempt=%d", outcome.attempt)
			} else {
				log.Printf("[LIFECYCLE] event=auto_restart_failed attempt=%d reason=%s", outcome.attempt, outcome.reason)
			}

			updateCtx, updateCancel := context.WithTimeout(ctx, cfg.EmbedUpdateTimeout)
			if err := statusEmbed.Update(updateCtx); err != nil {
				log.Printf("[MONITOR] 재시작 결과 임베드 업데이트 실패: %v", err)
			}
			updateCancel()

		case <-ticker.C:
			select {
			case restartSem <- struct{}{}:
			default:
				continue
			}

			status := controller.Status(ctx)

			if status.State != state.StateCrashed {
				<-restartSem
				if consecutiveAttempts > 0 {
					consecutiveAttempts = 0
				}
				continue
			}

			if cfg.MaxAutoRecoverAttempts > 0 && consecutiveAttempts >= cfg.MaxAutoRecoverAttempts {
				<-restartSem
				log.Printf("[MONITOR] 최대 자동 복구 시도 횟수(%d회) 초과, 자동 복구 중단",
					cfg.MaxAutoRecoverAttempts)
				continue
			}

			consecutiveAttempts++

			log.Printf("[LIFECYCLE] event=auto_restart_scheduled attempt=%d/%d",
				consecutiveAttempts, cfg.MaxAutoRecoverAttempts)

			updateCtx, updateCancel := context.WithTimeout(ctx, cfg.EmbedUpdateTimeout)
			if err := statusEmbed.Update(updateCtx); err != nil {
				log.Printf("[MONITOR] 크래시 상태 임베드 업데이트 실패: %v", err)
			}
			updateCancel()

			go runRestartOperation(ctx, controller, cfg.ServerOperationTimeout, consecutiveAttempts, outcomeCh)
		}
	}
}

func runRestartOperation(
	ctx context.Context,
	controller *mcserver.Controller,
	timeout time.Duration,
	attempt int,
	outcomeCh chan<- restartOutcome,
) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[MONITOR] Auto-restart goroutine recovered from panic: %v", r)
			trySendOutcome(ctx, outcomeCh, restartOutcome{attempt: attempt, success: false, reason: "panic"})
		}
	}()

	resultCh := controller.Start(context.Background())

	waitCtx, waitCancel := context.WithTimeout(context.Background(), timeout)
	defer waitCancel()

	var outcome restartOutcome
	outcome.attempt = attempt

	select {
	case result, ok := <-resultCh:
		if !ok {
			outcome.success = false
			outcome.reason = "channel_closed"
		} else if result.Success {
			outcome.success = true
		} else {
			outcome.success = false
			outcome.reason = result.ErrorMessage
		}

	case <-waitCtx.Done():
		outcome.success = false
		outcome.reason = "timeout"

	case <-ctx.Done():
		outcome.success = false
		outcome.reason = "monitor_shutdown"
	}

	trySendOutcome(ctx, outcomeCh, outcome)
}

type restartOutcome struct {
	attempt int
	success bool
	reason  string
}

func trySendOutcome(ctx context.Context, outcomeCh chan<- restartOutcome, outcome restartOutcome) {
	select {
	case outcomeCh <- outcome:
		return
	default:
	}

	select {
	case outcomeCh <- outcome:
	case <-ctx.Done():
	}
}

func drainRestartGoroutine(sem <-chan struct{}, outcomeCh <-chan restartOutcome, graceTimeout time.Duration) {
	select {
	case <-sem:
		select {
		case <-outcomeCh:
			log.Println("[MONITOR] 진행 중인 재시작 작업 결과 수신 완료")
		case <-time.After(graceTimeout):
			log.Println("[MONITOR] 진행 중인 재시작 작업 결과 대기 타임아웃")
		}
	default:
	}
}

func performFinalUpdate(statusEmbed *discord.StatusEmbedManager) {
	if err := statusEmbed.UpdateToOffline(); err != nil {
		log.Printf("[MONITOR] 봇 오프라인 상태 업데이트 실패: %v", err)
	} else {
		log.Println("[MONITOR] 봇 오프라인 상태 업데이트 완료")
	}
}

// slashCommands defines all slash commands to register.
// RCON subcommand is always included regardless of RCON_PASSWORD configuration.
//
// UX Trade-off: Discord caches slash commands, so conditional registration
// causes poor UX - users must refresh Discord client to see new commands.
// Instead, we always register all commands and handle disabled features
// gracefully at runtime with ephemeral error messages.
var slashCommands = []*discordgo.ApplicationCommand{
	{
		Name:        "마크봇",
		Description: "마크봇 명령어",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        "rcon",
				Description: "마인크래프트 서버에 RCON 명령어 실행",
				Options: []*discordgo.ApplicationCommandOption{
					{
						Type:        discordgo.ApplicationCommandOptionString,
						Name:        "command",
						Description: "실행할 RCON 명령어",
						Required:    true,
					},
				},
			},
		},
	},
}

func registerSlashCommands(s *discordgo.Session) []*discordgo.ApplicationCommand {
	registered := make([]*discordgo.ApplicationCommand, 0, len(slashCommands))
	for _, cmd := range slashCommands {
		created, err := s.ApplicationCommandCreate(s.State.User.ID, "", cmd)
		if err != nil {
			log.Printf("슬래시 커맨드 등록 실패 (%s): %v", cmd.Name, err)
			continue
		}
		registered = append(registered, created)
		log.Printf("슬래시 커맨드 등록 완료: /%s", cmd.Name)
	}
	return registered
}

func cleanupSlashCommands(s *discordgo.Session, commands []*discordgo.ApplicationCommand) {
	for _, cmd := range commands {
		if err := s.ApplicationCommandDelete(s.State.User.ID, "", cmd.ID); err != nil {
			log.Printf("슬래시 커맨드 삭제 실패 (%s): %v", cmd.Name, err)
		} else {
			log.Printf("슬래시 커맨드 삭제 완료: /%s", cmd.Name)
		}
	}
}
