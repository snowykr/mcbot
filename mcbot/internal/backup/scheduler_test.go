package backup

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/snowy/mcbot/internal/mcconfig"
)

func TestSchedulerNextRunAndMissedRunSemantics(t *testing.T) {
	policy := mcconfig.Defaults().Backup
	policy.DailyTime = "04:00"
	policy.Timezone = "UTC"
	now := time.Date(2026, 5, 22, 5, 0, 0, 0, time.UTC)
	s, err := NewScheduler(SchedulerOptions{
		Policy:        policy,
		SourceDir:     filepath.Join(t.TempDir(), "source"),
		ConfigPath:    filepath.Join(t.TempDir(), "mc-server.toml"),
		BackupDir:     filepath.Join(t.TempDir(), "backups"),
		Quiescer:      &fakeQuiescer{},
		ServerRunning: func(context.Context) (bool, error) { return true, nil },
		Now:           func() time.Time { return now },
		Logf:          func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("NewScheduler failed: %v", err)
	}
	next, err := s.NextRunAfter(now)
	if err != nil {
		t.Fatalf("NextRunAfter failed: %v", err)
	}
	want := time.Date(2026, 5, 23, 4, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("next = %s, want %s", next, want)
	}
	s.lastSuccessfulLocalDate = "2026-05-22"
	ran, err := s.RunMissedIfDue(context.Background())
	if err != nil {
		t.Fatalf("RunMissedIfDue failed: %v", err)
	}
	if ran {
		t.Fatal("RunMissedIfDue ran despite same-day success")
	}
}

func TestSchedulerNextRunUsesLocalCalendarDayAcrossDST(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load New York location: %v", err)
	}
	now := time.Date(2026, 3, 7, 5, 0, 0, 0, loc)
	next, err := nextDailyRun(now, "04:00", loc)
	if err != nil {
		t.Fatalf("nextDailyRun failed: %v", err)
	}
	want := time.Date(2026, 3, 8, 4, 0, 0, 0, loc)
	if !next.Equal(want) {
		t.Fatalf("next = %s, want %s", next, want)
	}
	if got := next.In(loc); got.Hour() != 4 || got.Minute() != 0 {
		t.Fatalf("next local wall time = %s, want 04:00", got.Format("15:04 MST"))
	}
}

func TestSchedulerRunMissedCreatesQuiescedBackupOnce(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "data", "minecraft")
	configPath := filepath.Join(root, "mc-server.toml")
	backups := filepath.Join(root, "backups")
	writeFile(t, filepath.Join(source, "world", "level.dat"), "level")
	writeFile(t, configPath, mcconfig.Render(mcconfig.Defaults()))
	policy := mcconfig.Defaults().Backup
	policy.DailyTime = "04:00"
	policy.Timezone = "UTC"
	policy.QuiesceTimeoutSeconds = 5
	now := time.Date(2026, 5, 22, 5, 0, 0, 0, time.UTC)
	q := &fakeQuiescer{}
	s, err := NewScheduler(SchedulerOptions{
		Policy:        policy,
		SourceDir:     source,
		ConfigPath:    configPath,
		BackupDir:     backups,
		Quiescer:      q,
		ServerRunning: func(context.Context) (bool, error) { return true, nil },
		Now:           func() time.Time { return now },
		Logf:          func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("NewScheduler failed: %v", err)
	}
	ran, err := s.RunMissedIfDue(context.Background())
	if err != nil {
		t.Fatalf("RunMissedIfDue failed: %v", err)
	}
	if !ran {
		t.Fatal("RunMissedIfDue did not run after scheduled time")
	}
	if got := strings.Join(q.commands, ","); got != "save-off,save-all flush,save-on" {
		t.Fatalf("quiesce commands = %s", got)
	}
	ran, err = s.RunMissedIfDue(context.Background())
	if err != nil {
		t.Fatalf("RunMissedIfDue second failed: %v", err)
	}
	if ran {
		t.Fatal("RunMissedIfDue ran twice for the same local date")
	}
}

func TestSchedulerRunOnceSkipsQuiesceWhenServerStopped(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "data", "minecraft")
	configPath := filepath.Join(root, "mc-server.toml")
	backups := filepath.Join(root, "backups")
	writeFile(t, filepath.Join(source, "world", "level.dat"), "level")
	writeFile(t, configPath, mcconfig.Render(mcconfig.Defaults()))
	policy := mcconfig.Defaults().Backup
	q := &fakeQuiescer{}
	s, err := NewScheduler(SchedulerOptions{
		Policy:        policy,
		SourceDir:     source,
		ConfigPath:    configPath,
		BackupDir:     backups,
		Quiescer:      q,
		ServerRunning: func(context.Context) (bool, error) { return false, nil },
		Now:           time.Now,
		Logf:          func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("NewScheduler failed: %v", err)
	}
	if _, err := s.RunOnce(context.Background(), "scheduled-stopped"); err != nil {
		t.Fatalf("RunOnce failed: %v", err)
	}
	if len(q.commands) != 0 {
		t.Fatalf("quiesce commands = %v, want none when server is stopped", q.commands)
	}
}

func TestSchedulerDisabledDoesNotSleepOrRun(t *testing.T) {
	policy := mcconfig.Defaults().Backup
	policy.Enabled = false
	called := false
	_, err := StartScheduler(context.Background(), SchedulerOptions{
		Policy:     policy,
		SourceDir:  "/tmp/source",
		ConfigPath: "/tmp/mc-server.toml",
		BackupDir:  "/tmp/backups",
		Sleep: func(context.Context, time.Duration) error {
			called = true
			return nil
		},
		Logf: func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("StartScheduler failed: %v", err)
	}
	if called {
		t.Fatal("disabled scheduler called sleep")
	}
}

func TestStartSchedulerEnabledRequiresQuiescerOrServerRunning(t *testing.T) {
	policy := mcconfig.Defaults().Backup
	policy.Enabled = true
	_, err := StartScheduler(context.Background(), SchedulerOptions{
		Policy:     policy,
		SourceDir:  "/tmp/source",
		ConfigPath: "/tmp/mc-server.toml",
		BackupDir:  "/tmp/backups",
		Logf:       func(string, ...any) {},
	})
	if err == nil || !strings.Contains(err.Error(), "requires RCON quiescer or server status checker") {
		t.Fatalf("StartScheduler error = %v, want missing runtime capability failure", err)
	}
}

func TestStartSchedulerEnabledAllowsColdOnlyWithoutQuiescer(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "data", "minecraft")
	configPath := filepath.Join(root, "mc-server.toml")
	backups := filepath.Join(root, "backups")
	writeFile(t, filepath.Join(source, "world", "level.dat"), "level")
	writeFile(t, configPath, mcconfig.Render(mcconfig.Defaults()))
	policy := mcconfig.Defaults().Backup
	loopStarted := make(chan struct{}, 1)
	_, err := StartScheduler(context.Background(), SchedulerOptions{
		Policy:        policy,
		SourceDir:     source,
		ConfigPath:    configPath,
		BackupDir:     backups,
		ServerRunning: func(context.Context) (bool, error) { return false, nil },
		Sleep: func(context.Context, time.Duration) error {
			select {
			case loopStarted <- struct{}{}:
			default:
			}
			return context.Canceled
		},
		Logf: func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("StartScheduler failed: %v", err)
	}
	select {
	case <-loopStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("cold-only scheduler did not start loop")
	}
}

func TestSchedulerRunOnceRequiresQuiescerWhenServerRunning(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "data", "minecraft")
	configPath := filepath.Join(root, "mc-server.toml")
	backups := filepath.Join(root, "backups")
	writeFile(t, filepath.Join(source, "world", "level.dat"), "level")
	writeFile(t, configPath, mcconfig.Render(mcconfig.Defaults()))
	s, err := NewScheduler(SchedulerOptions{
		Policy:        mcconfig.Defaults().Backup,
		SourceDir:     source,
		ConfigPath:    configPath,
		BackupDir:     backups,
		ServerRunning: func(context.Context) (bool, error) { return true, nil },
		Logf:          func(string, ...any) {},
	})
	if err != nil {
		t.Fatalf("NewScheduler failed: %v", err)
	}
	_, err = s.RunOnce(context.Background(), "scheduled-running")
	if err == nil || !strings.Contains(err.Error(), "requires RCON quiescer when server is running") {
		t.Fatalf("RunOnce error = %v, want running-server quiescer failure", err)
	}
}

func TestContainerSchedulerOptionsUsesNarrowContainerMounts(t *testing.T) {
	policy := mcconfig.Defaults().Backup
	opts := ContainerSchedulerOptions(policy, &fakeQuiescer{})
	if opts.SourceDir != filepath.Clean("/app/data/minecraft") {
		t.Fatalf("SourceDir = %q", opts.SourceDir)
	}
	if opts.ConfigPath != filepath.Clean("/app/mc-server.toml") {
		t.Fatalf("ConfigPath = %q", opts.ConfigPath)
	}
	if opts.BackupDir != filepath.Clean("/app/backups") {
		t.Fatalf("BackupDir = %q, want default narrow backup mount", opts.BackupDir)
	}
}
