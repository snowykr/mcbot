package backup

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"sync"
	"time"

	"github.com/snowy/mcbot/internal/mcconfig"
)

type SchedulerOptions struct {
	Policy        mcconfig.BackupConfig
	SourceDir     string
	ConfigPath    string
	BackupDir     string
	Quiescer      Quiescer
	ServerRunning func(ctx context.Context) (bool, error)
	Now           func() time.Time
	Sleep         func(context.Context, time.Duration) error
	Logf          func(string, ...any)
}

type Scheduler struct {
	opts                    SchedulerOptions
	loc                     *time.Location
	mu                      sync.Mutex
	lastSuccessfulLocalDate string
}

func StartScheduler(ctx context.Context, opts SchedulerOptions) (*Scheduler, error) {
	s, err := NewScheduler(opts)
	if err != nil {
		return nil, err
	}
	if !opts.Policy.Enabled {
		s.logf("[BACKUP] scheduler disabled by mc-server.toml")
		return s, nil
	}
	if err := validateSchedulerRuntime(opts); err != nil {
		return nil, err
	}
	go s.loop(ctx)
	return s, nil
}

func NewScheduler(opts SchedulerOptions) (*Scheduler, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Sleep == nil {
		opts.Sleep = sleepContext
	}
	if opts.Logf == nil {
		opts.Logf = log.Printf
	}
	if opts.BackupDir == "" {
		opts.BackupDir = opts.Policy.Directory
	}
	if opts.SourceDir == "" || opts.ConfigPath == "" || opts.BackupDir == "" {
		return nil, fmt.Errorf("backup scheduler paths must not be empty")
	}
	loc, err := schedulerLocation(opts.Policy.Timezone)
	if err != nil {
		return nil, err
	}
	if _, err := parseDailyTime(opts.Policy.DailyTime, loc, opts.Now()); err != nil {
		return nil, err
	}
	return &Scheduler{opts: opts, loc: loc}, nil
}

func (s *Scheduler) RunOnce(ctx context.Context, reason string) (CreateResult, error) {
	if !s.opts.Policy.Enabled {
		return CreateResult{}, fmt.Errorf("backup scheduler disabled")
	}
	stoppedProven := s.serverStoppedProven(ctx)
	if !stoppedProven && s.opts.Quiescer == nil {
		return CreateResult{}, fmt.Errorf("backup scheduler requires RCON quiescer when server is running")
	}
	result, err := Create(ctx, CreateOptions{
		SourceDir:     s.opts.SourceDir,
		ConfigPath:    s.opts.ConfigPath,
		BackupDir:     s.opts.BackupDir,
		Policy:        s.opts.Policy,
		CreatedBy:     "bot-auto",
		Reason:        reason,
		StoppedProven: stoppedProven,
		Quiescer:      s.opts.Quiescer,
		Now:           s.opts.Now,
	})
	if err != nil {
		if result.BackupID != "" {
			s.logf("[BACKUP] automatic backup created id=%s but retention failed: %v", result.BackupID, err)
		} else {
			s.logf("[BACKUP] automatic backup failed: %v", err)
			return CreateResult{}, err
		}
	}
	localDate := result.Manifest.CreatedAt.In(s.loc).Format("2006-01-02")
	s.mu.Lock()
	s.lastSuccessfulLocalDate = localDate
	s.mu.Unlock()
	s.logf("[BACKUP] automatic backup succeeded id=%s path=%s", result.BackupID, result.ArchivePath)
	return result, nil
}

func (s *Scheduler) RunMissedIfDue(ctx context.Context) (bool, error) {
	now := s.opts.Now().In(s.loc)
	scheduled, err := parseDailyTime(s.opts.Policy.DailyTime, s.loc, now)
	if err != nil {
		return false, err
	}
	if now.Before(scheduled) {
		return false, nil
	}
	if s.successfulBackupExistsForLocalDate(now) {
		return false, nil
	}
	if _, err := s.RunOnce(ctx, "scheduled-missed-startup"); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Scheduler) NextRunAfter(now time.Time) (time.Time, error) {
	return nextDailyRun(now.In(s.loc), s.opts.Policy.DailyTime, s.loc)
}

func (s *Scheduler) successfulBackupExistsForLocalDate(now time.Time) bool {
	date := now.In(s.loc).Format("2006-01-02")
	s.mu.Lock()
	if s.lastSuccessfulLocalDate == date {
		s.mu.Unlock()
		return true
	}
	s.mu.Unlock()
	listed, err := List(context.Background(), s.opts.BackupDir)
	if err != nil {
		s.logf("[BACKUP] could not inspect existing backups for missed-run check: %v", err)
		return false
	}
	for _, item := range listed.Valid {
		if isSafetyBackupReason(item.Reason) {
			continue
		}
		if item.CreatedAt.In(s.loc).Format("2006-01-02") == date {
			return true
		}
	}
	return false
}

func (s *Scheduler) loop(ctx context.Context) {
	s.logf("[BACKUP] scheduler active source=%s backups=%s time=%s timezone=%s", s.opts.SourceDir, s.opts.BackupDir, s.opts.Policy.DailyTime, s.opts.Policy.Timezone)
	if ran, err := s.RunMissedIfDue(ctx); err != nil {
		s.logf("[BACKUP] missed startup backup failed: %v", err)
	} else if ran {
		s.logf("[BACKUP] missed startup backup completed")
	}
	for {
		next, err := s.NextRunAfter(s.opts.Now())
		if err != nil {
			s.logf("[BACKUP] scheduler time calculation failed: %v", err)
			return
		}
		wait := time.Until(next)
		if s.opts.Now != nil {
			wait = next.Sub(s.opts.Now().In(s.loc))
		}
		if wait < 0 {
			wait = 0
		}
		s.logf("[BACKUP] next automatic backup at %s", next.Format(time.RFC3339))
		if err := s.opts.Sleep(ctx, wait); err != nil {
			s.logf("[BACKUP] scheduler stopped")
			return
		}
		if _, err := s.RunOnce(ctx, "scheduled-daily"); err != nil {
			continue
		}
	}
}

func (s *Scheduler) logf(format string, args ...any) {
	s.opts.Logf(format, args...)
}

func validateSchedulerRuntime(opts SchedulerOptions) error {
	if opts.Quiescer != nil || opts.ServerRunning != nil {
		return nil
	}
	return fmt.Errorf("backup scheduler requires RCON quiescer or server status checker")
}

func (s *Scheduler) serverStoppedProven(ctx context.Context) bool {
	if s.opts.ServerRunning == nil {
		return false
	}
	running, err := s.opts.ServerRunning(ctx)
	if err != nil {
		s.logf("[BACKUP] server state check failed, requiring RCON quiesce: %v", err)
		return false
	}
	return !running
}

func schedulerLocation(name string) (*time.Location, error) {
	if name == "" || name == "Local" {
		return time.Local, nil
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("load backup timezone %q: %w", name, err)
	}
	return loc, nil
}

func parseDailyTime(value string, loc *time.Location, base time.Time) (time.Time, error) {
	parsed, err := time.Parse("15:04", value)
	if err != nil || len(value) != len("04:00") {
		return time.Time{}, fmt.Errorf("backup.daily_time must use strict HH:MM format")
	}
	local := base.In(loc)
	return time.Date(local.Year(), local.Month(), local.Day(), parsed.Hour(), parsed.Minute(), 0, 0, loc), nil
}

func nextDailyRun(now time.Time, dailyTime string, loc *time.Location) (time.Time, error) {
	today, err := parseDailyTime(dailyTime, loc, now)
	if err != nil {
		return time.Time{}, err
	}
	if now.Before(today) {
		return today, nil
	}
	tomorrow := now.In(loc).AddDate(0, 0, 1)
	return parseDailyTime(dailyTime, loc, tomorrow)
}

func sleepContext(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func ContainerSchedulerOptions(policy mcconfig.BackupConfig, quiescer Quiescer) SchedulerOptions {
	return SchedulerOptions{
		Policy:     policy,
		SourceDir:  filepath.Clean("/app/data/minecraft"),
		ConfigPath: filepath.Clean("/app/mc-server.toml"),
		BackupDir:  filepath.Clean("/app/backups"),
		Quiescer:   quiescer,
	}
}
