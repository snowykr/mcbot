package serverops

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWriteStopIntentRoundTripAndLeavesNoTempFiles(t *testing.T) {
	store := NewFileIntentStore(filepath.Join(t.TempDir(), "data", "mcbot"))
	first := NewStopIntent("mc-server", "mc-test", time.Date(2026, 5, 17, 4, 0, 0, 0, time.UTC), 120)
	second := NewStopIntent("mc-server", "mc-test", time.Date(2026, 5, 17, 4, 1, 0, 0, time.UTC), 120)

	if err := store.WriteStopIntent(context.Background(), first); err != nil {
		t.Fatalf("WriteStopIntent(first) failed: %v", err)
	}
	if err := store.WriteStopIntent(context.Background(), second); err != nil {
		t.Fatalf("WriteStopIntent(second) failed: %v", err)
	}

	loaded, found, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if !found {
		t.Fatal("Load found = false, want true")
	}
	if !loaded.CreatedAt.Equal(second.CreatedAt) || !loaded.ExpiresAt.Equal(second.ExpiresAt) {
		t.Fatalf("loaded intent = %+v, want second intent %+v", loaded, second)
	}
	if _, err := os.Stat(store.Path); err != nil {
		t.Fatalf("Stat final intent file failed: %v", err)
	}
	tempMatches, err := filepath.Glob(filepath.Join(filepath.Dir(store.Path), ".operator-intent-*.tmp"))
	if err != nil {
		t.Fatalf("Glob temp files failed: %v", err)
	}
	if len(tempMatches) != 0 {
		t.Fatalf("temp files remain after write: %v", tempMatches)
	}
}

func TestClearStaleStopIntentClearsPreviousLifecycleIntent(t *testing.T) {
	store := NewFileIntentStore(filepath.Join(t.TempDir(), "data", "mcbot"))
	createdAt := time.Date(2026, 5, 17, 4, 0, 0, 0, time.UTC)
	lifecycleStartedAt := createdAt.Add(2 * time.Minute)
	now := lifecycleStartedAt.Add(10 * time.Second)

	if err := store.WriteStopIntent(context.Background(), NewStopIntent("mc-server", "mc-test", createdAt, 120)); err != nil {
		t.Fatalf("WriteStopIntent failed: %v", err)
	}

	cleared, err := store.ClearStaleStopIntent(context.Background(), "mc-test", now, lifecycleStartedAt)
	if err != nil {
		t.Fatalf("ClearStaleStopIntent failed: %v", err)
	}
	if !cleared {
		t.Fatal("ClearStaleStopIntent = false, want true")
	}
	if _, found, err := store.Load(context.Background()); err != nil || found {
		t.Fatalf("Load() found=%v err=%v, want cleared file", found, err)
	}
}

func TestClearStaleStopIntentPreservesCurrentLifecycleIntent(t *testing.T) {
	store := NewFileIntentStore(filepath.Join(t.TempDir(), "data", "mcbot"))
	lifecycleStartedAt := time.Date(2026, 5, 17, 4, 0, 0, 0, time.UTC)
	createdAt := lifecycleStartedAt.Add(2 * time.Second)
	now := createdAt.Add(10 * time.Second)

	if err := store.WriteStopIntent(context.Background(), NewStopIntent("mc-server", "mc-test", createdAt, 120)); err != nil {
		t.Fatalf("WriteStopIntent failed: %v", err)
	}

	cleared, err := store.ClearStaleStopIntent(context.Background(), "mc-test", now, lifecycleStartedAt)
	if err != nil {
		t.Fatalf("ClearStaleStopIntent failed: %v", err)
	}
	if cleared {
		t.Fatal("ClearStaleStopIntent = true, want false")
	}
	matched, err := store.ConsumeUnexpiredStopIntent(context.Background(), "mc-test", now)
	if err != nil {
		t.Fatalf("ConsumeUnexpiredStopIntent failed: %v", err)
	}
	if !matched {
		t.Fatal("ConsumeUnexpiredStopIntent = false, want true")
	}
}

func TestWriteStopIntentCanReplaceExistingFileRepeatedlyPreservesLatestContents(t *testing.T) {
	store := NewFileIntentStore(filepath.Join(t.TempDir(), "data", "mcbot"))
	first := NewStopIntent("mc-server", "first-container", time.Date(2026, 5, 22, 4, 0, 0, 0, time.UTC), 120)
	second := NewStopIntent("mc-server", "second-container", time.Date(2026, 5, 22, 4, 1, 0, 0, time.UTC), 5)

	if err := store.WriteStopIntent(context.Background(), first); err != nil {
		t.Fatalf("WriteStopIntent(first) failed: %v", err)
	}
	if err := store.WriteStopIntent(context.Background(), second); err != nil {
		t.Fatalf("WriteStopIntent(second) failed: %v", err)
	}

	loaded, found, err := store.Load(context.Background())
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if !found {
		t.Fatal("Load found = false, want true")
	}
	if loaded.Container != "second-container" || !loaded.CreatedAt.Equal(second.CreatedAt) || !loaded.ExpiresAt.Equal(second.ExpiresAt) {
		t.Fatalf("loaded intent = %+v, want second intent %+v", loaded, second)
	}
	data, err := os.ReadFile(store.Path)
	if err != nil {
		t.Fatalf("ReadFile intent failed: %v", err)
	}
	contents := string(data)
	if !strings.Contains(contents, "second-container") {
		t.Fatalf("intent file = %q, want second-container", contents)
	}
	if strings.Contains(contents, "first-container") {
		t.Fatalf("intent file still contains stale container: %q", contents)
	}
	assertNoMatches(t, filepath.Join(filepath.Dir(store.Path), ".operator-intent-*.tmp"))
}

func assertNoMatches(t *testing.T, pattern string) {
	t.Helper()
	matches, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatalf("Glob(%q) failed: %v", pattern, err)
	}
	if len(matches) != 0 {
		t.Fatalf("unexpected matches for %q: %v", pattern, matches)
	}
}
