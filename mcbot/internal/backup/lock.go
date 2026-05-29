package backup

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/snowy/mcbot/internal/atomicfile"
)

const (
	lockFileName                 = "mcbot-backup.lock"
	lockMutationGuardFileName    = lockFileName + ".guard"
	lockMutationGuardStalePeriod = time.Minute
)

var errLockMutationActive = errors.New("backup lock mutation is active")

var lockHeartbeatInterval = DefaultStaleTimeout / 3

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
	path     string
	token    string
	metadata LockMetadata
}

type lockMutationGuard struct {
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
	guard, err := acquireLockMutationGuardWithRetry(path)
	if err != nil {
		return nil, err
	}
	defer guard.Release()
	for attempt := 0; attempt < 2; attempt++ {
		err := writeLockFileExclusive(path, metadata)
		if err == nil {
			return &Lock{path: path, token: metadata.Token, metadata: metadata}, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
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
		if time.Now().UTC().Before(current.StaleAt) {
			return nil, fmt.Errorf("backup lock is active for %s by %s", current.Operation, current.Owner)
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

func acquireLockWithHeartbeat(backupDir string, metadata LockMetadata) (*Lock, func(), error) {
	lock, err := AcquireLock(backupDir, metadata)
	if err != nil {
		return nil, nil, err
	}
	stopHeartbeat, err := lock.StartHeartbeat(0, DefaultStaleTimeout)
	if err != nil {
		_ = lock.Release()
		return nil, nil, fmt.Errorf("start backup lock heartbeat: %w", err)
	}
	return lock, stopHeartbeat, nil
}

func acquireLockMutationGuardWithRetry(lockPath string) (*lockMutationGuard, error) {
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		guard, err := acquireLockMutationGuard(lockPath)
		if err == nil {
			return guard, nil
		}
		lastErr = err
		if !errors.Is(err, errLockMutationActive) {
			return nil, err
		}
		time.Sleep(10 * time.Millisecond)
	}
	return nil, lastErr
}

func acquireLockMutationGuard(lockPath string) (*lockMutationGuard, error) {
	path := filepath.Join(filepath.Dir(lockPath), lockMutationGuardFileName)
	for attempt := 0; attempt < 2; attempt++ {
		now := time.Now().UTC()
		token, err := lockToken()
		if err != nil {
			return nil, err
		}
		metadata := LockMetadata{
			Operation: "lock-mutation",
			Owner:     "cli",
			CreatedAt: now,
			StaleAt:   now.Add(lockMutationGuardStalePeriod),
			Token:     token,
			Status:    "serializing backup lock mutation",
		}
		err = writeLockFileExclusive(path, metadata)
		if err == nil {
			return &lockMutationGuard{path: path, token: token}, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		existing, readErr := readLock(path)
		if readErr != nil {
			return nil, fmt.Errorf("backup lock mutation guard exists and cannot be inspected: %w", readErr)
		}
		if time.Now().UTC().Before(existing.StaleAt) {
			return nil, errLockMutationActive
		}
		current, readErr := readLock(path)
		if readErr != nil {
			return nil, fmt.Errorf("backup lock mutation guard exists and cannot be reinspected before stale recovery: %w", readErr)
		}
		if time.Now().UTC().Before(current.StaleAt) {
			return nil, errLockMutationActive
		}
		if !sameLockInstance(existing, current) {
			continue
		}
		if removeErr := os.Remove(path); removeErr != nil {
			return nil, fmt.Errorf("remove stale backup lock mutation guard: %w", removeErr)
		}
	}
	return nil, fmt.Errorf("%w: contention", errLockMutationActive)
}

func (g *lockMutationGuard) Release() error {
	if g == nil || g.path == "" {
		return nil
	}
	metadata, err := readLock(g.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect backup lock mutation guard before release: %w", err)
	}
	if g.token != "" && metadata.Token != g.token {
		return nil
	}
	if err := os.Remove(g.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("release backup lock mutation guard: %w", err)
	}
	return nil
}

func writeLockFileExclusive(path string, metadata LockMetadata) error {
	data, err := encodeLock(metadata)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return err
		}
		return fmt.Errorf("create backup lock: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return fmt.Errorf("write backup lock metadata: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return fmt.Errorf("sync backup lock metadata: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("close backup lock metadata: %w", err)
	}
	return nil
}

func encodeLock(metadata LockMetadata) ([]byte, error) {
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode backup lock metadata: %w", err)
	}
	return append(data, '\n'), nil
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
		return a.Token != "" && a.Token == b.Token && a.StaleAt.Equal(b.StaleAt)
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

func (l *Lock) Refresh(staleAt time.Time, status string) error {
	if l == nil || l.path == "" {
		return nil
	}
	guard, err := acquireLockMutationGuardWithRetry(l.path)
	if err != nil {
		return err
	}
	defer guard.Release()
	current, err := readLock(l.path)
	if err != nil {
		return fmt.Errorf("inspect backup lock before refresh: %w", err)
	}
	if l.token != "" && current.Token != l.token {
		return fmt.Errorf("backup lock is no longer owned by this operation")
	}
	current.StaleAt = staleAt.UTC()
	if status != "" {
		current.Status = status
	}
	if err := replaceLockFile(l.path, current); err != nil {
		return err
	}
	l.metadata = current
	return nil
}

func (l *Lock) StartHeartbeat(interval, ttl time.Duration) (func(), error) {
	if l == nil || l.path == "" {
		return func() {}, nil
	}
	if interval <= 0 {
		interval = lockHeartbeatInterval
	}
	if ttl <= 0 {
		ttl = DefaultStaleTimeout
	}
	if err := l.Refresh(time.Now().UTC().Add(ttl), ""); err != nil {
		return nil, err
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := l.Refresh(time.Now().UTC().Add(ttl), ""); err != nil {
					if errors.Is(err, errLockMutationActive) {
						continue
					}
					return
				}
			case <-stop:
				return
			}
		}
	}()
	var stopOnce sync.Once
	return func() {
		stopOnce.Do(func() {
			close(stop)
		})
		<-done
	}, nil
}

func replaceLockFile(path string, metadata LockMetadata) error {
	data, err := encodeLock(metadata)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".mcbot-lock-*")
	if err != nil {
		return fmt.Errorf("create backup lock refresh temp file: %w", err)
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write backup lock refresh metadata: %w", err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod backup lock refresh metadata: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync backup lock refresh metadata: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close backup lock refresh metadata: %w", err)
	}
	if err := atomicfile.Replace(tmpPath, path); err != nil {
		return fmt.Errorf("replace backup lock refresh metadata: %w", err)
	}
	committed = true
	return nil
}

func (l *Lock) Release() error {
	if l == nil || l.path == "" {
		return nil
	}
	guard, err := acquireLockMutationGuardWithRetry(l.path)
	if err != nil {
		return err
	}
	defer guard.Release()
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
