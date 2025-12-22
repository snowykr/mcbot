package discord

import (
	"context"
	"errors"
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/snowy/mcbot/internal/config"
	"github.com/snowy/mcbot/internal/mcserver"
	"github.com/snowy/mcbot/internal/state"
)

type mockDiscordSession struct {
	editCalls     int
	editResponses []error
	sendCalls     int
	sendResponse  error
	sendMessageID string

	lastEdit      *discordgo.MessageEdit
	lastSend      *discordgo.MessageSend
	lastChannelID string
}

func (m *mockDiscordSession) ChannelMessageEditComplex(edit *discordgo.MessageEdit) (*discordgo.Message, error) {
	m.editCalls++
	m.lastEdit = edit

	if m.editCalls <= len(m.editResponses) {
		err := m.editResponses[m.editCalls-1]
		if err != nil {
			return nil, err
		}
	}
	return &discordgo.Message{ID: edit.ID}, nil
}

func (m *mockDiscordSession) ChannelMessageSendComplex(channelID string, data *discordgo.MessageSend) (*discordgo.Message, error) {
	m.sendCalls++
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

func (m *mockDiscordSession) ChannelMessages(_ string, _ int, _, _, _ string) ([]*discordgo.Message, error) {
	return nil, nil
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

	return &StatusEmbedManager{
		session:    session,
		cfg:        cfg,
		controller: controller,
		messageID:  "existing-message-id",
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
