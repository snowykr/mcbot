package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
	server   *httptest.Server
	mu       sync.Mutex
	requests []recordedDiscordRequest
}

func TestRegisterSlashCommands_UsesBulkOverwrite(t *testing.T) {
	testServer := newDiscordAPITestServer(t)

	session, err := discordgo.New("Bot test-token")
	if err != nil {
		t.Fatalf("failed to create discord session: %v", err)
	}
	session.Client = testServer.server.Client()
	session.State.User = &discordgo.User{ID: "app-id"}

	registered := registerSlashCommands(session)
	if len(registered) != len(slashCommands) {
		t.Fatalf("expected %d registered commands, got %d", len(slashCommands), len(registered))
	}

	requests := testServer.recordedRequests()
	if len(requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(requests))
	}
	if requests[0].Method != http.MethodPut {
		t.Fatalf("expected PUT request, got %s", requests[0].Method)
	}
	if requests[0].Path != "/api/v"+discordgo.APIVersion+"/applications/app-id/commands" {
		t.Fatalf("unexpected request path: %s", requests[0].Path)
	}

	var payload []*discordgo.ApplicationCommand
	if err := json.Unmarshal(requests[0].Body, &payload); err != nil {
		t.Fatalf("failed to unmarshal request body: %v", err)
	}
	if len(payload) != len(slashCommands) {
		t.Fatalf("expected %d commands in payload, got %d", len(slashCommands), len(payload))
	}
	if payload[0].Name != slashCommands[0].Name {
		t.Fatalf("expected payload command name %q, got %q", slashCommands[0].Name, payload[0].Name)
	}
}

func newDiscordAPITestServer(t *testing.T) *discordAPITestServer {
	t.Helper()

	testServer := &discordAPITestServer{}
	testServer.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("failed to read request body: %v", err)
		}

		var payload []*discordgo.ApplicationCommand
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("failed to unmarshal request body: %v", err)
		}

		testServer.mu.Lock()
		testServer.requests = append(testServer.requests, recordedDiscordRequest{
			Method: r.Method,
			Path:   r.URL.Path,
			Body:   body,
		})
		testServer.mu.Unlock()

		response := make([]*discordgo.ApplicationCommand, 0, len(payload))
		for i, cmd := range payload {
			response = append(response, &discordgo.ApplicationCommand{
				ID:            string(rune('1' + i)),
				ApplicationID: "app-id",
				Name:          cmd.Name,
				Type:          cmd.Type,
			})
		}

		encoded, err := json.Marshal(response)
		if err != nil {
			t.Fatalf("failed to marshal response body: %v", err)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(encoded)
	}))
	t.Cleanup(testServer.server.Close)

	restore := overrideDiscordEndpoints(testServer.server.URL)
	t.Cleanup(restore)

	return testServer
}

func overrideDiscordEndpoints(baseURL string) func() {
	oldEndpointDiscord := discordgo.EndpointDiscord
	oldEndpointAPI := discordgo.EndpointAPI
	oldEndpointApplications := discordgo.EndpointApplications

	discordgo.EndpointDiscord = baseURL + "/"
	discordgo.EndpointAPI = discordgo.EndpointDiscord + "api/v" + discordgo.APIVersion + "/"
	discordgo.EndpointApplications = discordgo.EndpointAPI + "applications"

	return func() {
		discordgo.EndpointDiscord = oldEndpointDiscord
		discordgo.EndpointAPI = oldEndpointAPI
		discordgo.EndpointApplications = oldEndpointApplications
	}
}

func (s *discordAPITestServer) recordedRequests() []recordedDiscordRequest {
	s.mu.Lock()
	defer s.mu.Unlock()

	requests := make([]recordedDiscordRequest, len(s.requests))
	copy(requests, s.requests)
	return requests
}
