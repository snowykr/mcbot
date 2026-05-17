package serverops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	operatorIntentFileName = "operator-intent.json"
	StopIntentBuffer       = 30 * time.Second
)

type StopIntent struct {
	Service   string    `json:"service"`
	Container string    `json:"container"`
	Reason    string    `json:"reason"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

type IntentStore interface {
	WriteStopIntent(ctx context.Context, intent StopIntent) error
	Clear(ctx context.Context) error
	Load(ctx context.Context) (StopIntent, bool, error)
	ClearStaleStopIntent(ctx context.Context, container string, now, lifecycleStartedAt time.Time) (bool, error)
	HasUnexpiredStopIntent(ctx context.Context, container string, now time.Time) (bool, error)
	ConsumeUnexpiredStopIntent(ctx context.Context, container string, now time.Time) (bool, error)
}

type FileIntentStore struct {
	Path string
}

func CLIDataDir(repoRoot string) string {
	return filepath.Join(repoRoot, "data", "mcbot")
}

func BotDataDir() string {
	return filepath.Join("/app/data/mcbot")
}

func IntentPath(dataDir string) string {
	return filepath.Join(dataDir, operatorIntentFileName)
}

func NewFileIntentStore(dataDir string) FileIntentStore {
	return FileIntentStore{Path: IntentPath(dataDir)}
}

func (s FileIntentStore) WriteStopIntent(_ context.Context, intent StopIntent) error {
	if s.Path == "" {
		return fmt.Errorf("operator intent path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return fmt.Errorf("create operator intent directory: %w", err)
	}
	data, err := json.MarshalIndent(intent, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal operator intent: %w", err)
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(s.Path), ".operator-intent-*.tmp")
	if err != nil {
		return fmt.Errorf("create operator intent temp file: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write operator intent temp file: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod operator intent temp file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync operator intent temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close operator intent temp file: %w", err)
	}
	if err := os.Rename(tmpPath, s.Path); err != nil {
		return fmt.Errorf("replace operator intent file: %w", err)
	}
	if dir, err := os.Open(filepath.Dir(s.Path)); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

func (s FileIntentStore) Clear(_ context.Context) error {
	if s.Path == "" {
		return nil
	}
	if err := os.Remove(s.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("clear operator intent file: %w", err)
	}
	return nil
}

func (s FileIntentStore) Load(_ context.Context) (StopIntent, bool, error) {
	data, err := os.ReadFile(s.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return StopIntent{}, false, nil
		}
		return StopIntent{}, false, fmt.Errorf("read operator intent file: %w", err)
	}
	var intent StopIntent
	if err := json.Unmarshal(data, &intent); err != nil {
		return StopIntent{}, false, fmt.Errorf("parse operator intent file: %w", err)
	}
	return intent, true, nil
}

func (s FileIntentStore) HasUnexpiredStopIntent(ctx context.Context, container string, now time.Time) (bool, error) {
	intent, found, err := s.Load(ctx)
	if err != nil || !found {
		return false, err
	}
	if intent.Container != container || intent.Reason != "cli_stop" {
		return false, nil
	}
	return now.Before(intent.ExpiresAt), nil
}

func (s FileIntentStore) ClearStaleStopIntent(ctx context.Context, container string, now, lifecycleStartedAt time.Time) (bool, error) {
	if lifecycleStartedAt.IsZero() {
		return false, nil
	}
	intent, found, err := s.Load(ctx)
	if err != nil || !found {
		return false, err
	}
	if intent.Container != container || intent.Reason != "cli_stop" {
		return false, nil
	}
	if !now.Before(intent.ExpiresAt) || !intent.CreatedAt.Before(lifecycleStartedAt) {
		return false, nil
	}
	if err := s.Clear(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func (s FileIntentStore) ConsumeUnexpiredStopIntent(ctx context.Context, container string, now time.Time) (bool, error) {
	matched, err := s.HasUnexpiredStopIntent(ctx, container, now)
	if err != nil || !matched {
		return false, err
	}
	if err := s.Clear(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func NewStopIntent(service, container string, now time.Time, stopTimeoutSeconds int) StopIntent {
	ttl := time.Duration(stopTimeoutSeconds)*time.Second + StopIntentBuffer
	return StopIntent{
		Service:   service,
		Container: container,
		Reason:    "cli_stop",
		CreatedAt: now,
		ExpiresAt: now.Add(ttl),
	}
}
