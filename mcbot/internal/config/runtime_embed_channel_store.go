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

var ErrCorruptRuntimeEmbedChannelStore = errors.New("corrupt runtime embed channel store")

type RuntimeEmbedChannelStore struct {
	path string
}

func NewRuntimeEmbedChannelStore(path string) *RuntimeEmbedChannelStore {
	return &RuntimeEmbedChannelStore{path: path}
}

func (s *RuntimeEmbedChannelStore) Load(ctx context.Context) (channelID string, found bool, err error) {
	if err := ctx.Err(); err != nil {
		return "", false, err
	}

	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read runtime embed channel store: %w", err)
	}

	var record runtimeEmbedChannelRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return "", false, fmt.Errorf("%w: decode runtime embed channel store: %v", ErrCorruptRuntimeEmbedChannelStore, err)
	}
	if err := validateRuntimeEmbedChannelID(record.EmbedChannelID); err != nil {
		return "", false, fmt.Errorf("%w: invalid runtime embed channel store: %v", ErrCorruptRuntimeEmbedChannelStore, err)
	}

	return record.EmbedChannelID, true, nil
}

func (s *RuntimeEmbedChannelStore) Save(ctx context.Context, channelID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateRuntimeEmbedChannelID(channelID); err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create runtime embed channel store directory: %w", err)
	}

	data, err := json.Marshal(runtimeEmbedChannelRecord{EmbedChannelID: channelID})
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
	EmbedChannelID string `json:"embed_channel_id"`
}

func validateRuntimeEmbedChannelID(value string) error {
	trimmedValue := strings.TrimSpace(value)
	if trimmedValue == "" {
		return fmt.Errorf("embed_channel_id is required")
	}
	if value != trimmedValue {
		return fmt.Errorf("embed_channel_id must not have leading or trailing whitespace")
	}
	parsedValue, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return fmt.Errorf("embed_channel_id must be a Discord snowflake ID (digits only)")
	}
	if strconv.FormatUint(parsedValue, 10) != value {
		return fmt.Errorf("embed_channel_id must be a canonical Discord snowflake ID (no leading zeros)")
	}
	if parsedValue == 0 {
		return fmt.Errorf("embed_channel_id must be a non-zero Discord snowflake ID")
	}
	return nil
}

func IsCorruptRuntimeEmbedChannelStoreError(err error) bool {
	return errors.Is(err, ErrCorruptRuntimeEmbedChannelStore)
}
