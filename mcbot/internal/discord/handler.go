package discord

import (
	"context"
	"log"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/snowy/mcbot/internal/config"
	"github.com/snowy/mcbot/internal/mcserver"
	"github.com/snowy/mcbot/internal/state"
)

type ServerController interface {
	Start(ctx context.Context) <-chan mcserver.StartResult
	Stop(ctx context.Context) <-chan mcserver.StopResult
	Presence(ctx context.Context) mcserver.PresenceState
}

type StatusEmbedUpdater interface {
	Update(ctx context.Context) error
}

type Handler struct {
	cfg         *config.Config
	controller  ServerController
	statusEmbed StatusEmbedUpdater
}

func NewHandler(cfg *config.Config, controller ServerController, statusEmbed StatusEmbedUpdater) *Handler {
	return &Handler{
		cfg:         cfg,
		controller:  controller,
		statusEmbed: statusEmbed,
	}
}

func (h *Handler) HandleInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	switch i.Type {
	case discordgo.InteractionMessageComponent:
		h.handleComponentInteraction(s, i)
		return

	case discordgo.InteractionApplicationCommand:
		h.handleApplicationCommand(s, i)
		return

	default:
		h.handleUnsupportedInteraction(s, i)
	}
}

func (h *Handler) handleApplicationCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	commandName := i.ApplicationCommandData().Name
	log.Printf("슬래시 커맨드 수신 (더 이상 지원하지 않음): %s (ID: %s, GuildID: %s, UserID: %s)",
		commandName, i.ID, i.GuildID, i.User.ID)

	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "이 봇은 슬래시 커맨드를 더 이상 지원하지 않습니다.\n버튼을 통해 서버를 제어해주세요.",
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
	if err != nil {
		log.Printf("슬래시 커맨드 응답 실패: %v", err)
	}
}

func (h *Handler) handleUnsupportedInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	log.Printf("지원하지 않는 인터랙션 타입: %v (ID: %s, GuildID: %s, UserID: %s)",
		i.Type, i.ID, i.GuildID, i.User.ID)

	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "지원하지 않는 인터랙션 타입입니다.",
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
	if err != nil {
		log.Printf("인터랙션 응답 실패: %v", err)
	}
}

func (h *Handler) handleComponentInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	customID := i.MessageComponentData().CustomID

	switch {
	case customID == ComponentIDToggle:
		h.handleToggleComponent(s, i)
	case strings.HasPrefix(customID, ComponentIDConfirmStopPrefix):
		h.handleStopConfirmComponent(s, i)
	case strings.HasPrefix(customID, ComponentIDCancelStopPrefix):
		h.handleStopCancelComponent(s, i)
	default:
		log.Printf("알 수 없는 컴포넌트 ID: %s (UserID: %s)", customID, h.getUserID(i))
	}
}

func (h *Handler) handleToggleComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !h.hasRequiredRole(s, i) {
		err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Embeds: []*discordgo.MessageEmbed{EmbedPermissionDenied(h.cfg.McbotRoleName)},
				Flags:  discordgo.MessageFlagsEphemeral,
			},
		})
		if err != nil {
			log.Printf("권한 거부 응답 실패: %v", err)
		}
		return
	}

	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredMessageUpdate,
	})
	if err != nil {
		log.Printf("인터랙션 응답 실패: %v", err)
		return
	}

	presenceCtx, presenceCancel := context.WithTimeout(context.Background(), h.cfg.EmbedUpdateTimeout)
	defer presenceCancel()

	presence := h.controller.Presence(presenceCtx)

	switch presence.ServerState {
	case state.StateStopped, state.StateError:
		h.handleButtonStart(s, i)
	case state.StateRunning:
		h.handleStopConfirmationRequest(s, i)
	default:
		log.Printf("버튼 클릭 무시: 현재 상태 %v", presence.ServerState)
	}
}

func (h *Handler) handleStopConfirmationRequest(s *discordgo.Session, i *discordgo.InteractionCreate) {
	userID := h.getUserID(i)

	confirmButton := discordgo.Button{
		Label:    "닫기",
		Style:    discordgo.DangerButton,
		CustomID: ComponentIDConfirmStopPrefix + userID,
	}

	cancelButton := discordgo.Button{
		Label:    "취소",
		Style:    discordgo.SecondaryButton,
		CustomID: ComponentIDCancelStopPrefix + userID,
	}

	_, err := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Content: "정말 서버를 닫을까요?\n현재 접속 중인 플레이어의 연결이 모두 종료됩니다.",
		Components: []discordgo.MessageComponent{
			discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{
					confirmButton,
					cancelButton,
				},
			},
		},
		Flags: discordgo.MessageFlagsEphemeral,
	})
	if err != nil {
		log.Printf("서버 닫기 확인 메시지 전송 실패: %v", err)
	}
}

func (h *Handler) handleStopConfirmComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	customID := i.MessageComponentData().CustomID
	expectedUserID := strings.TrimPrefix(customID, ComponentIDConfirmStopPrefix)
	actualUserID := h.getUserID(i)

	if expectedUserID != actualUserID {
		err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "이 확인창은 다른 사용자의 요청입니다.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		if err != nil {
			log.Printf("사용자 불일치 응답 실패: %v", err)
		}
		return
	}

	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})
	if err != nil {
		log.Printf("확인 버튼 응답 실패: %v", err)
		return
	}

	h.handleButtonStop(s, i)
}

func (h *Handler) handleStopCancelComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	customID := i.MessageComponentData().CustomID
	expectedUserID := strings.TrimPrefix(customID, ComponentIDCancelStopPrefix)
	actualUserID := h.getUserID(i)

	if expectedUserID != actualUserID {
		err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "이 취소 버튼은 다른 사용자의 요청입니다.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		if err != nil {
			log.Printf("사용자 불일치 응답 실패: %v", err)
		}
		return
	}

	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content:    "서버 닫기 요청이 취소되었습니다.",
			Components: []discordgo.MessageComponent{},
		},
	})
	if err != nil {
		log.Printf("취소 버튼 응답 실패: %v", err)
	}
}

func (h *Handler) hasRequiredRole(s *discordgo.Session, i *discordgo.InteractionCreate) bool {
	if i.Member == nil {
		return false
	}

	guild, err := s.State.Guild(i.GuildID)
	if err != nil {
		guild, err = s.Guild(i.GuildID)
		if err != nil {
			log.Printf("길드 정보 조회 실패: %v", err)
			return false
		}
	}

	for _, roleID := range i.Member.Roles {
		for _, role := range guild.Roles {
			if role.ID == roleID && role.Name == h.cfg.McbotRoleName {
				return true
			}
		}
	}

	return false
}

func (h *Handler) handleButtonStart(s *discordgo.Session, i *discordgo.InteractionCreate) {
	serverCtx := context.Background()
	resultCh := h.controller.Start(serverCtx)

	embedCtx, embedCancel := context.WithTimeout(context.Background(), h.cfg.EmbedUpdateTimeout)
	if err := h.statusEmbed.Update(embedCtx); err != nil {
		log.Printf("상태 임베드 업데이트 실패: %v", err)
	}
	embedCancel()

	go func() {
		waitCtx, waitCancel := context.WithTimeout(context.Background(), h.cfg.ServerOperationTimeout)
		defer waitCancel()

		var result mcserver.StartResult
		var waitTimedOut bool

		select {
		case res, ok := <-resultCh:
			if !ok {
				log.Printf("서버 시작 결과 채널이 값 없이 닫혔습니다 (UserID: %s)", h.getUserID(i))
				result = mcserver.StartResult{
					Success:      false,
					ErrorMessage: "서버 시작 작업이 예기치 않게 종료되었습니다.",
				}
			} else {
				result = res
			}
		case <-waitCtx.Done():
			waitTimedOut = true
			log.Printf("서버 시작 대기 타임아웃 (UserID: %s, Timeout: %v)",
				h.getUserID(i), h.cfg.ServerOperationTimeout)
			result = mcserver.StartResult{
				Success:      false,
				ErrorMessage: "서버 시작 대기 시간이 초과되었습니다. 서버가 나중에 열렸을 수 있으니 상시 임베드를 확인해주세요.",
			}
		}

		updateCtx, updateCancel := context.WithTimeout(context.Background(), h.cfg.EmbedUpdateTimeout)
		defer updateCancel()

		if err := h.statusEmbed.Update(updateCtx); err != nil {
			log.Printf("서버 시작 후 상태 임베드 업데이트 실패: %v", err)
		}

		requestedBy := h.getUsername(i)
		var embed *discordgo.MessageEmbed
		if result.Success {
			embed = EmbedStartSuccess(result.ReadyDuration, requestedBy)
		} else {
			if waitTimedOut {
				log.Printf("서버 시작 대기 타임아웃: %s", result.ErrorMessage)
			} else {
				log.Printf("서버 시작 실패: %s", result.ErrorMessage)
			}
			embed = EmbedStartFailed(result.ErrorMessage)
		}

		if s != nil {
			_, err := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
				Embeds: []*discordgo.MessageEmbed{embed},
				Flags:  discordgo.MessageFlagsEphemeral,
			})
			if err != nil {
				log.Printf("결과 메시지 전송 실패: %v", err)
			}
		}
	}()
}

func (h *Handler) handleButtonStop(s *discordgo.Session, i *discordgo.InteractionCreate) {
	serverCtx := context.Background()
	resultCh := h.controller.Stop(serverCtx)

	embedCtx, embedCancel := context.WithTimeout(context.Background(), h.cfg.EmbedUpdateTimeout)
	if err := h.statusEmbed.Update(embedCtx); err != nil {
		log.Printf("상태 임베드 업데이트 실패: %v", err)
	}
	embedCancel()

	go func() {
		waitCtx, waitCancel := context.WithTimeout(context.Background(), h.cfg.ServerOperationTimeout)
		defer waitCancel()

		var result mcserver.StopResult
		var waitTimedOut bool

		select {
		case res, ok := <-resultCh:
			if !ok {
				log.Printf("서버 종료 결과 채널이 값 없이 닫혔습니다 (UserID: %s)", h.getUserID(i))
				result = mcserver.StopResult{
					Success:      false,
					ErrorMessage: "서버 종료 작업이 예기치 않게 종료되었습니다.",
				}
			} else {
				result = res
			}
		case <-waitCtx.Done():
			waitTimedOut = true
			log.Printf("서버 종료 대기 타임아웃 (UserID: %s, Timeout: %v)",
				h.getUserID(i), h.cfg.ServerOperationTimeout)
			result = mcserver.StopResult{
				Success:      false,
				ErrorMessage: "서버 종료 대기 시간이 초과되었습니다. 서버가 나중에 닫혔을 수 있으니 상시 임베드를 확인해주세요.",
			}
		}

		updateCtx, updateCancel := context.WithTimeout(context.Background(), h.cfg.EmbedUpdateTimeout)
		defer updateCancel()

		if err := h.statusEmbed.Update(updateCtx); err != nil {
			log.Printf("서버 종료 후 상태 임베드 업데이트 실패: %v", err)
		}

		requestedBy := h.getUsername(i)
		var embed *discordgo.MessageEmbed
		if result.Success {
			embed = EmbedStopSuccess(requestedBy)
		} else {
			if waitTimedOut {
				log.Printf("서버 종료 대기 타임아웃: %s", result.ErrorMessage)
			} else {
				log.Printf("서버 종료 실패: %s", result.ErrorMessage)
			}
			embed = EmbedStopFailed(result.ErrorMessage)
		}

		if s != nil {
			_, err := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
				Embeds: []*discordgo.MessageEmbed{embed},
				Flags:  discordgo.MessageFlagsEphemeral,
			})
			if err != nil {
				log.Printf("결과 메시지 전송 실패: %v", err)
			}
		}
	}()
}

func (h *Handler) getUsername(i *discordgo.InteractionCreate) string {
	if i.Member != nil && i.Member.User != nil {
		if i.Member.Nick != "" {
			return i.Member.Nick
		}
		return i.Member.User.Username
	}
	if i.User != nil {
		return i.User.Username
	}
	return "알 수 없음"
}

func (h *Handler) getUserID(i *discordgo.InteractionCreate) string {
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User.ID
	}
	if i.User != nil {
		return i.User.ID
	}
	return "unknown"
}
