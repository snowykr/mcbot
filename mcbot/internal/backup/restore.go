package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/snowy/mcbot/internal/mcconfig"
)

var chownPath = os.Chown

type RestoreOptions struct {
	BackupDir     string
	BackupID      string
	TargetDir     string
	ConfigPath    string
	RepoRoot      string
	Policy        mcconfig.BackupConfig
	StoppedProven bool
	UID           int
	GID           int
	Now           func() time.Time
}

type RestoreResult struct {
	BackupID          string `json:"backup_id"`
	ArchivePath       string `json:"archive_path"`
	TargetDir         string `json:"target_dir"`
	ConfigPath        string `json:"config_path"`
	SafetyBackupID    string `json:"safety_backup_id"`
	SafetyArchivePath string `json:"safety_archive_path"`
	RestoredFiles     int    `json:"restored_files"`
	RestoredConfig    bool   `json:"restored_config"`
	ConfigIncluded    bool   `json:"config_included"`
	ValidatedFiles    int    `json:"validated_files"`
	ReplacementScope  string `json:"replacement_scope"`
	PreservedPaths    string `json:"preserved_paths"`
}

func Restore(ctx context.Context, opts RestoreOptions) (RestoreResult, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.BackupID == "" {
		return RestoreResult{}, fmt.Errorf("restore requires explicit backup_id")
	}
	if !opts.StoppedProven {
		return RestoreResult{}, fmt.Errorf("restore requires the server to be stopped before replacing game data")
	}
	if err := validateRestoreTarget(opts.TargetDir, opts.BackupDir); err != nil {
		return RestoreResult{}, err
	}
	archivePath, err := ArchivePathForID(opts.BackupDir, opts.BackupID)
	if err != nil {
		return RestoreResult{}, err
	}
	if err := validateRestoreConfigPath(opts.ConfigPath, opts.RepoRoot, opts.TargetDir, opts.BackupDir); err != nil {
		return RestoreResult{}, err
	}
	if err := os.MkdirAll(opts.TargetDir, 0o755); err != nil {
		return RestoreResult{}, fmt.Errorf("create restore target directory: %w", err)
	}
	lock, err := AcquireLock(opts.BackupDir, LockMetadata{
		Operation: "restore",
		Owner:     "cli",
		BackupID:  opts.BackupID,
		CreatedAt: opts.Now().UTC(),
		StaleAt:   opts.Now().UTC().Add(DefaultStaleTimeout),
		Status:    "restoring backup",
	})
	if err != nil {
		return RestoreResult{}, err
	}
	defer lock.Release()
	archiveFile, validation, err := openValidatedArchive(archivePath)
	if err != nil {
		return RestoreResult{}, err
	}
	defer archiveFile.Close()
	if validation.Manifest.BackupID != opts.BackupID {
		return RestoreResult{}, fmt.Errorf("validated manifest id mismatch")
	}
	safetyPolicy := opts.Policy
	if safetyPolicy.IncludeMCServerTOML || validation.Manifest.IncludeMCServerTOML {
		configExists, err := restoreConfigFileExists(opts.ConfigPath)
		if err != nil {
			return RestoreResult{}, err
		}
		safetyPolicy.IncludeMCServerTOML = configExists
	}
	safety, err := Create(ctx, CreateOptions{
		SourceDir:      opts.TargetDir,
		ConfigPath:     opts.ConfigPath,
		BackupDir:      opts.BackupDir,
		Policy:         safetyPolicy,
		CreatedBy:      "cli",
		Reason:         "restore-safety",
		StoppedProven:  true,
		Now:            opts.Now,
		NoRetention:    true,
		AssumeLockHeld: true,
	})
	if err != nil {
		return RestoreResult{}, fmt.Errorf("create pre-restore safety backup: %w", err)
	}
	if _, err := validateOpenArchive(archiveFile, archivePath); err != nil {
		return RestoreResult{}, withSafetyBackup("revalidate backup before restore", safety, err)
	}
	stage, err := os.MkdirTemp(filepath.Dir(opts.TargetDir), ".mcbot-restore-*")
	if err != nil {
		return RestoreResult{}, withSafetyBackup("create restore staging directory", safety, err)
	}
	defer os.RemoveAll(stage)
	if err := extractArchiveToStage(archiveFile, archivePath, stage); err != nil {
		return RestoreResult{}, withSafetyBackup("extract backup for restore", safety, err)
	}
	configStage := filepath.Join(stage, "mc-server.toml")
	if err := validateStagedConfig(configStage); err != nil {
		return RestoreResult{}, withSafetyBackup("validate staged mc-server.toml", safety, err)
	}
	worldStage := filepath.Join(stage, "world-data")
	restoredFiles, err := replaceDirectoryContents(opts.TargetDir, worldStage, opts.UID, opts.GID)
	if err != nil {
		return RestoreResult{}, withSafetyBackup("replace restore target", safety, err)
	}
	restoredConfig := false
	if _, err := os.Stat(configStage); err == nil {
		if err := backupExistingConfig(opts.ConfigPath, opts.Now); err != nil {
			return RestoreResult{}, withSafetyBackup("backup existing config", safety, err)
		}
		if err := copyFile(configStage, opts.ConfigPath, 0o644); err != nil {
			return RestoreResult{}, withSafetyBackup("restore mc-server.toml", safety, err)
		}
		if err := normalizePath(opts.ConfigPath, opts.UID, opts.GID, false); err != nil {
			return RestoreResult{}, withSafetyBackup("normalize restored config", safety, err)
		}
		restoredConfig = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return RestoreResult{}, withSafetyBackup("stat staged mc-server.toml", safety, err)
	}
	return RestoreResult{
		BackupID:          opts.BackupID,
		ArchivePath:       archivePath,
		TargetDir:         opts.TargetDir,
		ConfigPath:        opts.ConfigPath,
		SafetyBackupID:    safety.BackupID,
		SafetyArchivePath: safety.ArchivePath,
		RestoredFiles:     restoredFiles,
		RestoredConfig:    restoredConfig,
		ConfigIncluded:    validation.Manifest.IncludeMCServerTOML,
		ValidatedFiles:    validation.ValidatedFiles,
		ReplacementScope:  "target directory contents",
		PreservedPaths:    ".env,mcbot,data,backups,.locks,.git,.omx,.mcbot-restore-*",
	}, nil
}

func validateStagedConfig(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("stat staged mc-server.toml: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("staged mc-server.toml must not be a symlink")
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("staged mc-server.toml is not a regular file")
	}
	if err := mcconfig.ValidateFile(path); err != nil {
		return fmt.Errorf("staged mc-server.toml is invalid: %w", err)
	}
	return nil
}

func restoreConfigFileExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("stat current mc-server.toml: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return false, fmt.Errorf("current mc-server.toml must not be a symlink")
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("current mc-server.toml is not a regular file")
	}
	return true, nil
}

func withSafetyBackup(action string, safety CreateResult, err error) error {
	return fmt.Errorf("%s: %w (pre-restore safety backup %s at %s)", action, err, safety.BackupID, safety.ArchivePath)
}

func ArchivePathForID(backupDir, id string) (string, error) {
	if !validBackupID(id) {
		return "", fmt.Errorf("invalid backup_id %q", id)
	}
	return filepath.Join(backupDir, id+".tar.gz"), nil
}

func validBackupID(id string) bool {
	if len(id) != len("20260522T043000Z-abcdefgh") || id[8] != 'T' || id[15] != 'Z' || id[16] != '-' {
		return false
	}
	for i, r := range id {
		switch {
		case i < 8 || (i > 8 && i < 15):
			if r < '0' || r > '9' {
				return false
			}
		case i == 8:
			if r != 'T' {
				return false
			}
		case i == 15:
			if r != 'Z' {
				return false
			}
		case i == 16:
			if r != '-' {
				return false
			}
		default:
			if !((r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')) {
				return false
			}
		}
	}
	return true
}

func extractArchiveToStage(file *os.File, archivePath, stage string) error {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seek restore archive: %w", err)
	}
	gzr, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("open restore gzip: %w", err)
	}
	defer gzr.Close()
	tr := tar.NewReader(gzr)
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read restore archive: %w", err)
		}
		if err := validateTarHeader(header); err != nil {
			return err
		}
		if header.Name == "manifest.json" {
			continue
		}
		target := filepath.Join(stage, filepath.FromSlash(header.Name))
		if !strings.HasPrefix(filepath.Clean(target), filepath.Clean(stage)+string(filepath.Separator)) {
			return fmt.Errorf("archive entry escapes restore stage: %s", header.Name)
		}
		if header.Typeflag == tar.TypeDir {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("create staged directory %s: %w", header.Name, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("create staged parent %s: %w", header.Name, err)
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			return fmt.Errorf("create staged file %s: %w", header.Name, err)
		}
		if _, err := io.Copy(out, tr); err != nil {
			_ = out.Close()
			return fmt.Errorf("extract staged file %s: %w", header.Name, err)
		}
		if err := out.Close(); err != nil {
			return fmt.Errorf("close staged file %s: %w", header.Name, err)
		}
	}
	return nil
}

func replaceDirectoryContents(targetDir, sourceDir string, uid, gid int) (int, error) {
	parent := filepath.Dir(targetDir)
	replacement, err := os.MkdirTemp(parent, ".mcbot-restore-new-*")
	if err != nil {
		return 0, fmt.Errorf("create replacement restore directory: %w", err)
	}
	replacementCommitted := false
	defer func() {
		if !replacementCommitted {
			_ = os.RemoveAll(replacement)
		}
	}()
	if _, err := os.Stat(sourceDir); errors.Is(err, os.ErrNotExist) {
		// Empty backup world; preserve only explicitly protected entries below.
	} else if err != nil {
		return 0, fmt.Errorf("stat staged world-data: %w", err)
	} else if err := copyTree(sourceDir, replacement); err != nil {
		return 0, fmt.Errorf("prepare restored world-data: %w", err)
	}
	if err := copyPreservedRestoreEntries(targetDir, replacement); err != nil {
		return 0, err
	}
	normalizedFiles, err := normalizeTree(replacement, uid, gid)
	if err != nil {
		return 0, fmt.Errorf("normalize staged restore tree: %w", err)
	}

	rollback := ""
	if _, err := os.Lstat(targetDir); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(parent, 0o755); err != nil {
			return 0, fmt.Errorf("create restore target parent: %w", err)
		}
	} else if err != nil {
		return 0, fmt.Errorf("stat restore target directory: %w", err)
	} else {
		oldDir, err := os.MkdirTemp(parent, ".mcbot-restore-old-*")
		if err != nil {
			return 0, fmt.Errorf("reserve rollback restore directory: %w", err)
		}
		if err := os.Remove(oldDir); err != nil {
			return 0, fmt.Errorf("prepare rollback restore directory: %w", err)
		}
		if err := os.Rename(targetDir, oldDir); err != nil {
			return 0, fmt.Errorf("move current restore target aside: %w", err)
		}
		rollback = oldDir
	}
	if err := os.Rename(replacement, targetDir); err != nil {
		if rollback != "" {
			if rollbackErr := os.Rename(rollback, targetDir); rollbackErr != nil {
				return 0, fmt.Errorf("activate restored directory: %w; rollback failed: %v", err, rollbackErr)
			}
		}
		return 0, fmt.Errorf("activate restored directory: %w", err)
	}
	replacementCommitted = true
	if rollback != "" {
		_ = os.RemoveAll(rollback)
	}
	return normalizedFiles, nil
}

func validateRestoreTarget(targetDir, backupDir string) error {
	targetAbs, err := filepath.Abs(filepath.Clean(targetDir))
	if err != nil {
		return fmt.Errorf("resolve restore target directory: %w", err)
	}
	backupAbs, err := filepath.Abs(filepath.Clean(backupDir))
	if err != nil {
		return fmt.Errorf("resolve backup directory: %w", err)
	}
	if targetAbs == string(filepath.Separator) || targetAbs == "." {
		return fmt.Errorf("restore target must not be filesystem root")
	}
	base := filepath.Base(targetAbs)
	switch base {
	case ".", string(filepath.Separator), "data", "mcbot", "backups", ".git", ".omx":
		return fmt.Errorf("restore target %s is a protected path", targetDir)
	}
	if pathContains(targetAbs, backupAbs) || pathContains(backupAbs, targetAbs) {
		return fmt.Errorf("restore target must not overlap backup directory")
	}
	commonRoot := commonPathPrefix(targetAbs, backupAbs)
	if err := rejectSymlinkPathComponentsUnderRoot(commonRoot, targetAbs, "restore target"); err != nil {
		return err
	}
	if err := rejectSymlinkPathComponentsUnderRoot(commonRoot, backupAbs, "backup directory"); err != nil {
		return err
	}
	backupInfo, err := lstatNoSymlink(backupAbs, "backup directory", backupDir)
	if err != nil {
		return err
	}
	if !backupInfo.IsDir() {
		return fmt.Errorf("backup directory is not a directory: %s", backupDir)
	}
	slashTarget := filepath.ToSlash(targetAbs)
	if strings.Contains(slashTarget, "/data/mcbot") || strings.HasSuffix(slashTarget, "/data/mcbot") {
		return fmt.Errorf("restore target must not be bot runtime data")
	}
	if info, err := os.Lstat(targetAbs); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("restore target must not be a symlink")
		}
		if !info.IsDir() {
			return fmt.Errorf("restore target is not a directory: %s", targetDir)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat restore target directory: %w", err)
	}
	return nil
}

func validateRestoreConfigPath(configPath, repoRoot, targetDir, backupDir string) error {
	configAbs, err := filepath.Abs(filepath.Clean(configPath))
	if err != nil {
		return fmt.Errorf("resolve restore config path: %w", err)
	}
	if repoRoot == "" {
		repoRoot = commonPathPrefix(configAbs, commonPathPrefix(targetDir, backupDir))
	}
	repoAbs, err := filepath.Abs(filepath.Clean(repoRoot))
	if err != nil {
		return fmt.Errorf("resolve repository root for config restore: %w", err)
	}
	if !pathContains(repoAbs, configAbs) || configAbs == repoAbs {
		return fmt.Errorf("restore config path must be under the repository root")
	}
	if filepath.Base(configAbs) != "mc-server.toml" {
		return fmt.Errorf("restore config path must be mc-server.toml")
	}
	for _, protected := range []string{".env", "data", "data/minecraft", "data/mcbot", "backups", "mcbot", ".git", ".omx"} {
		if pathContains(filepath.Join(repoAbs, protected), configAbs) {
			return fmt.Errorf("restore config path must not be inside protected path %s", protected)
		}
	}
	if err := rejectSymlinkPathComponentsUnderRoot(repoAbs, configAbs, "restore config path"); err != nil {
		return err
	}
	if info, err := os.Lstat(configAbs); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("restore config path must not be a symlink")
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("restore config path is not a regular file: %s", configPath)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat restore config path: %w", err)
	}
	return nil
}

func copyPreservedRestoreEntries(targetDir, replacement string) error {
	entries, err := os.ReadDir(targetDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read restore target directory: %w", err)
	}
	for _, entry := range entries {
		if !preserveRestoreEntry(entry.Name()) {
			continue
		}
		src := filepath.Join(targetDir, entry.Name())
		dst := filepath.Join(replacement, entry.Name())
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("preserved restore entry must not be a symlink: %s", entry.Name())
		}
		info, err := lstatNoSymlink(src, "preserved restore entry", entry.Name())
		if err != nil {
			return err
		}
		if info.IsDir() {
			if err := copyTree(src, dst); err != nil {
				return fmt.Errorf("preserve restore directory %s: %w", entry.Name(), err)
			}
			continue
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("preserved restore entry is not regular: %s", entry.Name())
		}
		if err := copyFile(src, dst, info.Mode().Perm()); err != nil {
			return fmt.Errorf("preserve restore file %s: %w", entry.Name(), err)
		}
	}
	return nil
}

func preserveRestoreEntry(name string) bool {
	switch name {
	case ".env", "mcbot", "data", "backups", ".locks", ".git", ".omx":
		return true
	default:
		return strings.HasPrefix(name, ".mcbot-restore-")
	}
}

func pathContains(parent, child string) bool {
	parent = filepath.Clean(parent)
	child = filepath.Clean(child)
	if parent == child {
		return true
	}
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("staged restore entry must not be a symlink: %s", rel)
		}
		info, err := lstatNoSymlink(path, "staged restore entry", rel)
		if err != nil {
			return err
		}
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("staged restore entry is not regular: %s", rel)
		}
		return copyFile(path, target, 0o644)
	})
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, _, err := openVerifiedRegularFile(src, "copy source")
	if err != nil {
		return err
	}
	defer in.Close()
	srcAbs, err := filepath.Abs(filepath.Clean(src))
	if err != nil {
		return fmt.Errorf("resolve copy source: %w", err)
	}
	dstAbs, err := filepath.Abs(filepath.Clean(dst))
	if err != nil {
		return fmt.Errorf("resolve copy destination: %w", err)
	}
	if err := rejectSymlinkPathComponentsUnderRoot(commonPathPrefix(srcAbs, dstAbs), filepath.Dir(dstAbs), "copy destination parent"); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if info, err := os.Lstat(dst); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("destination must not be a symlink: %s", dst)
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".mcbot-copy-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := io.Copy(tmp, in); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		return err
	}
	committed = true
	return nil
}

func backupExistingConfig(path string, now func() time.Time) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("stat current mc-server.toml: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("current mc-server.toml must not be a symlink")
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("current mc-server.toml is not a regular file")
	}
	backupPath := fmt.Sprintf("%s.%s.bak", path, now().UTC().Format("20060102T150405Z"))
	return copyFile(path, backupPath, 0o600)
}

func normalizeTree(root string, uid, gid int) (int, error) {
	count := 0
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == "." {
			rel = filepath.Base(path)
		}
		if d.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("refuse to normalize restore symlink: %s", rel)
		}
		info, err := lstatNoSymlink(path, "restore entry", rel)
		if err != nil {
			return err
		}
		if path == root {
			return normalizePath(path, uid, gid, true)
		}
		if !info.IsDir() {
			count++
		}
		return normalizePath(path, uid, gid, info.IsDir())
	})
	return count, err
}

func normalizePath(path string, uid, gid int, dir bool) error {
	mode := os.FileMode(0o644)
	if dir {
		mode = 0o755
	}
	if err := os.Chmod(path, mode); err != nil {
		return fmt.Errorf("normalize permissions for %s: %w", path, err)
	}
	if uid >= 0 && gid >= 0 {
		if err := chownPath(path, uid, gid); err != nil {
			return fmt.Errorf("normalize ownership for %s: %w", path, err)
		}
	}
	return nil
}
