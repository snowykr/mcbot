package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

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

	controller, err := mcserver.NewController(cfg, stateManager)
	if err != nil {
		log.Fatalf("MC 서버 컨트롤러 생성 실패: %v", err)
	}

	statusEmbed := discord.NewStatusEmbedManager(session, cfg, controller)

	handler := discord.NewHandler(cfg, controller, statusEmbed)

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

		playerTracker := controller.GetPlayerTracker()
		playerTracker.SetOnChange(func(players []string) {
			if err := statusEmbed.Update(ctx); err != nil {
				log.Printf("플레이어 변경 시 상태 임베드 업데이트 실패: %v", err)
			}
		})
	})

	if err := session.Open(); err != nil {
		log.Fatalf("Discord 연결 실패: %v", err)
	}
	defer func() {
		if err := session.Close(); err != nil {
			log.Printf("Discord 세션 종료 실패: %v", err)
		}
	}()

	log.Println("마크봇이 실행 중입니다. 종료하려면 Ctrl+C를 누르세요.")

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	log.Println("마크봇을 종료합니다...")
	log.Println("마크봇이 정상적으로 종료되었습니다.")
}
