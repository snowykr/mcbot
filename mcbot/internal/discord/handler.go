package discord

import (
	"context"
	"log"
	"strings"
	"sync"
	"time"

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

type CurrentStatusMessageChecker interface {
	IsCurrentStatusMessage(channelID, messageID string) bool
}

type ChannelConfigurationService interface {
	ConfigureChannel(ctx context.Context, channelID string, presence mcserver.PresenceState) error
}

type RCONExecutor interface {
	Execute(ctx context.Context, command string) (string, error)
}

type StopConfirmationContext struct {
	ConfirmationID string
	UserID         string
	Interaction    *discordgo.Interaction
	MessageID      string
}

const defaultStopConfirmationTTL = 15 * time.Minute

const requiredChannelPermissions = discordgo.PermissionViewChannel |
	discordgo.PermissionSendMessages |
	discordgo.PermissionEmbedLinks |
	discordgo.PermissionReadMessageHistory

type stopConfirmationEntry struct {
	ctx       StopConfirmationContext
	expiresAt time.Time
}

type StopConfirmationStore struct {
	mu  sync.RWMutex
	m   map[string]stopConfirmationEntry
	ttl time.Duration
}

func NewStopConfirmationStore() *StopConfirmationStore {
	return &StopConfirmationStore{
		m:   make(map[string]stopConfirmationEntry),
		ttl: defaultStopConfirmationTTL,
	}
}

func (s *StopConfirmationStore) Save(ctx StopConfirmationContext) {
	expiresAt := time.Now().Add(s.ttl)

	s.mu.Lock()
	s.m[ctx.ConfirmationID] = stopConfirmationEntry{
		ctx:       ctx,
		expiresAt: expiresAt,
	}
	s.mu.Unlock()

	time.AfterFunc(s.ttl, func() {
		s.deleteIfExpired(ctx.ConfirmationID, expiresAt)
	})
}

func (s *StopConfirmationStore) Get(confirmationID string) (StopConfirmationContext, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.m[confirmationID]
	if !ok {
		return StopConfirmationContext{}, false
	}
	if !time.Now().Before(entry.expiresAt) {
		delete(s.m, confirmationID)
		return StopConfirmationContext{}, false
	}

	return entry.ctx, true
}

func (s *StopConfirmationStore) Delete(confirmationID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, confirmationID)
}

func (s *StopConfirmationStore) deleteIfExpired(confirmationID string, expiresAt time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.m[confirmationID]
	if !ok {
		return
	}
	if !entry.expiresAt.Equal(expiresAt) {
		return
	}
	if time.Now().Before(entry.expiresAt) {
		return
	}

	delete(s.m, confirmationID)
}

type Handler struct {
	cfg                   *config.Config
	controller            ServerController
	statusEmbed           StatusEmbedUpdater
	rconClient            RCONExecutor
	channelConfigurator   ChannelConfigurationService
	stopConfirmationStore *StopConfirmationStore
}

type HandlerOption func(*Handler)

func WithChannelConfigurator(channelConfigurator ChannelConfigurationService) HandlerOption {
	return func(h *Handler) {
		h.channelConfigurator = channelConfigurator
	}
}

func NewHandler(cfg *config.Config, controller ServerController, statusEmbed StatusEmbedUpdater, rconClient RCONExecutor, opts ...HandlerOption) *Handler {
	h := &Handler{
		cfg:                   cfg,
		controller:            controller,
		statusEmbed:           statusEmbed,
		rconClient:            rconClient,
		stopConfirmationStore: NewStopConfirmationStore(),
	}

	for _, opt := range opts {
		if opt != nil {
			opt(h)
		}
	}

	return h
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
	data := i.ApplicationCommandData()
	commandName := data.Name

	if commandName == "마크봇" && len(data.Options) > 0 {
		subCommand := data.Options[0]
		if subCommand.Name == "rcon" {
			h.handleRconCommand(s, i, subCommand.Options)
			return
		}
		if subCommand.Name == "채널" {
			h.handleChannelCommand(s, i, subCommand.Options)
			return
		}
	}

	log.Printf("알 수 없는 슬래시 커맨드: %s (UserID: %s)", commandName, h.getUserID(i))
	h.respondEphemeral(s, i, "알 수 없는 명령어입니다.")
}

func (h *Handler) handleUnsupportedInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	log.Printf("지원하지 않는 인터랙션 타입: %v (ID: %s, GuildID: %s, UserID: %s)",
		i.Type, i.ID, i.GuildID, h.getUserID(i))

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
	if !h.ensureTrustedGuild(s, i) {
		return
	}

	if !h.hasRequiredRole(s, i) {
		h.respondPermissionDenied(s, i)
		return
	}

	if !h.isCurrentStatusToggle(i) {
		log.Printf("이전 상태 메시지 토글 무시 (ChannelID: %s, MessageID: %s, UserID: %s)",
			h.getInteractionMessageChannelID(i), h.getInteractionMessageID(i), h.getUserID(i))
		h.respondEphemeral(s, i, "이전 제어 메시지입니다. 최신 상태 메시지를 사용해주세요.")
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

func (h *Handler) isCurrentStatusToggle(i *discordgo.InteractionCreate) bool {
	checker, ok := h.statusEmbed.(CurrentStatusMessageChecker)
	if !ok {
		return true
	}
	if i == nil || i.Message == nil {
		return false
	}
	return checker.IsCurrentStatusMessage(i.Message.ChannelID, i.Message.ID)
}

func (h *Handler) getInteractionMessageID(i *discordgo.InteractionCreate) string {
	if i == nil || i.Message == nil {
		return ""
	}
	return i.Message.ID
}

func (h *Handler) getInteractionMessageChannelID(i *discordgo.InteractionCreate) string {
	if i == nil || i.Message == nil {
		return ""
	}
	return i.Message.ChannelID
}

func (h *Handler) handleStopConfirmationRequest(s *discordgo.Session, i *discordgo.InteractionCreate) {
	userID := h.getUserID(i)
	confirmationID := i.ID

	confirmButton := discordgo.Button{
		Label:    "닫기",
		Style:    discordgo.DangerButton,
		CustomID: ComponentIDConfirmStopPrefix + confirmationID,
	}

	cancelButton := discordgo.Button{
		Label:    "취소",
		Style:    discordgo.SecondaryButton,
		CustomID: ComponentIDCancelStopPrefix + confirmationID,
	}

	msg, err := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
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
		return
	}

	ctx := StopConfirmationContext{
		ConfirmationID: confirmationID,
		UserID:         userID,
		Interaction:    i.Interaction,
		MessageID:      msg.ID,
	}
	h.stopConfirmationStore.Save(ctx)
}

func (h *Handler) handleStopConfirmComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !h.ensureTrustedGuild(s, i) {
		return
	}

	customID := i.MessageComponentData().CustomID
	confirmationID := strings.TrimPrefix(customID, ComponentIDConfirmStopPrefix)

	ctx, ok := h.stopConfirmationStore.Get(confirmationID)
	if !ok {
		err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "이 확인 요청은 이미 처리되었거나 만료되었습니다.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		if err != nil {
			log.Printf("만료된 확인 요청 응답 실패: %v", err)
		}
		return
	}

	actualUserID := h.getUserID(i)
	if ctx.UserID != actualUserID {
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

	if !h.hasRequiredRole(s, i) {
		h.stopConfirmationStore.Delete(confirmationID)
		h.respondPermissionDenied(s, i)
		return
	}

	err := s.FollowupMessageDelete(ctx.Interaction, ctx.MessageID)
	if err != nil {
		log.Printf("확인 메시지 삭제 실패 (무시하고 계속): %v", err)
	}

	err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})
	if err != nil {
		log.Printf("확인 버튼 응답 실패: %v", err)
		h.stopConfirmationStore.Delete(confirmationID)
		return
	}

	h.stopConfirmationStore.Delete(confirmationID)
	h.handleButtonStop(s, i)
}

func (h *Handler) handleStopCancelComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !h.ensureTrustedGuild(s, i) {
		return
	}

	customID := i.MessageComponentData().CustomID
	confirmationID := strings.TrimPrefix(customID, ComponentIDCancelStopPrefix)

	ctx, ok := h.stopConfirmationStore.Get(confirmationID)
	if !ok {
		err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "이 취소 요청은 이미 처리되었거나 만료되었습니다.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		if err != nil {
			log.Printf("만료된 취소 요청 응답 실패: %v", err)
		}
		return
	}

	actualUserID := h.getUserID(i)
	if ctx.UserID != actualUserID {
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

	err := s.FollowupMessageDelete(ctx.Interaction, ctx.MessageID)
	if err != nil {
		log.Printf("확인 메시지 삭제 실패 (무시하고 계속): %v", err)
	}

	err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "서버 닫기 작업이 취소되었습니다.",
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
	if err != nil {
		log.Printf("취소 응답 실패: %v", err)
	}

	h.stopConfirmationStore.Delete(confirmationID)
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

func (h *Handler) isTrustedGuild(i *discordgo.InteractionCreate) bool {
	if h.cfg == nil || strings.TrimSpace(h.cfg.TrustedGuildID) == "" {
		return false
	}

	return i.GuildID != "" && i.GuildID == h.cfg.TrustedGuildID
}

func noAllowedMentions() *discordgo.MessageAllowedMentions {
	return &discordgo.MessageAllowedMentions{
		Parse: []discordgo.AllowedMentionType{},
	}
}

func (h *Handler) respondPermissionDenied(s *discordgo.Session, i *discordgo.InteractionCreate) {
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds:          []*discordgo.MessageEmbed{EmbedPermissionDenied(h.cfg.McbotRoleName)},
			Flags:           discordgo.MessageFlagsEphemeral,
			AllowedMentions: noAllowedMentions(),
		},
	})
	if err != nil {
		log.Printf("권한 거부 응답 실패: %v", err)
	}
}

func (h *Handler) ensureTrustedGuild(s *discordgo.Session, i *discordgo.InteractionCreate) bool {
	if h.isTrustedGuild(i) {
		return true
	}

	log.Printf("신뢰되지 않은 길드에서 privileged interaction 거부 (GuildID: %s, UserID: %s)", i.GuildID, h.getUserID(i))
	h.respondEphemeral(s, i, "🚫 이 서버에서는 사용할 수 없는 명령어입니다.")
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

func (h *Handler) getBotUserID(s *discordgo.Session) string {
	if s != nil && s.State != nil && s.State.User != nil && s.State.User.ID != "" {
		return s.State.User.ID
	}

	if s != nil {
		me, err := s.User("@me")
		if err == nil && me != nil && me.ID != "" {
			return me.ID
		}
		if err != nil {
			log.Printf("봇 사용자 ID 조회 실패: %v", err)
		}
	}

	return ""
}

func (h *Handler) respondEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content:         content,
			Flags:           discordgo.MessageFlagsEphemeral,
			AllowedMentions: noAllowedMentions(),
		},
	})
	if err != nil {
		log.Printf("ephemeral 응답 실패: %v", err)
	}
}

func (h *Handler) respondEphemeralFollowup(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	_, err := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Content:         content,
		Flags:           discordgo.MessageFlagsEphemeral,
		AllowedMentions: noAllowedMentions(),
	})
	if err != nil {
		log.Printf("ephemeral followup 응답 실패: %v", err)
	}
}

func (h *Handler) handleRconCommand(s *discordgo.Session, i *discordgo.InteractionCreate, options []*discordgo.ApplicationCommandInteractionDataOption) {
	userID := h.getUserID(i)
	username := h.getUsername(i)

	if !h.ensureTrustedGuild(s, i) {
		return
	}

	if !h.hasRequiredRole(s, i) {
		log.Printf("[RCON] 권한 거부 (User: %s, ID: %s)", username, userID)
		h.respondEphemeral(s, i, "🚫 `"+EscapeDiscordText(h.cfg.McbotRoleName)+"` 역할이 필요합니다.")
		return
	}

	// RCON command is always registered regardless of RCON_PASSWORD configuration (UX trade-off).
	// Return a friendly error if RCON is not configured for this deployment.
	if h.rconClient == nil {
		log.Printf("[RCON] RCON 클라이언트 없음 (RCON_PASSWORD 미설정) (User: %s, ID: %s)", username, userID)
		h.respondEphemeral(s, i, "❌ RCON이 활성화되지 않았습니다. `RCON_PASSWORD` 환경 변수 설정 후 봇을 재시작해주세요.")
		return
	}

	var command string
	for _, opt := range options {
		if opt.Name == "command" {
			command = opt.StringValue()
			break
		}
	}

	if command == "" {
		h.respondEphemeral(s, i, "❌ 명령어를 입력해주세요.")
		return
	}

	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags:           discordgo.MessageFlagsEphemeral,
			AllowedMentions: noAllowedMentions(),
		},
	})
	if err != nil {
		log.Printf("[RCON] deferred 응답 실패: %v", err)
		return
	}

	presenceCtx, presenceCancel := context.WithTimeout(context.Background(), h.cfg.EmbedUpdateTimeout)
	defer presenceCancel()

	presence := h.controller.Presence(presenceCtx)

	if presence.ServerState != state.StateRunning {
		h.respondEphemeralFollowup(s, i, "❌ 서버가 실행 중이 아닙니다. (현재 상태: "+presence.ServerState.Korean()+")")
		return
	}

	log.Printf("[RCON] 명령 실행 (User: %s, ID: %s)", username, userID)

	ctx, cancel := context.WithTimeout(context.Background(), h.cfg.RCONTimeout)
	defer cancel()

	response, rconErr := h.rconClient.Execute(ctx, command)

	var content string
	if rconErr != nil {
		log.Printf("[RCON] 실행 실패 (User: %s, ID: %s, Error: %v)", username, userID, rconErr)
		content = "❌ RCON 실행 실패: " + EscapeDiscordText(rconErr.Error())
	} else {
		log.Printf("[RCON] 실행 성공 (User: %s, ID: %s)", username, userID)
		content = "✅ 명령 실행 완료"
		if response != "" {
			content += "\n```txt\n" + formatRCONResponse(response) + "\n```"
		}
	}

	h.respondEphemeralFollowup(s, i, content)
}

func (h *Handler) handleChannelCommand(s *discordgo.Session, i *discordgo.InteractionCreate, options []*discordgo.ApplicationCommandInteractionDataOption) {
	userID := h.getUserID(i)
	username := h.getUsername(i)

	if !h.ensureTrustedGuild(s, i) {
		return
	}

	if !h.hasRequiredRole(s, i) {
		log.Printf("[CHANNEL] 권한 거부 (User: %s, ID: %s)", username, userID)
		h.respondPermissionDenied(s, i)
		return
	}

	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags:           discordgo.MessageFlagsEphemeral,
			AllowedMentions: noAllowedMentions(),
		},
	})
	if err != nil {
		log.Printf("[CHANNEL] deferred 응답 실패: %v", err)
		return
	}

	channel, validationMessage := h.resolveChannelCommandTarget(s, options)
	if validationMessage != "" {
		h.respondEphemeralFollowup(s, i, validationMessage)
		return
	}

	if h.channelConfigurator == nil {
		h.respondEphemeralFollowup(s, i, "❌ 채널 설정 기능이 준비되지 않았습니다.")
		return
	}

	presenceCtx, presenceCancel := context.WithTimeout(context.Background(), h.cfg.EmbedUpdateTimeout)
	presence := h.controller.Presence(presenceCtx)
	presenceCancel()

	configureCtx, configureCancel := context.WithTimeout(context.Background(), h.cfg.ServerOperationTimeout)
	defer configureCancel()

	if err := h.channelConfigurator.ConfigureChannel(configureCtx, channel.ID, presence); err != nil {
		log.Printf("[CHANNEL] 설정 실패 (User: %s, ID: %s, ChannelID: %s, Error: %v)", username, userID, channel.ID, err)
		h.respondEphemeralFollowup(s, i, "❌ 채널 설정 실패: "+EscapeDiscordText(err.Error()))
		return
	}

	log.Printf("[CHANNEL] 설정 성공 (User: %s, ID: %s, ChannelID: %s)", username, userID, channel.ID)
	h.respondEphemeralFollowup(s, i, "✅ 상태 임베드 채널을 <#"+channel.ID+">로 설정했습니다.")
}

func (h *Handler) resolveChannelCommandTarget(s *discordgo.Session, options []*discordgo.ApplicationCommandInteractionDataOption) (*discordgo.Channel, string) {
	var channelOption *discordgo.ApplicationCommandInteractionDataOption
	for _, opt := range options {
		if opt.Name == "channel" && opt.Type == discordgo.ApplicationCommandOptionChannel {
			channelOption = opt
			break
		}
	}

	if channelOption == nil {
		return nil, "❌ 채널을 선택해주세요."
	}

	channel := channelOption.ChannelValue(s)
	if channel == nil || channel.ID == "" {
		return nil, "❌ 선택한 채널 정보를 확인할 수 없습니다."
	}

	if channel.Type != discordgo.ChannelTypeGuildText && channel.Type != discordgo.ChannelTypeGuildNews {
		return nil, "❌ 상태 임베드 채널은 일반 채팅 채널 또는 공지 채널만 선택할 수 있습니다."
	}

	if strings.TrimSpace(channel.GuildID) != h.cfg.TrustedGuildID {
		return nil, "❌ 선택한 채널은 이 서버의 채널이 아닙니다."
	}

	botUserID := h.getBotUserID(s)
	if botUserID == "" {
		return nil, "❌ 봇 사용자 정보를 확인할 수 없습니다."
	}

	permissions, err := h.getBotChannelPermissions(s, botUserID, channel.ID)
	if err != nil {
		log.Printf("[CHANNEL] 권한 계산 실패 (BotUserID: %s, ChannelID: %s, Error: %v)", botUserID, channel.ID, err)
		return nil, "❌ 봇이 선택한 채널의 권한을 확인할 수 없습니다."
	}

	if permissions&requiredChannelPermissions != requiredChannelPermissions {
		return nil, "❌ 봇에게 채널 보기, 메시지 전송, 임베드 링크, 메시지 기록 읽기 권한이 필요합니다."
	}

	return channel, ""
}

func (h *Handler) getBotChannelPermissions(s *discordgo.Session, botUserID, channelID string) (int64, error) {
	if s == nil {
		return 0, nil
	}

	if s.State != nil {
		if permissions, err := s.State.UserChannelPermissions(botUserID, channelID); err == nil {
			return permissions, nil
		}
	}

	return s.UserChannelPermissions(botUserID, channelID)
}

func formatRCONResponse(response string) string {
	const maxResponseRunes = 1800
	const truncatedSuffix = "\n... (응답이 너무 길어 잘렸습니다)"

	safe := strings.ReplaceAll(response, "```", "``\u200b`")
	runes := []rune(safe)
	if len(runes) <= maxResponseRunes {
		return safe
	}

	suffixRunes := []rune(truncatedSuffix)
	truncateAt := maxResponseRunes - len(suffixRunes)
	if truncateAt < 0 {
		truncateAt = 0
	}

	return string(runes[:truncateAt]) + truncatedSuffix
}
