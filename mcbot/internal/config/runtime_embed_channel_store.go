package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var (
	ErrCorruptRuntimeEmbedChannelStore = errors.New("corrupt runtime embed channel store")
	ErrStaleRuntimeEmbedChannelStore   = errors.New("stale runtime embed channel store")
)

type RuntimeEmbedChannelMode string

const (
	RuntimeEmbedChannelModeChannel  RuntimeEmbedChannelMode = "channel"
	RuntimeEmbedChannelModeDisabled RuntimeEmbedChannelMode = "disabled"
)

type RuntimeEmbedChannelSetting struct {
	Mode      RuntimeEmbedChannelMode
	ChannelID string
}

type RuntimeEmbedChannelStore struct {
	path string
}

func NewRuntimeEmbedChannelStore(path string) *RuntimeEmbedChannelStore {
	return &RuntimeEmbedChannelStore{path: path}
}

func (s *RuntimeEmbedChannelStore) Load(ctx context.Context, trustedGuildID string) (setting RuntimeEmbedChannelSetting, found bool, err error) {
	if err := ctx.Err(); err != nil {
		return RuntimeEmbedChannelSetting{}, false, err
	}

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return RuntimeEmbedChannelSetting{}, false, nil
		}
		return RuntimeEmbedChannelSetting{}, false, fmt.Errorf("read runtime embed channel store: %w", err)
	}

	var record runtimeEmbedChannelRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return RuntimeEmbedChannelSetting{}, false, fmt.Errorf("%w: decode runtime embed channel store: %v", ErrCorruptRuntimeEmbedChannelStore, err)
	}
	if strings.TrimSpace(record.TrustedGuildID) == "" {
		return RuntimeEmbedChannelSetting{}, false, fmt.Errorf("%w: runtime embed channel store is not scoped to a trusted guild", ErrStaleRuntimeEmbedChannelStore)
	}
	if err := validateRuntimeSnowflakeID("trusted_guild_id", record.TrustedGuildID); err != nil {
		return RuntimeEmbedChannelSetting{}, false, fmt.Errorf("%w: invalid runtime embed channel store: %v", ErrCorruptRuntimeEmbedChannelStore, err)
	}
	if err := validateRuntimeSnowflakeID("current trusted_guild_id", trustedGuildID); err != nil {
		return RuntimeEmbedChannelSetting{}, false, fmt.Errorf("invalid current trusted guild: %w", err)
	}
	if record.TrustedGuildID != trustedGuildID {
		return RuntimeEmbedChannelSetting{}, false, fmt.Errorf("%w: stored trusted_guild_id %s does not match current trusted_guild_id %s", ErrStaleRuntimeEmbedChannelStore, record.TrustedGuildID, trustedGuildID)
	}

	mode := RuntimeEmbedChannelMode(strings.TrimSpace(record.Mode))
	if mode == "" {
		mode = RuntimeEmbedChannelModeChannel
	}

	switch mode {
	case RuntimeEmbedChannelModeChannel:
		if err := validateRuntimeSnowflakeID("embed_channel_id", record.EmbedChannelID); err != nil {
			return RuntimeEmbedChannelSetting{}, false, fmt.Errorf("%w: invalid runtime embed channel store: %v", ErrCorruptRuntimeEmbedChannelStore, err)
		}
		return RuntimeEmbedChannelSetting{Mode: RuntimeEmbedChannelModeChannel, ChannelID: record.EmbedChannelID}, true, nil
	case RuntimeEmbedChannelModeDisabled:
		if strings.TrimSpace(record.EmbedChannelID) != "" {
			return RuntimeEmbedChannelSetting{}, false, fmt.Errorf("%w: disabled runtime embed channel store must not include embed_channel_id", ErrCorruptRuntimeEmbedChannelStore)
		}
		return RuntimeEmbedChannelSetting{Mode: RuntimeEmbedChannelModeDisabled}, true, nil
	default:
		return RuntimeEmbedChannelSetting{}, false, fmt.Errorf("%w: unsupported runtime embed channel mode %q", ErrCorruptRuntimeEmbedChannelStore, record.Mode)
	}
}

func (s *RuntimeEmbedChannelStore) Save(ctx context.Context, trustedGuildID, channelID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateRuntimeSnowflakeID("trusted_guild_id", trustedGuildID); err != nil {
		return err
	}
	if err := validateRuntimeSnowflakeID("embed_channel_id", channelID); err != nil {
		return err
	}

	return s.writeRecord(runtimeEmbedChannelRecord{
		TrustedGuildID: trustedGuildID,
		Mode:           string(RuntimeEmbedChannelModeChannel),
		EmbedChannelID: channelID,
	})
}

func (s *RuntimeEmbedChannelStore) SaveDisabled(ctx context.Context, trustedGuildID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateRuntimeSnowflakeID("trusted_guild_id", trustedGuildID); err != nil {
		return err
	}

	return s.writeRecord(runtimeEmbedChannelRecord{
		TrustedGuildID: trustedGuildID,
		Mode:           string(RuntimeEmbedChannelModeDisabled),
	})
}

func (s *RuntimeEmbedChannelStore) writeRecord(record runtimeEmbedChannelRecord) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create runtime embed channel store directory: %w", err)
	}

	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("encode runtime embed channel store: %w", err)
	}
	data = append(data, '\n')

	tempFile, err := os.CreateTemp(filepath.Dir(s.path), ".runtime-embed-channel-*.tmp")
	if err != nil {
		return fmt.Errorf("create runtime embed channel store temp file: %w", err)
	}
	tempPath := tempFile.Name()
	defer func() {
		_ = tempFile.Close()
		_ = os.Remove(tempPath)
	}()

	if _, err := tempFile.Write(data); err != nil {
		return fmt.Errorf("write runtime embed channel store temp file: %w", err)
	}
	if err := tempFile.Sync(); err != nil {
		return fmt.Errorf("sync runtime embed channel store temp file: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("close runtime embed channel store temp file: %w", err)
	}

	if err := os.Rename(tempPath, s.path); err != nil {
		return fmt.Errorf("replace runtime embed channel store: %w", err)
	}

	if dir, err := os.Open(filepath.Dir(s.path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}

	return nil
}

func (s *RuntimeEmbedChannelStore) Clear(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Remove(s.path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("clear runtime embed channel store: %w", err)
	}
	return nil
}

type runtimeEmbedChannelRecord struct {
	TrustedGuildID string `json:"trusted_guild_id"`
	Mode           string `json:"mode,omitempty"`
	EmbedChannelID string `json:"embed_channel_id,omitempty"`
}

func validateRuntimeSnowflakeID(field, value string) error {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return fmt.Errorf("%s is required", field)
	}
	if value != trimmedValue {
		return fmt.Errorf("%s must not have leading or trailing whitespace", field)
	}
	parsedValue, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return fmt.Errorf("%s must be a Discord snowflake ID (digits only)", field)
	}
	if strconv.FormatUint(parsedValue, 10) != value {
		return fmt.Errorf("%s must be a canonical Discord snowflake ID (no leading zeros)", field)
	}
	if parsedValue == 0 {
		return fmt.Errorf("%s must be a non-zero Discord snowflake ID", field)
	}
	return nil
}

func IsCorruptRuntimeEmbedChannelStoreError(err error) bool {
	return errors.Is(err, ErrCorruptRuntimeEmbedChannelStore)
}

func IsStaleRuntimeEmbedChannelStoreError(err error) bool {
	return errors.Is(err, ErrStaleRuntimeEmbedChannelStore)
}
