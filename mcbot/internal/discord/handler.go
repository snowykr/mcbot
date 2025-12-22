package discord

import (
	"context"
	"log"

	"github.com/bwmarrin/discordgo"
	"github.com/snowy/mcbot/internal/config"
	"github.com/snowy/mcbot/internal/mcserver"
	"github.com/snowy/mcbot/internal/state"
)

type Handler struct {
	cfg         *config.Config
	controller  *mcserver.Controller
	statusEmbed *StatusEmbedManager
}

func NewHandler(cfg *config.Config, controller *mcserver.Controller, statusEmbed *StatusEmbedManager) *Handler {
	return &Handler{
		cfg:         cfg,
		controller:  controller,
		statusEmbed: statusEmbed,
	}
}

func (h *Handler) HandleInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type == discordgo.InteractionMessageComponent {
		h.handleComponentInteraction(s, i)
		return
	}

	log.Printf("지원하지 않는 인터랙션 타입: %v (ID: %s)", i.Type, i.ID)
}

func (h *Handler) handleComponentInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	customID := i.MessageComponentData().CustomID

	if customID != ComponentIDToggle {
		return
	}

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

	ctx := context.Background()
	presence := h.controller.Presence(ctx)

	switch presence.ServerState {
	case state.StateStopped, state.StateError:
		h.handleButtonStart(ctx, s, i)
	case state.StateRunning:
		h.handleButtonStop(ctx, s, i)
	default:
		log.Printf("버튼 클릭 무시: 현재 상태 %v", presence.ServerState)
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

func (h *Handler) handleButtonStart(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) {
	presence := h.controller.Presence(ctx)
	resultCh := h.controller.Start(ctx)

	if err := h.statusEmbed.UpdateWithPresence(presence); err != nil {
		log.Printf("상태 임베드 업데이트 실패: %v", err)
	}

	go func() {
		result := <-resultCh

		if err := h.statusEmbed.Update(ctx); err != nil {
			log.Printf("서버 시작 후 상태 임베드 업데이트 실패: %v", err)
		}

		requestedBy := h.getUsername(i)
		var embed *discordgo.MessageEmbed
		if result.Success {
			embed = EmbedStartSuccess(result.ReadyDuration, requestedBy)
		} else {
			log.Printf("서버 시작 실패: %s", result.ErrorMessage)
			embed = EmbedStartFailed(result.ErrorMessage)
		}

		_, err := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
			Embeds: []*discordgo.MessageEmbed{embed},
			Flags:  discordgo.MessageFlagsEphemeral,
		})
		if err != nil {
			log.Printf("결과 메시지 전송 실패: %v", err)
		}
	}()
}

func (h *Handler) handleButtonStop(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) {
	presence := h.controller.Presence(ctx)
	resultCh := h.controller.Stop(ctx)

	if err := h.statusEmbed.UpdateWithPresence(presence); err != nil {
		log.Printf("상태 임베드 업데이트 실패: %v", err)
	}

	go func() {
		result := <-resultCh

		if err := h.statusEmbed.Update(ctx); err != nil {
			log.Printf("서버 종료 후 상태 임베드 업데이트 실패: %v", err)
		}

		requestedBy := h.getUsername(i)
		var embed *discordgo.MessageEmbed
		if result.Success {
			embed = EmbedStopSuccess(requestedBy)
		} else {
			log.Printf("서버 종료 실패: %s", result.ErrorMessage)
			embed = EmbedStopFailed(result.ErrorMessage)
		}

		_, err := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
			Embeds: []*discordgo.MessageEmbed{embed},
			Flags:  discordgo.MessageFlagsEphemeral,
		})
		if err != nil {
			log.Printf("결과 메시지 전송 실패: %v", err)
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
