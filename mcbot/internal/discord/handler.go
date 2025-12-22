package discord

import (
	"context"
	"fmt"
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
	switch i.Type {
	case discordgo.InteractionApplicationCommand:
		h.handleCommandInteraction(s, i)
	case discordgo.InteractionMessageComponent:
		h.handleComponentInteraction(s, i)
	}
}

func (h *Handler) handleCommandInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.ApplicationCommandData().Name != CommandName {
		return
	}

	if !h.hasRequiredRole(s, i) {
		h.respondEmbed(s, i, EmbedPermissionDenied(h.cfg.McbotRoleName), false)
		return
	}

	options := i.ApplicationCommandData().Options
	if len(options) == 0 {
		h.respondEmbed(s, i, EmbedError("오류", "action 옵션이 필요합니다."), false)
		return
	}

	action := options[0].StringValue()

	switch action {
	case ActionStart:
		h.handleStart(s, i)
	case ActionStop:
		h.handleStop(s, i)
	case ActionStatus:
		h.handleStatus(s, i)
	default:
		h.respondEmbed(s, i, EmbedError("오류", "알 수 없는 action입니다."), false)
	}
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
				Content: "이 버튼을 사용하려면 권한이 필요합니다.",
				Flags:   discordgo.MessageFlagsEphemeral,
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
	resultCh := h.controller.Start(ctx)

	if err := h.statusEmbed.Update(ctx); err != nil {
		log.Printf("상태 임베드 업데이트 실패: %v", err)
	}

	go func() {
		result := <-resultCh

		if err := h.statusEmbed.Update(ctx); err != nil {
			log.Printf("서버 시작 후 상태 임베드 업데이트 실패: %v", err)
		}

		if !result.Success {
			log.Printf("서버 시작 실패: %s", result.ErrorMessage)
			_, err := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
				Content: fmt.Sprintf("❌ 서버 시작 실패: %s", result.ErrorMessage),
				Flags:   discordgo.MessageFlagsEphemeral,
			})
			if err != nil {
				log.Printf("실패 메시지 전송 실패: %v", err)
			}
		} else {
			_, err := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
				Content: fmt.Sprintf("✅ 서버가 성공적으로 시작되었습니다 (%.2f초)", result.LoadSeconds),
				Flags:   discordgo.MessageFlagsEphemeral,
			})
			if err != nil {
				log.Printf("성공 메시지 전송 실패: %v", err)
			}
		}
	}()
}

func (h *Handler) handleButtonStop(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) {
	resultCh := h.controller.Stop(ctx)

	if err := h.statusEmbed.Update(ctx); err != nil {
		log.Printf("상태 임베드 업데이트 실패: %v", err)
	}

	go func() {
		result := <-resultCh

		if err := h.statusEmbed.Update(ctx); err != nil {
			log.Printf("서버 종료 후 상태 임베드 업데이트 실패: %v", err)
		}

		if !result.Success {
			log.Printf("서버 종료 실패: %s", result.ErrorMessage)
			_, err := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
				Content: fmt.Sprintf("❌ 서버 종료 실패: %s", result.ErrorMessage),
				Flags:   discordgo.MessageFlagsEphemeral,
			})
			if err != nil {
				log.Printf("실패 메시지 전송 실패: %v", err)
			}
		} else {
			_, err := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
				Content: "✅ 서버가 정상적으로 종료되었습니다",
				Flags:   discordgo.MessageFlagsEphemeral,
			})
			if err != nil {
				log.Printf("성공 메시지 전송 실패: %v", err)
			}
		}
	}()
}

func (h *Handler) handleStart(s *discordgo.Session, i *discordgo.InteractionCreate) {
	h.respondEmbed(s, i, EmbedStarting(), false)

	ctx := context.Background()
	resultCh := h.controller.Start(ctx)

	go func() {
		result := <-resultCh
		requestedBy := h.getUsername(i)

		var embed *discordgo.MessageEmbed
		if result.Success {
			embed = EmbedStartSuccess(result.LoadSeconds, requestedBy)
		} else {
			embed = EmbedStartFailed(result.ErrorMessage)
		}

		h.editResponseEmbed(s, i, embed)

		if err := h.statusEmbed.Update(ctx); err != nil {
			log.Printf("상태 임베드 업데이트 실패: %v", err)
		}
	}()
}

func (h *Handler) handleStop(s *discordgo.Session, i *discordgo.InteractionCreate) {
	h.respondEmbed(s, i, EmbedStopping(), false)

	ctx := context.Background()
	resultCh := h.controller.Stop(ctx)

	go func() {
		result := <-resultCh
		requestedBy := h.getUsername(i)

		var embed *discordgo.MessageEmbed
		if result.Success {
			embed = EmbedStopSuccess(requestedBy)
		} else {
			embed = EmbedStopFailed(result.ErrorMessage)
		}

		h.editResponseEmbed(s, i, embed)

		if err := h.statusEmbed.Update(ctx); err != nil {
			log.Printf("상태 임베드 업데이트 실패: %v", err)
		}
	}()
}

func (h *Handler) handleStatus(s *discordgo.Session, i *discordgo.InteractionCreate) {
	ctx := context.Background()
	status := h.controller.Status(ctx)

	embed := EmbedStatus(
		status.State.Korean(),
		status.ContainerRunning,
		status.LastStartTime,
		status.LastReadyDuration,
	)

	h.respondEmbed(s, i, embed, true)
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

func (h *Handler) respondEmbed(s *discordgo.Session, i *discordgo.InteractionCreate, embed *discordgo.MessageEmbed, ephemeral bool) {
	var flags discordgo.MessageFlags
	if ephemeral {
		flags = discordgo.MessageFlagsEphemeral
	}

	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{embed},
			Flags:  flags,
		},
	})
	if err != nil {
		log.Printf("응답 전송 실패: %v", err)
	}
}

func (h *Handler) editResponseEmbed(s *discordgo.Session, i *discordgo.InteractionCreate, embed *discordgo.MessageEmbed) {
	_, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Embeds: &[]*discordgo.MessageEmbed{embed},
	})
	if err != nil {
		log.Printf("응답 수정 실패: %v", err)
	}
}
