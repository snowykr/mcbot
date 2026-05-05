package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeEmbedChannelStore_RoundTrip(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "nested", "runtime_embed_channel.json")
	store := NewRuntimeEmbedChannelStore(storePath)
	ctx := context.Background()
	channelID := "123456789012345678"

	if err := store.Save(ctx, "123456789012345679", channelID); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loadedSetting, found, err := store.Load(ctx, "123456789012345679")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !found {
		t.Fatalf("Load() found = false, want true")
	}
	if loadedSetting.Mode != RuntimeEmbedChannelModeChannel {
		t.Fatalf("Load() mode = %q, want %q", loadedSetting.Mode, RuntimeEmbedChannelModeChannel)
	}
	if loadedSetting.ChannelID != channelID {
		t.Fatalf("Load() channelID = %q, want %q", loadedSetting.ChannelID, channelID)
	}

	data, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	want := "{\"trusted_guild_id\":\"123456789012345679\",\"mode\":\"channel\",\"embed_channel_id\":\"123456789012345678\"}\n"
	if string(data) != want {
		t.Fatalf("stored JSON = %q, want %q", string(data), want)
	}
}

func TestRuntimeEmbedChannelStore_SaveDisabledRoundTrip(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "nested", "runtime_embed_channel.json")
	store := NewRuntimeEmbedChannelStore(storePath)
	ctx := context.Background()
	guildID := "123456789012345679"

	if err := store.SaveDisabled(ctx, guildID); err != nil {
		t.Fatalf("SaveDisabled() error = %v", err)
	}

	loadedSetting, found, err := store.Load(ctx, guildID)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !found {
		t.Fatalf("Load() found = false, want true")
	}
	if loadedSetting.Mode != RuntimeEmbedChannelModeDisabled {
		t.Fatalf("Load() mode = %q, want %q", loadedSetting.Mode, RuntimeEmbedChannelModeDisabled)
	}
	if loadedSetting.ChannelID != "" {
		t.Fatalf("Load() channelID = %q, want empty", loadedSetting.ChannelID)
	}

	data, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	want := "{\"trusted_guild_id\":\"123456789012345679\",\"mode\":\"disabled\"}\n"
	if string(data) != want {
		t.Fatalf("stored JSON = %q, want %q", string(data), want)
	}
}

func TestRuntimeEmbedChannelStore_MissingFile(t *testing.T) {
	store := NewRuntimeEmbedChannelStore(filepath.Join(t.TempDir(), "runtime_embed_channel.json"))
	ctx := context.Background()

	loadedSetting, found, err := store.Load(ctx, "123456789012345679")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if found {
		t.Fatalf("Load() found = true, want false")
	}
	if loadedSetting != (RuntimeEmbedChannelSetting{}) {
		t.Fatalf("Load() setting = %#v, want empty", loadedSetting)
	}

	if err := store.Clear(ctx); err != nil {
		t.Fatalf("Clear() error = %v", err)
	}
}

func TestRuntimeEmbedChannelStore_Clear(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "runtime_embed_channel.json")
	store := NewRuntimeEmbedChannelStore(storePath)
	ctx := context.Background()

	if err := store.Save(ctx, "123456789012345679", "123456789012345678"); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := store.Clear(ctx); err != nil {
		t.Fatalf("Clear() error = %v", err)
	}

	_, found, err := store.Load(ctx, "123456789012345679")
	if err != nil {
		t.Fatalf("Load() after Clear() error = %v", err)
	}
	if found {
		t.Fatalf("Load() after Clear() found = true, want false")
	}

	if err := store.Clear(ctx); err != nil {
		t.Fatalf("Clear() on missing file error = %v", err)
	}
}

func TestRuntimeEmbedChannelStore_RejectsInvalidSnowflake(t *testing.T) {
	tests := []struct {
		name      string
		channelID string
		wantErr   string
	}{
		{name: "NonNumeric", channelID: "abc123", wantErr: "digits only"},
		{name: "Zero", channelID: "0", wantErr: "non-zero Discord snowflake ID"},
		{name: "LeadingZero", channelID: "012345678901234567", wantErr: "canonical Discord snowflake ID"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := NewRuntimeEmbedChannelStore(filepath.Join(t.TempDir(), "runtime_embed_channel.json"))
			err := store.Save(context.Background(), "123456789012345679", tt.channelID)
			if err == nil {
				t.Fatalf("Save() error = nil, want non-nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Save() error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestRuntimeEmbedChannelStore_AtomicWrite(t *testing.T) {
	baseDir := t.TempDir()
	storePath := filepath.Join(baseDir, "nested", "runtime_embed_channel.json")
	store := NewRuntimeEmbedChannelStore(storePath)
	ctx := context.Background()

	if err := store.Save(ctx, "123456789012345679", "123456789012345678"); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := store.Save(ctx, "123456789012345679", "223456789012345678"); err != nil {
		t.Fatalf("second Save() error = %v", err)
	}

	entries, err := os.ReadDir(filepath.Dir(storePath))
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("ReadDir() entries = %d, want 1", len(entries))
	}
	if entries[0].Name() != filepath.Base(storePath) {
		t.Fatalf("ReadDir() entry = %q, want %q", entries[0].Name(), filepath.Base(storePath))
	}

	loadedSetting, found, err := store.Load(ctx, "123456789012345679")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !found {
		t.Fatalf("Load() found = false, want true")
	}
	if loadedSetting.Mode != RuntimeEmbedChannelModeChannel {
		t.Fatalf("Load() mode = %q, want %q", loadedSetting.Mode, RuntimeEmbedChannelModeChannel)
	}
	if loadedSetting.ChannelID != "223456789012345678" {
		t.Fatalf("Load() channelID = %q, want %q", loadedSetting.ChannelID, "223456789012345678")
	}
}

func TestRuntimeEmbedChannelStore_LoadMarksCorruptData(t *testing.T) {
	tests := []struct {
		name     string
		contents string
	}{
		{name: "invalid JSON", contents: "{not-json"},
		{name: "invalid snowflake", contents: `{"trusted_guild_id":"123456789012345679","embed_channel_id":"012345678901234567"}`},
		{name: "unknown mode", contents: `{"trusted_guild_id":"123456789012345679","mode":"surprise"}`},
		{name: "disabled with channel", contents: `{"trusted_guild_id":"123456789012345679","mode":"disabled","embed_channel_id":"123456789012345678"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storePath := filepath.Join(t.TempDir(), "runtime_embed_channel.json")
			if err := os.WriteFile(storePath, []byte(tt.contents), 0o644); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}

			_, found, err := NewRuntimeEmbedChannelStore(storePath).Load(context.Background(), "123456789012345679")
			if err == nil {
				t.Fatal("Load() error = nil, want non-nil")
			}
			if found {
				t.Fatal("Load() found = true, want false")
			}
			if !errors.Is(err, ErrCorruptRuntimeEmbedChannelStore) {
				t.Fatalf("Load() error = %v, want ErrCorruptRuntimeEmbedChannelStore", err)
			}
		})
	}
}

func TestRuntimeEmbedChannelStore_LoadMarksStaleGuildScopedData(t *testing.T) {
	tests := []struct {
		name     string
		contents string
	}{
		{name: "legacy unscoped record", contents: `{"embed_channel_id":"123456789012345678"}`},
		{name: "different trusted guild", contents: `{"trusted_guild_id":"223456789012345678","embed_channel_id":"123456789012345678"}`},
		{name: "different trusted guild disabled", contents: `{"trusted_guild_id":"223456789012345678","mode":"disabled"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storePath := filepath.Join(t.TempDir(), "runtime_embed_channel.json")
			if err := os.WriteFile(storePath, []byte(tt.contents), 0o644); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}

			_, found, err := NewRuntimeEmbedChannelStore(storePath).Load(context.Background(), "123456789012345679")
			if err == nil {
				t.Fatal("Load() error = nil, want non-nil")
			}
			if found {
				t.Fatal("Load() found = true, want false")
			}
			if !errors.Is(err, ErrStaleRuntimeEmbedChannelStore) {
				t.Fatalf("Load() error = %v, want ErrStaleRuntimeEmbedChannelStore", err)
			}
		})
	}
}
