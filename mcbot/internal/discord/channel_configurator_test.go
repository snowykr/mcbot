package discord

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/snowy/mcbot/internal/config"
	"github.com/snowy/mcbot/internal/mcserver"
	"github.com/snowy/mcbot/internal/state"
)

type channelConfiguratorStore struct {
	loadChannelID string
	loadFound     bool
	loadErr       error
	loadCalls     int
	saveCalls     []string
	clearCalls    int
	saveErrs      []error
	clearErr      error
}

func (s *channelConfiguratorStore) Load(context.Context) (string, bool, error) {
	s.loadCalls++
	return s.loadChannelID, s.loadFound, s.loadErr
}

func (s *channelConfiguratorStore) Save(_ context.Context, channelID string) error {
	s.saveCalls = append(s.saveCalls, channelID)
	if len(s.saveErrs) == 0 {
		return nil
	}
	err := s.saveErrs[0]
	s.saveErrs = s.saveErrs[1:]
	return err
}

func (s *channelConfiguratorStore) Clear(context.Context) error {
	s.clearCalls++
	return s.clearErr
}

type channelConfiguratorManager struct {
	switchCalls []string
	presence    []mcserver.PresenceState
	switchErrs  []error
}

func (m *channelConfiguratorManager) SwitchChannel(_ context.Context, channelID string, presence mcserver.PresenceState) error {
	m.switchCalls = append(m.switchCalls, channelID)
	m.presence = append(m.presence, presence)
	if len(m.switchErrs) == 0 {
		return nil
	}
	err := m.switchErrs[0]
	m.switchErrs = m.switchErrs[1:]
	return err
}

func TestChannelConfigurator_SwitchesThenPersists(t *testing.T) {
	store := &channelConfiguratorStore{}
	manager := &channelConfiguratorManager{}
	configurator := NewChannelConfigurator(store, manager, "source-channel")

	presence := mcserver.PresenceState{ServerState: state.StateRunning}
	if err := configurator.ConfigureChannel(context.Background(), "target-channel", presence); err != nil {
		t.Fatalf("ConfigureChannel() error = %v", err)
	}

	if got := manager.switchCalls; len(got) != 1 || got[0] != "target-channel" {
		t.Fatalf("manager switch calls = %v, want [target-channel]", got)
	}
	if got := store.saveCalls; len(got) != 1 || got[0] != "target-channel" {
		t.Fatalf("store save calls = %v, want [target-channel]", got)
	}
	if store.clearCalls != 0 {
		t.Fatalf("store clear calls = %d, want 0", store.clearCalls)
	}
	if configurator.currentChannelID != "target-channel" {
		t.Fatalf("currentChannelID = %q, want target-channel", configurator.currentChannelID)
	}
}

func TestChannelConfigurator_RestoresPreviousOverrideOnPersistFailure(t *testing.T) {
	store := &channelConfiguratorStore{
		loadChannelID: "override-channel",
		loadFound:     true,
		saveErrs:      []error{errors.New("persist failed")},
	}
	manager := &channelConfiguratorManager{}
	configurator := NewChannelConfigurator(store, manager, "override-channel")

	err := configurator.ConfigureChannel(context.Background(), "target-channel", mcserver.PresenceState{})
	if err == nil {
		t.Fatal("ConfigureChannel() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "persist failed") {
		t.Fatalf("ConfigureChannel() error = %q, want persist failure context", err.Error())
	}

	if got := manager.switchCalls; len(got) != 2 || got[0] != "target-channel" || got[1] != "override-channel" {
		t.Fatalf("manager switch calls = %v, want [target-channel override-channel]", got)
	}
	if got := store.saveCalls; len(got) != 2 || got[0] != "target-channel" || got[1] != "override-channel" {
		t.Fatalf("store save calls = %v, want [target-channel override-channel]", got)
	}
	if store.clearCalls != 0 {
		t.Fatalf("store clear calls = %d, want 0", store.clearCalls)
	}
	if configurator.currentChannelID != "override-channel" {
		t.Fatalf("currentChannelID = %q, want override-channel", configurator.currentChannelID)
	}
}

func TestChannelConfigurator_ClearRollbackWhenNoPreviousOverride(t *testing.T) {
	store := &channelConfiguratorStore{
		saveErrs: []error{errors.New("persist failed")},
	}
	manager := &channelConfiguratorManager{}
	configurator := NewChannelConfigurator(store, manager, "source-channel")

	err := configurator.ConfigureChannel(context.Background(), "target-channel", mcserver.PresenceState{})
	if err == nil {
		t.Fatal("ConfigureChannel() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "persist failed") {
		t.Fatalf("ConfigureChannel() error = %q, want persist failure context", err.Error())
	}

	if got := manager.switchCalls; len(got) != 2 || got[0] != "target-channel" || got[1] != "source-channel" {
		t.Fatalf("manager switch calls = %v, want [target-channel source-channel]", got)
	}
	if got := store.saveCalls; len(got) != 1 || got[0] != "target-channel" {
		t.Fatalf("store save calls = %v, want [target-channel]", got)
	}
	if store.clearCalls != 1 {
		t.Fatalf("store clear calls = %d, want 1", store.clearCalls)
	}
	if configurator.currentChannelID != "source-channel" {
		t.Fatalf("currentChannelID = %q, want source-channel", configurator.currentChannelID)
	}
}

func TestChannelConfigurator_CombinedRollbackFailure(t *testing.T) {
	store := &channelConfiguratorStore{
		loadChannelID: "override-channel",
		loadFound:     true,
		saveErrs:      []error{errors.New("persist failed"), errors.New("restore persist failed")},
		clearErr:      errors.New("clear failed"),
	}
	manager := &channelConfiguratorManager{
		switchErrs: []error{nil, errors.New("rollback switch failed")},
	}
	configurator := NewChannelConfigurator(store, manager, "override-channel")

	err := configurator.ConfigureChannel(context.Background(), "target-channel", mcserver.PresenceState{})
	if err == nil {
		t.Fatal("ConfigureChannel() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "persist failed") || !strings.Contains(err.Error(), "rollback switch failed") || !strings.Contains(err.Error(), "restore runtime embed channel") {
		t.Fatalf("ConfigureChannel() error = %q, want combined forward and rollback context", err.Error())
	}

	if got := manager.switchCalls; len(got) != 2 || got[0] != "target-channel" || got[1] != "override-channel" {
		t.Fatalf("manager switch calls = %v, want [target-channel override-channel]", got)
	}
	if got := store.saveCalls; len(got) != 2 || got[0] != "target-channel" || got[1] != "override-channel" {
		t.Fatalf("store save calls = %v, want [target-channel override-channel]", got)
	}
	if store.clearCalls != 0 {
		t.Fatalf("store clear calls = %d, want 0", store.clearCalls)
	}
	if configurator.currentChannelID != "override-channel" {
		t.Fatalf("currentChannelID = %q, want override-channel", configurator.currentChannelID)
	}
}

func TestChannelConfigurator_RepairsCorruptRuntimeOverride(t *testing.T) {
	store := &channelConfiguratorStore{
		loadErr: fmt.Errorf("%w: invalid runtime embed channel store: bad data", config.ErrCorruptRuntimeEmbedChannelStore),
	}
	manager := &channelConfiguratorManager{}
	configurator := NewChannelConfigurator(store, manager, "source-channel")

	err := configurator.ConfigureChannel(context.Background(), "target-channel", mcserver.PresenceState{ServerState: state.StateRunning})
	if err != nil {
		t.Fatalf("ConfigureChannel() error = %v", err)
	}

	if got := manager.switchCalls; len(got) != 1 || got[0] != "target-channel" {
		t.Fatalf("manager switch calls = %v, want [target-channel]", got)
	}
	if got := store.saveCalls; len(got) != 1 || got[0] != "target-channel" {
		t.Fatalf("store save calls = %v, want [target-channel]", got)
	}
	if store.clearCalls != 0 {
		t.Fatalf("store clear calls = %d, want 0", store.clearCalls)
	}
	if configurator.currentChannelID != "target-channel" {
		t.Fatalf("currentChannelID = %q, want target-channel", configurator.currentChannelID)
	}
}
