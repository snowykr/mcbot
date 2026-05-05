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
	channelID   string
	messageID   string
	mu          sync.Mutex
}

func NewStatusEmbedManager(session *discordgo.Session, cfg *config.Config, controller *mcserver.Controller) *StatusEmbedManager {
	return &StatusEmbedManager{
		session:     &discordSessionWrapper{Session: session},
		realSession: session,
		cfg:         cfg,
		controller:  controller,
		channelID:   strings.TrimSpace(cfg.EmbedChannelID),
	}
}

func (m *StatusEmbedManager) Init(ctx context.Context) error {
	presence := m.controller.Presence(ctx)

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.channelID == "" {
		log.Printf("상시 임베드 채널이 설정되지 않아 초기화를 건너뜁니다")
		return nil
	}

	existingMsg, err := m.findExistingMessage(m.channelID)
	if err != nil {
		log.Printf("기존 메시지 검색 실패: %v", err)
	}

	messageID := m.messageID
	if existingMsg != nil {
		messageID = existingMsg.ID
		log.Printf("기존 상시 임베드 메시지 발견: %s", existingMsg.ID)
	} else {
		newMessageID, err := m.createNewMessageLocked(ctx, m.channelID, presence)
		if err != nil {
			return fmt.Errorf("새 메시지 생성 실패: %w", err)
		}
		messageID = newMessageID
		log.Printf("새 상시 임베드 메시지 생성: %s", messageID)
	}

	m.messageID = messageID

	updatedMessageID, err := m.applyPresenceLocked(m.channelID, messageID, presence, true)
	if err != nil {
		log.Printf("초기 상태 업데이트 실패: %v", err)
		return nil
	}

	m.messageID = updatedMessageID

	return nil
}

func (m *StatusEmbedManager) findExistingMessage(channelID string) (*discordgo.Message, error) {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		return nil, nil
	}

	botID := ""
	if m.realSession != nil && m.realSession.State != nil && m.realSession.State.User != nil {
		botID = m.realSession.State.User.ID
	}
	if botID == "" {
		log.Printf("봇 ID를 확인할 수 없어 기존 메시지 검색을 건너뜁니다")
		return nil, nil
	}

	messages, err := m.session.ChannelMessages(channelID, 100, "", "", "")
	if err != nil {
		return nil, fmt.Errorf("채널 메시지 조회 실패: %w", err)
	}

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

func (m *StatusEmbedManager) createNewMessageLocked(_ context.Context, channelID string, presence mcserver.PresenceState) (string, error) {
	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		log.Printf("상시 임베드 채널이 설정되지 않아 새 메시지 생성을 건너뜁니다")
		return "", nil
	}

	embed := EmbedPersistentStatus(presence)
	button := BuildToggleButton(presence)

	msg, err := m.session.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
		Embeds: []*discordgo.MessageEmbed{embed},
		Components: []discordgo.MessageComponent{
			discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{button},
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("메시지 전송 실패: %w", err)
	}

	return msg.ID, nil
}

func isUnknownMessageError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := strings.ToLower(err.Error())
	return strings.Contains(errMsg, "unknown message") || strings.Contains(errMsg, "10008")
}

func (m *StatusEmbedManager) Update(ctx context.Context) error {
	presence := m.controller.Presence(ctx)

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.messageID == "" {
		log.Printf("상시 임베드가 아직 초기화되지 않아 업데이트를 건너뜁니다")
		return nil
	}

	updatedMessageID, err := m.applyPresenceLocked(m.channelID, m.messageID, presence, true)
	if err != nil {
		return err
	}
	m.messageID = updatedMessageID
	return nil
}

func (m *StatusEmbedManager) UpdateWithPresence(presence mcserver.PresenceState) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	updatedMessageID, err := m.applyPresenceLocked(m.channelID, m.messageID, presence, true)
	if err != nil {
		return err
	}
	m.messageID = updatedMessageID
	return nil
}

func (m *StatusEmbedManager) IsCurrentStatusMessage(channelID, messageID string) bool {
	channelID = strings.TrimSpace(channelID)
	messageID = strings.TrimSpace(messageID)
	if channelID == "" || messageID == "" {
		return false
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	return channelID == m.channelID && messageID == m.messageID
}

func (m *StatusEmbedManager) applyPresenceLocked(channelID, messageID string, presence mcserver.PresenceState, allowRecovery bool) (string, error) {
	const maxUnknownMessageRetries = 2

	channelID = strings.TrimSpace(channelID)
	if channelID == "" {
		log.Printf("상시 임베드 채널이 설정되지 않아 업데이트를 건너뜁니다")
		return messageID, nil
	}

	currentMessageID := messageID
	for attempt := 0; attempt <= maxUnknownMessageRetries; attempt++ {
		if currentMessageID == "" {
			return "", fmt.Errorf("메시지 ID가 설정되지 않음")
		}

		embed := EmbedPersistentStatus(presence)
		button := BuildToggleButton(presence)

		embeds := []*discordgo.MessageEmbed{embed}
		components := []discordgo.MessageComponent{
			discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{button},
			},
		}

		err := m.editMessageLocked(channelID, currentMessageID, embeds, components)
		if err == nil {
			return currentMessageID, nil
		}

		if allowRecovery && isUnknownMessageError(err) {
			log.Printf("상시 임베드 메시지가 삭제되었습니다. 새 메시지를 생성합니다. (시도 %d/%d)", attempt+1, maxUnknownMessageRetries+1)

			newMessageID, err := m.createNewMessageLocked(context.Background(), channelID, presence)
			if err != nil {
				return "", fmt.Errorf("삭제된 메시지 재생성 실패: %w", err)
			}
			currentMessageID = newMessageID

			continue
		}

		return "", fmt.Errorf("메시지 업데이트 실패: %w", err)
	}

	return "", fmt.Errorf("메시지 업데이트 재시도 횟수 초과: unknown message 에러가 지속됨")
}

func (m *StatusEmbedManager) SwitchChannel(ctx context.Context, channelID string, presence mcserver.PresenceState) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	previousChannelID := m.channelID
	previousMessageID := m.messageID
	trimmedChannelID := strings.TrimSpace(channelID)
	if trimmedChannelID == "" {
		if err := m.archiveMessageLocked(previousChannelID, previousMessageID, "상태 임베드 채널 설정이 해제되었습니다."); err != nil {
			log.Printf("기존 상태 메시지 비활성화 실패(채널 비활성화는 계속 진행): %v", err)
		}
		m.channelID = ""
		m.messageID = ""
		log.Printf("상시 임베드 채널이 설정되지 않아 채널 전환을 건너뜁니다")
		return nil
	}

	existingMsg, err := m.findExistingMessage(trimmedChannelID)
	if err != nil {
		return fmt.Errorf("기존 메시지 검색 실패: %w", err)
	}

	candidateMessageID := ""
	if existingMsg != nil {
		candidateMessageID = existingMsg.ID
		log.Printf("전환된 채널에서 기존 상시 임베드 메시지 재사용: %s", existingMsg.ID)
	} else {
		newMessageID, err := m.createNewMessageLocked(ctx, trimmedChannelID, presence)
		if err != nil {
			return fmt.Errorf("새 메시지 생성 실패: %w", err)
		}
		candidateMessageID = newMessageID
		log.Printf("전환된 채널에 새 상시 임베드 메시지 생성: %s", candidateMessageID)
	}

	updatedMessageID, err := m.applyPresenceLocked(trimmedChannelID, candidateMessageID, presence, true)
	if err != nil {
		return err
	}

	if err := m.archiveReplacedMessageLocked(previousChannelID, previousMessageID, trimmedChannelID, updatedMessageID); err != nil {
		log.Printf("기존 상태 메시지 비활성화 실패(채널 전환은 계속 진행): %v", err)
	}

	m.channelID = trimmedChannelID
	m.messageID = updatedMessageID

	return nil
}

func (m *StatusEmbedManager) editMessageLocked(channelID, messageID string, embeds []*discordgo.MessageEmbed, components []discordgo.MessageComponent) error {
	_, err := m.session.ChannelMessageEditComplex(&discordgo.MessageEdit{
		Channel:    channelID,
		ID:         messageID,
		Embeds:     &embeds,
		Components: &components,
	})
	if err != nil {
		return err
	}
	return nil
}

func (m *StatusEmbedManager) archiveReplacedMessageLocked(previousChannelID, previousMessageID, nextChannelID, nextMessageID string) error {
	if previousChannelID == "" || previousMessageID == "" {
		return nil
	}
	if previousChannelID == nextChannelID && previousMessageID == nextMessageID {
		return nil
	}
	return m.archiveMessageLocked(previousChannelID, previousMessageID, "상태 임베드가 다른 채널로 이동했습니다. 최신 제어 메시지를 사용해주세요.")
}

func (m *StatusEmbedManager) archiveMessageLocked(channelID, messageID, note string) error {
	channelID = strings.TrimSpace(channelID)
	messageID = strings.TrimSpace(messageID)
	if channelID == "" || messageID == "" {
		return nil
	}

	embed := EmbedInactiveControl(note)
	button := BuildInactiveButton()
	embeds := []*discordgo.MessageEmbed{embed}
	components := []discordgo.MessageComponent{
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{button},
		},
	}

	if err := m.editMessageLocked(channelID, messageID, embeds, components); err != nil {
		if isUnknownMessageError(err) {
			return nil
		}
		return fmt.Errorf("message %s in channel %s: %w", messageID, channelID, err)
	}

	return nil
}

func (m *StatusEmbedManager) UpdateToOffline() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.updateOfflineLocked()
}

func (m *StatusEmbedManager) updateOfflineLocked() error {
	if m.channelID == "" {
		log.Printf("상시 임베드 채널이 설정되지 않아 오프라인 업데이트를 건너뜁니다")
		return nil
	}

	if m.messageID == "" {
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
		Channel:    m.channelID,
		ID:         m.messageID,
		Embeds:     &embeds,
		Components: &components,
	})

	if err != nil {
		return fmt.Errorf("오프라인 상태 업데이트 실패: %w", err)
	}

	return nil
}
