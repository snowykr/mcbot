package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/bwmarrin/discordgo"
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
	if len(payload) != len(slashCommands) {
		t.Fatalf("expected %d commands in payload, got %d", len(slashCommands), len(payload))
	}
	if payload[0].Name != slashCommands[0].Name {
		t.Fatalf("expected payload command name %q, got %q", slashCommands[0].Name, payload[0].Name)
	}
}

func writeJSONResponse(w http.ResponseWriter, response any) error {
	encoded, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("failed to marshal response body: %w", err)
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(encoded)
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
