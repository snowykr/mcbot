package discord

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/snowy/mcbot/internal/config"
	"github.com/snowy/mcbot/internal/mcserver"
	"github.com/snowy/mcbot/internal/state"
)

const (
	testGuildID      = "123456789012345678"
	testOtherGuildID = "987654321098765432"
	testBotUserID    = "246813579024681357"
)

var discordEndpointOverrideMu sync.Mutex

type testServerController struct {
	startCh         chan mcserver.StartResult
	stopCh          chan mcserver.StopResult
	stopStarted     chan struct{}
	presenceVal     mcserver.PresenceState
	presenceStarted chan struct{}
	presenceBlock   <-chan struct{}
}

func (t *testServerController) Start(_ context.Context) <-chan mcserver.StartResult {
	return t.startCh
}

func (t *testServerController) Stop(_ context.Context) <-chan mcserver.StopResult {
	if t.stopStarted != nil {
		select {
		case <-t.stopStarted:
		default:
			close(t.stopStarted)
		}
	}
	return t.stopCh
}

func (t *testServerController) Presence(_ context.Context) mcserver.PresenceState {
	if t.presenceStarted != nil {
		select {
		case <-t.presenceStarted:
		default:
			close(t.presenceStarted)
		}
	}
	if t.presenceBlock != nil {
		<-t.presenceBlock
	}
	return t.presenceVal
}

type testStatusEmbedUpdater struct {
	mu               sync.Mutex
	updateCalls      []contextInfo
	checkCurrent     bool
	currentChannelID string
	currentMessageID string
}

type contextInfo struct {
	ctx              context.Context
	hasDeadline      bool
	deadlineDuration time.Duration
	isCancelled      bool
}

func (t *testStatusEmbedUpdater) Update(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	info := contextInfo{
		ctx: ctx,
	}

	if deadline, ok := ctx.Deadline(); ok {
		info.hasDeadline = true
		info.deadlineDuration = time.Until(deadline)
	}

	select {
	case <-ctx.Done():
		info.isCancelled = true
	default:
	}

	t.updateCalls = append(t.updateCalls, info)
	return nil
}

func (t *testStatusEmbedUpdater) getUpdateCalls() []contextInfo {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]contextInfo{}, t.updateCalls...)
}

func (t *testStatusEmbedUpdater) IsCurrentStatusMessage(channelID, messageID string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.checkCurrent {
		return true
	}
	return channelID == t.currentChannelID && messageID == t.currentMessageID
}

type testRCONExecutor struct {
	response    string
	err         error
	lastCtx     context.Context
	lastCommand string
}

func (t *testRCONExecutor) Execute(ctx context.Context, command string) (string, error) {
	t.lastCtx = ctx
	t.lastCommand = command
	return t.response, t.err
}

type testChannelConfigurator struct {
	mu            sync.Mutex
	called        bool
	callCount     int
	lastChannelID string
	lastPresence  mcserver.PresenceState
	err           error
	started       chan struct{}
	block         <-chan struct{}
	once          sync.Once
}

func (t *testChannelConfigurator) ConfigureChannel(_ context.Context, channelID string, presence mcserver.PresenceState) error {
	t.mu.Lock()
	t.called = true
	t.callCount++
	t.lastChannelID = channelID
	t.lastPresence = presence
	t.mu.Unlock()

	if t.started != nil {
		t.once.Do(func() { close(t.started) })
	}

	if t.block != nil {
		<-t.block
	}

	return t.err
}

func (t *testChannelConfigurator) snapshot() (called bool, callCount int, lastChannelID string, lastPresence mcserver.PresenceState) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.called, t.callCount, t.lastChannelID, t.lastPresence
}

type testRuntimeEmbedChannelStore struct {
	mu            sync.Mutex
	loadChannelID string
	loadFound     bool
	loadErr       error
	saveCalls     []string
	clearCalls    int
}

func (s *testRuntimeEmbedChannelStore) Load(context.Context) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadChannelID, s.loadFound, s.loadErr
}

func (s *testRuntimeEmbedChannelStore) Save(_ context.Context, channelID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.saveCalls = append(s.saveCalls, channelID)
	return nil
}

func (s *testRuntimeEmbedChannelStore) Clear(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clearCalls++
	return nil
}

type testChannelSwitcher struct {
	mu          sync.Mutex
	switchCalls []string
	presences   []mcserver.PresenceState
}

func (s *testChannelSwitcher) SwitchChannel(_ context.Context, channelID string, presence mcserver.PresenceState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.switchCalls = append(s.switchCalls, channelID)
	s.presences = append(s.presences, presence)
	return nil
}

type recordedDiscordRequest struct {
	Method string
	Path   string
	Body   []byte
}

type discordAPITestServer struct {
	server        *httptest.Server
	mu            sync.Mutex
	requests      []recordedDiscordRequest
	handlerErrors []error
}

func newDiscordAPITestSession(t *testing.T) (*discordgo.Session, *discordAPITestServer) {
	t.Helper()

	testServer := &discordAPITestServer{}
	testServer.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			testServer.recordHandlerError(fmt.Errorf("failed to read request body: %w", err))
			http.Error(w, "failed to read request body", http.StatusInternalServerError)
			return
		}

		testServer.mu.Lock()
		testServer.requests = append(testServer.requests, recordedDiscordRequest{
			Method: r.Method,
			Path:   r.URL.Path,
			Body:   body,
		})
		testServer.mu.Unlock()

		switch {
		case strings.HasSuffix(r.URL.Path, "/callback"):
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/users/"):
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.Write([]byte(`{"id":"` + testBotUserID + `","username":"bot"}`)); err != nil {
				testServer.recordHandlerError(fmt.Errorf("failed to write /users/@me response: %w", err))
			}
		case strings.Contains(r.URL.Path, "/webhooks/"):
			w.Header().Set("Content-Type", "application/json")
			if _, err := w.Write([]byte(`{"id":"followup-message-id"}`)); err != nil {
				testServer.recordHandlerError(fmt.Errorf("failed to write followup response: %w", err))
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(testServer.server.Close)
	t.Cleanup(func() {
		testServer.assertNoHandlerErrors(t)
	})

	restore := overrideDiscordEndpoints(testServer.server.URL)
	t.Cleanup(restore)

	session, err := discordgo.New("Bot test-token")
	if err != nil {
		t.Fatalf("failed to create discord session: %v", err)
	}
	session.Client = testServer.server.Client()

	return session, testServer
}

func overrideDiscordEndpoints(baseURL string) func() {
	discordEndpointOverrideMu.Lock()

	oldEndpointDiscord := discordgo.EndpointDiscord
	oldEndpointAPI := discordgo.EndpointAPI
	oldEndpointUsers := discordgo.EndpointUsers
	oldEndpointWebhooks := discordgo.EndpointWebhooks
	oldEndpointApplications := discordgo.EndpointApplications

	discordgo.EndpointDiscord = baseURL + "/"
	discordgo.EndpointAPI = discordgo.EndpointDiscord + "api/v" + discordgo.APIVersion + "/"
	discordgo.EndpointUsers = discordgo.EndpointAPI + "users/"
	discordgo.EndpointWebhooks = discordgo.EndpointAPI + "webhooks/"
	discordgo.EndpointApplications = discordgo.EndpointAPI + "applications"

	return func() {
		discordgo.EndpointDiscord = oldEndpointDiscord
		discordgo.EndpointAPI = oldEndpointAPI
		discordgo.EndpointUsers = oldEndpointUsers
		discordgo.EndpointWebhooks = oldEndpointWebhooks
		discordgo.EndpointApplications = oldEndpointApplications
		discordEndpointOverrideMu.Unlock()
	}
}

func (s *discordAPITestServer) recordedRequests() []recordedDiscordRequest {
	s.mu.Lock()
	defer s.mu.Unlock()

	requests := make([]recordedDiscordRequest, len(s.requests))
	copy(requests, s.requests)
	return requests
}

func (s *discordAPITestServer) recordHandlerError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlerErrors = append(s.handlerErrors, err)
}

func (s *discordAPITestServer) assertNoHandlerErrors(t *testing.T) {
	t.Helper()

	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.handlerErrors) == 0 {
		return
	}

	messages := make([]string, 0, len(s.handlerErrors))
	for _, err := range s.handlerErrors {
		messages = append(messages, err.Error())
	}
	t.Fatalf("Discord API test server handler errors: %s", strings.Join(messages, "; "))
}

func addGuildRole(t *testing.T, session *discordgo.Session, guildID, roleID, roleName string) {
	t.Helper()

	err := session.State.GuildAdd(&discordgo.Guild{
		ID: guildID,
		Roles: []*discordgo.Role{{
			ID:   roleID,
			Name: roleName,
		}},
	})
	if err != nil {
		t.Fatalf("failed to add guild state: %v", err)
	}
}

func seedChannelCommandState(t *testing.T, session *discordgo.Session, guildID, channelID, channelName string, channelType discordgo.ChannelType, roleID, roleName string, rolePermissions int64, memberHasRole bool) {
	t.Helper()

	if err := session.State.GuildAdd(&discordgo.Guild{
		ID: guildID,
		Roles: []*discordgo.Role{{
			ID:          roleID,
			Name:        roleName,
			Permissions: rolePermissions,
		}},
	}); err != nil {
		t.Fatalf("failed to add guild state: %v", err)
	}

	if err := session.State.ChannelAdd(&discordgo.Channel{
		ID:      channelID,
		GuildID: guildID,
		Name:    channelName,
		Type:    channelType,
	}); err != nil {
		t.Fatalf("failed to add channel state: %v", err)
	}

	memberRoles := []string{}
	if memberHasRole {
		memberRoles = []string{roleID}
	}

	if err := session.State.MemberAdd(&discordgo.Member{
		GuildID: guildID,
		User: &discordgo.User{
			ID:       testBotUserID,
			Username: "bot",
		},
		Roles: memberRoles,
	}); err != nil {
		t.Fatalf("failed to add member state: %v", err)
	}

	session.State.User = &discordgo.User{ID: testBotUserID, Username: "bot"}
}

func newApplicationCommandInteraction(commandName string, options []*discordgo.ApplicationCommandInteractionDataOption, roles []string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:      "123456789012345670",
			AppID:   "123456789012345678",
			Token:   "interaction-token",
			Type:    discordgo.InteractionApplicationCommand,
			GuildID: testGuildID,
			Member: &discordgo.Member{
				Roles: roles,
				User: &discordgo.User{
					ID:       "user-id",
					Username: "test-user",
				},
			},
			Data: discordgo.ApplicationCommandInteractionData{
				Name:    commandName,
				Options: options,
			},
		},
	}
}

func newRCONInteraction(command string, roles []string) *discordgo.InteractionCreate {
	return newApplicationCommandInteraction("마크봇", []*discordgo.ApplicationCommandInteractionDataOption{{
		Name: "rcon",
		Type: discordgo.ApplicationCommandOptionSubCommand,
		Options: []*discordgo.ApplicationCommandInteractionDataOption{{
			Name:  "command",
			Type:  discordgo.ApplicationCommandOptionString,
			Value: command,
		}},
	}}, roles)
}

func newChannelInteraction(channelID string, roles []string) *discordgo.InteractionCreate {
	return newApplicationCommandInteraction("마크봇", []*discordgo.ApplicationCommandInteractionDataOption{{
		Name: "채널",
		Type: discordgo.ApplicationCommandOptionSubCommand,
		Options: []*discordgo.ApplicationCommandInteractionDataOption{{
			Name:  "channel",
			Type:  discordgo.ApplicationCommandOptionChannel,
			Value: channelID,
		}},
	}}, roles)
}

func newComponentInteraction(customID, guildID string, roles []string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:      "123456789012345671",
			AppID:   "123456789012345678",
			Token:   "interaction-token",
			Type:    discordgo.InteractionMessageComponent,
			GuildID: guildID,
			Member: &discordgo.Member{
				Roles: roles,
				User: &discordgo.User{
					ID:       "user-id",
					Username: "test-user",
				},
			},
			Data: discordgo.MessageComponentInteractionData{CustomID: customID},
			Message: &discordgo.Message{
				ID:        "current-message-id",
				ChannelID: "current-channel",
				GuildID:   guildID,
			},
		},
	}
}

func decodeInteractionResponse(t *testing.T, body []byte) discordgo.InteractionResponse {
	t.Helper()

	var response discordgo.InteractionResponse
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("failed to decode interaction response: %v", err)
	}
	return response
}

func decodeWebhookParams(t *testing.T, body []byte) discordgo.WebhookParams {
	t.Helper()

	var params discordgo.WebhookParams
	if err := json.Unmarshal(body, &params); err != nil {
		t.Fatalf("failed to decode webhook params: %v", err)
	}
	return params
}

func assertAllowedMentionsParseEmpty(t *testing.T, body []byte, fieldPath ...string) {
	t.Helper()

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("failed to decode raw payload: %v", err)
	}

	current := payload
	for _, field := range fieldPath {
		raw, ok := current[field]
		if !ok {
			t.Fatalf("expected %q field in payload", field)
		}

		var next map[string]json.RawMessage
		if err := json.Unmarshal(raw, &next); err != nil {
			t.Fatalf("failed to decode %q field: %v", field, err)
		}
		current = next
	}

	allowedMentionsRaw, ok := current["allowed_mentions"]
	if !ok {
		t.Fatal("expected allowed_mentions field in payload")
	}

	var allowedMentions map[string]json.RawMessage
	if err := json.Unmarshal(allowedMentionsRaw, &allowedMentions); err != nil {
		t.Fatalf("failed to decode allowed_mentions field: %v", err)
	}

	parseRaw, ok := allowedMentions["parse"]
	if !ok {
		t.Fatal("expected allowed_mentions.parse field in payload")
	}

	var parse []string
	if err := json.Unmarshal(parseRaw, &parse); err != nil {
		t.Fatalf("failed to decode allowed_mentions.parse: %v", err)
	}
	if len(parse) != 0 {
		t.Fatalf("expected allowed_mentions.parse to be empty, got %v", parse)
	}
}

func TestHandleButtonStart_FirstUpdateUsesEmbedTimeout(t *testing.T) {
	cfg := &config.Config{
		EmbedUpdateTimeout:     5 * time.Second,
		ServerOperationTimeout: 10 * time.Second,
	}

	controller := &testServerController{
		startCh: make(chan mcserver.StartResult, 1),
		presenceVal: mcserver.PresenceState{
			ServerState: state.StateStopped,
		},
	}

	updater := &testStatusEmbedUpdater{}

	handler := NewHandler(cfg, controller, updater, &testRCONExecutor{})

	mockInteraction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			Member: &discordgo.Member{
				User: &discordgo.User{
					Username: "testuser",
				},
			},
		},
	}

	handler.handleButtonStart(nil, mockInteraction)

	calls := updater.getUpdateCalls()
	if len(calls) != 1 {
		t.Fatalf("Expected 1 Update call immediately, got %d", len(calls))
	}

	firstCall := calls[0]
	if !firstCall.hasDeadline {
		t.Error("First Update call should have a deadline (EmbedUpdateTimeout)")
	}

	if firstCall.isCancelled {
		t.Error("First Update context should not be cancelled")
	}
}

func TestHandleInteraction_UnknownApplicationCommandRespondsEphemeral(t *testing.T) {
	session, api := newDiscordAPITestSession(t)
	handler := NewHandler(&config.Config{}, &testServerController{}, &testStatusEmbedUpdater{}, nil)

	interaction := newApplicationCommandInteraction("unknown", nil, nil)
	handler.HandleInteraction(session, interaction)

	requests := api.recordedRequests()
	if len(requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(requests))
	}

	response := decodeInteractionResponse(t, requests[0].Body)
	if response.Data == nil || response.Data.Content != "알 수 없는 명령어입니다." {
		t.Fatalf("unexpected interaction response: %+v", response.Data)
	}
	assertAllowedMentionsParseEmpty(t, requests[0].Body, "data")
}

func TestHandleInteraction_RCONDisabledRespondsEphemeral(t *testing.T) {
	session, api := newDiscordAPITestSession(t)
	addGuildRole(t, session, testGuildID, "role-id", "마크봇")

	handler := NewHandler(&config.Config{McbotRoleName: "마크봇", TrustedGuildID: testGuildID}, &testServerController{}, &testStatusEmbedUpdater{}, nil)
	interaction := newRCONInteraction("list", []string{"role-id"})

	handler.HandleInteraction(session, interaction)

	requests := api.recordedRequests()
	if len(requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(requests))
	}

	response := decodeInteractionResponse(t, requests[0].Body)
	if response.Type != discordgo.InteractionResponseChannelMessageWithSource {
		t.Fatalf("expected immediate response, got %v", response.Type)
	}
	if response.Data == nil || !strings.Contains(response.Data.Content, "RCON이 활성화되지 않았습니다") {
		t.Fatalf("unexpected response content: %+v", response.Data)
	}
	assertAllowedMentionsParseEmpty(t, requests[0].Body, "data")
}

func TestHandleInteraction_RCONEmptyCommandRespondsEphemeral(t *testing.T) {
	session, api := newDiscordAPITestSession(t)
	addGuildRole(t, session, testGuildID, "role-id", "마크봇")

	handler := NewHandler(&config.Config{McbotRoleName: "마크봇", TrustedGuildID: testGuildID}, &testServerController{}, &testStatusEmbedUpdater{}, &testRCONExecutor{})
	interaction := newRCONInteraction("", []string{"role-id"})

	handler.HandleInteraction(session, interaction)

	requests := api.recordedRequests()
	if len(requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(requests))
	}

	response := decodeInteractionResponse(t, requests[0].Body)
	if response.Type != discordgo.InteractionResponseChannelMessageWithSource {
		t.Fatalf("expected immediate response, got %v", response.Type)
	}
	if response.Data == nil || !strings.Contains(response.Data.Content, "명령어를 입력해주세요") {
		t.Fatalf("unexpected response content: %+v", response.Data)
	}
	assertAllowedMentionsParseEmpty(t, requests[0].Body, "data")
}

func TestHandleInteraction_RCONPermissionDenied(t *testing.T) {
	session, api := newDiscordAPITestSession(t)
	addGuildRole(t, session, testGuildID, "role-id", "마크봇")

	handler := NewHandler(&config.Config{McbotRoleName: "마크봇", TrustedGuildID: testGuildID}, &testServerController{}, &testStatusEmbedUpdater{}, &testRCONExecutor{})
	interaction := newRCONInteraction("list", nil)

	handler.HandleInteraction(session, interaction)

	requests := api.recordedRequests()
	if len(requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(requests))
	}
	assertAllowedMentionsParseEmpty(t, requests[0].Body, "data")

	response := decodeInteractionResponse(t, requests[0].Body)
	if response.Type != discordgo.InteractionResponseChannelMessageWithSource {
		t.Fatalf("expected immediate response, got %v", response.Type)
	}
	if response.Data == nil || !strings.Contains(response.Data.Content, "역할이 필요합니다") {
		t.Fatalf("unexpected response data: %+v", response.Data)
	}
}

func TestHandleInteraction_ChannelCommand_WrongGuildRespondsEphemeralBeforeDeferred(t *testing.T) {
	session, api := newDiscordAPITestSession(t)
	configurator := &testChannelConfigurator{}

	handler := NewHandler(&config.Config{
		McbotRoleName:          "마크봇",
		TrustedGuildID:         testGuildID,
		ServerOperationTimeout: time.Second,
	}, &testServerController{}, &testStatusEmbedUpdater{}, &testRCONExecutor{}, WithChannelConfigurator(configurator))
	interaction := newChannelInteraction("target-channel", nil)
	interaction.GuildID = testOtherGuildID

	handler.HandleInteraction(session, interaction)

	called, _, _, _ := configurator.snapshot()
	if called {
		t.Fatal("channel configurator was called for an untrusted guild")
	}

	requests := api.recordedRequests()
	if len(requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(requests))
	}

	response := decodeInteractionResponse(t, requests[0].Body)
	if response.Type != discordgo.InteractionResponseChannelMessageWithSource {
		t.Fatalf("expected immediate response, got %v", response.Type)
	}
	if response.Data == nil || !strings.Contains(response.Data.Content, "이 서버에서는 사용할 수 없는 명령어") {
		t.Fatalf("unexpected response content: %+v", response.Data)
	}
	assertAllowedMentionsParseEmpty(t, requests[0].Body, "data")
}

func TestHandleInteraction_ChannelCommand_MissingRoleRespondsPermissionDenied(t *testing.T) {
	session, api := newDiscordAPITestSession(t)
	addGuildRole(t, session, testGuildID, "role-id", "마크봇")
	configurator := &testChannelConfigurator{}

	handler := NewHandler(&config.Config{
		McbotRoleName:          "마크봇",
		TrustedGuildID:         testGuildID,
		ServerOperationTimeout: time.Second,
	}, &testServerController{}, &testStatusEmbedUpdater{}, &testRCONExecutor{}, WithChannelConfigurator(configurator))
	interaction := newChannelInteraction("target-channel", nil)

	handler.HandleInteraction(session, interaction)

	called, _, _, _ := configurator.snapshot()
	if called {
		t.Fatal("channel configurator was called despite missing role")
	}

	requests := api.recordedRequests()
	if len(requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(requests))
	}

	response := decodeInteractionResponse(t, requests[0].Body)
	if response.Type != discordgo.InteractionResponseChannelMessageWithSource {
		t.Fatalf("expected immediate response, got %v", response.Type)
	}
	if response.Data == nil || len(response.Data.Embeds) != 1 || !strings.Contains(response.Data.Embeds[0].Description, "역할이 필요합니다") {
		t.Fatalf("unexpected response data: %+v", response.Data)
	}
	assertAllowedMentionsParseEmpty(t, requests[0].Body, "data")
}

func TestHandleInteraction_ChannelCommand_RejectsUnsupportedChannelType(t *testing.T) {
	session, api := newDiscordAPITestSession(t)
	seedChannelCommandState(t, session, testGuildID, "target-channel", "voice-channel", discordgo.ChannelTypeGuildVoice, "role-id", "마크봇",
		discordgo.PermissionViewChannel|discordgo.PermissionSendMessages|discordgo.PermissionEmbedLinks|discordgo.PermissionReadMessageHistory, true)
	configurator := &testChannelConfigurator{}

	handler := NewHandler(&config.Config{
		McbotRoleName:          "마크봇",
		TrustedGuildID:         testGuildID,
		ServerOperationTimeout: time.Second,
	}, &testServerController{}, &testStatusEmbedUpdater{}, &testRCONExecutor{}, WithChannelConfigurator(configurator))
	interaction := newChannelInteraction("target-channel", []string{"role-id"})

	handler.HandleInteraction(session, interaction)

	called, _, _, _ := configurator.snapshot()
	if called {
		t.Fatal("channel configurator was called for an unsupported channel type")
	}

	requests := api.recordedRequests()
	if len(requests) != 2 {
		t.Fatalf("expected deferred response and followup, got %d requests", len(requests))
	}

	deferred := decodeInteractionResponse(t, requests[0].Body)
	if deferred.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource {
		t.Fatalf("expected deferred response, got %v", deferred.Type)
	}

	followup := decodeWebhookParams(t, requests[1].Body)
	if !strings.Contains(followup.Content, "일반 채팅 채널 또는 공지 채널") {
		t.Fatalf("unexpected followup content: %q", followup.Content)
	}
}

func TestHandleInteraction_ChannelCommand_RejectsChannelOutsideTrustedGuild(t *testing.T) {
	session, api := newDiscordAPITestSession(t)
	addGuildRole(t, session, testGuildID, "role-id", "마크봇")
	seedChannelCommandState(t, session, testOtherGuildID, "target-channel", "target-channel", discordgo.ChannelTypeGuildText, "role-id", "마크봇",
		discordgo.PermissionViewChannel|discordgo.PermissionSendMessages|discordgo.PermissionEmbedLinks|discordgo.PermissionReadMessageHistory, true)
	configurator := &testChannelConfigurator{}

	handler := NewHandler(&config.Config{
		McbotRoleName:          "마크봇",
		TrustedGuildID:         testGuildID,
		ServerOperationTimeout: time.Second,
	}, &testServerController{}, &testStatusEmbedUpdater{}, &testRCONExecutor{}, WithChannelConfigurator(configurator))
	interaction := newChannelInteraction("target-channel", []string{"role-id"})

	handler.HandleInteraction(session, interaction)

	called, _, _, _ := configurator.snapshot()
	if called {
		t.Fatal("channel configurator was called for a channel outside the trusted guild")
	}

	requests := api.recordedRequests()
	if len(requests) != 2 {
		t.Fatalf("expected deferred response and followup, got %d requests", len(requests))
	}

	deferred := decodeInteractionResponse(t, requests[0].Body)
	if deferred.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource {
		t.Fatalf("expected deferred response, got %v", deferred.Type)
	}

	followup := decodeWebhookParams(t, requests[1].Body)
	if !strings.Contains(followup.Content, "이 서버의 채널이 아닙니다") {
		t.Fatalf("unexpected followup content: %q", followup.Content)
	}
}

func TestHandleInteraction_ChannelCommand_RejectsWhenBotLacksHistoryEmbedOrSendPermission(t *testing.T) {
	basePermissions := int64(discordgo.PermissionViewChannel | discordgo.PermissionSendMessages | discordgo.PermissionEmbedLinks | discordgo.PermissionReadMessageHistory)
	for _, tc := range []struct {
		name        string
		drop        int64
		dropMessage string
	}{
		{name: "history", drop: discordgo.PermissionReadMessageHistory, dropMessage: "메시지 기록 읽기"},
		{name: "embed", drop: discordgo.PermissionEmbedLinks, dropMessage: "임베드 링크"},
		{name: "send", drop: discordgo.PermissionSendMessages, dropMessage: "메시지 전송"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			session, api := newDiscordAPITestSession(t)
			seedChannelCommandState(t, session, testGuildID, "target-channel", "target-channel", discordgo.ChannelTypeGuildText, "role-id", "마크봇", basePermissions&^tc.drop, true)
			configurator := &testChannelConfigurator{}

			handler := NewHandler(&config.Config{
				McbotRoleName:          "마크봇",
				TrustedGuildID:         testGuildID,
				ServerOperationTimeout: time.Second,
			}, &testServerController{}, &testStatusEmbedUpdater{}, &testRCONExecutor{}, WithChannelConfigurator(configurator))
			interaction := newChannelInteraction("target-channel", []string{"role-id"})

			handler.HandleInteraction(session, interaction)

			called, _, _, _ := configurator.snapshot()
			if called {
				t.Fatalf("channel configurator was called despite missing %s permission", tc.dropMessage)
			}

			requests := api.recordedRequests()
			if len(requests) != 2 {
				t.Fatalf("expected deferred response and followup, got %d requests", len(requests))
			}

			deferred := decodeInteractionResponse(t, requests[0].Body)
			if deferred.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource {
				t.Fatalf("expected deferred response, got %v", deferred.Type)
			}

			followup := decodeWebhookParams(t, requests[1].Body)
			if !strings.Contains(followup.Content, "권한") {
				t.Fatalf("unexpected followup content: %q", followup.Content)
			}
		})
	}
}

func TestHandleInteraction_ChannelCommand_RequiresChannelOption(t *testing.T) {
	session, api := newDiscordAPITestSession(t)
	addGuildRole(t, session, testGuildID, "role-id", "마크봇")
	configurator := &testChannelConfigurator{}

	handler := NewHandler(&config.Config{
		McbotRoleName:          "마크봇",
		TrustedGuildID:         testGuildID,
		ServerOperationTimeout: time.Second,
	}, &testServerController{}, &testStatusEmbedUpdater{}, &testRCONExecutor{}, WithChannelConfigurator(configurator))
	interaction := newApplicationCommandInteraction("마크봇", []*discordgo.ApplicationCommandInteractionDataOption{{
		Name: "채널",
		Type: discordgo.ApplicationCommandOptionSubCommand,
	}}, []string{"role-id"})

	handler.HandleInteraction(session, interaction)

	called, _, _, _ := configurator.snapshot()
	if called {
		t.Fatal("channel configurator was called despite missing channel option")
	}

	requests := api.recordedRequests()
	if len(requests) != 2 {
		t.Fatalf("expected deferred response and followup, got %d requests", len(requests))
	}

	deferred := decodeInteractionResponse(t, requests[0].Body)
	if deferred.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource {
		t.Fatalf("expected deferred response, got %v", deferred.Type)
	}

	followup := decodeWebhookParams(t, requests[1].Body)
	if !strings.Contains(followup.Content, "채널을 선택해주세요") {
		t.Fatalf("unexpected followup content: %q", followup.Content)
	}
}

func TestHandleInteraction_ChannelCommand_ResolvesBotIdentityWhenStateUserMissing(t *testing.T) {
	session, api := newDiscordAPITestSession(t)
	seedChannelCommandState(t, session, testGuildID, "target-channel", "target-channel", discordgo.ChannelTypeGuildText, "role-id", "마크봇",
		discordgo.PermissionViewChannel|discordgo.PermissionSendMessages|discordgo.PermissionEmbedLinks|discordgo.PermissionReadMessageHistory, true)
	session.State.User = nil
	configurator := &testChannelConfigurator{}

	handler := NewHandler(&config.Config{
		McbotRoleName:          "마크봇",
		TrustedGuildID:         testGuildID,
		EmbedUpdateTimeout:     time.Second,
		ServerOperationTimeout: time.Second,
	}, &testServerController{
		presenceVal: mcserver.PresenceState{ServerState: state.StateRunning},
	}, &testStatusEmbedUpdater{}, &testRCONExecutor{}, WithChannelConfigurator(configurator))
	interaction := newChannelInteraction("target-channel", []string{"role-id"})

	handler.HandleInteraction(session, interaction)

	requests := api.recordedRequests()
	if len(requests) != 3 {
		t.Fatalf("expected deferred response, bot identity lookup, and followup, got %d requests", len(requests))
	}

	if requests[1].Method != http.MethodGet {
		t.Fatalf("expected identity fallback GET request in the middle, got %s %s", requests[1].Method, requests[1].Path)
	}

	called, callCount, lastChannelID, lastPresence := configurator.snapshot()
	if !called || callCount != 1 || lastChannelID != "target-channel" {
		t.Fatalf("configurator call state = called:%v count:%d channel:%q, want one call to target-channel", called, callCount, lastChannelID)
	}
	if lastPresence.ServerState != state.StateRunning {
		t.Fatalf("configurator presence = %v, want running", lastPresence.ServerState)
	}

	followup := decodeWebhookParams(t, requests[len(requests)-1].Body)
	if !strings.Contains(followup.Content, "<#target-channel>") {
		t.Fatalf("expected success followup to mention target channel, got %q", followup.Content)
	}
}

func TestHandleInteraction_ChannelCommand_TrustedGuildAllowsExecution(t *testing.T) {
	session, api := newDiscordAPITestSession(t)
	seedChannelCommandState(t, session, testGuildID, "target-channel", "target-channel", discordgo.ChannelTypeGuildText, "role-id", "마크봇",
		discordgo.PermissionViewChannel|discordgo.PermissionSendMessages|discordgo.PermissionEmbedLinks|discordgo.PermissionReadMessageHistory, true)

	started := make(chan struct{})
	unblock := make(chan struct{})
	configurator := &testChannelConfigurator{started: started, block: unblock}
	handler := NewHandler(&config.Config{
		McbotRoleName:          "마크봇",
		TrustedGuildID:         testGuildID,
		EmbedUpdateTimeout:     time.Second,
		ServerOperationTimeout: time.Second,
	}, &testServerController{
		presenceVal: mcserver.PresenceState{ServerState: state.StateRunning},
	}, &testStatusEmbedUpdater{}, &testRCONExecutor{}, WithChannelConfigurator(configurator))
	interaction := newChannelInteraction("target-channel", []string{"role-id"})

	handlerDone := make(chan struct{})
	go func() {
		defer close(handlerDone)
		handler.HandleInteraction(session, interaction)
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("channel configurator was not called")
	}

	requests := api.recordedRequests()
	if len(requests) != 1 {
		close(unblock)
		<-handlerDone
		t.Fatalf("expected deferred response before reconfiguration finishes, got %d requests", len(requests))
	}

	deferred := decodeInteractionResponse(t, requests[0].Body)
	if deferred.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource {
		close(unblock)
		<-handlerDone
		t.Fatalf("expected deferred response, got %v", deferred.Type)
	}
	assertAllowedMentionsParseEmpty(t, requests[0].Body, "data")

	called, callCount, lastChannelID, lastPresence := configurator.snapshot()
	if !called || callCount != 1 {
		close(unblock)
		<-handlerDone
		t.Fatalf("configurator call state = called:%v count:%d, want one call", called, callCount)
	}
	if lastChannelID != "target-channel" {
		close(unblock)
		<-handlerDone
		t.Fatalf("configurator channel = %q, want target-channel", lastChannelID)
	}
	if lastPresence.ServerState != state.StateRunning {
		close(unblock)
		<-handlerDone
		t.Fatalf("configurator presence = %v, want running", lastPresence.ServerState)
	}

	close(unblock)

	select {
	case <-handlerDone:
	case <-time.After(time.Second):
		t.Fatal("handler did not finish after reconfiguration completed")
	}

	requests = api.recordedRequests()
	if len(requests) != 2 {
		t.Fatalf("expected deferred response and followup, got %d requests", len(requests))
	}
}

func TestHandleInteraction_ChannelCommand_RepairsUnconfiguredMode(t *testing.T) {
	session, api := newDiscordAPITestSession(t)
	seedChannelCommandState(t, session, testGuildID, "target-channel", "target-channel", discordgo.ChannelTypeGuildText, "role-id", "마크봇",
		discordgo.PermissionViewChannel|discordgo.PermissionSendMessages|discordgo.PermissionEmbedLinks|discordgo.PermissionReadMessageHistory, true)

	store := &testRuntimeEmbedChannelStore{}
	manager := &testChannelSwitcher{}
	configurator := NewChannelConfigurator(store, manager, "")

	handler := NewHandler(&config.Config{
		McbotRoleName:          "마크봇",
		TrustedGuildID:         testGuildID,
		EmbedUpdateTimeout:     time.Second,
		ServerOperationTimeout: time.Second,
	}, &testServerController{
		presenceVal: mcserver.PresenceState{ServerState: state.StateRunning},
	}, &testStatusEmbedUpdater{}, &testRCONExecutor{}, WithChannelConfigurator(configurator))
	interaction := newChannelInteraction("target-channel", []string{"role-id"})

	handler.HandleInteraction(session, interaction)

	requests := api.recordedRequests()
	if len(requests) != 2 {
		t.Fatalf("expected deferred response and followup, got %d requests", len(requests))
	}

	deferred := decodeInteractionResponse(t, requests[0].Body)
	if deferred.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource {
		t.Fatalf("expected deferred response, got %v", deferred.Type)
	}

	followup := decodeWebhookParams(t, requests[1].Body)
	if !strings.Contains(followup.Content, "<#target-channel>") {
		t.Fatalf("expected success followup to mention target channel, got %q", followup.Content)
	}

	store.mu.Lock()
	if len(store.saveCalls) != 1 || store.saveCalls[0] != "target-channel" {
		store.mu.Unlock()
		t.Fatalf("store save calls = %v, want [target-channel]", store.saveCalls)
	}
	if store.clearCalls != 0 {
		store.mu.Unlock()
		t.Fatalf("store clear calls = %d, want 0", store.clearCalls)
	}
	store.mu.Unlock()

	manager.mu.Lock()
	if len(manager.switchCalls) != 1 || manager.switchCalls[0] != "target-channel" {
		manager.mu.Unlock()
		t.Fatalf("manager switch calls = %v, want [target-channel]", manager.switchCalls)
	}
	manager.mu.Unlock()

	if configurator.currentChannelID != "target-channel" {
		t.Fatalf("configurator currentChannelID = %q, want target-channel", configurator.currentChannelID)
	}
}

func TestHandleInteraction_ChannelCommand_ConfiguratorFailureRespondsEphemeral(t *testing.T) {
	session, api := newDiscordAPITestSession(t)
	seedChannelCommandState(t, session, testGuildID, "target-channel", "target-channel", discordgo.ChannelTypeGuildText, "role-id", "마크봇",
		discordgo.PermissionViewChannel|discordgo.PermissionSendMessages|discordgo.PermissionEmbedLinks|discordgo.PermissionReadMessageHistory, true)
	configurator := &testChannelConfigurator{err: errors.New("persist failed")}

	handler := NewHandler(&config.Config{
		McbotRoleName:          "마크봇",
		TrustedGuildID:         testGuildID,
		EmbedUpdateTimeout:     time.Second,
		ServerOperationTimeout: time.Second,
	}, &testServerController{
		presenceVal: mcserver.PresenceState{ServerState: state.StateRunning},
	}, &testStatusEmbedUpdater{}, &testRCONExecutor{}, WithChannelConfigurator(configurator))
	interaction := newChannelInteraction("target-channel", []string{"role-id"})

	handler.HandleInteraction(session, interaction)

	requests := api.recordedRequests()
	if len(requests) != 2 {
		t.Fatalf("expected deferred response and failure followup, got %d requests", len(requests))
	}

	deferred := decodeInteractionResponse(t, requests[0].Body)
	if deferred.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource {
		t.Fatalf("expected deferred response, got %v", deferred.Type)
	}

	followup := decodeWebhookParams(t, requests[1].Body)
	if !strings.Contains(followup.Content, "채널 설정 실패") {
		t.Fatalf("unexpected followup content: %q", followup.Content)
	}
	called, _, lastChannelID, _ := configurator.snapshot()
	if !called || lastChannelID != "target-channel" {
		t.Fatalf("configurator call state = called:%v channel:%q, want target-channel", called, lastChannelID)
	}
}

func TestHandleInteraction_ChannelCommand_SuccessFollowupMentionsTargetChannel(t *testing.T) {
	session, api := newDiscordAPITestSession(t)
	seedChannelCommandState(t, session, testGuildID, "target-channel", "target-channel", discordgo.ChannelTypeGuildText, "role-id", "마크봇",
		discordgo.PermissionViewChannel|discordgo.PermissionSendMessages|discordgo.PermissionEmbedLinks|discordgo.PermissionReadMessageHistory, true)
	configurator := &testChannelConfigurator{}

	handler := NewHandler(&config.Config{
		McbotRoleName:          "마크봇",
		TrustedGuildID:         testGuildID,
		EmbedUpdateTimeout:     time.Second,
		ServerOperationTimeout: time.Second,
	}, &testServerController{
		presenceVal: mcserver.PresenceState{ServerState: state.StateRunning},
	}, &testStatusEmbedUpdater{}, &testRCONExecutor{}, WithChannelConfigurator(configurator))
	interaction := newChannelInteraction("target-channel", []string{"role-id"})

	handler.HandleInteraction(session, interaction)

	requests := api.recordedRequests()
	if len(requests) != 2 {
		t.Fatalf("expected deferred response and followup, got %d requests", len(requests))
	}

	deferred := decodeInteractionResponse(t, requests[0].Body)
	if deferred.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource {
		t.Fatalf("expected deferred response, got %v", deferred.Type)
	}

	followup := decodeWebhookParams(t, requests[1].Body)
	if !strings.Contains(followup.Content, "<#target-channel>") {
		t.Fatalf("expected target channel mention in followup, got %q", followup.Content)
	}
	assertAllowedMentionsParseEmpty(t, requests[1].Body)

	called, callCount, lastChannelID, _ := configurator.snapshot()
	if !called || callCount != 1 || lastChannelID != "target-channel" {
		t.Fatalf("configurator call state = called:%v count:%d channel:%q, want one call to target-channel", called, callCount, lastChannelID)
	}
}

func TestHandleInteraction_RCONRejectsWhenTrustedGuildUnset(t *testing.T) {
	session, api := newDiscordAPITestSession(t)
	addGuildRole(t, session, testGuildID, "role-id", "마크봇")

	handler := NewHandler(&config.Config{McbotRoleName: "마크봇"}, &testServerController{}, &testStatusEmbedUpdater{}, &testRCONExecutor{})
	interaction := newRCONInteraction("list", []string{"role-id"})

	handler.HandleInteraction(session, interaction)

	requests := api.recordedRequests()
	if len(requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(requests))
	}

	response := decodeInteractionResponse(t, requests[0].Body)
	if response.Type != discordgo.InteractionResponseChannelMessageWithSource {
		t.Fatalf("expected immediate response, got %v", response.Type)
	}
	if response.Data == nil || !strings.Contains(response.Data.Content, "이 서버에서는 사용할 수 없는 명령어") {
		t.Fatalf("unexpected response content: %+v", response.Data)
	}
	assertAllowedMentionsParseEmpty(t, requests[0].Body, "data")
}

func TestHandleInteraction_RCONWrongGuildRespondsEphemeralBeforeDeferred(t *testing.T) {
	session, api := newDiscordAPITestSession(t)
	addGuildRole(t, session, testGuildID, "role-id", "마크봇")

	handler := NewHandler(&config.Config{
		McbotRoleName:  "마크봇",
		TrustedGuildID: testGuildID,
	}, &testServerController{}, &testStatusEmbedUpdater{}, &testRCONExecutor{})
	interaction := newRCONInteraction("list", []string{"role-id"})
	interaction.GuildID = testOtherGuildID

	handler.HandleInteraction(session, interaction)

	requests := api.recordedRequests()
	if len(requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(requests))
	}

	response := decodeInteractionResponse(t, requests[0].Body)
	if response.Type != discordgo.InteractionResponseChannelMessageWithSource {
		t.Fatalf("expected immediate response, got %v", response.Type)
	}
	if response.Data == nil || !strings.Contains(response.Data.Content, "이 서버에서는 사용할 수 없는 명령어") {
		t.Fatalf("unexpected response content: %+v", response.Data)
	}
	assertAllowedMentionsParseEmpty(t, requests[0].Body, "data")
}

func TestHandleInteraction_RCONTrustedGuildAllowsExecution(t *testing.T) {
	session, api := newDiscordAPITestSession(t)
	addGuildRole(t, session, testGuildID, "role-id", "마크봇")

	executor := &testRCONExecutor{response: "ok"}
	handler := NewHandler(&config.Config{
		McbotRoleName:      "마크봇",
		TrustedGuildID:     testGuildID,
		EmbedUpdateTimeout: time.Second,
		RCONTimeout:        time.Second,
	}, &testServerController{
		presenceVal: mcserver.PresenceState{ServerState: state.StateRunning},
	}, &testStatusEmbedUpdater{}, executor)
	interaction := newRCONInteraction("list", []string{"role-id"})

	handler.HandleInteraction(session, interaction)

	requests := api.recordedRequests()
	if len(requests) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(requests))
	}

	deferred := decodeInteractionResponse(t, requests[0].Body)
	if deferred.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource {
		t.Fatalf("expected deferred response, got %v", deferred.Type)
	}
	if executor.lastCommand != "list" {
		t.Fatalf("expected command to be executed, got %q", executor.lastCommand)
	}
}

func TestHandleInteraction_ToggleWrongGuildRespondsEphemeral(t *testing.T) {
	session, api := newDiscordAPITestSession(t)
	addGuildRole(t, session, testGuildID, "role-id", "마크봇")

	handler := NewHandler(&config.Config{
		McbotRoleName:  "마크봇",
		TrustedGuildID: testGuildID,
	}, &testServerController{}, &testStatusEmbedUpdater{}, &testRCONExecutor{})
	interaction := newComponentInteraction(ComponentIDToggle, testOtherGuildID, []string{"role-id"})

	handler.HandleInteraction(session, interaction)

	requests := api.recordedRequests()
	if len(requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(requests))
	}

	response := decodeInteractionResponse(t, requests[0].Body)
	if response.Type != discordgo.InteractionResponseChannelMessageWithSource {
		t.Fatalf("expected immediate response, got %v", response.Type)
	}
	if response.Data == nil || !strings.Contains(response.Data.Content, "이 서버에서는 사용할 수 없는 명령어") {
		t.Fatalf("unexpected response content: %+v", response.Data)
	}
	assertAllowedMentionsParseEmpty(t, requests[0].Body, "data")
}

func TestHandleInteraction_ToggleTrustedGuildDefersMessageUpdate(t *testing.T) {
	session, api := newDiscordAPITestSession(t)
	addGuildRole(t, session, testGuildID, "role-id", "마크봇")

	controller := &testServerController{
		startCh:     make(chan mcserver.StartResult, 1),
		presenceVal: mcserver.PresenceState{ServerState: state.StateStopped},
	}
	controller.startCh <- mcserver.StartResult{Success: true, ReadyDuration: time.Second}

	handler := NewHandler(&config.Config{
		McbotRoleName:          "마크봇",
		TrustedGuildID:         testGuildID,
		EmbedUpdateTimeout:     time.Second,
		ServerOperationTimeout: time.Second,
	}, controller, &testStatusEmbedUpdater{}, &testRCONExecutor{})
	interaction := newComponentInteraction(ComponentIDToggle, testGuildID, []string{"role-id"})

	handler.HandleInteraction(session, interaction)

	var requests []recordedDiscordRequest
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		requests = api.recordedRequests()
		if len(requests) >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(requests) < 2 {
		t.Fatalf("expected deferred response and followup, got %d requests", len(requests))
	}

	response := decodeInteractionResponse(t, requests[0].Body)
	if response.Type != discordgo.InteractionResponseDeferredMessageUpdate {
		t.Fatalf("expected deferred message update, got %v", response.Type)
	}
}

func TestHandleInteraction_ToggleStaleStatusMessageRespondsEphemeralAndDoesNotReadPresence(t *testing.T) {
	session, api := newDiscordAPITestSession(t)
	addGuildRole(t, session, testGuildID, "role-id", "마크봇")

	presenceStarted := make(chan struct{})
	controller := &testServerController{
		presenceStarted: presenceStarted,
		presenceVal:     mcserver.PresenceState{ServerState: state.StateStopped},
	}
	updater := &testStatusEmbedUpdater{
		checkCurrent:     true,
		currentChannelID: "target-channel",
		currentMessageID: "current-message-id",
	}
	handler := NewHandler(&config.Config{
		McbotRoleName:      "마크봇",
		TrustedGuildID:     testGuildID,
		EmbedUpdateTimeout: time.Second,
	}, controller, updater, &testRCONExecutor{})
	interaction := newComponentInteraction(ComponentIDToggle, testGuildID, []string{"role-id"})
	interaction.Message = &discordgo.Message{ID: "old-message-id", ChannelID: "source-channel", GuildID: testGuildID}

	handler.HandleInteraction(session, interaction)

	select {
	case <-presenceStarted:
		t.Fatal("presence was read for a stale status message toggle")
	default:
	}

	requests := api.recordedRequests()
	if len(requests) != 1 {
		t.Fatalf("expected one stale-message response, got %d requests", len(requests))
	}

	response := decodeInteractionResponse(t, requests[0].Body)
	if response.Type != discordgo.InteractionResponseChannelMessageWithSource {
		t.Fatalf("expected immediate ephemeral response, got %v", response.Type)
	}
	if response.Data == nil || response.Data.Flags&discordgo.MessageFlagsEphemeral == 0 {
		t.Fatalf("expected ephemeral response data, got %+v", response.Data)
	}
	if !strings.Contains(response.Data.Content, "이전 제어 메시지") {
		t.Fatalf("unexpected stale-message response content: %q", response.Data.Content)
	}
	assertAllowedMentionsParseEmpty(t, requests[0].Body, "data")
}

func TestHandleInteraction_ToggleCurrentStatusMessageStillDefersAndCreatesStopConfirmation(t *testing.T) {
	session, api := newDiscordAPITestSession(t)
	addGuildRole(t, session, testGuildID, "role-id", "마크봇")

	updater := &testStatusEmbedUpdater{
		checkCurrent:     true,
		currentChannelID: "target-channel",
		currentMessageID: "current-message-id",
	}
	handler := NewHandler(&config.Config{
		McbotRoleName:      "마크봇",
		TrustedGuildID:     testGuildID,
		EmbedUpdateTimeout: time.Second,
	}, &testServerController{
		presenceVal: mcserver.PresenceState{ServerState: state.StateRunning},
	}, updater, &testRCONExecutor{})
	interaction := newComponentInteraction(ComponentIDToggle, testGuildID, []string{"role-id"})
	interaction.Message = &discordgo.Message{ID: "current-message-id", ChannelID: "target-channel", GuildID: testGuildID}

	handler.HandleInteraction(session, interaction)

	var requests []recordedDiscordRequest
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		requests = api.recordedRequests()
		if len(requests) >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(requests) < 2 {
		t.Fatalf("expected deferred response and stop confirmation followup, got %d requests", len(requests))
	}

	deferred := decodeInteractionResponse(t, requests[0].Body)
	if deferred.Type != discordgo.InteractionResponseDeferredMessageUpdate {
		t.Fatalf("expected deferred message update, got %v", deferred.Type)
	}

	var followupPayload map[string]json.RawMessage
	if err := json.Unmarshal(requests[1].Body, &followupPayload); err != nil {
		t.Fatalf("failed to decode followup payload: %v", err)
	}

	var followupContent string
	if err := json.Unmarshal(followupPayload["content"], &followupContent); err != nil {
		t.Fatalf("failed to decode followup content: %v", err)
	}
	if !strings.Contains(followupContent, "정말 서버를 닫을까요?") {
		t.Fatalf("unexpected followup content: %q", followupContent)
	}

	if _, ok := handler.stopConfirmationStore.Get(interaction.ID); !ok {
		t.Fatal("expected current status toggle to create stop confirmation")
	}
}

func TestHandleInteraction_StopConfirmWrongGuildRespondsEphemeral(t *testing.T) {
	session, api := newDiscordAPITestSession(t)

	handler := NewHandler(&config.Config{TrustedGuildID: testGuildID}, &testServerController{}, &testStatusEmbedUpdater{}, &testRCONExecutor{})
	interaction := newComponentInteraction(ComponentIDConfirmStopPrefix+"confirmation-id", testOtherGuildID, nil)

	handler.HandleInteraction(session, interaction)

	requests := api.recordedRequests()
	if len(requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(requests))
	}

	response := decodeInteractionResponse(t, requests[0].Body)
	if response.Data == nil || !strings.Contains(response.Data.Content, "이 서버에서는 사용할 수 없는 명령어") {
		t.Fatalf("unexpected response content: %+v", response.Data)
	}
	assertAllowedMentionsParseEmpty(t, requests[0].Body, "data")
}

func TestHandleInteraction_StopCancelWrongGuildRespondsEphemeral(t *testing.T) {
	session, api := newDiscordAPITestSession(t)

	handler := NewHandler(&config.Config{TrustedGuildID: testGuildID}, &testServerController{}, &testStatusEmbedUpdater{}, &testRCONExecutor{})
	interaction := newComponentInteraction(ComponentIDCancelStopPrefix+"confirmation-id", testOtherGuildID, nil)

	handler.HandleInteraction(session, interaction)

	requests := api.recordedRequests()
	if len(requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(requests))
	}

	response := decodeInteractionResponse(t, requests[0].Body)
	if response.Data == nil || !strings.Contains(response.Data.Content, "이 서버에서는 사용할 수 없는 명령어") {
		t.Fatalf("unexpected response content: %+v", response.Data)
	}
	assertAllowedMentionsParseEmpty(t, requests[0].Body, "data")
}

func TestHandleInteraction_StopConfirmRechecksRoleBeforeStop(t *testing.T) {
	session, api := newDiscordAPITestSession(t)
	addGuildRole(t, session, testGuildID, "role-id", "마크봇")

	stopStarted := make(chan struct{})
	controller := &testServerController{
		stopCh:      make(chan mcserver.StopResult),
		stopStarted: stopStarted,
	}
	handler := NewHandler(&config.Config{
		McbotRoleName:  "마크봇",
		TrustedGuildID: testGuildID,
	}, controller, &testStatusEmbedUpdater{}, &testRCONExecutor{})
	handler.stopConfirmationStore.Save(StopConfirmationContext{
		ConfirmationID: "confirmation-id",
		UserID:         "user-id",
		Interaction: &discordgo.Interaction{
			AppID: "123456789012345678",
			Token: "original-interaction-token",
		},
		MessageID: "followup-message-id",
	})

	interaction := newComponentInteraction(ComponentIDConfirmStopPrefix+"confirmation-id", testGuildID, nil)

	handler.HandleInteraction(session, interaction)

	select {
	case <-stopStarted:
		t.Fatal("stop started even though confirmer no longer has the required role")
	default:
	}

	requests := api.recordedRequests()
	if len(requests) != 1 {
		t.Fatalf("expected 1 permission-denied response, got %d", len(requests))
	}

	response := decodeInteractionResponse(t, requests[0].Body)
	if response.Type != discordgo.InteractionResponseChannelMessageWithSource {
		t.Fatalf("expected immediate response, got %v", response.Type)
	}
	if response.Data == nil || len(response.Data.Embeds) != 1 || !strings.Contains(response.Data.Embeds[0].Description, "역할이 필요합니다") {
		t.Fatalf("unexpected response data: %+v", response.Data)
	}
	assertAllowedMentionsParseEmpty(t, requests[0].Body, "data")

	if _, ok := handler.stopConfirmationStore.Get("confirmation-id"); ok {
		t.Fatal("confirmation was not deleted after role revalidation failed")
	}
}

func TestHandleInteraction_ToggleRunningStoresAndExpiresStopConfirmation(t *testing.T) {
	session, api := newDiscordAPITestSession(t)
	addGuildRole(t, session, testGuildID, "role-id", "마크봇")

	handler := NewHandler(&config.Config{
		McbotRoleName:  "마크봇",
		TrustedGuildID: testGuildID,
	}, &testServerController{
		presenceVal: mcserver.PresenceState{ServerState: state.StateRunning},
	}, &testStatusEmbedUpdater{}, &testRCONExecutor{})
	handler.stopConfirmationStore.ttl = 20 * time.Millisecond

	interaction := newComponentInteraction(ComponentIDToggle, testGuildID, []string{"role-id"})

	handler.HandleInteraction(session, interaction)

	var requests []recordedDiscordRequest
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		requests = api.recordedRequests()
		if len(requests) >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(requests) < 2 {
		t.Fatalf("expected deferred response and followup, got %d requests", len(requests))
	}

	deferred := decodeInteractionResponse(t, requests[0].Body)
	if deferred.Type != discordgo.InteractionResponseDeferredMessageUpdate {
		t.Fatalf("expected deferred message update, got %v", deferred.Type)
	}

	var followupPayload map[string]json.RawMessage
	if err := json.Unmarshal(requests[1].Body, &followupPayload); err != nil {
		t.Fatalf("failed to decode followup payload: %v", err)
	}

	var followupContent string
	if err := json.Unmarshal(followupPayload["content"], &followupContent); err != nil {
		t.Fatalf("failed to decode followup content: %v", err)
	}
	if !strings.Contains(followupContent, "정말 서버를 닫을까요?") {
		t.Fatalf("unexpected followup content: %q", followupContent)
	}

	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		handler.stopConfirmationStore.mu.RLock()
		_, ok := handler.stopConfirmationStore.m[interaction.ID]
		handler.stopConfirmationStore.mu.RUnlock()
		if !ok {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}

	handler.stopConfirmationStore.mu.RLock()
	defer handler.stopConfirmationStore.mu.RUnlock()
	if _, ok := handler.stopConfirmationStore.m[interaction.ID]; ok {
		t.Fatal("stop confirmation was not automatically expired from the store")
	}
}

func TestHandleInteraction_StopConfirmExpiredRespondsExpiredAndDoesNotStop(t *testing.T) {
	session, api := newDiscordAPITestSession(t)
	addGuildRole(t, session, testGuildID, "role-id", "마크봇")

	stopStarted := make(chan struct{})
	controller := &testServerController{
		stopCh:      make(chan mcserver.StopResult),
		stopStarted: stopStarted,
	}
	handler := NewHandler(&config.Config{
		McbotRoleName:  "마크봇",
		TrustedGuildID: testGuildID,
	}, controller, &testStatusEmbedUpdater{}, &testRCONExecutor{})
	handler.stopConfirmationStore.ttl = 20 * time.Millisecond
	handler.stopConfirmationStore.Save(StopConfirmationContext{
		ConfirmationID: "confirmation-id",
		UserID:         "user-id",
		Interaction: &discordgo.Interaction{
			AppID: "123456789012345678",
			Token: "original-interaction-token",
		},
		MessageID: "followup-message-id",
	})

	time.Sleep(50 * time.Millisecond)

	interaction := newComponentInteraction(ComponentIDConfirmStopPrefix+"confirmation-id", testGuildID, []string{"role-id"})

	handler.HandleInteraction(session, interaction)

	select {
	case <-stopStarted:
		t.Fatal("stop started for an expired confirmation")
	default:
	}

	requests := api.recordedRequests()
	if len(requests) != 1 {
		t.Fatalf("expected 1 expired-response request, got %d", len(requests))
	}

	response := decodeInteractionResponse(t, requests[0].Body)
	if response.Type != discordgo.InteractionResponseChannelMessageWithSource {
		t.Fatalf("expected immediate expired response, got %v", response.Type)
	}
	if response.Data == nil || !strings.Contains(response.Data.Content, "이미 처리되었거나 만료되었습니다") {
		t.Fatalf("unexpected response data: %+v", response.Data)
	}
}

func TestHandleInteraction_RCONNoGuildRespondsEphemeral(t *testing.T) {
	session, api := newDiscordAPITestSession(t)
	addGuildRole(t, session, testGuildID, "role-id", "마크봇")

	handler := NewHandler(&config.Config{
		McbotRoleName:  "마크봇",
		TrustedGuildID: testGuildID,
	}, &testServerController{}, &testStatusEmbedUpdater{}, &testRCONExecutor{})
	interaction := newRCONInteraction("list", []string{"role-id"})
	interaction.GuildID = ""

	handler.HandleInteraction(session, interaction)

	requests := api.recordedRequests()
	if len(requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(requests))
	}

	response := decodeInteractionResponse(t, requests[0].Body)
	if response.Type != discordgo.InteractionResponseChannelMessageWithSource {
		t.Fatalf("expected immediate response, got %v", response.Type)
	}
	if response.Data == nil || !strings.Contains(response.Data.Content, "이 서버에서는 사용할 수 없는 명령어") {
		t.Fatalf("unexpected response content: %+v", response.Data)
	}
	assertAllowedMentionsParseEmpty(t, requests[0].Body, "data")
}

func TestHandleInteraction_RCONRequiresRunningServer(t *testing.T) {
	session, api := newDiscordAPITestSession(t)
	addGuildRole(t, session, testGuildID, "role-id", "마크봇")

	handler := NewHandler(&config.Config{McbotRoleName: "마크봇", TrustedGuildID: testGuildID, EmbedUpdateTimeout: time.Second}, &testServerController{
		presenceVal: mcserver.PresenceState{ServerState: state.StateStopped},
	}, &testStatusEmbedUpdater{}, &testRCONExecutor{})
	interaction := newRCONInteraction("list", []string{"role-id"})

	handler.HandleInteraction(session, interaction)

	requests := api.recordedRequests()
	if len(requests) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(requests))
	}

	deferred := decodeInteractionResponse(t, requests[0].Body)
	if deferred.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource {
		t.Fatalf("expected deferred response, got %v", deferred.Type)
	}
	assertAllowedMentionsParseEmpty(t, requests[0].Body, "data")

	followup := decodeWebhookParams(t, requests[1].Body)
	if !strings.Contains(followup.Content, "서버가 실행 중이 아닙니다") {
		t.Fatalf("unexpected followup content: %q", followup.Content)
	}
	assertAllowedMentionsParseEmpty(t, requests[1].Body)
}

func TestHandleInteraction_RCONDefersBeforePresenceCheck(t *testing.T) {
	session, api := newDiscordAPITestSession(t)
	addGuildRole(t, session, testGuildID, "role-id", "마크봇")

	presenceStarted := make(chan struct{})
	presenceBlock := make(chan struct{})
	controller := &testServerController{
		presenceVal:     mcserver.PresenceState{ServerState: state.StateRunning},
		presenceStarted: presenceStarted,
		presenceBlock:   presenceBlock,
	}
	executor := &testRCONExecutor{response: "There are 0 of a max of 20 players online"}
	handler := NewHandler(&config.Config{
		McbotRoleName:      "마크봇",
		TrustedGuildID:     testGuildID,
		EmbedUpdateTimeout: time.Second,
		RCONTimeout:        time.Second,
	}, controller, &testStatusEmbedUpdater{}, executor)
	interaction := newRCONInteraction("list", []string{"role-id"})

	handlerDone := make(chan struct{})
	go func() {
		defer close(handlerDone)
		handler.HandleInteraction(session, interaction)
	}()

	select {
	case <-presenceStarted:
	case <-time.After(time.Second):
		t.Fatal("Presence was not called")
	}

	requests := api.recordedRequests()
	if len(requests) != 1 {
		close(presenceBlock)
		<-handlerDone
		t.Fatalf("expected deferred response before presence completes, got %d requests", len(requests))
	}

	deferred := decodeInteractionResponse(t, requests[0].Body)
	if deferred.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource {
		close(presenceBlock)
		<-handlerDone
		t.Fatalf("expected deferred response, got %v", deferred.Type)
	}
	assertAllowedMentionsParseEmpty(t, requests[0].Body, "data")

	close(presenceBlock)

	select {
	case <-handlerDone:
	case <-time.After(time.Second):
		t.Fatal("handler did not finish after unblocking presence")
	}

	requests = api.recordedRequests()
	if len(requests) != 2 {
		t.Fatalf("expected 2 requests after handler completes, got %d", len(requests))
	}

	followup := decodeWebhookParams(t, requests[1].Body)
	if !strings.Contains(followup.Content, "명령 실행 완료") {
		t.Fatalf("unexpected followup content: %q", followup.Content)
	}
	assertAllowedMentionsParseEmpty(t, requests[1].Body)
}

func TestHandleInteraction_RCONSuccessSanitizesAndTruncatesResponse(t *testing.T) {
	session, api := newDiscordAPITestSession(t)
	addGuildRole(t, session, testGuildID, "role-id", "마크봇")

	executor := &testRCONExecutor{response: "```@everyone" + strings.Repeat("a", 1805)}
	handler := NewHandler(&config.Config{
		McbotRoleName:      "마크봇",
		TrustedGuildID:     testGuildID,
		EmbedUpdateTimeout: time.Second,
		RCONTimeout:        time.Second,
	}, &testServerController{
		presenceVal: mcserver.PresenceState{ServerState: state.StateRunning},
	}, &testStatusEmbedUpdater{}, executor)

	interaction := newRCONInteraction("list", []string{"role-id"})
	handler.HandleInteraction(session, interaction)

	requests := api.recordedRequests()
	if len(requests) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(requests))
	}

	deferred := decodeInteractionResponse(t, requests[0].Body)
	if deferred.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource {
		t.Fatalf("expected deferred response, got %v", deferred.Type)
	}

	followup := decodeWebhookParams(t, requests[1].Body)
	if executor.lastCommand != "list" {
		t.Fatalf("expected command to be forwarded, got %q", executor.lastCommand)
	}
	assertAllowedMentionsParseEmpty(t, requests[1].Body)
	if !strings.Contains(followup.Content, "✅ 명령 실행 완료") {
		t.Fatalf("unexpected followup content: %q", followup.Content)
	}
	if !strings.Contains(followup.Content, "``\u200b`") {
		t.Fatalf("expected response fences to be neutralized, got %q", followup.Content)
	}
	if !strings.Contains(followup.Content, "응답이 너무 길어 잘렸습니다") {
		t.Fatalf("expected truncation notice, got %q", followup.Content)
	}
}

func TestHandleInteraction_RCONExecutionFailureUsesEscapedFollowup(t *testing.T) {
	session, api := newDiscordAPITestSession(t)
	addGuildRole(t, session, testGuildID, "role-id", "마크봇")

	executor := &testRCONExecutor{err: errors.New("```@everyone```")}
	handler := NewHandler(&config.Config{
		McbotRoleName:      "마크봇",
		TrustedGuildID:     testGuildID,
		EmbedUpdateTimeout: time.Second,
		RCONTimeout:        time.Second,
	}, &testServerController{
		presenceVal: mcserver.PresenceState{ServerState: state.StateRunning},
	}, &testStatusEmbedUpdater{}, executor)
	interaction := newRCONInteraction("list", []string{"role-id"})

	handler.HandleInteraction(session, interaction)

	requests := api.recordedRequests()
	if len(requests) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(requests))
	}

	deferred := decodeInteractionResponse(t, requests[0].Body)
	if deferred.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource {
		t.Fatalf("expected deferred response, got %v", deferred.Type)
	}

	followup := decodeWebhookParams(t, requests[1].Body)
	assertAllowedMentionsParseEmpty(t, requests[1].Body)
	if !strings.Contains(followup.Content, "RCON 실행 실패:") {
		t.Fatalf("unexpected followup content: %q", followup.Content)
	}
	if strings.Contains(followup.Content, "```@everyone```") {
		t.Fatalf("expected raw markdown and mention to be escaped, got %q", followup.Content)
	}
	if !strings.Contains(followup.Content, "\\`\\`\\`") {
		t.Fatalf("expected code fences to be escaped, got %q", followup.Content)
	}
	if !strings.Contains(followup.Content, "@\u200beveryone") {
		t.Fatalf("unexpected followup content: %q", followup.Content)
	}
}

func TestHandleInteraction_RCONLogsDoNotContainRawCommand(t *testing.T) {
	session, api := newDiscordAPITestSession(t)
	addGuildRole(t, session, testGuildID, "role-id", "마크봇")

	command := "whitelist add SensitivePlayer\nforged-log-line"
	executor := &testRCONExecutor{}
	handler := NewHandler(&config.Config{
		McbotRoleName:      "마크봇",
		TrustedGuildID:     testGuildID,
		EmbedUpdateTimeout: time.Second,
		RCONTimeout:        time.Second,
	}, &testServerController{
		presenceVal: mcserver.PresenceState{ServerState: state.StateRunning},
	}, &testStatusEmbedUpdater{}, executor)
	interaction := newRCONInteraction(command, []string{"role-id"})

	originalWriter := log.Writer()
	originalFlags := log.Flags()
	var logBuf bytes.Buffer
	log.SetFlags(0)
	log.SetOutput(&logBuf)
	t.Cleanup(func() {
		log.SetOutput(originalWriter)
		log.SetFlags(originalFlags)
	})

	handler.HandleInteraction(session, interaction)

	requests := api.recordedRequests()
	if len(requests) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(requests))
	}

	followup := decodeWebhookParams(t, requests[1].Body)
	if followup.Content != "✅ 명령 실행 완료" {
		t.Fatalf("unexpected followup content: %q", followup.Content)
	}
	if executor.lastCommand != command {
		t.Fatalf("expected command to be forwarded, got %q", executor.lastCommand)
	}

	logs := logBuf.String()
	if !strings.Contains(logs, "[RCON] 명령 실행") {
		t.Fatalf("expected execution log, got %q", logs)
	}
	if !strings.Contains(logs, "[RCON] 실행 성공") {
		t.Fatalf("expected success log, got %q", logs)
	}
	if strings.Contains(logs, command) {
		t.Fatalf("expected logs to omit raw command, got %q", logs)
	}
	assertAllowedMentionsParseEmpty(t, requests[1].Body)
}

func TestHandleInteraction_RCONFailureLogsDoNotContainRawCommand(t *testing.T) {
	session, api := newDiscordAPITestSession(t)
	addGuildRole(t, session, testGuildID, "role-id", "마크봇")

	command := "op SensitivePlayer\nforged-log-line"
	executor := &testRCONExecutor{err: errors.New("boom")}
	handler := NewHandler(&config.Config{
		McbotRoleName:      "마크봇",
		TrustedGuildID:     testGuildID,
		EmbedUpdateTimeout: time.Second,
		RCONTimeout:        time.Second,
	}, &testServerController{
		presenceVal: mcserver.PresenceState{ServerState: state.StateRunning},
	}, &testStatusEmbedUpdater{}, executor)
	interaction := newRCONInteraction(command, []string{"role-id"})

	originalWriter := log.Writer()
	originalFlags := log.Flags()
	var logBuf bytes.Buffer
	log.SetFlags(0)
	log.SetOutput(&logBuf)
	t.Cleanup(func() {
		log.SetOutput(originalWriter)
		log.SetFlags(originalFlags)
	})

	handler.HandleInteraction(session, interaction)

	requests := api.recordedRequests()
	if len(requests) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(requests))
	}

	followup := decodeWebhookParams(t, requests[1].Body)
	if !strings.Contains(followup.Content, "RCON 실행 실패:") {
		t.Fatalf("unexpected followup content: %q", followup.Content)
	}
	if executor.lastCommand != command {
		t.Fatalf("expected command to be forwarded, got %q", executor.lastCommand)
	}

	logs := logBuf.String()
	if !strings.Contains(logs, "[RCON] 명령 실행") {
		t.Fatalf("expected execution log, got %q", logs)
	}
	if !strings.Contains(logs, "[RCON] 실행 실패") {
		t.Fatalf("expected failure log, got %q", logs)
	}
	if strings.Contains(logs, command) {
		t.Fatalf("expected logs to omit raw command, got %q", logs)
	}
	assertAllowedMentionsParseEmpty(t, requests[1].Body)
}

func TestHandleInteraction_RCONSuccessWithEmptyResponseUsesPlainSuccessMessage(t *testing.T) {
	session, api := newDiscordAPITestSession(t)
	addGuildRole(t, session, testGuildID, "role-id", "마크봇")

	executor := &testRCONExecutor{}
	handler := NewHandler(&config.Config{
		McbotRoleName:      "마크봇",
		TrustedGuildID:     testGuildID,
		EmbedUpdateTimeout: time.Second,
		RCONTimeout:        time.Second,
	}, &testServerController{
		presenceVal: mcserver.PresenceState{ServerState: state.StateRunning},
	}, &testStatusEmbedUpdater{}, executor)
	interaction := newRCONInteraction("list", []string{"role-id"})

	handler.HandleInteraction(session, interaction)

	requests := api.recordedRequests()
	if len(requests) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(requests))
	}

	deferred := decodeInteractionResponse(t, requests[0].Body)
	if deferred.Type != discordgo.InteractionResponseDeferredChannelMessageWithSource {
		t.Fatalf("expected deferred response, got %v", deferred.Type)
	}
	assertAllowedMentionsParseEmpty(t, requests[0].Body, "data")

	followup := decodeWebhookParams(t, requests[1].Body)
	if followup.Content != "✅ 명령 실행 완료" {
		t.Fatalf("unexpected followup content: %q", followup.Content)
	}
	assertAllowedMentionsParseEmpty(t, requests[1].Body)
}

func TestHandleButtonStart_GoroutineUpdateUsesIndependentContext(t *testing.T) {
	cfg := &config.Config{
		EmbedUpdateTimeout:     5 * time.Second,
		ServerOperationTimeout: 10 * time.Second,
	}

	controller := &testServerController{
		startCh: make(chan mcserver.StartResult, 1),
		presenceVal: mcserver.PresenceState{
			ServerState: state.StateStopped,
		},
	}

	updater := &testStatusEmbedUpdater{}

	handler := NewHandler(cfg, controller, updater, &testRCONExecutor{})

	mockInteraction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:    "test-interaction-id",
			Token: "test-token",
			Member: &discordgo.Member{
				User: &discordgo.User{
					Username: "testuser",
					ID:       "test-user-id",
				},
			},
		},
	}

	goroutineDone := make(chan bool, 1)

	handler.handleButtonStart(nil, mockInteraction)

	time.Sleep(10 * time.Millisecond)

	controller.startCh <- mcserver.StartResult{
		Success:       true,
		ReadyDuration: 10 * time.Second,
	}

	go func() {
		for {
			if len(updater.getUpdateCalls()) >= 2 {
				goroutineDone <- true
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	select {
	case <-goroutineDone:
	case <-time.After(1 * time.Second):
		t.Fatal("Goroutine did not complete second update within timeout")
	}

	calls := updater.getUpdateCalls()
	if len(calls) != 2 {
		t.Fatalf("Expected 2 Update calls total, got %d", len(calls))
	}

	secondCall := calls[1]

	if secondCall.isCancelled {
		t.Error("Second Update context should not be cancelled")
	}

	if !secondCall.hasDeadline {
		t.Error("Second Update context should have a deadline (timeout)")
	}

	if secondCall.deadlineDuration > cfg.EmbedUpdateTimeout {
		t.Errorf("Second Update context deadline is too far: %v (expected <= %v)",
			secondCall.deadlineDuration, cfg.EmbedUpdateTimeout)
	}
}

func TestHandleButtonStop_FirstUpdateUsesEmbedTimeout(t *testing.T) {
	cfg := &config.Config{
		EmbedUpdateTimeout:     5 * time.Second,
		ServerOperationTimeout: 10 * time.Second,
	}

	controller := &testServerController{
		stopCh: make(chan mcserver.StopResult, 1),
		presenceVal: mcserver.PresenceState{
			ServerState: state.StateRunning,
		},
	}

	updater := &testStatusEmbedUpdater{}

	handler := NewHandler(cfg, controller, updater, &testRCONExecutor{})

	mockInteraction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			Member: &discordgo.Member{
				User: &discordgo.User{
					Username: "testuser",
				},
			},
		},
	}

	handler.handleButtonStop(nil, mockInteraction)

	calls := updater.getUpdateCalls()
	if len(calls) != 1 {
		t.Fatalf("Expected 1 Update call immediately, got %d", len(calls))
	}

	firstCall := calls[0]
	if !firstCall.hasDeadline {
		t.Error("First Update call should have a deadline (EmbedUpdateTimeout)")
	}

	if firstCall.isCancelled {
		t.Error("First Update context should not be cancelled")
	}
}

func TestHandleButtonStop_GoroutineUpdateUsesIndependentContext(t *testing.T) {
	cfg := &config.Config{
		EmbedUpdateTimeout:     5 * time.Second,
		ServerOperationTimeout: 10 * time.Second,
	}

	controller := &testServerController{
		stopCh: make(chan mcserver.StopResult, 1),
		presenceVal: mcserver.PresenceState{
			ServerState: state.StateRunning,
		},
	}

	updater := &testStatusEmbedUpdater{}

	handler := NewHandler(cfg, controller, updater, &testRCONExecutor{})

	mockInteraction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:    "test-interaction-id",
			Token: "test-token",
			Member: &discordgo.Member{
				User: &discordgo.User{
					Username: "testuser",
					ID:       "test-user-id",
				},
			},
		},
	}

	goroutineDone := make(chan bool, 1)

	handler.handleButtonStop(nil, mockInteraction)

	time.Sleep(10 * time.Millisecond)

	controller.stopCh <- mcserver.StopResult{
		Success: true,
	}

	go func() {
		for {
			if len(updater.getUpdateCalls()) >= 2 {
				goroutineDone <- true
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	select {
	case <-goroutineDone:
	case <-time.After(1 * time.Second):
		t.Fatal("Goroutine did not complete second update within timeout")
	}

	calls := updater.getUpdateCalls()
	if len(calls) != 2 {
		t.Fatalf("Expected 2 Update calls total, got %d", len(calls))
	}

	secondCall := calls[1]

	if secondCall.isCancelled {
		t.Error("Second Update context should not be cancelled")
	}

	if !secondCall.hasDeadline {
		t.Error("Second Update context should have a deadline (timeout)")
	}

	if secondCall.deadlineDuration > cfg.EmbedUpdateTimeout {
		t.Errorf("Second Update context deadline is too far: %v (expected <= %v)",
			secondCall.deadlineDuration, cfg.EmbedUpdateTimeout)
	}
}

func TestHandleButtonStart_GoroutineContextEventuallyTimesOut(t *testing.T) {
	cfg := &config.Config{
		EmbedUpdateTimeout:     50 * time.Millisecond,
		ServerOperationTimeout: 10 * time.Second,
	}

	controller := &testServerController{
		startCh: make(chan mcserver.StartResult, 1),
		presenceVal: mcserver.PresenceState{
			ServerState: state.StateStopped,
		},
	}

	updater := &testStatusEmbedUpdater{}

	handler := NewHandler(cfg, controller, updater, &testRCONExecutor{})

	mockInteraction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:    "test-interaction-id",
			Token: "test-token",
			Member: &discordgo.Member{
				User: &discordgo.User{
					Username: "testuser",
					ID:       "test-user-id",
				},
			},
		},
	}

	goroutineDone := make(chan bool, 1)

	handler.handleButtonStart(nil, mockInteraction)

	time.Sleep(10 * time.Millisecond)

	controller.startCh <- mcserver.StartResult{
		Success:       true,
		ReadyDuration: 10 * time.Second,
	}

	go func() {
		for {
			if len(updater.getUpdateCalls()) >= 2 {
				goroutineDone <- true
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	select {
	case <-goroutineDone:
	case <-time.After(1 * time.Second):
		t.Fatal("Goroutine did not complete within timeout")
	}

	calls := updater.getUpdateCalls()
	if len(calls) != 2 {
		t.Fatalf("Expected 2 Update calls, got %d", len(calls))
	}

	goroutineCtx := calls[1].ctx

	time.Sleep(100 * time.Millisecond)

	select {
	case <-goroutineCtx.Done():
	default:
		t.Error("Goroutine context should be cancelled after timeout")
	}

	if goroutineCtx.Err() == nil {
		t.Error("Goroutine context should have an error after timeout")
	}
}

func TestHandleButtonStart_TimeoutWhenChannelNeverSends(t *testing.T) {
	cfg := &config.Config{
		EmbedUpdateTimeout:     5 * time.Second,
		ServerOperationTimeout: 100 * time.Millisecond,
	}

	controller := &testServerController{
		startCh: make(chan mcserver.StartResult),
		presenceVal: mcserver.PresenceState{
			ServerState: state.StateStopped,
		},
	}

	updater := &testStatusEmbedUpdater{}
	handler := NewHandler(cfg, controller, updater, &testRCONExecutor{})

	mockInteraction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:    "test-interaction-id",
			Token: "test-token",
			Member: &discordgo.Member{
				User: &discordgo.User{
					Username: "testuser",
					ID:       "test-user-id",
				},
			},
		},
	}

	goroutineDone := make(chan bool, 1)

	handler.handleButtonStart(nil, mockInteraction)

	go func() {
		for {
			if len(updater.getUpdateCalls()) >= 2 {
				goroutineDone <- true
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	select {
	case <-goroutineDone:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("handleButtonStart goroutine did not complete within timeout")
	}

	calls := updater.getUpdateCalls()
	if len(calls) != 2 {
		t.Fatalf("Expected 2 Update calls (initial + after timeout), got %d", len(calls))
	}
}

func TestHandleButtonStop_TimeoutWhenChannelNeverSends(t *testing.T) {
	cfg := &config.Config{
		EmbedUpdateTimeout:     5 * time.Second,
		ServerOperationTimeout: 100 * time.Millisecond,
	}

	controller := &testServerController{
		stopCh: make(chan mcserver.StopResult),
		presenceVal: mcserver.PresenceState{
			ServerState: state.StateRunning,
		},
	}

	updater := &testStatusEmbedUpdater{}
	handler := NewHandler(cfg, controller, updater, &testRCONExecutor{})

	mockInteraction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:    "test-interaction-id",
			Token: "test-token",
			Member: &discordgo.Member{
				User: &discordgo.User{
					Username: "testuser",
					ID:       "test-user-id",
				},
			},
		},
	}

	goroutineDone := make(chan bool, 1)

	handler.handleButtonStop(nil, mockInteraction)

	go func() {
		for {
			if len(updater.getUpdateCalls()) >= 2 {
				goroutineDone <- true
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	select {
	case <-goroutineDone:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("handleButtonStop goroutine did not complete within timeout")
	}

	calls := updater.getUpdateCalls()
	if len(calls) != 2 {
		t.Fatalf("Expected 2 Update calls (initial + after timeout), got %d", len(calls))
	}
}

func TestHandleButtonStart_ChannelClosedWithoutValue(t *testing.T) {
	cfg := &config.Config{
		EmbedUpdateTimeout:     5 * time.Second,
		ServerOperationTimeout: 10 * time.Second,
	}

	controller := &testServerController{
		startCh: make(chan mcserver.StartResult),
		presenceVal: mcserver.PresenceState{
			ServerState: state.StateStopped,
		},
	}

	updater := &testStatusEmbedUpdater{}
	handler := NewHandler(cfg, controller, updater, &testRCONExecutor{})

	mockInteraction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:    "test-interaction-id",
			Token: "test-token",
			Member: &discordgo.Member{
				User: &discordgo.User{
					Username: "testuser",
					ID:       "test-user-id",
				},
			},
		},
	}

	goroutineDone := make(chan bool, 1)

	handler.handleButtonStart(nil, mockInteraction)

	time.Sleep(10 * time.Millisecond)
	close(controller.startCh)

	go func() {
		for {
			if len(updater.getUpdateCalls()) >= 2 {
				goroutineDone <- true
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	select {
	case <-goroutineDone:
	case <-time.After(1 * time.Second):
		t.Fatal("handleButtonStart goroutine did not complete after channel close")
	}

	calls := updater.getUpdateCalls()
	if len(calls) != 2 {
		t.Fatalf("Expected 2 Update calls, got %d", len(calls))
	}
}

func TestHandleButtonStop_ChannelClosedWithoutValue(t *testing.T) {
	cfg := &config.Config{
		EmbedUpdateTimeout:     5 * time.Second,
		ServerOperationTimeout: 10 * time.Second,
	}

	controller := &testServerController{
		stopCh: make(chan mcserver.StopResult),
		presenceVal: mcserver.PresenceState{
			ServerState: state.StateRunning,
		},
	}

	updater := &testStatusEmbedUpdater{}
	handler := NewHandler(cfg, controller, updater, &testRCONExecutor{})

	mockInteraction := &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			ID:    "test-interaction-id",
			Token: "test-token",
			Member: &discordgo.Member{
				User: &discordgo.User{
					Username: "testuser",
					ID:       "test-user-id",
				},
			},
		},
	}

	goroutineDone := make(chan bool, 1)

	handler.handleButtonStop(nil, mockInteraction)

	time.Sleep(10 * time.Millisecond)
	close(controller.stopCh)

	go func() {
		for {
			if len(updater.getUpdateCalls()) >= 2 {
				goroutineDone <- true
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	select {
	case <-goroutineDone:
	case <-time.After(1 * time.Second):
		t.Fatal("handleButtonStop goroutine did not complete after channel close")
	}

	calls := updater.getUpdateCalls()
	if len(calls) != 2 {
		t.Fatalf("Expected 2 Update calls, got %d", len(calls))
	}
}
