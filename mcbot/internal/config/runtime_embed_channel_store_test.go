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

	if err := store.Save(ctx, channelID); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	loadedID, found, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !found {
		t.Fatalf("Load() found = false, want true")
	}
	if loadedID != channelID {
		t.Fatalf("Load() channelID = %q, want %q", loadedID, channelID)
	}

	data, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	want := "{\"embed_channel_id\":\"123456789012345678\"}\n"
	if string(data) != want {
		t.Fatalf("stored JSON = %q, want %q", string(data), want)
	}
}

func TestRuntimeEmbedChannelStore_MissingFile(t *testing.T) {
	store := NewRuntimeEmbedChannelStore(filepath.Join(t.TempDir(), "runtime_embed_channel.json"))
	ctx := context.Background()

	loadedID, found, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if found {
		t.Fatalf("Load() found = true, want false")
	}
	if loadedID != "" {
		t.Fatalf("Load() channelID = %q, want empty", loadedID)
	}

	if err := store.Clear(ctx); err != nil {
		t.Fatalf("Clear() error = %v", err)
	}
}

func TestRuntimeEmbedChannelStore_Clear(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "runtime_embed_channel.json")
	store := NewRuntimeEmbedChannelStore(storePath)
	ctx := context.Background()

	if err := store.Save(ctx, "123456789012345678"); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := store.Clear(ctx); err != nil {
		t.Fatalf("Clear() error = %v", err)
	}

	_, found, err := store.Load(ctx)
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
			err := store.Save(context.Background(), tt.channelID)
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

	if err := store.Save(ctx, "123456789012345678"); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := store.Save(ctx, "223456789012345678"); err != nil {
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

	loadedID, found, err := store.Load(ctx)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !found {
		t.Fatalf("Load() found = false, want true")
	}
	if loadedID != "223456789012345678" {
		t.Fatalf("Load() channelID = %q, want %q", loadedID, "223456789012345678")
	}
}

func TestRuntimeEmbedChannelStore_LoadMarksCorruptData(t *testing.T) {
	tests := []struct {
		name     string
		contents string
	}{
		{name: "invalid JSON", contents: "{not-json"},
		{name: "invalid snowflake", contents: `{"embed_channel_id":"012345678901234567"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			storePath := filepath.Join(t.TempDir(), "runtime_embed_channel.json")
			if err := os.WriteFile(storePath, []byte(tt.contents), 0o644); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}

			_, found, err := NewRuntimeEmbedChannelStore(storePath).Load(context.Background())
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
