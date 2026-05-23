package backup

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/snowy/mcbot/internal/mcconfig"
)

type PruneOptions struct {
	BackupDir            string
	Policy               mcconfig.BackupConfig
	DryRun               bool
	DeleteAfterDays      *int
	MaxTotalSizeBytes    *int64
	Now                  func() time.Time
	AssumeLockHeld       bool
	IgnoreActiveBackupID string
}

type PruneResult struct {
	Candidates     []BackupSummary `json:"candidates"`
	Retained       []BackupSummary `json:"retained"`
	Deleted        []BackupSummary `json:"deleted"`
	Protected      []BackupSummary `json:"protected"`
	ReclaimedBytes int64           `json:"reclaimed_bytes"`
	DryRun         bool            `json:"dry_run"`
}

type BackupSummary struct {
	BackupID    string    `json:"backup_id"`
	ArchivePath string    `json:"archive_path"`
	CreatedAt   time.Time `json:"created_at"`
	SizeBytes   int64     `json:"size_bytes"`
	Reason      string    `json:"reason,omitempty"`
	Protected   bool      `json:"protected,omitempty"`
}

func List(ctx context.Context, backupDir string) ([]BackupSummary, error) {
	_ = ctx
	entries, err := verifiedBackups(backupDir)
	if err != nil {
		return nil, err
	}
	out := make([]BackupSummary, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry.summary)
	}
	return out, nil
}

func Prune(ctx context.Context, opts PruneOptions) (PruneResult, error) {
	_ = ctx
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Policy.RetentionCount < 1 {
		return PruneResult{}, fmt.Errorf("backup.retention_count must be at least 1")
	}
	if !opts.AssumeLockHeld {
		lock, err := AcquireLock(opts.BackupDir, LockMetadata{
			Operation: "prune",
			Owner:     "cli",
			CreatedAt: opts.Now().UTC(),
			StaleAt:   opts.Now().UTC().Add(DefaultStaleTimeout),
			Status:    "pruning backups",
		})
		if err != nil {
			return PruneResult{}, err
		}
		defer lock.Release()
	}
	entries, err := verifiedBackups(opts.BackupDir)
	if err != nil {
		return PruneResult{}, err
	}
	active := ActiveLockBackupIDs(opts.BackupDir)
	neverDelete := map[string]struct{}{}
	if opts.IgnoreActiveBackupID != "" {
		delete(active, opts.IgnoreActiveBackupID)
		neverDelete[opts.IgnoreActiveBackupID] = struct{}{}
	}
	var result PruneResult
	result.DryRun = opts.DryRun
	var candidates []verifiedBackup
	for _, entry := range entries {
		if isProtectedBackup(entry.manifest, active) {
			s := entry.summary
			s.Protected = true
			result.Protected = append(result.Protected, s)
			continue
		}
		candidates = append(candidates, entry)
		result.Candidates = append(result.Candidates, entry.summary)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if !candidates[i].manifest.CreatedAt.Equal(candidates[j].manifest.CreatedAt) {
			return candidates[i].manifest.CreatedAt.After(candidates[j].manifest.CreatedAt)
		}
		return candidates[i].manifest.BackupID > candidates[j].manifest.BackupID
	})

	toDelete := map[string]verifiedBackup{}
	markDelete := func(entry verifiedBackup) {
		if _, ok := neverDelete[entry.manifest.BackupID]; ok {
			return
		}
		toDelete[entry.manifest.BackupID] = entry
	}
	for i, entry := range candidates {
		if i >= opts.Policy.RetentionCount {
			markDelete(entry)
		}
	}
	retentionDays := opts.Policy.RetentionDays
	if opts.DeleteAfterDays != nil {
		retentionDays = *opts.DeleteAfterDays
	}
	if retentionDays > 0 {
		cutoff := opts.Now().Add(-time.Duration(retentionDays) * 24 * time.Hour)
		for _, entry := range candidates {
			if entry.manifest.CreatedAt.Before(cutoff) {
				markDelete(entry)
			}
		}
	}
	maxTotal := opts.Policy.RetentionMaxBytes
	if opts.MaxTotalSizeBytes != nil {
		maxTotal = *opts.MaxTotalSizeBytes
	}
	if maxTotal > 0 {
		var total int64
		for _, entry := range candidates {
			total += entry.summary.SizeBytes
		}
		for i := len(candidates) - 1; i >= 0 && total > maxTotal; i-- {
			entry := candidates[i]
			if _, ok := neverDelete[entry.manifest.BackupID]; ok {
				continue
			}
			markDelete(entry)
			total -= entry.summary.SizeBytes
		}
	}
	for _, entry := range candidates {
		if doomed, ok := toDelete[entry.manifest.BackupID]; ok {
			result.Deleted = append(result.Deleted, doomed.summary)
			result.ReclaimedBytes += doomed.summary.SizeBytes
			if !opts.DryRun {
				info, err := os.Lstat(doomed.summary.ArchivePath)
				if err != nil {
					return result, fmt.Errorf("stat backup %s before delete: %w", doomed.manifest.BackupID, err)
				}
				if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
					return result, fmt.Errorf("refuse to delete non-regular backup candidate %s", doomed.summary.ArchivePath)
				}
				if err := os.Remove(doomed.summary.ArchivePath); err != nil {
					return result, fmt.Errorf("delete backup %s: %w", doomed.manifest.BackupID, err)
				}
			}
			continue
		}
		result.Retained = append(result.Retained, entry.summary)
	}
	sortSummaries(result.Deleted)
	sortSummaries(result.Retained)
	sortSummaries(result.Protected)
	return result, nil
}

type verifiedBackup struct {
	manifest Manifest
	summary  BackupSummary
}

func verifiedBackups(backupDir string) ([]verifiedBackup, error) {
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read backup directory: %w", err)
	}
	var backups []verifiedBackup
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".tar.gz") {
			continue
		}
		if !validBackupID(strings.TrimSuffix(entry.Name(), ".tar.gz")) {
			continue
		}
		path := filepath.Join(backupDir, entry.Name())
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			continue
		}
		validation, err := ValidateArchive(path)
		if err != nil {
			continue
		}
		backups = append(backups, verifiedBackup{
			manifest: validation.Manifest,
			summary: BackupSummary{
				BackupID:    validation.Manifest.BackupID,
				ArchivePath: path,
				CreatedAt:   validation.Manifest.CreatedAt,
				SizeBytes:   validation.ArchiveBytes,
				Reason:      validation.Manifest.BackupReason,
			},
		})
	}
	sort.Slice(backups, func(i, j int) bool {
		if !backups[i].summary.CreatedAt.Equal(backups[j].summary.CreatedAt) {
			return backups[i].summary.CreatedAt.After(backups[j].summary.CreatedAt)
		}
		return backups[i].summary.BackupID > backups[j].summary.BackupID
	})
	return backups, nil
}

func isProtectedBackup(manifest Manifest, active map[string]struct{}) bool {
	if _, ok := active[manifest.BackupID]; ok {
		return true
	}
	reason := strings.ToLower(manifest.BackupReason)
	return strings.Contains(reason, "restore-safety") || strings.Contains(reason, "safety")
}

func sortSummaries(items []BackupSummary) {
	sort.Slice(items, func(i, j int) bool {
		if !items[i].CreatedAt.Equal(items[j].CreatedAt) {
			return items[i].CreatedAt.After(items[j].CreatedAt)
		}
		return items[i].BackupID > items[j].BackupID
	})
}
