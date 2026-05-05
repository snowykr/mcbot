package discord

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/snowy/mcbot/internal/config"
	"github.com/snowy/mcbot/internal/mcserver"
	"github.com/snowy/mcbot/internal/state"
)

type mockDiscordSession struct {
	mu                sync.Mutex
	editCalls         int
	editChannels      []string
	editResponses     []error
	sendCalls         int
	sendChannels      []string
	sendResponse      error
	sendMessageID     string
	messagesByChannel map[string][]*discordgo.Message
	editStarted       chan struct{}
	editBlockCh       chan struct{}
	editStartedOnce   sync.Once

	lastEdit      *discordgo.MessageEdit
	allEdits      []*discordgo.MessageEdit
	lastSend      *discordgo.MessageSend
	lastChannelID string
}

func (m *mockDiscordSession) ChannelMessageEditComplex(edit *discordgo.MessageEdit) (*discordgo.Message, error) {
	m.mu.Lock()
	m.editCalls++
	m.editChannels = append(m.editChannels, edit.Channel)
	m.lastEdit = edit
	m.allEdits = append(m.allEdits, edit)
	m.editStartedOnce.Do(func() {
		if m.editStarted != nil {
			close(m.editStarted)
		}
	})
	m.mu.Unlock()

	if m.editBlockCh != nil {
		<-m.editBlockCh
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.editCalls <= len(m.editResponses) {
		err := m.editResponses[m.editCalls-1]
		if err != nil {
			return nil, err
		}
	}
	return &discordgo.Message{ID: edit.ID}, nil
}

func (m *mockDiscordSession) ChannelMessageSendComplex(channelID string, data *discordgo.MessageSend) (*discordgo.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sendCalls++
	m.sendChannels = append(m.sendChannels, channelID)
	m.lastChannelID = channelID
	m.lastSend = data

	if m.sendResponse != nil {
		return nil, m.sendResponse
	}
	msgID := m.sendMessageID
	if msgID == "" {
		msgID = "new-message-id"
	}
	return &discordgo.Message{ID: msgID}, nil
}

func (m *mockDiscordSession) ChannelMessages(channelID string, _ int, _, _, _ string) ([]*discordgo.Message, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.messagesByChannel[channelID], nil
}

type mockController struct {
	presence mcserver.PresenceState
	startCh  chan mcserver.StartResult
	stopCh   chan mcserver.StopResult
}

func (m *mockController) Presence(_ context.Context) mcserver.PresenceState {
	return m.presence
}

func (m *mockController) Start(_ context.Context) <-chan mcserver.StartResult {
	if m.startCh != nil {
		return m.startCh
	}
	ch := make(chan mcserver.StartResult, 1)
	close(ch)
	return ch
}

func (m *mockController) Stop(_ context.Context) <-chan mcserver.StopResult {
	if m.stopCh != nil {
		return m.stopCh
	}
	ch := make(chan mcserver.StopResult, 1)
	close(ch)
	return ch
}

func newTestStatusEmbedManager(session discordSession, controller *mockController) *StatusEmbedManager {
	cfg := &config.Config{
		EmbedChannelID: "test-channel",
	}

	realSession := &discordgo.Session{
		State: &discordgo.State{
			Ready: discordgo.Ready{User: &discordgo.User{ID: "bot-id"}},
		},
	}

	return &StatusEmbedManager{
		session:     session,
		realSession: realSession,
		cfg:         cfg,
		controller:  controller,
		channelID:   cfg.EmbedChannelID,
		messageID:   "existing-message-id",
	}
}

func TestIsUnknownMessageError(t *testing.T) {
	tests := []struct {
		name     string
		errMsg   string
		expected bool
	}{
		{
			name:     "Unknown Message lowercase",
			errMsg:   "HTTP 404 Not Found, {\"message\": \"unknown message\", \"code\": 10008}",
			expected: true,
		},
		{
			name:     "Unknown Message uppercase",
			errMsg:   "HTTP 404 Not Found, {\"message\": \"Unknown Message\", \"code\": 10008}",
			expected: true,
		},
		{
			name:     "Error code 10008",
			errMsg:   "Discord API error: code 10008",
			expected: true,
		},
		{
			name:     "Different error",
			errMsg:   "HTTP 403 Forbidden",
			expected: false,
		},
		{
			name:     "Empty error",
			errMsg:   "",
			expected: false,
		},
		{
			name:     "Nil error",
			errMsg:   "",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var err error
			if tt.errMsg != "" {
				err = errors.New(tt.errMsg)
			}

			result := isUnknownMessageError(err)
			if result != tt.expected {
				t.Errorf("isUnknownMessageError(%v) = %v, expected %v", tt.errMsg, result, tt.expected)
			}
		})
	}
}

func TestUpdateWithPresence_Success(t *testing.T) {
	mockSession := &mockDiscordSession{
		editResponses: []error{nil},
	}

	mockCtrl := &mockController{
		presence: mcserver.PresenceState{
			ServerState: state.StateRunning,
			Players:     []string{"Player1"},
		},
	}

	manager := newTestStatusEmbedManager(mockSession, mockCtrl)

	err := manager.UpdateWithPresence(mockCtrl.presence)
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if mockSession.editCalls != 1 {
		t.Errorf("Expected 1 edit call, got %d", mockSession.editCalls)
	}

	if mockSession.sendCalls != 0 {
		t.Errorf("Expected 0 send calls, got %d", mockSession.sendCalls)
	}

	if mockSession.lastEdit == nil {
		t.Fatal("Expected lastEdit to be set")
	}

	if mockSession.lastEdit.Channel != "test-channel" {
		t.Errorf("Expected edit channel to be 'test-channel', got '%s'", mockSession.lastEdit.Channel)
	}

	if mockSession.lastEdit.ID != "existing-message-id" {
		t.Errorf("Expected edit message ID to be 'existing-message-id', got '%s'", mockSession.lastEdit.ID)
	}
}

func TestUpdateWithPresence_EmptyMessageID(t *testing.T) {
	mockSession := &mockDiscordSession{}

	mockCtrl := &mockController{
		presence: mcserver.PresenceState{
			ServerState: state.StateRunning,
		},
	}

	manager := newTestStatusEmbedManager(mockSession, mockCtrl)
	manager.messageID = ""

	err := manager.UpdateWithPresence(mockCtrl.presence)
	if err == nil {
		t.Fatalf("Expected error for empty messageID, got nil")
	}

	expectedErrMsg := "메시지 ID가 설정되지 않음"
	if err.Error() != expectedErrMsg {
		t.Errorf("Expected error message '%s', got '%s'", expectedErrMsg, err.Error())
	}

	if mockSession.editCalls != 0 {
		t.Errorf("Expected 0 edit calls, got %d", mockSession.editCalls)
	}

	if mockSession.sendCalls != 0 {
		t.Errorf("Expected 0 send calls, got %d", mockSession.sendCalls)
	}
}

func TestUpdateWithPresence_UnknownMessageRecovery(t *testing.T) {
	mockSession := &mockDiscordSession{
		editResponses: []error{
			errors.New("HTTP 404 Not Found, {\"message\": \"Unknown Message\", \"code\": 10008}"),
			nil,
		},
		sendMessageID: "new-message-id",
	}

	mockCtrl := &mockController{
		presence: mcserver.PresenceState{
			ServerState: state.StateRunning,
		},
	}

	manager := newTestStatusEmbedManager(mockSession, mockCtrl)

	err := manager.UpdateWithPresence(mockCtrl.presence)
	if err != nil {
		t.Errorf("Expected no error after recovery, got %v", err)
	}

	if mockSession.editCalls != 2 {
		t.Errorf("Expected 2 edit calls (1 fail + 1 success), got %d", mockSession.editCalls)
	}

	if mockSession.sendCalls != 1 {
		t.Errorf("Expected 1 send call (create new message), got %d", mockSession.sendCalls)
	}

	if manager.messageID != "new-message-id" {
		t.Errorf("Expected messageID to be updated to 'new-message-id', got '%s'", manager.messageID)
	}

	if mockSession.lastChannelID != "test-channel" {
		t.Errorf("Expected send channel to be 'test-channel', got '%s'", mockSession.lastChannelID)
	}

	if mockSession.lastEdit == nil {
		t.Fatal("Expected lastEdit to be set after recovery")
	}

	if mockSession.lastEdit.ID != "new-message-id" {
		t.Errorf("Expected second edit to use new message ID 'new-message-id', got '%s'", mockSession.lastEdit.ID)
	}
}

func TestUpdateWithPresence_UnknownMessageExceedsRetries(t *testing.T) {
	mockSession := &mockDiscordSession{
		editResponses: []error{
			errors.New("HTTP 404 Not Found, {\"message\": \"Unknown Message\", \"code\": 10008}"),
			errors.New("HTTP 404 Not Found, {\"message\": \"Unknown Message\", \"code\": 10008}"),
			errors.New("HTTP 404 Not Found, {\"message\": \"Unknown Message\", \"code\": 10008}"),
			errors.New("HTTP 404 Not Found, {\"message\": \"Unknown Message\", \"code\": 10008}"),
		},
	}

	mockCtrl := &mockController{
		presence: mcserver.PresenceState{
			ServerState: state.StateRunning,
		},
	}

	manager := newTestStatusEmbedManager(mockSession, mockCtrl)

	err := manager.UpdateWithPresence(mockCtrl.presence)
	if err == nil {
		t.Fatalf("Expected error after exceeding retries, got nil")
	}

	expectedErrMsg := "메시지 업데이트 재시도 횟수 초과: unknown message 에러가 지속됨"
	if err.Error() != expectedErrMsg {
		t.Errorf("Expected error message '%s', got '%s'", expectedErrMsg, err.Error())
	}

	if mockSession.editCalls != 3 {
		t.Errorf("Expected 3 edit calls (maxRetries=2 means 3 total attempts), got %d", mockSession.editCalls)
	}

	if mockSession.sendCalls != 3 {
		t.Errorf("Expected 3 send calls (one per retry), got %d", mockSession.sendCalls)
	}
}

func TestUpdateWithPresence_NonUnknownMessageError(t *testing.T) {
	mockSession := &mockDiscordSession{
		editResponses: []error{
			errors.New("HTTP 403 Forbidden"),
		},
	}

	mockCtrl := &mockController{
		presence: mcserver.PresenceState{
			ServerState: state.StateRunning,
		},
	}

	manager := newTestStatusEmbedManager(mockSession, mockCtrl)

	err := manager.UpdateWithPresence(mockCtrl.presence)
	if err == nil {
		t.Error("Expected error, got nil")
	}

	if mockSession.editCalls != 1 {
		t.Errorf("Expected 1 edit call (no retry for non-unknown errors), got %d", mockSession.editCalls)
	}

	if mockSession.sendCalls != 0 {
		t.Errorf("Expected 0 send calls (no retry), got %d", mockSession.sendCalls)
	}
}

func TestUpdateWithPresence_CreateNewMessageFails(t *testing.T) {
	mockSession := &mockDiscordSession{
		editResponses: []error{
			errors.New("HTTP 404 Not Found, {\"message\": \"Unknown Message\", \"code\": 10008}"),
		},
		sendResponse: errors.New("Failed to create message"),
	}

	mockCtrl := &mockController{
		presence: mcserver.PresenceState{
			ServerState: state.StateRunning,
		},
	}

	manager := newTestStatusEmbedManager(mockSession, mockCtrl)

	err := manager.UpdateWithPresence(mockCtrl.presence)
	if err == nil {
		t.Error("Expected error when createNewMessage fails, got nil")
	}

	if mockSession.editCalls != 1 {
		t.Errorf("Expected 1 edit call, got %d", mockSession.editCalls)
	}

	if mockSession.sendCalls != 1 {
		t.Errorf("Expected 1 send call, got %d", mockSession.sendCalls)
	}
}

func TestStatusEmbedManager_SwitchChannelUsesProvidedPresence(t *testing.T) {
	mockSession := &mockDiscordSession{messagesByChannel: map[string][]*discordgo.Message{}}

	mockCtrl := &mockController{
		presence: mcserver.PresenceState{ServerState: state.StateRunning},
	}

	manager := newTestStatusEmbedManager(mockSession, mockCtrl)
	manager.messageID = ""
	manager.cfg.EmbedChannelID = "source-channel"
	manager.channelID = "source-channel"

	provided := mcserver.PresenceState{ServerState: state.StateStarting}

	err := manager.SwitchChannel(context.Background(), "target-channel", provided)
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if mockSession.sendCalls != 1 {
		t.Fatalf("Expected 1 send call, got %d", mockSession.sendCalls)
	}

	if mockSession.lastChannelID != "target-channel" {
		t.Fatalf("Expected send to target-channel, got %q", mockSession.lastChannelID)
	}

	if mockSession.lastSend == nil || len(mockSession.lastSend.Embeds) == 0 {
		t.Fatal("Expected send embed to be set")
	}

	if got := mockSession.lastSend.Embeds[0].Description; got != "🟡 **여는중**" {
		t.Fatalf("Expected switch render to use provided presence, got %q", got)
	}

	if mockSession.editCalls != 1 {
		t.Fatalf("Expected 1 edit call after switch, got %d", mockSession.editCalls)
	}

	if mockSession.lastEdit == nil || len(*mockSession.lastEdit.Embeds) == 0 {
		t.Fatal("Expected last edit embed to be set")
	}

	if got := (*mockSession.lastEdit.Embeds)[0].Description; got != "🟡 **여는중**" {
		t.Fatalf("Expected switch update to use provided presence, got %q", got)
	}

	if manager.channelID != "target-channel" {
		t.Fatalf("Expected manager channelID to switch, got %q", manager.channelID)
	}
}

func TestStatusEmbedManager_SwitchChannel_ReusesExistingMessage(t *testing.T) {
	msg := &discordgo.Message{
		ID:     "reused-message-id",
		Author: &discordgo.User{ID: "bot-id"},
		Embeds: []*discordgo.MessageEmbed{{Footer: &discordgo.MessageEmbedFooter{Text: EmbedMarkerFooter}}},
	}

	mockSession := &mockDiscordSession{messagesByChannel: map[string][]*discordgo.Message{
		"target-channel": {msg},
	}}

	mockCtrl := &mockController{presence: mcserver.PresenceState{ServerState: state.StateRunning}}
	manager := newTestStatusEmbedManager(mockSession, mockCtrl)
	manager.messageID = "old-message-id"
	manager.channelID = "source-channel"

	err := manager.SwitchChannel(context.Background(), "target-channel", mcserver.PresenceState{ServerState: state.StateRunning})
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if mockSession.sendCalls != 0 {
		t.Fatalf("Expected no send when reusing existing message, got %d", mockSession.sendCalls)
	}

	if mockSession.editCalls != 2 {
		t.Fatalf("Expected 2 edit calls (reused active + old disabled), got %d", mockSession.editCalls)
	}

	if manager.messageID != "reused-message-id" {
		t.Fatalf("Expected reused message ID, got %q", manager.messageID)
	}

	if got := mockSession.editChannels; len(got) != 2 || got[0] != "target-channel" || got[1] != "source-channel" {
		t.Fatalf("edit channels = %v, want [target-channel source-channel]", got)
	}
}

func TestStatusEmbedManager_SwitchChannel_DisablesPreviousMessage(t *testing.T) {
	mockSession := &mockDiscordSession{messagesByChannel: map[string][]*discordgo.Message{}}

	mockCtrl := &mockController{presence: mcserver.PresenceState{ServerState: state.StateRunning}}
	manager := newTestStatusEmbedManager(mockSession, mockCtrl)
	manager.messageID = "source-message-id"
	manager.channelID = "source-channel"

	err := manager.SwitchChannel(context.Background(), "target-channel", mcserver.PresenceState{ServerState: state.StateRunning})
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if mockSession.sendCalls != 1 {
		t.Fatalf("Expected 1 send call, got %d", mockSession.sendCalls)
	}

	if mockSession.editCalls != 2 {
		t.Fatalf("Expected 2 edit calls (new active + old disabled), got %d", mockSession.editCalls)
	}

	if len(mockSession.allEdits) != 2 {
		t.Fatalf("Expected 2 stored edits, got %d", len(mockSession.allEdits))
	}

	if mockSession.allEdits[0].Channel != "target-channel" || mockSession.allEdits[0].ID != "new-message-id" {
		t.Fatalf("first edit = channel %q message %q, want target-channel/new-message-id", mockSession.allEdits[0].Channel, mockSession.allEdits[0].ID)
	}

	if mockSession.allEdits[1].Channel != "source-channel" || mockSession.allEdits[1].ID != "source-message-id" {
		t.Fatalf("second edit = channel %q message %q, want source-channel/source-message-id", mockSession.allEdits[1].Channel, mockSession.allEdits[1].ID)
	}

	if mockSession.allEdits[1].Components == nil || len(*mockSession.allEdits[1].Components) == 0 {
		t.Fatal("Expected previous message edit to include disabled components")
	}

	row, ok := (*mockSession.allEdits[1].Components)[0].(discordgo.ActionsRow)
	if !ok || len(row.Components) != 1 {
		t.Fatalf("Expected one actions row on previous message, got %#v", (*mockSession.allEdits[1].Components)[0])
	}

	button, ok := row.Components[0].(discordgo.Button)
	if !ok {
		t.Fatalf("Expected previous message component to be a button, got %#v", row.Components[0])
	}
	if !button.Disabled {
		t.Fatal("Expected previous message button to be disabled")
	}

	if mockSession.allEdits[1].Embeds == nil || len(*mockSession.allEdits[1].Embeds) == 0 {
		t.Fatal("Expected previous message edit to include an embed")
	}
	if got := (*mockSession.allEdits[1].Embeds)[0].Description; !strings.Contains(got, "비활성화") {
		t.Fatalf("Expected previous message embed to explain deactivation, got %q", got)
	}

	if manager.channelID != "target-channel" {
		t.Fatalf("Expected manager channelID to be target-channel, got %q", manager.channelID)
	}
	if manager.messageID != "new-message-id" {
		t.Fatalf("Expected manager messageID to be new-message-id, got %q", manager.messageID)
	}
}

func TestStatusEmbedManager_SwitchChannel_EmptyDisablesEmbedAndArchivesPreviousMessage(t *testing.T) {
	mockSession := &mockDiscordSession{messagesByChannel: map[string][]*discordgo.Message{}}

	mockCtrl := &mockController{presence: mcserver.PresenceState{ServerState: state.StateRunning}}
	manager := newTestStatusEmbedManager(mockSession, mockCtrl)
	manager.messageID = "source-message-id"
	manager.channelID = "source-channel"

	err := manager.SwitchChannel(context.Background(), "", mcserver.PresenceState{ServerState: state.StateRunning})
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if mockSession.sendCalls != 0 {
		t.Fatalf("Expected 0 send calls, got %d", mockSession.sendCalls)
	}
	if mockSession.editCalls != 1 {
		t.Fatalf("Expected 1 edit call to archive previous message, got %d", mockSession.editCalls)
	}
	if mockSession.lastEdit == nil || mockSession.lastEdit.Channel != "source-channel" || mockSession.lastEdit.ID != "source-message-id" {
		t.Fatalf("last edit = %#v, want source-channel/source-message-id", mockSession.lastEdit)
	}
	if mockSession.lastEdit.Embeds == nil || len(*mockSession.lastEdit.Embeds) == 0 {
		t.Fatal("Expected archived previous message to include an embed")
	}
	if got := (*mockSession.lastEdit.Embeds)[0].Description; !strings.Contains(got, "비활성화") {
		t.Fatalf("Expected archive embed to explain deactivation, got %q", got)
	}
	if manager.channelID != "" {
		t.Fatalf("Expected manager channelID to be empty, got %q", manager.channelID)
	}
	if manager.messageID != "" {
		t.Fatalf("Expected manager messageID to be empty, got %q", manager.messageID)
	}
}

func TestStatusEmbedManager_SwitchChannel_EmptyDisablesEmbedWhenArchiveFails(t *testing.T) {
	mockSession := &mockDiscordSession{
		messagesByChannel: map[string][]*discordgo.Message{},
		editResponses:     []error{errors.New("archive failed")},
	}

	mockCtrl := &mockController{presence: mcserver.PresenceState{ServerState: state.StateRunning}}
	manager := newTestStatusEmbedManager(mockSession, mockCtrl)
	manager.messageID = "source-message-id"
	manager.channelID = "source-channel"

	err := manager.SwitchChannel(context.Background(), "", mcserver.PresenceState{ServerState: state.StateRunning})
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if mockSession.editCalls != 1 {
		t.Fatalf("Expected one best-effort archive attempt, got %d", mockSession.editCalls)
	}
	if mockSession.sendCalls != 0 {
		t.Fatalf("Expected 0 send calls, got %d", mockSession.sendCalls)
	}
	if manager.channelID != "" {
		t.Fatalf("Expected manager channelID to be empty, got %q", manager.channelID)
	}
	if manager.messageID != "" {
		t.Fatalf("Expected manager messageID to be empty, got %q", manager.messageID)
	}
}

func TestStatusEmbedManager_InitPreservesMessageIDWhenInitialUpdateFails(t *testing.T) {
	msg := &discordgo.Message{
		ID:     "recovered-message-id",
		Author: &discordgo.User{ID: "bot-id"},
		Embeds: []*discordgo.MessageEmbed{{Footer: &discordgo.MessageEmbedFooter{Text: EmbedMarkerFooter}}},
	}

	mockSession := &mockDiscordSession{
		messagesByChannel: map[string][]*discordgo.Message{
			"test-channel": {msg},
		},
		editResponses: []error{
			errors.New("HTTP 500 Internal Server Error"),
			nil,
		},
	}

	mockCtrl := &mockController{presence: mcserver.PresenceState{ServerState: state.StateRunning}}
	manager := newTestStatusEmbedManager(mockSession, mockCtrl)
	manager.messageID = ""

	if err := manager.Init(context.Background()); err != nil {
		t.Fatalf("Expected Init to tolerate initial update failure, got %v", err)
	}

	if manager.messageID != "recovered-message-id" {
		t.Fatalf("Expected Init to preserve discovered message ID, got %q", manager.messageID)
	}

	if err := manager.Update(context.Background()); err != nil {
		t.Fatalf("Expected later Update to retry preserved message ID, got %v", err)
	}

	if mockSession.editCalls != 2 {
		t.Fatalf("Expected initial failed edit plus retry update, got %d edit calls", mockSession.editCalls)
	}

	if mockSession.allEdits[1].ID != "recovered-message-id" {
		t.Fatalf("Expected retry update to use preserved message ID, got %q", mockSession.allEdits[1].ID)
	}
}

func TestStatusEmbedManager_InitPreservesCreatedMessageIDWhenInitialUpdateFails(t *testing.T) {
	mockSession := &mockDiscordSession{
		messagesByChannel: map[string][]*discordgo.Message{},
		sendMessageID:     "created-message-id",
		editResponses: []error{
			errors.New("HTTP 500 Internal Server Error"),
			nil,
		},
	}

	mockCtrl := &mockController{presence: mcserver.PresenceState{ServerState: state.StateRunning}}
	manager := newTestStatusEmbedManager(mockSession, mockCtrl)
	manager.messageID = ""

	if err := manager.Init(context.Background()); err != nil {
		t.Fatalf("Expected Init to tolerate initial update failure, got %v", err)
	}

	if manager.messageID != "created-message-id" {
		t.Fatalf("Expected Init to preserve created message ID, got %q", manager.messageID)
	}

	if mockSession.sendCalls != 1 {
		t.Fatalf("Expected Init to create one message, got %d sends", mockSession.sendCalls)
	}

	if err := manager.Update(context.Background()); err != nil {
		t.Fatalf("Expected later Update to retry preserved message ID, got %v", err)
	}

	if mockSession.editCalls != 2 {
		t.Fatalf("Expected initial failed edit plus retry update, got %d edit calls", mockSession.editCalls)
	}

	if mockSession.allEdits[1].ID != "created-message-id" {
		t.Fatalf("Expected retry update to use preserved message ID, got %q", mockSession.allEdits[1].ID)
	}
}

func TestStatusEmbedManager_SwitchChannel_CommitsWhenPreviousArchiveFails(t *testing.T) {
	mockSession := &mockDiscordSession{
		messagesByChannel: map[string][]*discordgo.Message{},
		sendMessageID:     "target-message-id",
		editResponses: []error{
			nil,
			errors.New("HTTP 403 Forbidden"),
		},
	}

	mockCtrl := &mockController{presence: mcserver.PresenceState{ServerState: state.StateRunning}}
	manager := newTestStatusEmbedManager(mockSession, mockCtrl)
	manager.channelID = "source-channel"
	manager.messageID = "source-message-id"

	err := manager.SwitchChannel(context.Background(), "target-channel", mcserver.PresenceState{ServerState: state.StateRunning})
	if err != nil {
		t.Fatalf("Expected switch to commit despite previous archive failure, got %v", err)
	}

	if manager.channelID != "target-channel" {
		t.Fatalf("Expected manager channelID to be target-channel, got %q", manager.channelID)
	}
	if manager.messageID != "target-message-id" {
		t.Fatalf("Expected manager messageID to be target-message-id, got %q", manager.messageID)
	}

	if mockSession.editCalls != 2 {
		t.Fatalf("Expected target update and best-effort previous archive, got %d edit calls", mockSession.editCalls)
	}
	if mockSession.allEdits[0].Channel != "target-channel" || mockSession.allEdits[0].ID != "target-message-id" {
		t.Fatalf("first edit = channel %q message %q, want target-channel/target-message-id", mockSession.allEdits[0].Channel, mockSession.allEdits[0].ID)
	}
	if mockSession.allEdits[1].Channel != "source-channel" || mockSession.allEdits[1].ID != "source-message-id" {
		t.Fatalf("second edit = channel %q message %q, want source-channel/source-message-id", mockSession.allEdits[1].Channel, mockSession.allEdits[1].ID)
	}
	if !manager.IsCurrentStatusMessage("target-channel", "target-message-id") {
		t.Fatal("Expected committed target message to be the current status message")
	}
	if manager.IsCurrentStatusMessage("source-channel", "source-message-id") {
		t.Fatal("Expected previous source message to be stale after committed switch")
	}
	if manager.IsCurrentStatusMessage("target-channel", "source-message-id") {
		t.Fatal("Expected mismatched channel/message pair to be stale")
	}
}

func TestStatusEmbedManager_SwitchChannel_DoesNotCommitOnFailure(t *testing.T) {
	mockSession := &mockDiscordSession{
		messagesByChannel: map[string][]*discordgo.Message{},
		editResponses: []error{
			errors.New("HTTP 403 Forbidden"),
		},
	}

	mockCtrl := &mockController{presence: mcserver.PresenceState{ServerState: state.StateRunning}}
	manager := newTestStatusEmbedManager(mockSession, mockCtrl)
	manager.channelID = "source-channel"
	manager.messageID = "source-message"

	err := manager.SwitchChannel(context.Background(), "target-channel", mcserver.PresenceState{ServerState: state.StateStarting})
	if err == nil {
		t.Fatal("Expected switch to fail")
	}

	if manager.channelID != "source-channel" {
		t.Fatalf("Expected channelID to remain source-channel, got %q", manager.channelID)
	}

	if manager.messageID != "source-message" {
		t.Fatalf("Expected messageID to remain source-message, got %q", manager.messageID)
	}

	if mockSession.sendCalls != 1 {
		t.Fatalf("Expected 1 send call for new target message, got %d", mockSession.sendCalls)
	}

	if mockSession.editCalls != 1 {
		t.Fatalf("Expected 1 edit call, got %d", mockSession.editCalls)
	}
}

func TestStatusEmbedManager_SwitchChannel_FromUnconfiguredStateRepairsEmbed(t *testing.T) {
	mockSession := &mockDiscordSession{messagesByChannel: map[string][]*discordgo.Message{}}

	mockCtrl := &mockController{presence: mcserver.PresenceState{ServerState: state.StateRunning}}
	manager := newTestStatusEmbedManager(mockSession, mockCtrl)
	manager.channelID = ""
	manager.messageID = ""

	err := manager.SwitchChannel(context.Background(), "target-channel", mcserver.PresenceState{ServerState: state.StateRunning})
	if err != nil {
		t.Fatalf("Expected no error, got %v", err)
	}

	if mockSession.sendCalls != 1 {
		t.Fatalf("Expected 1 send call, got %d", mockSession.sendCalls)
	}
	if mockSession.editCalls != 1 {
		t.Fatalf("Expected 1 edit call, got %d", mockSession.editCalls)
	}
	if len(mockSession.sendChannels) != 1 || mockSession.sendChannels[0] != "target-channel" {
		t.Fatalf("send channels = %v, want [target-channel]", mockSession.sendChannels)
	}
	if len(mockSession.editChannels) != 1 || mockSession.editChannels[0] != "target-channel" {
		t.Fatalf("edit channels = %v, want [target-channel]", mockSession.editChannels)
	}
	if manager.channelID != "target-channel" {
		t.Fatalf("Expected manager channelID to be target-channel, got %q", manager.channelID)
	}
	if manager.messageID == "" {
		t.Fatal("Expected manager messageID to be set after repair")
	}
}

func TestStatusEmbedManager_InitWithoutConfiguredChannel(t *testing.T) {
	mockSession := &mockDiscordSession{}
	mockCtrl := &mockController{presence: mcserver.PresenceState{ServerState: state.StateRunning}}
	manager := newTestStatusEmbedManager(mockSession, mockCtrl)
	manager.cfg.EmbedChannelID = ""
	manager.channelID = ""
	manager.messageID = ""

	if err := manager.Init(context.Background()); err != nil {
		t.Fatalf("Expected Init to skip without error, got %v", err)
	}

	if mockSession.sendCalls != 0 || mockSession.editCalls != 0 {
		t.Fatalf("Expected no Discord mutations, got send=%d edit=%d", mockSession.sendCalls, mockSession.editCalls)
	}
}

func TestStatusEmbedManager_UpdateToOfflineWithoutConfiguredChannel(t *testing.T) {
	mockSession := &mockDiscordSession{}
	mockCtrl := &mockController{presence: mcserver.PresenceState{ServerState: state.StateRunning}}
	manager := newTestStatusEmbedManager(mockSession, mockCtrl)
	manager.cfg.EmbedChannelID = ""
	manager.channelID = ""
	manager.messageID = "existing-message-id"

	if err := manager.UpdateToOffline(); err != nil {
		t.Fatalf("Expected UpdateToOffline to skip without error, got %v", err)
	}

	if mockSession.editCalls != 0 || mockSession.sendCalls != 0 {
		t.Fatalf("Expected no Discord mutations, got send=%d edit=%d", mockSession.sendCalls, mockSession.editCalls)
	}
}

func TestStatusEmbedManager_ConcurrentSwitchAndUpdate(t *testing.T) {
	mockSession := &mockDiscordSession{
		editStarted:       make(chan struct{}),
		editBlockCh:       make(chan struct{}),
		messagesByChannel: map[string][]*discordgo.Message{},
	}

	mockCtrl := &mockController{presence: mcserver.PresenceState{ServerState: state.StateRunning}}
	manager := newTestStatusEmbedManager(mockSession, mockCtrl)
	manager.channelID = "source-channel"
	manager.messageID = "source-message"

	updateDone := make(chan error, 1)
	switchDone := make(chan error, 1)

	go func() {
		updateDone <- manager.Update(context.Background())
	}()

	<-mockSession.editStarted

	go func() {
		switchDone <- manager.SwitchChannel(context.Background(), "target-channel", mcserver.PresenceState{ServerState: state.StateStarting})
	}()

	select {
	case err := <-switchDone:
		t.Fatalf("SwitchChannel finished too early: %v", err)
	default:
	}

	close(mockSession.editBlockCh)

	if err := <-updateDone; err != nil {
		t.Fatalf("Expected Update to succeed, got %v", err)
	}

	if err := <-switchDone; err != nil {
		t.Fatalf("Expected SwitchChannel to succeed, got %v", err)
	}

	if mockSession.editCalls < 2 {
		t.Fatalf("Expected at least 2 edit calls, got %d", mockSession.editCalls)
	}

	if len(mockSession.allEdits) < 3 {
		t.Fatalf("Expected recorded edits for update + switch + archive, got %d", len(mockSession.allEdits))
	}

	sawTargetEdit := false
	sawArchivedSourceEdit := false
	for _, edit := range mockSession.allEdits {
		if edit.Channel == "target-channel" {
			sawTargetEdit = true
		}
		if edit.Channel == "source-channel" && edit.Components != nil && len(*edit.Components) > 0 {
			row, ok := (*edit.Components)[0].(discordgo.ActionsRow)
			if ok && len(row.Components) == 1 {
				if button, ok := row.Components[0].(discordgo.Button); ok && button.Disabled {
					sawArchivedSourceEdit = true
				}
			}
		}
	}

	if !sawTargetEdit {
		t.Fatal("Expected switch to edit the target channel message")
	}
	if !sawArchivedSourceEdit {
		t.Fatal("Expected switch to archive the previous source message")
	}

	if manager.channelID != "target-channel" {
		t.Fatalf("Expected final manager channelID to be target-channel, got %q", manager.channelID)
	}
}

func TestUpdate_BeforeInit_SafelySkips(t *testing.T) {
	mockSession := &mockDiscordSession{}

	mockCtrl := &mockController{
		presence: mcserver.PresenceState{
			ServerState: state.StateRunning,
			Players:     []string{"Player1"},
		},
	}

	manager := newTestStatusEmbedManager(mockSession, mockCtrl)
	manager.messageID = ""

	err := manager.Update(context.Background())
	if err != nil {
		t.Errorf("Expected Update to safely skip when messageID is empty, got error: %v", err)
	}

	if mockSession.editCalls != 0 {
		t.Errorf("Expected 0 edit calls when messageID is empty, got %d", mockSession.editCalls)
	}

	if mockSession.sendCalls != 0 {
		t.Errorf("Expected 0 send calls when messageID is empty, got %d", mockSession.sendCalls)
	}
}

func TestUpdate_AfterInit_UpdatesMessage(t *testing.T) {
	mockSession := &mockDiscordSession{
		editResponses: []error{nil},
	}

	mockCtrl := &mockController{
		presence: mcserver.PresenceState{
			ServerState: state.StateRunning,
			Players:     []string{"Player1", "Player2"},
		},
	}

	manager := newTestStatusEmbedManager(mockSession, mockCtrl)

	err := manager.Update(context.Background())
	if err != nil {
		t.Errorf("Expected no error, got %v", err)
	}

	if mockSession.editCalls != 1 {
		t.Errorf("Expected 1 edit call, got %d", mockSession.editCalls)
	}

	if mockSession.sendCalls != 0 {
		t.Errorf("Expected 0 send calls, got %d", mockSession.sendCalls)
	}

	if mockSession.lastEdit == nil {
		t.Fatal("Expected lastEdit to be set")
	}

	if mockSession.lastEdit.ID != "existing-message-id" {
		t.Errorf("Expected edit message ID to be 'existing-message-id', got '%s'", mockSession.lastEdit.ID)
	}
}
