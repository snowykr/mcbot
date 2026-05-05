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
	loadChannelID     string
	loadSetting       config.RuntimeEmbedChannelSetting
	loadUseSetting    bool
	loadFound         bool
	loadErr           error
	honorContext      bool
	loadCalls         int
	saveCalls         []string
	saveContextErrs   []error
	saveDisabledCalls int
	disabledCtxErrs   []error
	clearCalls        int
	clearContextErrs  []error
	saveErrs          []error
	saveDisabledErrs  []error
	clearErr          error
}

func (s *channelConfiguratorStore) Load(ctx context.Context, _ string) (config.RuntimeEmbedChannelSetting, bool, error) {
	if s.honorContext {
		if err := ctx.Err(); err != nil {
			return config.RuntimeEmbedChannelSetting{}, false, err
		}
	}
	s.loadCalls++
	if s.loadUseSetting {
		return s.loadSetting, s.loadFound, s.loadErr
	}
	setting := config.RuntimeEmbedChannelSetting{}
	if s.loadFound {
		setting = config.RuntimeEmbedChannelSetting{Mode: config.RuntimeEmbedChannelModeChannel, ChannelID: s.loadChannelID}
	}
	return setting, s.loadFound, s.loadErr
}

func (s *channelConfiguratorStore) Save(ctx context.Context, _ string, channelID string) error {
	if s.honorContext {
		if err := ctx.Err(); err != nil {
			s.saveContextErrs = append(s.saveContextErrs, err)
			return err
		}
	}
	s.saveCalls = append(s.saveCalls, channelID)
	if len(s.saveErrs) == 0 {
		return nil
	}
	err := s.saveErrs[0]
	s.saveErrs = s.saveErrs[1:]
	return err
}

func (s *channelConfiguratorStore) SaveDisabled(ctx context.Context, _ string) error {
	if s.honorContext {
		if err := ctx.Err(); err != nil {
			s.disabledCtxErrs = append(s.disabledCtxErrs, err)
			return err
		}
	}
	s.saveDisabledCalls++
	if len(s.saveDisabledErrs) == 0 {
		return nil
	}
	err := s.saveDisabledErrs[0]
	s.saveDisabledErrs = s.saveDisabledErrs[1:]
	return err
}

func (s *channelConfiguratorStore) Clear(ctx context.Context) error {
	if s.honorContext {
		if err := ctx.Err(); err != nil {
			s.clearContextErrs = append(s.clearContextErrs, err)
			return err
		}
	}
	s.clearCalls++
	return s.clearErr
}

type channelConfiguratorManager struct {
	switchCalls           []string
	presence              []mcserver.PresenceState
	switchErrs            []error
	honorContext          bool
	switchContextErrs     []error
	cancelAfterSwitchCall int
	cancel                context.CancelFunc
}

func (m *channelConfiguratorManager) SwitchChannel(ctx context.Context, channelID string, presence mcserver.PresenceState) error {
	if m.honorContext {
		if err := ctx.Err(); err != nil {
			m.switchContextErrs = append(m.switchContextErrs, err)
			return err
		}
	}
	m.switchCalls = append(m.switchCalls, channelID)
	m.presence = append(m.presence, presence)
	if m.cancel != nil && len(m.switchCalls) == m.cancelAfterSwitchCall {
		m.cancel()
	}
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
	configurator := NewChannelConfigurator(store, manager, "123456789012345678", "source-channel", "source-channel")

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
	configurator := NewChannelConfigurator(store, manager, "123456789012345678", "override-channel", "source-channel")

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

func TestChannelConfigurator_RestoresPreviousDisabledModeOnPersistFailure(t *testing.T) {
	store := &channelConfiguratorStore{
		loadSetting:    config.RuntimeEmbedChannelSetting{Mode: config.RuntimeEmbedChannelModeDisabled},
		loadUseSetting: true,
		loadFound:      true,
		saveErrs:       []error{errors.New("persist failed")},
	}
	manager := &channelConfiguratorManager{}
	configurator := NewChannelConfigurator(store, manager, "123456789012345678", "", "source-channel")

	err := configurator.ConfigureChannel(context.Background(), "target-channel", mcserver.PresenceState{})
	if err == nil {
		t.Fatal("ConfigureChannel() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "persist failed") {
		t.Fatalf("ConfigureChannel() error = %q, want persist failure context", err.Error())
	}

	if got := manager.switchCalls; len(got) != 2 || got[0] != "target-channel" || got[1] != "" {
		t.Fatalf("manager switch calls = %v, want [target-channel empty]", got)
	}
	if got := store.saveCalls; len(got) != 1 || got[0] != "target-channel" {
		t.Fatalf("store save calls = %v, want [target-channel]", got)
	}
	if store.saveDisabledCalls != 1 {
		t.Fatalf("store save disabled calls = %d, want 1", store.saveDisabledCalls)
	}
	if store.clearCalls != 0 {
		t.Fatalf("store clear calls = %d, want 0", store.clearCalls)
	}
	if configurator.currentChannelID != "" {
		t.Fatalf("currentChannelID = %q, want empty", configurator.currentChannelID)
	}
}

func TestChannelConfigurator_ClearRollbackWhenNoPreviousOverride(t *testing.T) {
	store := &channelConfiguratorStore{
		saveErrs: []error{errors.New("persist failed")},
	}
	manager := &channelConfiguratorManager{}
	configurator := NewChannelConfigurator(store, manager, "123456789012345678", "source-channel", "source-channel")

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

func TestChannelConfigurator_RollbackUsesFreshContextWhenSaveFailsFromCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	store := &channelConfiguratorStore{
		loadChannelID: "override-channel",
		loadFound:     true,
		honorContext:  true,
	}
	manager := &channelConfiguratorManager{
		honorContext:          true,
		cancelAfterSwitchCall: 1,
		cancel:                cancel,
	}
	configurator := NewChannelConfigurator(store, manager, "123456789012345678", "override-channel", "source-channel")

	err := configurator.ConfigureChannel(ctx, "target-channel", mcserver.PresenceState{})
	if err == nil {
		t.Fatal("ConfigureChannel() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "save runtime embed channel after switching manager") || !errors.Is(err, context.Canceled) {
		t.Fatalf("ConfigureChannel() error = %q, want canceled save failure context", err.Error())
	}
	if strings.Contains(err.Error(), "restore manager channel") || strings.Contains(err.Error(), "restore runtime embed channel") {
		t.Fatalf("ConfigureChannel() error = %q, want forward failure without rollback context errors", err.Error())
	}

	if got := manager.switchCalls; len(got) != 2 || got[0] != "target-channel" || got[1] != "override-channel" {
		t.Fatalf("manager switch calls = %v, want [target-channel override-channel]", got)
	}
	if got := store.saveCalls; len(got) != 1 || got[0] != "override-channel" {
		t.Fatalf("store save calls = %v, want rollback save [override-channel]", got)
	}
	if got := store.saveContextErrs; len(got) != 1 || !errors.Is(got[0], context.Canceled) {
		t.Fatalf("store save context errors = %v, want one canceled forward save", got)
	}
	if len(manager.switchContextErrs) != 0 {
		t.Fatalf("manager switch context errors = %v, want none during rollback", manager.switchContextErrs)
	}
	if configurator.currentChannelID != "override-channel" {
		t.Fatalf("currentChannelID = %q, want override-channel", configurator.currentChannelID)
	}
}

func TestChannelConfigurator_RollbackUsesFreshContextWhenSaveFailsWithNoPreviousSetting(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	store := &channelConfiguratorStore{
		honorContext: true,
	}
	manager := &channelConfiguratorManager{
		honorContext:          true,
		cancelAfterSwitchCall: 1,
		cancel:                cancel,
	}
	configurator := NewChannelConfigurator(store, manager, "123456789012345678", "source-channel", "source-channel")

	err := configurator.ConfigureChannel(ctx, "target-channel", mcserver.PresenceState{})
	if err == nil {
		t.Fatal("ConfigureChannel() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "save runtime embed channel after switching manager") || !errors.Is(err, context.Canceled) {
		t.Fatalf("ConfigureChannel() error = %q, want canceled save failure context", err.Error())
	}
	if strings.Contains(err.Error(), "restore manager channel") || strings.Contains(err.Error(), "clear runtime embed channel") {
		t.Fatalf("ConfigureChannel() error = %q, want forward failure without rollback context errors", err.Error())
	}

	if got := manager.switchCalls; len(got) != 2 || got[0] != "target-channel" || got[1] != "source-channel" {
		t.Fatalf("manager switch calls = %v, want [target-channel source-channel]", got)
	}
	if got := store.saveCalls; len(got) != 0 {
		t.Fatalf("store save calls = %v, want none because canceled context failed before save", got)
	}
	if got := store.saveContextErrs; len(got) != 1 || !errors.Is(got[0], context.Canceled) {
		t.Fatalf("store save context errors = %v, want one canceled forward save", got)
	}
	if store.clearCalls != 1 {
		t.Fatalf("store clear calls = %d, want 1 rollback clear", store.clearCalls)
	}
	if len(manager.switchContextErrs) != 0 {
		t.Fatalf("manager switch context errors = %v, want none during rollback", manager.switchContextErrs)
	}
	if configurator.currentChannelID != "source-channel" {
		t.Fatalf("currentChannelID = %q, want source-channel", configurator.currentChannelID)
	}
}

func TestChannelConfigurator_RollbackUsesFreshContextWhenClearFailsFromCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	store := &channelConfiguratorStore{
		loadChannelID: "override-channel",
		loadFound:     true,
		honorContext:  true,
	}
	manager := &channelConfiguratorManager{
		honorContext:          true,
		cancelAfterSwitchCall: 1,
		cancel:                cancel,
	}
	configurator := NewChannelConfigurator(store, manager, "123456789012345678", "override-channel", "default-channel")

	channelID, err := configurator.ConfigureDefaultChannel(ctx, mcserver.PresenceState{})
	if channelID != "default-channel" {
		t.Fatalf("ConfigureDefaultChannel() channelID = %q, want default-channel", channelID)
	}
	if err == nil {
		t.Fatal("ConfigureDefaultChannel() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "clear runtime embed channel after switching manager") || !errors.Is(err, context.Canceled) {
		t.Fatalf("ConfigureDefaultChannel() error = %q, want canceled clear failure context", err.Error())
	}
	if strings.Contains(err.Error(), "restore manager channel") || strings.Contains(err.Error(), "restore runtime embed channel") {
		t.Fatalf("ConfigureDefaultChannel() error = %q, want forward failure without rollback context errors", err.Error())
	}

	if got := manager.switchCalls; len(got) != 2 || got[0] != "default-channel" || got[1] != "override-channel" {
		t.Fatalf("manager switch calls = %v, want [default-channel override-channel]", got)
	}
	if store.clearCalls != 0 {
		t.Fatalf("store clear calls = %d, want 0 because canceled context failed before clear", store.clearCalls)
	}
	if got := store.clearContextErrs; len(got) != 1 || !errors.Is(got[0], context.Canceled) {
		t.Fatalf("store clear context errors = %v, want one canceled forward clear", got)
	}
	if got := store.saveCalls; len(got) != 1 || got[0] != "override-channel" {
		t.Fatalf("store save calls = %v, want rollback save [override-channel]", got)
	}
	if len(manager.switchContextErrs) != 0 {
		t.Fatalf("manager switch context errors = %v, want none during rollback", manager.switchContextErrs)
	}
	if configurator.currentChannelID != "override-channel" {
		t.Fatalf("currentChannelID = %q, want override-channel", configurator.currentChannelID)
	}
}

func TestChannelConfigurator_RollbackUsesFreshContextWhenSaveDisabledFailsFromCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	store := &channelConfiguratorStore{
		loadChannelID: "override-channel",
		loadFound:     true,
		honorContext:  true,
	}
	manager := &channelConfiguratorManager{
		honorContext:          true,
		cancelAfterSwitchCall: 1,
		cancel:                cancel,
	}
	configurator := NewChannelConfigurator(store, manager, "123456789012345678", "override-channel", "source-channel")

	err := configurator.DisableChannel(ctx, mcserver.PresenceState{})
	if err == nil {
		t.Fatal("DisableChannel() error = nil, want non-nil")
	}
	if !strings.Contains(err.Error(), "save disabled runtime embed channel after switching manager") || !errors.Is(err, context.Canceled) {
		t.Fatalf("DisableChannel() error = %q, want canceled save disabled failure context", err.Error())
	}
	if strings.Contains(err.Error(), "restore manager channel") || strings.Contains(err.Error(), "restore runtime embed channel") {
		t.Fatalf("DisableChannel() error = %q, want forward failure without rollback context errors", err.Error())
	}

	if got := manager.switchCalls; len(got) != 2 || got[0] != "" || got[1] != "override-channel" {
		t.Fatalf("manager switch calls = %v, want [empty override-channel]", got)
	}
	if store.saveDisabledCalls != 0 {
		t.Fatalf("store save disabled calls = %d, want 0 because canceled context failed before save disabled", store.saveDisabledCalls)
	}
	if got := store.disabledCtxErrs; len(got) != 1 || !errors.Is(got[0], context.Canceled) {
		t.Fatalf("store save disabled context errors = %v, want one canceled forward save disabled", got)
	}
	if got := store.saveCalls; len(got) != 1 || got[0] != "override-channel" {
		t.Fatalf("store save calls = %v, want rollback save [override-channel]", got)
	}
	if len(manager.switchContextErrs) != 0 {
		t.Fatalf("manager switch context errors = %v, want none during rollback", manager.switchContextErrs)
	}
	if configurator.currentChannelID != "override-channel" {
		t.Fatalf("currentChannelID = %q, want override-channel", configurator.currentChannelID)
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
	configurator := NewChannelConfigurator(store, manager, "123456789012345678", "override-channel", "source-channel")

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
	configurator := NewChannelConfigurator(store, manager, "123456789012345678", "source-channel", "source-channel")

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

func TestChannelConfigurator_DisablePersistsDisabledModeAndSwitchesToEmpty(t *testing.T) {
	store := &channelConfiguratorStore{
		loadChannelID: "override-channel",
		loadFound:     true,
	}
	manager := &channelConfiguratorManager{}
	configurator := NewChannelConfigurator(store, manager, "123456789012345678", "override-channel", "source-channel")

	if err := configurator.ConfigureChannel(context.Background(), " ", mcserver.PresenceState{}); err != nil {
		t.Fatalf("ConfigureChannel() error = %v", err)
	}

	if got := manager.switchCalls; len(got) != 1 || got[0] != "" {
		t.Fatalf("manager switch calls = %v, want empty channel switch", got)
	}
	if len(store.saveCalls) != 0 {
		t.Fatalf("store save calls = %v, want none", store.saveCalls)
	}
	if store.saveDisabledCalls != 1 {
		t.Fatalf("store save disabled calls = %d, want 1", store.saveDisabledCalls)
	}
	if store.clearCalls != 0 {
		t.Fatalf("store clear calls = %d, want 0", store.clearCalls)
	}
	if configurator.currentChannelID != "" {
		t.Fatalf("currentChannelID = %q, want empty", configurator.currentChannelID)
	}
}

func TestChannelConfigurator_DefaultClearsOverrideAndSwitchesToDefault(t *testing.T) {
	store := &channelConfiguratorStore{
		loadChannelID: "override-channel",
		loadFound:     true,
	}
	manager := &channelConfiguratorManager{}
	configurator := NewChannelConfigurator(store, manager, "123456789012345678", "override-channel", "default-channel")

	channelID, err := configurator.ConfigureDefaultChannel(context.Background(), mcserver.PresenceState{ServerState: state.StateRunning})
	if err != nil {
		t.Fatalf("ConfigureDefaultChannel() error = %v", err)
	}

	if channelID != "default-channel" {
		t.Fatalf("ConfigureDefaultChannel() channelID = %q, want default-channel", channelID)
	}
	if got := manager.switchCalls; len(got) != 1 || got[0] != "default-channel" {
		t.Fatalf("manager switch calls = %v, want [default-channel]", got)
	}
	if len(store.saveCalls) != 0 {
		t.Fatalf("store save calls = %v, want none", store.saveCalls)
	}
	if store.clearCalls != 1 {
		t.Fatalf("store clear calls = %d, want 1", store.clearCalls)
	}
	if configurator.currentChannelID != "default-channel" {
		t.Fatalf("currentChannelID = %q, want default-channel", configurator.currentChannelID)
	}
}
