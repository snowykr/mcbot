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

type StatusEmbedManager struct {
	session    *discordgo.Session
	cfg        *config.Config
	controller *mcserver.Controller
	messageID  string
	mu         sync.RWMutex
}

func NewStatusEmbedManager(session *discordgo.Session, cfg *config.Config, controller *mcserver.Controller) *StatusEmbedManager {
	return &StatusEmbedManager{
		session:    session,
		cfg:        cfg,
		controller: controller,
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

	botID := m.session.State.User.ID

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
		return fmt.Errorf("메시지 ID가 설정되지 않음")
	}

	presence := m.controller.Presence(ctx)
	return m.UpdateWithPresence(presence)
}

func (m *StatusEmbedManager) UpdateWithPresence(presence mcserver.PresenceState) error {
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

	if err != nil {
		if isUnknownMessageError(err) {
			log.Printf("상시 임베드 메시지가 삭제되었습니다. 새 메시지를 생성합니다.")

			m.mu.Lock()
			m.messageID = ""
			m.mu.Unlock()

			ctx := context.Background()
			if err := m.createNewMessage(ctx); err != nil {
				return fmt.Errorf("삭제된 메시지 재생성 실패: %w", err)
			}

			return m.UpdateWithPresence(presence)
		}

		return fmt.Errorf("메시지 업데이트 실패: %w", err)
	}

	return nil
}
