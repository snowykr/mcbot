package backup

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const lockFileName = "mcbot-backup.lock"

type LockMetadata struct {
	Operation string    `json:"operation"`
	Owner     string    `json:"owner"`
	BackupID  string    `json:"backup_id,omitempty"`
	Hostname  string    `json:"hostname,omitempty"`
	PID       int       `json:"pid"`
	Token     string    `json:"token,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	StaleAt   time.Time `json:"stale_at"`
	Status    string    `json:"status"`
}

type Lock struct {
	path  string
	token string
}

func AcquireLock(backupDir string, metadata LockMetadata) (*Lock, error) {
	if metadata.CreatedAt.IsZero() {
		metadata.CreatedAt = time.Now().UTC()
	}
	if metadata.StaleAt.IsZero() {
		metadata.StaleAt = metadata.CreatedAt.Add(DefaultStaleTimeout)
	}
	if metadata.PID == 0 {
		metadata.PID = os.Getpid()
	}
	if metadata.Hostname == "" {
		metadata.Hostname, _ = os.Hostname()
	}
	if metadata.Token == "" {
		token, err := lockToken()
		if err != nil {
			return nil, err
		}
		metadata.Token = token
	}
	lockDir := filepath.Join(backupDir, LockDirName)
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		return nil, fmt.Errorf("create backup lock directory: %w", err)
	}
	path := filepath.Join(lockDir, lockFileName)
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode backup lock metadata: %w", err)
	}
	data = append(data, '\n')
	for attempt := 0; attempt < 2; attempt++ {
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			if _, err := file.Write(data); err != nil {
				_ = file.Close()
				_ = os.Remove(path)
				return nil, fmt.Errorf("write backup lock metadata: %w", err)
			}
			if err := file.Sync(); err != nil {
				_ = file.Close()
				_ = os.Remove(path)
				return nil, fmt.Errorf("sync backup lock metadata: %w", err)
			}
			if err := file.Close(); err != nil {
				_ = os.Remove(path)
				return nil, fmt.Errorf("close backup lock metadata: %w", err)
			}
			return &Lock{path: path, token: metadata.Token}, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("create backup lock: %w", err)
		}
		existing, readErr := readLock(path)
		if readErr != nil {
			return nil, fmt.Errorf("backup lock exists and cannot be inspected: %w", readErr)
		}
		if time.Now().UTC().Before(existing.StaleAt) {
			return nil, fmt.Errorf("backup lock is active for %s by %s", existing.Operation, existing.Owner)
		}
		current, readErr := readLock(path)
		if readErr != nil {
			return nil, fmt.Errorf("backup lock exists and cannot be reinspected before stale recovery: %w", readErr)
		}
		if !sameLockInstance(existing, current) {
			continue
		}
		if removeErr := os.Remove(path); removeErr != nil {
			return nil, fmt.Errorf("remove stale backup lock: %w", removeErr)
		}
	}
	return nil, fmt.Errorf("backup lock contention")
}

func lockToken() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate backup lock token: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func sameLockInstance(a, b LockMetadata) bool {
	if a.Token != "" || b.Token != "" {
		return a.Token != "" && a.Token == b.Token
	}
	return a.Operation == b.Operation &&
		a.Owner == b.Owner &&
		a.BackupID == b.BackupID &&
		a.Hostname == b.Hostname &&
		a.PID == b.PID &&
		a.CreatedAt.Equal(b.CreatedAt) &&
		a.StaleAt.Equal(b.StaleAt)
}

func readLock(path string) (LockMetadata, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return LockMetadata{}, err
	}
	var metadata LockMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return LockMetadata{}, err
	}
	if metadata.Operation == "" || metadata.Owner == "" || metadata.StaleAt.IsZero() {
		return LockMetadata{}, fmt.Errorf("invalid backup lock metadata")
	}
	return metadata, nil
}

func ActiveLockBackupIDs(backupDir string) map[string]struct{} {
	result := map[string]struct{}{}
	lockPath := filepath.Join(backupDir, LockDirName, lockFileName)
	metadata, err := readLock(lockPath)
	if err == nil && metadata.BackupID != "" && time.Now().UTC().Before(metadata.StaleAt) {
		result[metadata.BackupID] = struct{}{}
	}
	return result
}

func (l *Lock) Release() error {
	if l == nil || l.path == "" {
		return nil
	}
	metadata, err := readLock(l.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect backup lock before release: %w", err)
	}
	if l.token != "" && metadata.Token != l.token {
		return nil
	}
	if err := os.Remove(l.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("release backup lock: %w", err)
	}
	return nil
}
