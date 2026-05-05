package discord

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/snowy/mcbot/internal/config"
	"github.com/snowy/mcbot/internal/mcserver"
)

type runtimeEmbedChannelStore interface {
	Load(ctx context.Context, trustedGuildID string) (setting config.RuntimeEmbedChannelSetting, found bool, err error)
	Save(ctx context.Context, trustedGuildID, channelID string) error
	SaveDisabled(ctx context.Context, trustedGuildID string) error
	Clear(ctx context.Context) error
}

type channelSwitcher interface {
	SwitchChannel(ctx context.Context, channelID string, presence mcserver.PresenceState) error
}

type channelPersistenceMode int

const (
	channelPersistenceChannel channelPersistenceMode = iota
	channelPersistenceClear
	channelPersistenceDisabled

	channelRollbackTimeout = 5 * time.Second
)

type ChannelConfigurator struct {
	store            runtimeEmbedChannelStore
	manager          channelSwitcher
	trustedGuildID   string
	currentChannelID string
	defaultChannelID string
	mu               sync.Mutex
}

func NewChannelConfigurator(store runtimeEmbedChannelStore, manager channelSwitcher, trustedGuildID, currentChannelID, defaultChannelID string) *ChannelConfigurator {
	return &ChannelConfigurator{
		store:            store,
		manager:          manager,
		trustedGuildID:   strings.TrimSpace(trustedGuildID),
		currentChannelID: strings.TrimSpace(currentChannelID),
		defaultChannelID: strings.TrimSpace(defaultChannelID),
	}
}

func (c *ChannelConfigurator) ConfigureChannel(ctx context.Context, channelID string, presence mcserver.PresenceState) error {
	trimmedChannelID := strings.TrimSpace(channelID)
	if trimmedChannelID == "" {
		return c.configureChannel(ctx, "", channelPersistenceDisabled, presence)
	}
	return c.configureChannel(ctx, trimmedChannelID, channelPersistenceChannel, presence)
}

func (c *ChannelConfigurator) ConfigureDefaultChannel(ctx context.Context, presence mcserver.PresenceState) (string, error) {
	channelID := c.defaultChannelID
	return channelID, c.configureChannel(ctx, channelID, channelPersistenceClear, presence)
}

func (c *ChannelConfigurator) DisableChannel(ctx context.Context, presence mcserver.PresenceState) error {
	return c.configureChannel(ctx, "", channelPersistenceDisabled, presence)
}

func (c *ChannelConfigurator) configureChannel(ctx context.Context, channelID string, persistenceMode channelPersistenceMode, presence mcserver.PresenceState) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}

	trimmedChannelID := strings.TrimSpace(channelID)

	c.mu.Lock()
	defer c.mu.Unlock()

	previousSetting, hadPreviousSetting, err := c.store.Load(ctx, c.trustedGuildID)
	if err != nil {
		if !config.IsCorruptRuntimeEmbedChannelStoreError(err) && !config.IsStaleRuntimeEmbedChannelStoreError(err) {
			return fmt.Errorf("load runtime embed channel: %w", err)
		}
		previousSetting = config.RuntimeEmbedChannelSetting{}
		hadPreviousSetting = false
	}

	previousEffectiveChannelID := c.currentChannelID

	if err := c.manager.SwitchChannel(ctx, trimmedChannelID, presence); err != nil {
		return fmt.Errorf("switch manager to %q: %w", trimmedChannelID, err)
	}

	if err := c.persistRuntimeSetting(ctx, persistenceMode, trimmedChannelID); err != nil {
		// The command context may expire after the manager has already switched channels.
		// Rollback must detach from cancellation so the in-memory/embed state and
		// runtime store can be restored, but stay bounded to avoid hanging while
		// the configurator mutex is held.
		rollbackCtx, rollbackCancel := context.WithTimeout(context.WithoutCancel(ctx), channelRollbackTimeout)
		defer rollbackCancel()

		rollbackErr := c.rollback(rollbackCtx, previousEffectiveChannelID, previousSetting, hadPreviousSetting, presence)
		if rollbackErr != nil {
			return errors.Join(err, rollbackErr)
		}
		return err
	}

	c.currentChannelID = trimmedChannelID
	return nil
}

func (c *ChannelConfigurator) persistRuntimeSetting(ctx context.Context, persistenceMode channelPersistenceMode, channelID string) error {
	switch persistenceMode {
	case channelPersistenceChannel:
		if err := c.store.Save(ctx, c.trustedGuildID, channelID); err != nil {
			return fmt.Errorf("save runtime embed channel after switching manager: %w", err)
		}
	case channelPersistenceClear:
		if err := c.store.Clear(ctx); err != nil {
			return fmt.Errorf("clear runtime embed channel after switching manager: %w", err)
		}
	case channelPersistenceDisabled:
		if err := c.store.SaveDisabled(ctx, c.trustedGuildID); err != nil {
			return fmt.Errorf("save disabled runtime embed channel after switching manager: %w", err)
		}
	default:
		return fmt.Errorf("unsupported channel persistence mode %d", persistenceMode)
	}
	return nil
}

func (c *ChannelConfigurator) rollback(ctx context.Context, previousEffectiveChannelID string, previousSetting config.RuntimeEmbedChannelSetting, hadPreviousSetting bool, presence mcserver.PresenceState) error {
	var errs []error

	if err := c.manager.SwitchChannel(ctx, previousEffectiveChannelID, presence); err != nil {
		errs = append(errs, fmt.Errorf("restore manager channel to %q: %w", previousEffectiveChannelID, err))
	}

	if hadPreviousSetting {
		switch previousSetting.Mode {
		case config.RuntimeEmbedChannelModeChannel:
			if err := c.store.Save(ctx, c.trustedGuildID, previousSetting.ChannelID); err != nil {
				errs = append(errs, fmt.Errorf("restore runtime embed channel to %q: %w", previousSetting.ChannelID, err))
			}
		case config.RuntimeEmbedChannelModeDisabled:
			if err := c.store.SaveDisabled(ctx, c.trustedGuildID); err != nil {
				errs = append(errs, fmt.Errorf("restore disabled runtime embed channel: %w", err))
			}
		default:
			errs = append(errs, fmt.Errorf("restore runtime embed channel: unsupported previous mode %q", previousSetting.Mode))
		}
	} else {
		if err := c.store.Clear(ctx); err != nil {
			errs = append(errs, fmt.Errorf("clear runtime embed channel: %w", err))
		}
	}

	if len(errs) == 0 {
		c.currentChannelID = previousEffectiveChannelID
		return nil
	}

	return errors.Join(errs...)
}
