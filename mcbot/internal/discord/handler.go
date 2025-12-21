package discord

import (
	"context"
	"log"

	"github.com/bwmarrin/discordgo"
	"github.com/snowy/mcbot/internal/config"
	"github.com/snowy/mcbot/internal/mcserver"
)

type Handler struct {
	cfg        *config.Config
	controller *mcserver.Controller
}

func NewHandler(cfg *config.Config, controller *mcserver.Controller) *Handler {
	return &Handler{
		cfg:        cfg,
		controller: controller,
	}
}

func (h *Handler) HandleInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}

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
