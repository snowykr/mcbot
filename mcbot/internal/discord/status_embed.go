package discord

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/bwmarrin/discordgo"
	"github.com/snowy/mcbot/internal/config"
	"github.com/snowy/mcbot/internal/mcserver"
)

type discordSession interface {
	ChannelMessageEditComplex(*discordgo.MessageEdit) (*discordgo.Message, error)
	ChannelMessageSendComplex(string, *discordgo.MessageSend) (*discordgo.Message, error)
	ChannelMessages(string, int, string, string, string) ([]*discordgo.Message, error)
}

type serverController interface {
	Presence(context.Context) mcserver.PresenceState
}

type discordSessionWrapper struct {
	*discordgo.Session
}

func (w *discordSessionWrapper) ChannelMessageEditComplex(edit *discordgo.MessageEdit) (*discordgo.Message, error) {
	return w.Session.ChannelMessageEditComplex(edit)
}

func (w *discordSessionWrapper) ChannelMessageSendComplex(channelID string, data *discordgo.MessageSend) (*discordgo.Message, error) {
	return w.Session.ChannelMessageSendComplex(channelID, data)
}

func (w *discordSessionWrapper) ChannelMessages(channelID string, limit int, beforeID, afterID, aroundID string) ([]*discordgo.Message, error) {
	return w.Session.ChannelMessages(channelID, limit, beforeID, afterID, aroundID)
}

type StatusEmbedManager struct {
	session     discordSession
	realSession *discordgo.Session
	cfg         *config.Config
	controller  serverController
	messageID   string
	mu          sync.RWMutex
}

func NewStatusEmbedManager(session *discordgo.Session, cfg *config.Config, controller *mcserver.Controller) *StatusEmbedManager {
	return &StatusEmbedManager{
		session:     &discordSessionWrapper{Session: session},
		realSession: session,
		cfg:         cfg,
		controller:  controller,
	}
}

func (m *StatusEmbedManager) Init(ctx context.Context) error {
	existingMsg, err := m.findExistingMessage()
	if err != nil {
		log.Printf("기존 메시지 검색 실패: %v", err)
	}

	if existingMsg != nil {
		m.mu.Lock()
		m.messageID = existingMsg.ID
		m.mu.Unlock()
		log.Printf("기존 상시 임베드 메시지 발견: %s", existingMsg.ID)
	} else {
		if err := m.createNewMessage(ctx); err != nil {
			return fmt.Errorf("새 메시지 생성 실패: %w", err)
		}
		log.Printf("새 상시 임베드 메시지 생성: %s", m.messageID)
	}

	if err := m.Update(ctx); err != nil {
		log.Printf("초기 상태 업데이트 실패: %v", err)
	}

	return nil
}

func (m *StatusEmbedManager) findExistingMessage() (*discordgo.Message, error) {
	messages, err := m.session.ChannelMessages(m.cfg.EmbedChannelID, 100, "", "", "")
	if err != nil {
		return nil, fmt.Errorf("채널 메시지 조회 실패: %w", err)
	}

	botID := m.realSession.State.User.ID

	for _, msg := range messages {
		if msg.Author.ID != botID {
			continue
		}

		if len(msg.Embeds) == 0 {
			continue
		}

		embed := msg.Embeds[0]
		if embed.Footer != nil && embed.Footer.Text == EmbedMarkerFooter {
			return msg, nil
		}
	}

	return nil, nil
}

func (m *StatusEmbedManager) createNewMessage(ctx context.Context) error {
	presence := m.controller.Presence(ctx)
	embed := EmbedPersistentStatus(presence)
	button := BuildToggleButton(presence)

	msg, err := m.session.ChannelMessageSendComplex(m.cfg.EmbedChannelID, &discordgo.MessageSend{
		Embeds: []*discordgo.MessageEmbed{embed},
		Components: []discordgo.MessageComponent{
			discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{button},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("메시지 전송 실패: %w", err)
	}

	m.mu.Lock()
	m.messageID = msg.ID
	m.mu.Unlock()

	return nil
}

func isUnknownMessageError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := strings.ToLower(err.Error())
	return strings.Contains(errMsg, "unknown message") || strings.Contains(errMsg, "10008")
}

func (m *StatusEmbedManager) Update(ctx context.Context) error {
	m.mu.RLock()
	msgID := m.messageID
	m.mu.RUnlock()

	if msgID == "" {
		log.Printf("상시 임베드가 아직 초기화되지 않아 업데이트를 건너뜁니다")
		return nil
	}

	presence := m.controller.Presence(ctx)
	return m.UpdateWithPresence(presence)
}

func (m *StatusEmbedManager) UpdateWithPresence(presence mcserver.PresenceState) error {
	const maxUnknownMessageRetries = 2

	for attempt := 0; attempt <= maxUnknownMessageRetries; attempt++ {
		m.mu.RLock()
		msgID := m.messageID
		m.mu.RUnlock()

		if msgID == "" {
			return fmt.Errorf("메시지 ID가 설정되지 않음")
		}

		embed := EmbedPersistentStatus(presence)
		button := BuildToggleButton(presence)

		embeds := []*discordgo.MessageEmbed{embed}
		components := []discordgo.MessageComponent{
			discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{button},
			},
		}

		_, err := m.session.ChannelMessageEditComplex(&discordgo.MessageEdit{
			Channel:    m.cfg.EmbedChannelID,
			ID:         msgID,
			Embeds:     &embeds,
			Components: &components,
		})

		if err == nil {
			return nil
		}

		if isUnknownMessageError(err) {
			log.Printf("상시 임베드 메시지가 삭제되었습니다. 새 메시지를 생성합니다. (시도 %d/%d)", attempt+1, maxUnknownMessageRetries+1)

			m.mu.Lock()
			m.messageID = ""
			m.mu.Unlock()

			recreateCtx := context.Background()
			if err := m.createNewMessage(recreateCtx); err != nil {
				return fmt.Errorf("삭제된 메시지 재생성 실패: %w", err)
			}

			continue
		}

		return fmt.Errorf("메시지 업데이트 실패: %w", err)
	}

	return fmt.Errorf("메시지 업데이트 재시도 횟수 초과: unknown message 에러가 지속됨")
}

func (m *StatusEmbedManager) UpdateToOffline() error {
	m.mu.RLock()
	msgID := m.messageID
	m.mu.RUnlock()

	if msgID == "" {
		log.Printf("상시 임베드가 아직 초기화되지 않아 오프라인 업데이트를 건너뜁니다")
		return nil
	}

	embed := EmbedBotOffline()
	button := BuildOfflineButton()

	embeds := []*discordgo.MessageEmbed{embed}
	components := []discordgo.MessageComponent{
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{button},
		},
	}

	_, err := m.session.ChannelMessageEditComplex(&discordgo.MessageEdit{
		Channel:    m.cfg.EmbedChannelID,
		ID:         msgID,
		Embeds:     &embeds,
		Components: &components,
	})

	if err != nil {
		return fmt.Errorf("오프라인 상태 업데이트 실패: %w", err)
	}

	return nil
}
