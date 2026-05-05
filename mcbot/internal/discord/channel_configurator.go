package discord

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/snowy/mcbot/internal/config"
	"github.com/snowy/mcbot/internal/mcserver"
)

type runtimeEmbedChannelStore interface {
	Load(ctx context.Context) (channelID string, found bool, err error)
	Save(ctx context.Context, channelID string) error
	Clear(ctx context.Context) error
}

type channelSwitcher interface {
	SwitchChannel(ctx context.Context, channelID string, presence mcserver.PresenceState) error
}

type ChannelConfigurator struct {
	store            runtimeEmbedChannelStore
	manager          channelSwitcher
	currentChannelID string
	mu               sync.Mutex
}

func NewChannelConfigurator(store runtimeEmbedChannelStore, manager channelSwitcher, currentChannelID string) *ChannelConfigurator {
	return &ChannelConfigurator{
		store:            store,
		manager:          manager,
		currentChannelID: strings.TrimSpace(currentChannelID),
	}
}

func (c *ChannelConfigurator) ConfigureChannel(ctx context.Context, channelID string, presence mcserver.PresenceState) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}

	trimmedChannelID := strings.TrimSpace(channelID)

	c.mu.Lock()
	defer c.mu.Unlock()

	previousOverride, hadPreviousOverride, err := c.store.Load(ctx)
	if err != nil {
		if !config.IsCorruptRuntimeEmbedChannelStoreError(err) {
			return fmt.Errorf("load runtime embed channel: %w", err)
		}
		previousOverride = ""
		hadPreviousOverride = false
	}

	previousEffectiveChannelID := c.currentChannelID
	if hadPreviousOverride {
		previousEffectiveChannelID = previousOverride
	}

	if err := c.manager.SwitchChannel(ctx, trimmedChannelID, presence); err != nil {
		return fmt.Errorf("switch manager to %q: %w", trimmedChannelID, err)
	}

	if trimmedChannelID == "" {
		if err := c.store.Clear(ctx); err != nil {
			rollbackErr := c.rollback(ctx, previousEffectiveChannelID, previousOverride, hadPreviousOverride, presence)
			if rollbackErr != nil {
				return errors.Join(fmt.Errorf("clear runtime embed channel after switching manager: %w", err), rollbackErr)
			}
			return fmt.Errorf("clear runtime embed channel after switching manager: %w", err)
		}
		c.currentChannelID = ""
		return nil
	}

	if err := c.store.Save(ctx, trimmedChannelID); err != nil {
		rollbackErr := c.rollback(ctx, previousEffectiveChannelID, previousOverride, hadPreviousOverride, presence)
		if rollbackErr != nil {
			return errors.Join(fmt.Errorf("save runtime embed channel after switching manager: %w", err), rollbackErr)
		}
		return fmt.Errorf("save runtime embed channel after switching manager: %w", err)
	}

	c.currentChannelID = trimmedChannelID
	return nil
}

func (c *ChannelConfigurator) rollback(ctx context.Context, previousEffectiveChannelID, previousOverride string, hadPreviousOverride bool, presence mcserver.PresenceState) error {
	var errs []error

	if err := c.manager.SwitchChannel(ctx, previousEffectiveChannelID, presence); err != nil {
		errs = append(errs, fmt.Errorf("restore manager channel to %q: %w", previousEffectiveChannelID, err))
	}

	if hadPreviousOverride {
		if err := c.store.Save(ctx, previousOverride); err != nil {
			errs = append(errs, fmt.Errorf("restore runtime embed channel to %q: %w", previousOverride, err))
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
