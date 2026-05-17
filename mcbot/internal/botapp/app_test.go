package botapp

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/snowy/mcbot/internal/composectl"
	"github.com/snowy/mcbot/internal/serverops"
)

func TestExternalStopIntentDataDirUsesRepoDataDirWhenRepoDiscovered(t *testing.T) {
	restore := overrideDiscoverRepoRootForExternalStopIntentForTest(func(string) (composectl.Paths, error) {
		return composectl.DefaultPaths(filepath.Join("/tmp", "mcbot-repo")), nil
	})
	defer restore()

	got := externalStopIntentDataDir()
	want := filepath.Join("/tmp", "mcbot-repo", "data", "mcbot")
	if got != want {
		t.Fatalf("externalStopIntentDataDir() = %q, want %q", got, want)
	}
}

func TestExternalStopIntentDataDirFallsBackToContainerPath(t *testing.T) {
	restore := overrideDiscoverRepoRootForExternalStopIntentForTest(func(string) (composectl.Paths, error) {
		return composectl.Paths{}, errors.New("boom")
	})
	defer restore()

	got := externalStopIntentDataDir()
	want := serverops.BotDataDir()
	if got != want {
		t.Fatalf("externalStopIntentDataDir() = %q, want %q", got, want)
	}
}

func overrideDiscoverRepoRootForExternalStopIntentForTest(fn func(string) (composectl.Paths, error)) func() {
	old := discoverRepoRootForExternalStopIntent
	discoverRepoRootForExternalStopIntent = fn
	return func() {
		discoverRepoRootForExternalStopIntent = old
	}
}
