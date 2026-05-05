package main

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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/snowy/mcbot/internal/config"
	"github.com/snowy/mcbot/internal/mcserver"
	"github.com/snowy/mcbot/internal/state"
)

type recordedDiscordRequest struct {
	Method string
	Path   string
	Body   []byte
}

type discordAPITestServer struct {
	server             *httptest.Server
	mu                 sync.Mutex
	requests           []recordedDiscordRequest
	handlerErrors      []error
	oauthApplicationID string
}

var discordEndpointOverrideMu sync.Mutex

type discordAPITestServerOptions struct {
	oauthApplicationID string
}

const testGuildID = "123456789012345679"

func TestBootstrapEmbedChannelPrecedence(t *testing.T) {
	runtimeStorePath := filepath.Join(t.TempDir(), "runtime-config.json")
	overrideBootstrapRuntimeStore(t, runtimeStorePath)

	t.Run("runtime override wins over env", func(t *testing.T) {
		runtimeID := "123456789012345678"
		envID := "223456789012345678"
		if err := config.NewRuntimeEmbedChannelStore(runtimeStorePath).Save(context.Background(), testGuildID, runtimeID); err != nil {
			t.Fatalf("Save() error = %v", err)
		}

		cfg := &config.Config{TrustedGuildID: testGuildID, EmbedChannelID: envID}
		logs := captureLogs(t, func() {
			if got := resolveStartupEmbedChannelID(context.Background(), cfg, newRuntimeEmbedChannelStore()); got != runtimeID {
				t.Fatalf("resolved embed channel = %q, want %q", got, runtimeID)
			}
		})
		if !strings.Contains(logs, "source=runtime override") {
			t.Fatalf("expected runtime override log, got %q", logs)
		}
		if strings.Contains(logs, "source=env fallback") {
			t.Fatalf("unexpected env fallback log: %q", logs)
		}
	})

	t.Run("runtime disabled wins over env", func(t *testing.T) {
		if err := config.NewRuntimeEmbedChannelStore(runtimeStorePath).SaveDisabled(context.Background(), testGuildID); err != nil {
			t.Fatalf("SaveDisabled() error = %v", err)
		}

		cfg := &config.Config{TrustedGuildID: testGuildID, EmbedChannelID: "223456789012345678"}
		logs := captureLogs(t, func() {
			if got := resolveStartupEmbedChannelID(context.Background(), cfg, newRuntimeEmbedChannelStore()); got != "" {
				t.Fatalf("resolved embed channel = %q, want empty disabled channel", got)
			}
		})
		if !strings.Contains(logs, "source=runtime disabled") {
			t.Fatalf("expected runtime disabled log, got %q", logs)
		}
		if strings.Contains(logs, "source=env fallback") {
			t.Fatalf("unexpected env fallback log: %q", logs)
		}
	})

	t.Run("missing runtime file falls back to env", func(t *testing.T) {
		if err := os.Remove(runtimeStorePath); err != nil && !os.IsNotExist(err) {
			t.Fatalf("Remove() error = %v", err)
		}
		cfg := &config.Config{TrustedGuildID: testGuildID, EmbedChannelID: "323456789012345678"}
		logs := captureLogs(t, func() {
			if got := resolveStartupEmbedChannelID(context.Background(), cfg, newRuntimeEmbedChannelStore()); got != cfg.EmbedChannelID {
				t.Fatalf("resolved embed channel = %q, want %q", got, cfg.EmbedChannelID)
			}
		})
		if !strings.Contains(logs, "source=env fallback") {
			t.Fatalf("expected env fallback log, got %q", logs)
		}
	})

	t.Run("missing runtime file and empty env stays unconfigured", func(t *testing.T) {
		if err := os.Remove(runtimeStorePath); err != nil && !os.IsNotExist(err) {
			t.Fatalf("Remove() error = %v", err)
		}
		cfg := &config.Config{TrustedGuildID: testGuildID}
		logs := captureLogs(t, func() {
			if got := resolveStartupEmbedChannelID(context.Background(), cfg, newRuntimeEmbedChannelStore()); got != "" {
				t.Fatalf("resolved embed channel = %q, want empty", got)
			}
		})
		if !strings.Contains(logs, "source=unconfigured") {
			t.Fatalf("expected unconfigured log, got %q", logs)
		}
	})
}

func TestBootstrapIgnoresMalformedRuntimeOverride(t *testing.T) {
	runtimeStorePath := filepath.Join(t.TempDir(), "runtime-config.json")
	overrideBootstrapRuntimeStore(t, runtimeStorePath)

	tests := []struct {
		name     string
		contents string
	}{
		{name: "invalid JSON", contents: "{not-json"},
		{name: "invalid snowflake", contents: `{"trusted_guild_id":"123456789012345679","embed_channel_id":"012345678901234567"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := os.WriteFile(runtimeStorePath, []byte(tt.contents), 0o644); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}

			cfg := &config.Config{TrustedGuildID: testGuildID, EmbedChannelID: "423456789012345678"}
			logs := captureLogs(t, func() {
				if got := resolveStartupEmbedChannelID(context.Background(), cfg, newRuntimeEmbedChannelStore()); got != cfg.EmbedChannelID {
					t.Fatalf("resolved embed channel = %q, want %q", got, cfg.EmbedChannelID)
				}
			})
			if !strings.Contains(logs, "[WARN] runtime embed channel override unavailable:") {
				t.Fatalf("expected warning log, got %q", logs)
			}
			if !strings.Contains(logs, "source=env fallback") {
				t.Fatalf("expected env fallback log, got %q", logs)
			}
		})
	}
}

func TestBootstrapClearsStaleSavedChannelFromDifferentTrustedGuild(t *testing.T) {
	runtimeStorePath := filepath.Join(t.TempDir(), "runtime-config.json")
	overrideBootstrapRuntimeStore(t, runtimeStorePath)

	runtimeID := "523456789012345678"
	oldGuildID := "923456789012345678"
	if err := config.NewRuntimeEmbedChannelStore(runtimeStorePath).Save(context.Background(), oldGuildID, runtimeID); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	cfg := &config.Config{TrustedGuildID: testGuildID, EmbedChannelID: "623456789012345678"}
	logs := captureLogs(t, func() {
		if got := resolveStartupEmbedChannelID(context.Background(), cfg, newRuntimeEmbedChannelStore()); got != cfg.EmbedChannelID {
			t.Fatalf("resolved embed channel = %q, want env fallback %q", got, cfg.EmbedChannelID)
		}
	})
	if !strings.Contains(logs, "stale runtime embed channel override cleared") {
		t.Fatalf("expected stale override clear log, got %q", logs)
	}
	if !strings.Contains(logs, "source=env fallback") {
		t.Fatalf("expected env fallback log, got %q", logs)
	}
	if _, err := os.Stat(runtimeStorePath); !os.IsNotExist(err) {
		t.Fatalf("runtime store still exists after stale clear, stat err = %v", err)
	}
}

func TestBootstrapClearsStaleDisabledSettingFromDifferentTrustedGuild(t *testing.T) {
	runtimeStorePath := filepath.Join(t.TempDir(), "runtime-config.json")
	overrideBootstrapRuntimeStore(t, runtimeStorePath)

	oldGuildID := "923456789012345678"
	if err := config.NewRuntimeEmbedChannelStore(runtimeStorePath).SaveDisabled(context.Background(), oldGuildID); err != nil {
		t.Fatalf("SaveDisabled() error = %v", err)
	}

	cfg := &config.Config{TrustedGuildID: testGuildID, EmbedChannelID: "623456789012345678"}
	logs := captureLogs(t, func() {
		if got := resolveStartupEmbedChannelID(context.Background(), cfg, newRuntimeEmbedChannelStore()); got != cfg.EmbedChannelID {
			t.Fatalf("resolved embed channel = %q, want env fallback %q", got, cfg.EmbedChannelID)
		}
	})
	if !strings.Contains(logs, "stale runtime embed channel override cleared") {
		t.Fatalf("expected stale override clear log, got %q", logs)
	}
	if !strings.Contains(logs, "source=env fallback") {
		t.Fatalf("expected env fallback log, got %q", logs)
	}
	if _, err := os.Stat(runtimeStorePath); !os.IsNotExist(err) {
		t.Fatalf("runtime store still exists after stale clear, stat err = %v", err)
	}
}

func TestBootstrapClearsLegacyUnscopedSavedChannel(t *testing.T) {
	runtimeStorePath := filepath.Join(t.TempDir(), "runtime-config.json")
	overrideBootstrapRuntimeStore(t, runtimeStorePath)

	if err := os.WriteFile(runtimeStorePath, []byte(`{"embed_channel_id":"523456789012345678"}`), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	cfg := &config.Config{TrustedGuildID: testGuildID}
	logs := captureLogs(t, func() {
		if got := resolveStartupEmbedChannelID(context.Background(), cfg, newRuntimeEmbedChannelStore()); got != "" {
			t.Fatalf("resolved embed channel = %q, want empty", got)
		}
	})
	if !strings.Contains(logs, "stale runtime embed channel override cleared") {
		t.Fatalf("expected stale override clear log, got %q", logs)
	}
	if !strings.Contains(logs, "source=unconfigured") {
		t.Fatalf("expected unconfigured log, got %q", logs)
	}
	if _, err := os.Stat(runtimeStorePath); !os.IsNotExist(err) {
		t.Fatalf("runtime store still exists after stale clear, stat err = %v", err)
	}
}

func TestNewDiscordHandler_WiresRuntimeConfiguratorWithResolvedChannel(t *testing.T) {
	runtimeStore := &testRuntimeEmbedChannelStore{
		loadChannelID: "111111111111111111",
		loadFound:     true,
		saveErrs:      []error{errors.New("persist failed")},
	}

	cfg := &config.Config{
		TrustedGuildID:         testGuildID,
		McbotRoleName:          "마크봇",
		ServerOperationTimeout: time.Second,
	}
	cfg.EmbedChannelID = resolveStartupEmbedChannelID(context.Background(), cfg, runtimeStore)
	if got, want := cfg.EmbedChannelID, runtimeStore.loadChannelID; got != want {
		t.Fatalf("resolved embed channel = %q, want %q", got, want)
	}

	controller := &testMainController{presenceValue: mcserver.PresenceState{ServerState: state.StateRunning}}
	statusEmbed := &testMainStatusEmbed{}
	handler := newDiscordHandler(cfg, controller, statusEmbed, nil, runtimeStore, "env-default-channel")

	session := newMainInteractionSession(t)
	seedMainChannelCommandState(t, session, testGuildID, "target-channel", "target-channel", discordgo.ChannelTypeGuildText, "role-id", "마크봇", discordgo.PermissionViewChannel|discordgo.PermissionSendMessages|discordgo.PermissionEmbedLinks|discordgo.PermissionReadMessageHistory, true)

	handler.HandleInteraction(session, newMainChannelInteraction("target-channel", []string{"role-id"}))

	if got, want := runtimeStore.saveCalls, []string{"target-channel", runtimeStore.loadChannelID}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("store save calls = %v, want %v", got, want)
	}
	if runtimeStore.clearCalls != 0 {
		t.Fatalf("store clear calls = %d, want 0", runtimeStore.clearCalls)
	}
	if got, want := statusEmbed.switchCalls, []string{"target-channel", runtimeStore.loadChannelID}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("status embed switch calls = %v, want %v", got, want)
	}
}

func overrideBootstrapRuntimeStore(t *testing.T, path string) {
	t.Helper()

	oldFactory := newRuntimeEmbedChannelStore
	newRuntimeEmbedChannelStore = func() *config.RuntimeEmbedChannelStore {
		return config.NewRuntimeEmbedChannelStore(path)
	}
	t.Cleanup(func() {
		newRuntimeEmbedChannelStore = oldFactory
	})
}

var captureLogsMu sync.Mutex

func captureLogs(t *testing.T, fn func()) string {
	t.Helper()

	captureLogsMu.Lock()
	defer captureLogsMu.Unlock()

	var buf bytes.Buffer
	oldFlags := log.Flags()
	oldPrefix := log.Prefix()
	oldWriter := log.Writer()
	log.SetFlags(0)
	log.SetPrefix("")
	log.SetOutput(&buf)
	defer log.SetOutput(oldWriter)
	defer log.SetFlags(oldFlags)
	defer log.SetPrefix(oldPrefix)

	fn()
	return buf.String()
}

func TestCaptureLogs_RestoresPreviousWriter(t *testing.T) {
	var previous bytes.Buffer

	originalWriter := log.Writer()
	originalFlags := log.Flags()
	originalPrefix := log.Prefix()
	log.SetOutput(&previous)
	t.Cleanup(func() {
		log.SetOutput(originalWriter)
		log.SetFlags(originalFlags)
		log.SetPrefix(originalPrefix)
	})

	ok := t.Run("capture", func(t *testing.T) {
		logs := captureLogs(t, func() {
			log.Print("captured")
		})
		if !strings.Contains(logs, "captured") {
			t.Fatalf("expected captured log, got %q", logs)
		}
	})
	if !ok {
		return
	}

	log.Print("after capture")
	if !strings.Contains(previous.String(), "after capture") {
		t.Fatalf("expected logger output to be restored to previous writer, got %q", previous.String())
	}
}

type testRuntimeEmbedChannelStore struct {
	loadChannelID     string
	loadSetting       config.RuntimeEmbedChannelSetting
	loadUseSetting    bool
	loadFound         bool
	loadErr           error
	saveCalls         []string
	saveDisabledCalls int
	saveErrs          []error
	clearCalls        int
}

func (s *testRuntimeEmbedChannelStore) Load(_ context.Context, _ string) (config.RuntimeEmbedChannelSetting, bool, error) {
	if s.loadUseSetting {
		return s.loadSetting, s.loadFound, s.loadErr
	}
	setting := config.RuntimeEmbedChannelSetting{}
	if s.loadFound {
		setting = config.RuntimeEmbedChannelSetting{Mode: config.RuntimeEmbedChannelModeChannel, ChannelID: s.loadChannelID}
	}
	return setting, s.loadFound, s.loadErr
}

func (s *testRuntimeEmbedChannelStore) Save(_ context.Context, _ string, channelID string) error {
	s.saveCalls = append(s.saveCalls, channelID)
	if len(s.saveErrs) == 0 {
		return nil
	}
	err := s.saveErrs[0]
	s.saveErrs = s.saveErrs[1:]
	return err
}

func (s *testRuntimeEmbedChannelStore) SaveDisabled(context.Context, string) error {
	s.saveDisabledCalls++
	return nil
}

func (s *testRuntimeEmbedChannelStore) Clear(context.Context) error {
	s.clearCalls++
	return nil
}

type testMainStatusEmbed struct {
	switchCalls []string
}

func (e *testMainStatusEmbed) Update(context.Context) error {
	return nil
}

func (e *testMainStatusEmbed) SwitchChannel(_ context.Context, channelID string, _ mcserver.PresenceState) error {
	e.switchCalls = append(e.switchCalls, channelID)
	return nil
}

type testMainController struct {
	presenceValue mcserver.PresenceState
}

func (c *testMainController) Start(context.Context) <-chan mcserver.StartResult {
	ch := make(chan mcserver.StartResult)
	close(ch)
	return ch
}

func (c *testMainController) Stop(context.Context) <-chan mcserver.StopResult {
	ch := make(chan mcserver.StopResult)
	close(ch)
	return ch
}

func (c *testMainController) Presence(context.Context) mcserver.PresenceState {
	return c.presenceValue
}

func newMainInteractionSession(t *testing.T) *discordgo.Session {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(server.Close)
	restore := overrideDiscordEndpoints(server.URL)
	t.Cleanup(restore)

	session, err := discordgo.New("Bot test-token")
	if err != nil {
		t.Fatalf("failed to create discord session: %v", err)
	}
	session.Client = server.Client()
	return session
}

func seedMainChannelCommandState(t *testing.T, session *discordgo.Session, guildID, channelID, channelName string, channelType discordgo.ChannelType, roleID, roleName string, rolePermissions int64, memberHasRole bool) {
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
			ID:       "test-bot-user-id",
			Username: "bot",
		},
		Roles: memberRoles,
	}); err != nil {
		t.Fatalf("failed to add member state: %v", err)
	}

	session.State.User = &discordgo.User{ID: "test-bot-user-id", Username: "bot"}
}

func newMainChannelInteraction(channelID string, roles []string) *discordgo.InteractionCreate {
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
					ID:       "test-bot-user-id",
					Username: "test-user",
				},
			},
			Data: discordgo.ApplicationCommandInteractionData{
				Name: "마크봇",
				Options: []*discordgo.ApplicationCommandInteractionDataOption{{
					Name: "채널",
					Type: discordgo.ApplicationCommandOptionSubCommand,
					Options: []*discordgo.ApplicationCommandInteractionDataOption{{
						Name:  "channel",
						Type:  discordgo.ApplicationCommandOptionChannel,
						Value: channelID,
					}},
				}},
			},
		},
	}
}

func TestRegisterSlashCommands_UsesStateApplicationID(t *testing.T) {
	testServer := newDiscordAPITestServer(t, discordAPITestServerOptions{})

	session, err := discordgo.New("Bot test-token")
	if err != nil {
		t.Fatalf("failed to create discord session: %v", err)
	}
	session.Client = testServer.server.Client()
	session.State.Application = &discordgo.Application{ID: "app-id"}

	registered := registerSlashCommands(session)
	assertRegisteredCommands(t, registered)

	requests := testServer.recordedRequests()
	if len(requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(requests))
	}
	if requests[0].Method != http.MethodPut {
		t.Fatalf("expected PUT request, got %s", requests[0].Method)
	}
	assertCommandOverwriteRequest(t, requests[0], "app-id")
	assertCommandPayload(t, requests[0].Body)
}

func TestRegisterSlashCommands_IncludesChannelSubcommand(t *testing.T) {
	testServer := newDiscordAPITestServer(t, discordAPITestServerOptions{})

	session, err := discordgo.New("Bot test-token")
	if err != nil {
		t.Fatalf("failed to create discord session: %v", err)
	}
	session.Client = testServer.server.Client()
	session.State.Application = &discordgo.Application{ID: "app-id"}

	registered := registerSlashCommands(session)
	assertRegisteredCommands(t, registered)

	requests := testServer.recordedRequests()
	if len(requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(requests))
	}
	assertCommandOverwriteRequest(t, requests[0], "app-id")
	assertCommandPayload(t, requests[0].Body)
}

func TestRegisterSlashCommands_FallsBackToStateUserID(t *testing.T) {
	testServer := newDiscordAPITestServer(t, discordAPITestServerOptions{})

	session, err := discordgo.New("Bot test-token")
	if err != nil {
		t.Fatalf("failed to create discord session: %v", err)
	}
	session.Client = testServer.server.Client()
	session.State.User = &discordgo.User{ID: "app-id"}

	registered := registerSlashCommands(session)
	assertRegisteredCommands(t, registered)

	requests := testServer.recordedRequests()
	if len(requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(requests))
	}
	if requests[0].Method != http.MethodPut {
		t.Fatalf("expected PUT request, got %s", requests[0].Method)
	}
	assertCommandOverwriteRequest(t, requests[0], "app-id")
	assertCommandPayload(t, requests[0].Body)
}

func TestRegisterSlashCommands_FallsBackToOAuthApplicationID(t *testing.T) {
	testServer := newDiscordAPITestServer(t, discordAPITestServerOptions{oauthApplicationID: "app-id"})

	session, err := discordgo.New("Bot test-token")
	if err != nil {
		t.Fatalf("failed to create discord session: %v", err)
	}
	session.Client = testServer.server.Client()
	session.State.Application = nil
	session.State.User = nil

	registered := registerSlashCommands(session)
	assertRegisteredCommands(t, registered)

	requests := testServer.recordedRequests()
	if len(requests) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(requests))
	}
	if requests[0].Method != http.MethodGet || requests[0].Path != "/api/v"+discordgo.APIVersion+"/oauth2/applications/@me" {
		t.Fatalf("unexpected OAuth lookup request: %s %s", requests[0].Method, requests[0].Path)
	}
	if requests[1].Method != http.MethodPut {
		t.Fatalf("expected PUT request, got %s", requests[1].Method)
	}
	assertCommandOverwriteRequest(t, requests[1], "app-id")
	assertCommandPayload(t, requests[1].Body)
}

func TestRegisterSlashCommands_SkipsSyncWithoutApplicationID(t *testing.T) {
	testServer := newDiscordAPITestServer(t, discordAPITestServerOptions{})

	session, err := discordgo.New("Bot test-token")
	if err != nil {
		t.Fatalf("failed to create discord session: %v", err)
	}
	session.Client = testServer.server.Client()
	session.State.Application = nil
	session.State.User = nil

	registered := registerSlashCommands(session)
	if len(registered) != 0 {
		t.Fatalf("expected no registered commands, got %d", len(registered))
	}

	requests := testServer.recordedRequests()
	if len(requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(requests))
	}
	if requests[0].Method != http.MethodGet || requests[0].Path != "/api/v"+discordgo.APIVersion+"/oauth2/applications/@me" {
		t.Fatalf("unexpected OAuth lookup request: %s %s", requests[0].Method, requests[0].Path)
	}
	if hasRequestPath(requests, "/api/v"+discordgo.APIVersion+"/applications/app-id/commands") {
		t.Fatalf("unexpected command overwrite request")
	}
}

func newDiscordAPITestServer(t *testing.T, opts discordAPITestServerOptions) *discordAPITestServer {
	t.Helper()

	testServer := &discordAPITestServer{oauthApplicationID: opts.oauthApplicationID}
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
		case r.Method == http.MethodGet && r.URL.Path == "/api/v"+discordgo.APIVersion+"/oauth2/applications/@me":
			if err := writeJSONResponse(w, &discordgo.Application{ID: testServer.oauthApplicationID}); err != nil {
				testServer.recordHandlerError(err)
				http.Error(w, "failed to write response", http.StatusInternalServerError)
			}
			return

		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/v"+discordgo.APIVersion+"/applications/") && strings.HasSuffix(r.URL.Path, "/commands"):
			var payload []*discordgo.ApplicationCommand
			if err := json.Unmarshal(body, &payload); err != nil {
				testServer.recordHandlerError(fmt.Errorf("failed to unmarshal request body: %w", err))
				http.Error(w, "failed to unmarshal request body", http.StatusBadRequest)
				return
			}

			appID := applicationIDFromCommandPath(r.URL.Path)
			response := make([]*discordgo.ApplicationCommand, 0, len(payload))
			for i, cmd := range payload {
				response = append(response, &discordgo.ApplicationCommand{
					ID:            string(rune('1' + i)),
					ApplicationID: appID,
					Name:          cmd.Name,
					Type:          cmd.Type,
				})
			}

			if err := writeJSONResponse(w, response); err != nil {
				testServer.recordHandlerError(err)
				http.Error(w, "failed to write response", http.StatusInternalServerError)
			}
			return
		}

		testServer.recordHandlerError(fmt.Errorf("unexpected Discord API request: %s %s", r.Method, r.URL.Path))
		http.NotFound(w, r)
	}))
	t.Cleanup(testServer.server.Close)
	t.Cleanup(func() {
		testServer.assertNoHandlerErrors(t)
	})

	restore := overrideDiscordEndpoints(testServer.server.URL)
	t.Cleanup(restore)

	return testServer
}

func overrideDiscordEndpoints(baseURL string) func() {
	discordEndpointOverrideMu.Lock()

	oldEndpointDiscord := discordgo.EndpointDiscord
	oldEndpointAPI := discordgo.EndpointAPI
	oldEndpointApplications := discordgo.EndpointApplications
	oldEndpointOAuth2 := discordgo.EndpointOAuth2
	oldEndpointOAuth2Applications := discordgo.EndpointOAuth2Applications
	oldEndpointOAuth2Application := discordgo.EndpointOAuth2Application

	discordgo.EndpointDiscord = baseURL + "/"
	discordgo.EndpointAPI = discordgo.EndpointDiscord + "api/v" + discordgo.APIVersion + "/"
	discordgo.EndpointApplications = discordgo.EndpointAPI + "applications"
	discordgo.EndpointOAuth2 = discordgo.EndpointAPI + "oauth2/"
	discordgo.EndpointOAuth2Applications = discordgo.EndpointOAuth2 + "applications"
	discordgo.EndpointOAuth2Application = func(aID string) string { return discordgo.EndpointOAuth2Applications + "/" + aID }

	return func() {
		discordgo.EndpointDiscord = oldEndpointDiscord
		discordgo.EndpointAPI = oldEndpointAPI
		discordgo.EndpointApplications = oldEndpointApplications
		discordgo.EndpointOAuth2 = oldEndpointOAuth2
		discordgo.EndpointOAuth2Applications = oldEndpointOAuth2Applications
		discordgo.EndpointOAuth2Application = oldEndpointOAuth2Application
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

func assertRegisteredCommands(t *testing.T, registered []*discordgo.ApplicationCommand) {
	t.Helper()

	if len(registered) != len(slashCommands) {
		t.Fatalf("expected %d registered commands, got %d", len(slashCommands), len(registered))
	}
	if len(registered) == 0 {
		t.Fatalf("expected at least one registered command")
	}
	if registered[0].Name != slashCommands[0].Name {
		t.Fatalf("expected first registered command %q, got %q", slashCommands[0].Name, registered[0].Name)
	}
}

func assertCommandOverwriteRequest(t *testing.T, request recordedDiscordRequest, appID string) {
	t.Helper()

	if request.Path != "/api/v"+discordgo.APIVersion+"/applications/"+appID+"/commands" {
		t.Fatalf("unexpected request path: %s", request.Path)
	}
}

func assertCommandPayload(t *testing.T, body []byte) {
	t.Helper()

	var payload []*discordgo.ApplicationCommand
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatalf("failed to unmarshal request body: %v", err)
	}
	if len(payload) != 1 {
		t.Fatalf("expected 1 command in payload, got %d", len(payload))
	}
	root := payload[0]
	if root.Name != "마크봇" {
		t.Fatalf("expected payload command name %q, got %q", "마크봇", root.Name)
	}
	if len(root.Options) != 2 {
		t.Fatalf("expected 2 subcommands, got %d", len(root.Options))
	}

	rcon := findOptionByName(t, root.Options, "rcon")
	if rcon.Type != discordgo.ApplicationCommandOptionSubCommand {
		t.Fatalf("expected rcon subcommand type, got %v", rcon.Type)
	}
	if len(rcon.Options) != 1 {
		t.Fatalf("expected 1 rcon option, got %d", len(rcon.Options))
	}
	rconCommand := rcon.Options[0]
	if rconCommand.Name != "command" || rconCommand.Type != discordgo.ApplicationCommandOptionString || !rconCommand.Required {
		t.Fatalf("unexpected rcon option: %+v", rconCommand)
	}

	channel := findOptionByName(t, root.Options, "채널")
	if channel.Type != discordgo.ApplicationCommandOptionSubCommandGroup {
		t.Fatalf("expected 채널 subcommand group type, got %v", channel.Type)
	}
	if len(channel.Options) != 3 {
		t.Fatalf("expected 3 channel subcommands, got %d", len(channel.Options))
	}
	setChannel := findOptionByName(t, channel.Options, "설정")
	if setChannel.Type != discordgo.ApplicationCommandOptionSubCommand {
		t.Fatalf("expected 설정 subcommand type, got %v", setChannel.Type)
	}
	if len(setChannel.Options) != 1 {
		t.Fatalf("expected 1 설정 option, got %d", len(setChannel.Options))
	}
	channelOption := setChannel.Options[0]
	if channelOption.Name != "channel" {
		t.Fatalf("expected channel option name %q, got %q", "channel", channelOption.Name)
	}
	if channelOption.Type != discordgo.ApplicationCommandOptionChannel {
		t.Fatalf("expected channel option type %v, got %v", discordgo.ApplicationCommandOptionChannel, channelOption.Type)
	}
	if !channelOption.Required {
		t.Fatalf("expected channel option to be required")
	}
	if len(channelOption.ChannelTypes) != 2 || channelOption.ChannelTypes[0] != discordgo.ChannelTypeGuildText || channelOption.ChannelTypes[1] != discordgo.ChannelTypeGuildNews {
		t.Fatalf("unexpected channel types: %#v", channelOption.ChannelTypes)
	}

	defaultChannel := findOptionByName(t, channel.Options, "기본값")
	if defaultChannel.Type != discordgo.ApplicationCommandOptionSubCommand {
		t.Fatalf("expected 기본값 subcommand type, got %v", defaultChannel.Type)
	}
	if len(defaultChannel.Options) != 0 {
		t.Fatalf("expected 기본값 to have no options, got %d", len(defaultChannel.Options))
	}

	disableChannel := findOptionByName(t, channel.Options, "끄기")
	if disableChannel.Type != discordgo.ApplicationCommandOptionSubCommand {
		t.Fatalf("expected 끄기 subcommand type, got %v", disableChannel.Type)
	}
	if len(disableChannel.Options) != 0 {
		t.Fatalf("expected 끄기 to have no options, got %d", len(disableChannel.Options))
	}
	if !strings.Contains(disableChannel.Description, "저장") || strings.Contains(disableChannel.Description, "지우") {
		t.Fatalf("unexpected 끄기 description: %q", disableChannel.Description)
	}
}

func findOptionByName(t *testing.T, options []*discordgo.ApplicationCommandOption, name string) *discordgo.ApplicationCommandOption {
	t.Helper()

	for _, option := range options {
		if option.Name == name {
			return option
		}
	}
	t.Fatalf("option %q not found", name)
	return nil
}

func writeJSONResponse(w http.ResponseWriter, response any) error {
	encoded, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("failed to marshal response body: %w", err)
	}

	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(encoded); err != nil {
		return fmt.Errorf("failed to write response body: %w", err)
	}
	return nil
}

func applicationIDFromCommandPath(path string) string {
	prefix := "/api/v" + discordgo.APIVersion + "/applications/"
	suffix := "/commands"
	trimmed := strings.TrimPrefix(path, prefix)
	trimmed = strings.TrimSuffix(trimmed, suffix)
	return strings.TrimSuffix(trimmed, "/")
}

func hasRequestPath(requests []recordedDiscordRequest, path string) bool {
	for _, request := range requests {
		if request.Path == path {
			return true
		}
	}
	return false
}
