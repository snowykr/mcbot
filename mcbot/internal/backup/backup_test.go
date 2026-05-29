package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/snowy/mcbot/internal/mcconfig"
)

func TestCreateStoppedBackupWritesManifestAndGameDataOnly(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "data", "minecraft")
	backups := filepath.Join(root, "backups")
	configPath := filepath.Join(root, "mc-server.toml")
	writeFile(t, filepath.Join(source, "world", "level.dat"), "level")
	writeFile(t, filepath.Join(source, ".env"), "secret")
	writeFile(t, filepath.Join(source, ".secret"), "hidden")
	writeFile(t, filepath.Join(source, "logs", "latest.log"), "log")
	writeFile(t, filepath.Join(source, "world", "session.lock"), "lock")
	writeFile(t, filepath.Join(source, "world", "scratch.tmp"), "tmp")
	writeFile(t, configPath, mcconfig.Render(mcconfig.Defaults()))

	result, err := Create(context.Background(), CreateOptions{
		SourceDir:     source,
		ConfigPath:    configPath,
		BackupDir:     backups,
		Policy:        mcconfig.Defaults().Backup,
		CreatedBy:     "cli",
		Reason:        "manual",
		StoppedProven: true,
		Now:           fixedNow,
		RandomSuffix:  fixedSuffix("abcdefgh"),
		NoRetention:   true,
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if result.BackupID != "20260522T043000Z-abcdefgh" {
		t.Fatalf("backup id = %q", result.BackupID)
	}
	validation, err := ValidateArchive(result.ArchivePath)
	if err != nil {
		t.Fatalf("ValidateArchive failed: %v", err)
	}
	if validation.Manifest.BackupID != result.BackupID {
		t.Fatalf("manifest backup id = %q", validation.Manifest.BackupID)
	}
	names := tarNames(t, result.ArchivePath)
	for _, want := range []string{"manifest.json", "mc-server.toml", "world-data/world", "world-data/world/level.dat"} {
		if !contains(names, want) {
			t.Fatalf("archive names %v missing %s", names, want)
		}
	}
	for _, forbidden := range []string{"world-data/.env", "world-data/.secret", "world-data/logs/latest.log", "world-data/world/session.lock", "world-data/world/scratch.tmp", "data/mcbot"} {
		if contains(names, forbidden) {
			t.Fatalf("archive names %v unexpectedly contains %s", names, forbidden)
		}
	}
}

func TestCreateRunningBackupRequiresAndUsesQuiesce(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "data", "minecraft")
	backups := filepath.Join(root, "backups")
	configPath := filepath.Join(root, "mc-server.toml")
	writeFile(t, filepath.Join(source, "world", "level.dat"), "level")
	writeFile(t, configPath, mcconfig.Render(mcconfig.Defaults()))

	if _, err := Create(context.Background(), CreateOptions{
		SourceDir:     source,
		ConfigPath:    configPath,
		BackupDir:     backups,
		Policy:        mcconfig.Defaults().Backup,
		StoppedProven: false,
		Now:           fixedNow,
		RandomSuffix:  fixedSuffix("bbbbbbbb"),
		NoRetention:   true,
	}); err == nil {
		t.Fatal("Create without quiescer succeeded, want fail closed")
	}

	q := &fakeQuiescer{}
	_, err := Create(context.Background(), CreateOptions{
		SourceDir:     source,
		ConfigPath:    configPath,
		BackupDir:     backups,
		Policy:        mcconfig.Defaults().Backup,
		StoppedProven: false,
		Quiescer:      q,
		Now:           fixedNow,
		RandomSuffix:  fixedSuffix("cccccccc"),
		NoRetention:   true,
	})
	if err != nil {
		t.Fatalf("Create with quiescer failed: %v", err)
	}
	want := []string{"save-off", "save-all flush", "save-on"}
	if !reflect.DeepEqual(q.commands, want) {
		t.Fatalf("quiesce commands = %v, want %v", q.commands, want)
	}
}

func TestRunQuiescedArchiveOutlivesQuiesceTimeout(t *testing.T) {
	q := &fakeQuiescer{}
	err := RunQuiesced(context.Background(), q, 50*time.Millisecond, func(opCtx context.Context, _ QuiesceManifest) error {
		timer := time.NewTimer(150 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-opCtx.Done():
			return opCtx.Err()
		case <-timer.C:
			return nil
		}
	})
	if err != nil {
		t.Fatalf("RunQuiesced error = %v, want archive work to outlive quiesce timeout", err)
	}
	if got := strings.Join(q.commands, ","); got != "save-off,save-all flush,save-on" {
		t.Fatalf("commands = %s", got)
	}
}

func TestRunQuiescedReportsSaveOnCleanupAfterOperationFailure(t *testing.T) {
	q := &failingQuiescer{failures: map[string]error{"save-on": errors.New("save-on down")}}
	err := RunQuiesced(context.Background(), q, time.Second, func(context.Context, QuiesceManifest) error {
		return errors.New("archive failed")
	})
	if err == nil || !strings.Contains(err.Error(), "archive failed") || !strings.Contains(err.Error(), "save-on cleanup failed") {
		t.Fatalf("RunQuiesced error = %v, want operation and save-on cleanup failures", err)
	}
	if got := strings.Join(q.commands, ","); got != "save-off,save-all flush,save-on" {
		t.Fatalf("commands = %s", got)
	}
}

func TestCreateRunningBackupCancelsArchiveAndStillSaveOn(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "data", "minecraft")
	backups := filepath.Join(root, "backups")
	configPath := filepath.Join(root, "mc-server.toml")
	writeFile(t, filepath.Join(source, "world", "level.dat"), "level")
	writeFile(t, configPath, mcconfig.Render(mcconfig.Defaults()))
	policy := mcconfig.Defaults().Backup
	ctx, cancel := context.WithCancel(context.Background())
	q := &cancelAfterFlushQuiescer{cancel: cancel}

	_, err := Create(ctx, CreateOptions{
		SourceDir:     source,
		ConfigPath:    configPath,
		BackupDir:     backups,
		Policy:        policy,
		StoppedProven: false,
		Quiescer:      q,
		Now:           fixedNow,
		RandomSuffix:  fixedSuffix("ctxabort"),
		NoRetention:   true,
	})
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("Create error = %v, want context.Canceled", err)
	}
	if got := strings.Join(q.commands, ","); got != "save-off,save-all flush,save-on" {
		t.Fatalf("commands = %s, want save-on cleanup after archive cancellation", got)
	}
	if _, err := os.Stat(filepath.Join(backups, "20260522T043000Z-ctxabort.tar.gz")); !os.IsNotExist(err) {
		t.Fatalf("archive exists after canceled live backup or stat failed: %v", err)
	}
}

func TestCreateRejectsSourceSymlink(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "data", "minecraft")
	backups := filepath.Join(root, "backups")
	configPath := filepath.Join(root, "mc-server.toml")
	if err := os.MkdirAll(source, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, configPath, mcconfig.Render(mcconfig.Defaults()))
	if err := os.Symlink("/tmp", filepath.Join(source, "escape")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err := Create(context.Background(), CreateOptions{
		SourceDir:     source,
		ConfigPath:    configPath,
		BackupDir:     backups,
		Policy:        mcconfig.Defaults().Backup,
		StoppedProven: true,
		Now:           fixedNow,
		RandomSuffix:  fixedSuffix("dddddddd"),
		NoRetention:   true,
	})
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("Create error = %v, want symlink rejection", err)
	}
}

func TestCreateRejectsSourceRootSymlink(t *testing.T) {
	root := t.TempDir()
	realSource := filepath.Join(root, "real-minecraft")
	sourceLink := filepath.Join(root, "data", "minecraft")
	backups := filepath.Join(root, "backups")
	configPath := filepath.Join(root, "mc-server.toml")
	writeFile(t, filepath.Join(realSource, "world", "level.dat"), "level")
	writeFile(t, configPath, mcconfig.Render(mcconfig.Defaults()))
	if err := os.MkdirAll(filepath.Dir(sourceLink), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realSource, sourceLink); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err := Create(context.Background(), CreateOptions{
		SourceDir:     sourceLink,
		ConfigPath:    configPath,
		BackupDir:     backups,
		Policy:        mcconfig.Defaults().Backup,
		StoppedProven: true,
		Now:           fixedNow,
		RandomSuffix:  fixedSuffix("rootsyml"),
		NoRetention:   true,
	})
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("Create error = %v, want source root symlink rejection", err)
	}
	if _, err := os.Stat(filepath.Join(backups, "20260522T043000Z-rootsyml.tar.gz")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("archive exists after rejected source root symlink or stat failed: %v", err)
	}
}

func TestCreateRejectsSourceParentSymlink(t *testing.T) {
	root := t.TempDir()
	realData := filepath.Join(root, "real-data")
	dataLink := filepath.Join(root, "data")
	source := filepath.Join(dataLink, "minecraft")
	backups := filepath.Join(root, "backups")
	configPath := filepath.Join(root, "mc-server.toml")
	writeFile(t, filepath.Join(realData, "minecraft", "world", "level.dat"), "level")
	writeFile(t, configPath, mcconfig.Render(mcconfig.Defaults()))
	if err := os.Symlink(realData, dataLink); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err := Create(context.Background(), CreateOptions{
		SourceDir:     source,
		ConfigPath:    configPath,
		BackupDir:     backups,
		Policy:        mcconfig.Defaults().Backup,
		StoppedProven: true,
		Now:           fixedNow,
		RandomSuffix:  fixedSuffix("parents"),
		NoRetention:   true,
	})
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("Create error = %v, want source parent symlink rejection", err)
	}
}

func TestCreateRejectsBackupParentSymlink(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "data", "minecraft")
	realBackups := filepath.Join(root, "real-backups")
	backupsLink := filepath.Join(root, "backup-link")
	backups := filepath.Join(backupsLink, "archives")
	configPath := filepath.Join(root, "mc-server.toml")
	writeFile(t, filepath.Join(source, "world", "level.dat"), "level")
	writeFile(t, configPath, mcconfig.Render(mcconfig.Defaults()))
	if err := os.MkdirAll(realBackups, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realBackups, backupsLink); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err := Create(context.Background(), CreateOptions{
		SourceDir:     source,
		ConfigPath:    configPath,
		BackupDir:     backups,
		Policy:        mcconfig.Defaults().Backup,
		StoppedProven: true,
		Now:           fixedNow,
		RandomSuffix:  fixedSuffix("backsyml"),
		NoRetention:   true,
	})
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("Create error = %v, want backup parent symlink rejection", err)
	}
	if entries, err := os.ReadDir(realBackups); err != nil {
		t.Fatal(err)
	} else if len(entries) != 0 {
		t.Fatalf("real backup directory changed after rejected symlink path: %v", entries)
	}
}

func TestCreateRejectsConfigParentSymlink(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "data", "minecraft")
	backups := filepath.Join(root, "backups")
	realConfigDir := filepath.Join(root, "real-config")
	configLink := filepath.Join(root, "config-link")
	configPath := filepath.Join(configLink, "mc-server.toml")
	writeFile(t, filepath.Join(source, "world", "level.dat"), "level")
	writeFile(t, filepath.Join(realConfigDir, "mc-server.toml"), mcconfig.Render(mcconfig.Defaults()))
	if err := os.Symlink(realConfigDir, configLink); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err := Create(context.Background(), CreateOptions{
		SourceDir:     source,
		ConfigPath:    configPath,
		BackupDir:     backups,
		Policy:        mcconfig.Defaults().Backup,
		StoppedProven: true,
		Now:           fixedNow,
		RandomSuffix:  fixedSuffix("cfgsymln"),
		NoRetention:   true,
	})
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("Create error = %v, want config parent symlink rejection", err)
	}
}

func TestRejectSymlinkPathComponentsUsesTrustedRootBoundary(t *testing.T) {
	root := t.TempDir()
	trustedRoot := filepath.Join(root, "trusted")
	path := filepath.Join(trustedRoot, "safe", "child")
	writeFile(t, filepath.Join(path, "level.dat"), "level")
	if err := rejectSymlinkPathComponentsUnderRoot(trustedRoot, path, "test path"); err != nil {
		t.Fatalf("rejectSymlinkPathComponentsUnderRoot safe path error = %v", err)
	}
	if err := os.Symlink(filepath.Join(root, "outside"), filepath.Join(trustedRoot, "linked")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	err := rejectSymlinkPathComponentsUnderRoot(trustedRoot, filepath.Join(trustedRoot, "linked", "child"), "test path")
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("rejectSymlinkPathComponentsUnderRoot error = %v, want symlink rejection below trusted root", err)
	}
}

func TestRejectSymlinkPathComponentsRequiresTrustedRoot(t *testing.T) {
	root := t.TempDir()
	trustedRoot := filepath.Join(root, "trusted")
	path := filepath.Join(root, "other", "safe", "child")
	writeFile(t, filepath.Join(path, "level.dat"), "level")
	err := rejectSymlinkPathComponentsUnderRoot(trustedRoot, path, "test path")
	if err == nil || !strings.Contains(err.Error(), "trusted root") {
		t.Fatalf("rejectSymlinkPathComponentsUnderRoot error = %v, want trusted root rejection", err)
	}
}

func TestCreateRejectsSourceSymlinkToRegularFile(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "data", "minecraft")
	backups := filepath.Join(root, "backups")
	configPath := filepath.Join(root, "mc-server.toml")
	outside := filepath.Join(root, "outside-secret.txt")
	writeFile(t, filepath.Join(source, "world", "level.dat"), "level")
	writeFile(t, outside, "secret")
	writeFile(t, configPath, mcconfig.Render(mcconfig.Defaults()))
	if err := os.Symlink(outside, filepath.Join(source, "world", "linked-secret.txt")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err := Create(context.Background(), CreateOptions{
		SourceDir:     source,
		ConfigPath:    configPath,
		BackupDir:     backups,
		Policy:        mcconfig.Defaults().Backup,
		StoppedProven: true,
		Now:           fixedNow,
		RandomSuffix:  fixedSuffix("filesyml"),
		NoRetention:   true,
	})
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("Create error = %v, want symlink rejection", err)
	}
}

func TestCreateRejectsExcludedSourceSymlinkBeforeSkipping(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "data", "minecraft")
	backups := filepath.Join(root, "backups")
	configPath := filepath.Join(root, "mc-server.toml")
	outsideLogs := filepath.Join(root, "outside-logs")
	writeFile(t, filepath.Join(source, "world", "level.dat"), "level")
	writeFile(t, filepath.Join(outsideLogs, "latest.log"), "secret")
	writeFile(t, configPath, mcconfig.Render(mcconfig.Defaults()))
	if err := os.Symlink(outsideLogs, filepath.Join(source, "logs")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err := Create(context.Background(), CreateOptions{
		SourceDir:     source,
		ConfigPath:    configPath,
		BackupDir:     backups,
		Policy:        mcconfig.Defaults().Backup,
		StoppedProven: true,
		Now:           fixedNow,
		RandomSuffix:  fixedSuffix("excludes"),
		NoRetention:   true,
	})
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("Create error = %v, want excluded symlink rejection", err)
	}
	if _, err := os.Stat(filepath.Join(backups, "20260522T043000Z-excludes.tar.gz")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("archive exists after rejected excluded symlink or stat failed: %v", err)
	}
}

func TestCreateArchiveWithLongAndUnicodePathsValidatesAndRestores(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "data", "minecraft")
	target := filepath.Join(root, "data", "minecraft-restored")
	backups := filepath.Join(root, "backups")
	configPath := filepath.Join(root, "mc-server.toml")
	longDir := strings.Repeat("longsegment", 12)
	unicodePath := filepath.Join(source, "world", longDir, "한글-월드-데이터.dat")
	writeFile(t, unicodePath, "unicode-long")
	writeFile(t, configPath, mcconfig.Render(mcconfig.Defaults()))

	created, err := Create(context.Background(), CreateOptions{
		SourceDir:     source,
		ConfigPath:    configPath,
		BackupDir:     backups,
		Policy:        mcconfig.Defaults().Backup,
		StoppedProven: true,
		Now:           fixedNow,
		RandomSuffix:  fixedSuffix("paxpath1"),
		NoRetention:   true,
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if _, err := ValidateArchive(created.ArchivePath); err != nil {
		t.Fatalf("ValidateArchive rejected self-created PAX archive: %v", err)
	}
	if _, err := Restore(context.Background(), RestoreOptions{
		BackupDir:     backups,
		BackupID:      created.BackupID,
		TargetDir:     target,
		ConfigPath:    configPath,
		Policy:        mcconfig.Defaults().Backup,
		StoppedProven: true,
		UID:           os.Getuid(),
		GID:           os.Getgid(),
		Now:           fixedNow,
	}); err != nil {
		t.Fatalf("Restore failed for self-created PAX archive: %v", err)
	}
	if got := readFile(t, filepath.Join(target, "world", longDir, "한글-월드-데이터.dat")); got != "unicode-long" {
		t.Fatalf("restored unicode long file = %q", got)
	}
}

func TestValidateArchiveRejectsUnsafeTarEntries(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "20260522T043000Z-eeeeeeee.tar.gz")
	manifest := Manifest{
		FormatVersion: ManifestFormatVersion,
		BackupID:      "20260522T043000Z-eeeeeeee",
		CreatedAt:     fixedNow(),
		CreatedBy:     "cli",
		BackupReason:  "manual",
		Files:         []FileManifest{{Path: "world-data/escape", Size: 0, SHA256: emptySHA256}},
	}
	writeCustomArchive(t, path, manifest, func(tw *tar.Writer) {
		if err := tw.WriteHeader(&tar.Header{Name: "world-data/escape", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}); err != nil {
			t.Fatalf("write symlink header: %v", err)
		}
	})
	if _, err := ValidateArchive(path); err == nil || !strings.Contains(err.Error(), "unsupported type") {
		t.Fatalf("ValidateArchive error = %v, want unsupported type", err)
	}
}

func TestValidateArchiveRejectsExcludedPayloadPaths(t *testing.T) {
	for _, entryName := range []string{"world-data/.secret", "world-data/logs/latest.log", "world-data/world/scratch.tmp", "world-data/world/session.lock"} {
		t.Run(entryName, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "20260522T043000Z-excluded.tar.gz")
			manifest := Manifest{
				FormatVersion: ManifestFormatVersion,
				BackupID:      "20260522T043000Z-excluded",
				CreatedAt:     fixedNow(),
				CreatedBy:     "cli",
				BackupReason:  "manual",
				Files:         []FileManifest{{Path: entryName, Size: 0, SHA256: emptySHA256}},
			}
			writeCustomArchive(t, path, manifest, func(tw *tar.Writer) {
				if err := tw.WriteHeader(&tar.Header{Name: entryName, Typeflag: tar.TypeReg, Size: 0}); err != nil {
					t.Fatalf("write header: %v", err)
				}
			})
			if _, err := ValidateArchive(path); err == nil || !strings.Contains(err.Error(), "excluded") {
				t.Fatalf("ValidateArchive error = %v, want excluded path rejection", err)
			}
		})
	}
}

func TestValidateArchiveAllowsReservedWordsInsideSafeSegments(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "20260522T043000Z-segment1.tar.gz")
	files := map[string][]byte{
		"world-data/world/mybackups/level.dat":    []byte("backup-looking segment"),
		"world-data/world/data/mcbot_state.dat":   []byte("mcbot-looking segment"),
		"world-data/world/player-backups/read.me": []byte("hyphenated backups segment"),
	}
	manifest := Manifest{
		FormatVersion: ManifestFormatVersion,
		BackupID:      "20260522T043000Z-segment1",
		CreatedAt:     fixedNow(),
		CreatedBy:     "cli",
		BackupReason:  "manual",
	}
	for name, data := range files {
		manifest.Files = append(manifest.Files, FileManifest{Path: name, Size: int64(len(data)), SHA256: sha256Hex(data)})
	}
	writeCustomArchive(t, path, manifest, func(tw *tar.Writer) {
		for name, data := range files {
			if err := tw.WriteHeader(&tar.Header{Name: name, Typeflag: tar.TypeReg, Size: int64(len(data))}); err != nil {
				t.Fatalf("write header: %v", err)
			}
			if _, err := tw.Write(data); err != nil {
				t.Fatalf("write file: %v", err)
			}
		}
	})
	if _, err := ValidateArchive(path); err != nil {
		t.Fatalf("ValidateArchive rejected safe reserved-word segments: %v", err)
	}
}

func TestValidateArchiveRejectsDuplicateManifestPathsAndNonCanonicalID(t *testing.T) {
	t.Run("duplicate manifest paths", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "20260522T043000Z-dupepath.tar.gz")
		manifest := Manifest{
			FormatVersion: ManifestFormatVersion,
			BackupID:      "20260522T043000Z-dupepath",
			CreatedAt:     fixedNow(),
			CreatedBy:     "cli",
			BackupReason:  "manual",
			Files: []FileManifest{
				{Path: "world-data/level.dat", Size: 0, SHA256: emptySHA256},
				{Path: "world-data/level.dat", Size: 0, SHA256: emptySHA256},
			},
		}
		writeCustomArchive(t, path, manifest, func(tw *tar.Writer) {
			if err := tw.WriteHeader(&tar.Header{Name: "world-data/level.dat", Typeflag: tar.TypeReg, Size: 0}); err != nil {
				t.Fatalf("write header: %v", err)
			}
		})
		if _, err := ValidateArchive(path); err == nil || !strings.Contains(err.Error(), "duplicate manifest") {
			t.Fatalf("ValidateArchive error = %v, want duplicate manifest path rejection", err)
		}
	})
	t.Run("non canonical backup id", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "not-canonical.tar.gz")
		manifest := Manifest{
			FormatVersion: ManifestFormatVersion,
			BackupID:      "not-canonical",
			CreatedAt:     fixedNow(),
			CreatedBy:     "cli",
			BackupReason:  "manual",
		}
		writeCustomArchive(t, path, manifest, func(*tar.Writer) {})
		if _, err := ValidateArchive(path); err == nil || !strings.Contains(err.Error(), "canonical") {
			t.Fatalf("ValidateArchive error = %v, want canonical id rejection", err)
		}
	})
}

func TestValidateArchiveRejectsConfigManifestMismatch(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "20260522T043000Z-cfgmismt.tar.gz")
	configBytes := []byte("config")
	sum := sha256Hex(configBytes)
	manifest := Manifest{
		FormatVersion:       ManifestFormatVersion,
		BackupID:            "20260522T043000Z-cfgmismt",
		CreatedAt:           fixedNow(),
		CreatedBy:           "cli",
		BackupReason:        "manual",
		IncludeMCServerTOML: false,
		Files:               []FileManifest{{Path: "mc-server.toml", Size: int64(len(configBytes)), SHA256: sum}},
	}
	writeCustomArchive(t, path, manifest, func(tw *tar.Writer) {
		if err := tw.WriteHeader(&tar.Header{Name: "mc-server.toml", Typeflag: tar.TypeReg, Size: int64(len(configBytes))}); err != nil {
			t.Fatalf("write config header: %v", err)
		}
		if _, err := tw.Write(configBytes); err != nil {
			t.Fatalf("write config: %v", err)
		}
	})
	if _, err := ValidateArchive(path); err == nil || !strings.Contains(err.Error(), "include_mc_server_toml") {
		t.Fatalf("ValidateArchive error = %v, want config flag mismatch rejection", err)
	}
}

func TestValidateArchiveRejectsReservedPathDirectories(t *testing.T) {
	for _, entryName := range []string{"manifest.json", "mc-server.toml"} {
		t.Run(entryName, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "20260522T043000Z-reservd1.tar.gz")
			manifest := Manifest{
				FormatVersion:       ManifestFormatVersion,
				BackupID:            "20260522T043000Z-reservd1",
				CreatedAt:           fixedNow(),
				CreatedBy:           "cli",
				BackupReason:        "manual",
				IncludeMCServerTOML: false,
			}
			writeCustomArchive(t, path, manifest, func(tw *tar.Writer) {
				if err := tw.WriteHeader(&tar.Header{Name: entryName, Typeflag: tar.TypeDir}); err != nil {
					t.Fatalf("write directory header: %v", err)
				}
			})
			if _, err := ValidateArchive(path); err == nil || !strings.Contains(err.Error(), "regular file") {
				t.Fatalf("ValidateArchive error = %v, want reserved path directory rejection", err)
			}
		})
	}
}

func TestValidateArchiveRejectsSymlinkArchive(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "data", "minecraft")
	backups := filepath.Join(root, "backups")
	configPath := filepath.Join(root, "mc-server.toml")
	writeFile(t, filepath.Join(source, "world", "level.dat"), "level")
	writeFile(t, configPath, mcconfig.Render(mcconfig.Defaults()))
	created, err := Create(context.Background(), CreateOptions{
		SourceDir:     source,
		ConfigPath:    configPath,
		BackupDir:     backups,
		Policy:        mcconfig.Defaults().Backup,
		StoppedProven: true,
		Now:           fixedNow,
		RandomSuffix:  fixedSuffix("symlink1"),
		NoRetention:   true,
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	linkPath := filepath.Join(root, "20260522T043000Z-symlink1.tar.gz")
	if err := os.Symlink(created.ArchivePath, linkPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := ValidateArchive(linkPath); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("ValidateArchive error = %v, want symlink rejection", err)
	}
}

func TestAcquireLockBlocksActiveAndRecoversStale(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "backups")
	now := time.Now().UTC()
	first, err := AcquireLock(dir, LockMetadata{
		Operation: "create",
		Owner:     "cli",
		BackupID:  "20260522T043000Z-locktest",
		CreatedAt: now,
		StaleAt:   now.Add(time.Hour),
		Status:    "active",
	})
	if err != nil {
		t.Fatalf("AcquireLock first failed: %v", err)
	}
	if _, err := AcquireLock(dir, LockMetadata{Operation: "prune", Owner: "cli", CreatedAt: now, StaleAt: now.Add(time.Hour)}); err == nil {
		t.Fatal("AcquireLock with active lock succeeded, want contention")
	}
	if err := first.Release(); err != nil {
		t.Fatalf("Release failed: %v", err)
	}
	stalePath := filepath.Join(dir, LockDirName, lockFileName)
	stale := LockMetadata{Operation: "create", Owner: "cli", CreatedAt: now.Add(-time.Hour), StaleAt: now.Add(-time.Minute), Status: "stale"}
	data, err := json.Marshal(stale)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, stalePath, string(data))
	recovered, err := AcquireLock(dir, LockMetadata{Operation: "restore", Owner: "cli", CreatedAt: now, StaleAt: now.Add(time.Hour)})
	if err != nil {
		t.Fatalf("AcquireLock did not recover stale lock: %v", err)
	}
	if err := recovered.Release(); err != nil {
		t.Fatalf("Release recovered failed: %v", err)
	}
}

func TestLockReleaseDoesNotRemoveSuccessorAfterStaleRecovery(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "backups")
	now := time.Now().UTC()
	original, err := AcquireLock(dir, LockMetadata{
		Operation: "create",
		Owner:     "first",
		CreatedAt: now.Add(-2 * time.Hour),
		StaleAt:   now.Add(-time.Hour),
		Status:    "stale original",
	})
	if err != nil {
		t.Fatalf("AcquireLock original failed: %v", err)
	}
	successor, err := AcquireLock(dir, LockMetadata{
		Operation: "restore",
		Owner:     "second",
		CreatedAt: now,
		StaleAt:   now.Add(time.Hour),
		Status:    "successor active",
	})
	if err != nil {
		t.Fatalf("AcquireLock successor failed: %v", err)
	}
	if err := original.Release(); err != nil {
		t.Fatalf("Release original failed: %v", err)
	}
	if _, err := AcquireLock(dir, LockMetadata{
		Operation: "prune",
		Owner:     "third",
		CreatedAt: now,
		StaleAt:   now.Add(time.Hour),
		Status:    "should block",
	}); err == nil {
		t.Fatal("AcquireLock after stale owner's Release succeeded, successor lock was removed")
	}
	if err := successor.Release(); err != nil {
		t.Fatalf("Release successor failed: %v", err)
	}
}

func TestSameLockInstanceRequiresUnchangedTokenLease(t *testing.T) {
	now := time.Now().UTC()
	first := LockMetadata{Token: "same-token", StaleAt: now}
	refreshed := LockMetadata{Token: "same-token", StaleAt: now.Add(time.Minute)}
	if sameLockInstance(first, refreshed) {
		t.Fatal("sameLockInstance matched refreshed token lease, want changed lease to block stale removal")
	}
}

func TestLockRefreshDoesNotOverwriteSuccessorAfterStaleRecovery(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "backups")
	now := time.Now().UTC()
	original, err := AcquireLock(dir, LockMetadata{
		Operation: "create",
		Owner:     "first",
		CreatedAt: now.Add(-2 * time.Hour),
		StaleAt:   now.Add(-time.Hour),
		Status:    "stale original",
	})
	if err != nil {
		t.Fatalf("AcquireLock original failed: %v", err)
	}
	successor, err := AcquireLock(dir, LockMetadata{
		Operation: "restore",
		Owner:     "second",
		CreatedAt: now,
		StaleAt:   now.Add(time.Hour),
		Status:    "successor active",
	})
	if err != nil {
		t.Fatalf("AcquireLock successor failed: %v", err)
	}
	if err := original.Refresh(now.Add(2*time.Hour), "stale owner refresh"); err == nil {
		t.Fatal("stale owner Refresh succeeded after successor acquired lock")
	}
	metadata, err := readLock(filepath.Join(dir, LockDirName, lockFileName))
	if err != nil {
		t.Fatalf("read successor lock failed: %v", err)
	}
	if metadata.Owner != "second" || metadata.Operation != "restore" {
		t.Fatalf("successor lock overwritten by stale owner: %+v", metadata)
	}
	if err := successor.Release(); err != nil {
		t.Fatalf("Release successor failed: %v", err)
	}
}

func TestLockHeartbeatKeepsExpiredOwnerActive(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "backups")
	now := time.Now().UTC()
	lock, err := AcquireLock(dir, LockMetadata{
		Operation: "create",
		Owner:     "test",
		CreatedAt: now.Add(-time.Hour),
		StaleAt:   now.Add(-time.Minute),
		Status:    "long-running backup",
	})
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}
	stopHeartbeat, _, err := lock.StartHeartbeat(10*time.Millisecond, time.Hour)
	if err != nil {
		t.Fatalf("StartHeartbeat failed: %v", err)
	}
	defer lock.Release()
	defer stopHeartbeat()
	metadata, err := readLock(filepath.Join(dir, LockDirName, lockFileName))
	if err != nil {
		t.Fatalf("read refreshed lock failed: %v", err)
	}
	if !metadata.StaleAt.After(time.Now().UTC()) {
		t.Fatalf("heartbeat did not refresh stale deadline: %s", metadata.StaleAt)
	}
	if _, err := AcquireLock(dir, LockMetadata{Operation: "prune", Owner: "contender"}); err == nil || !strings.Contains(err.Error(), "active") {
		t.Fatalf("AcquireLock contender error = %v, want active lock contention", err)
	}
}

func TestLockHeartbeatSurvivesMutationGuardContention(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "backups")
	now := time.Now().UTC()
	lock, err := AcquireLock(dir, LockMetadata{
		Operation: "create",
		Owner:     "test",
		CreatedAt: now,
		StaleAt:   now.Add(50 * time.Millisecond),
		Status:    "long-running backup",
	})
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}
	stopHeartbeat, _, err := lock.StartHeartbeat(10*time.Millisecond, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("StartHeartbeat failed: %v", err)
	}
	defer lock.Release()
	defer stopHeartbeat()
	guard, err := acquireLockMutationGuard(lock.path)
	if err != nil {
		t.Fatalf("acquire mutation guard failed: %v", err)
	}
	time.Sleep(80 * time.Millisecond)
	if err := guard.Release(); err != nil {
		t.Fatalf("release mutation guard failed: %v", err)
	}
	time.Sleep(30 * time.Millisecond)
	if _, err := AcquireLock(dir, LockMetadata{Operation: "prune", Owner: "contender"}); err == nil || !strings.Contains(err.Error(), "active") {
		t.Fatalf("AcquireLock contender error = %v, want heartbeat to survive transient guard contention", err)
	}
}

func TestCreateRefreshesLockWhileQuiesceIsRunning(t *testing.T) {
	oldInterval := lockHeartbeatInterval
	lockHeartbeatInterval = 10 * time.Millisecond
	defer func() { lockHeartbeatInterval = oldInterval }()
	root := t.TempDir()
	source := filepath.Join(root, "data", "minecraft")
	backups := filepath.Join(root, "backups")
	configPath := filepath.Join(root, "mc-server.toml")
	writeFile(t, filepath.Join(source, "world", "level.dat"), "level")
	writeFile(t, configPath, mcconfig.Render(mcconfig.Defaults()))
	q := &blockingFlushQuiescer{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
	resultCh := make(chan error, 1)
	staleNow := func() time.Time { return time.Now().UTC().Add(-time.Hour) }
	go func() {
		_, err := Create(context.Background(), CreateOptions{
			SourceDir:     source,
			ConfigPath:    configPath,
			BackupDir:     backups,
			Policy:        mcconfig.Defaults().Backup,
			StoppedProven: false,
			Quiescer:      q,
			Now:           staleNow,
			RandomSuffix:  fixedSuffix("livebeat"),
			NoRetention:   true,
		})
		resultCh <- err
	}()
	select {
	case <-q.entered:
	case <-time.After(time.Second):
		t.Fatal("Create did not reach blocking quiesce point")
	}
	if _, err := AcquireLock(backups, LockMetadata{Operation: "prune", Owner: "contender"}); err == nil || !strings.Contains(err.Error(), "active") {
		close(q.release)
		t.Fatalf("AcquireLock contender error = %v, want active lock contention", err)
	}
	close(q.release)
	select {
	case err := <-resultCh:
		if err != nil {
			t.Fatalf("Create failed: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Create did not finish after quiesce release")
	}
}

func TestPruneUsesCanonicalLock(t *testing.T) {
	backups := filepath.Join(t.TempDir(), "backups")
	now := time.Now().UTC()
	lock, err := AcquireLock(backups, LockMetadata{
		Operation: "create",
		Owner:     "test",
		CreatedAt: now,
		StaleAt:   now.Add(time.Hour),
		Status:    "active",
	})
	if err != nil {
		t.Fatalf("AcquireLock failed: %v", err)
	}
	defer lock.Release()
	if _, err := Prune(context.Background(), PruneOptions{BackupDir: backups, Policy: mcconfig.Defaults().Backup}); err == nil || !strings.Contains(err.Error(), "active") {
		t.Fatalf("Prune error = %v, want active lock contention", err)
	}
}

func TestPruneRetainsNewestCountAndProtectsSafetyBackups(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "data", "minecraft")
	backups := filepath.Join(root, "backups")
	configPath := filepath.Join(root, "mc-server.toml")
	writeFile(t, filepath.Join(source, "world", "level.dat"), "level")
	writeFile(t, configPath, mcconfig.Render(mcconfig.Defaults()))
	policy := mcconfig.Defaults().Backup
	policy.RetentionCount = 2

	createAt := func(ts string, suffix string, reason string) {
		t.Helper()
		when, err := time.Parse(time.RFC3339, ts)
		if err != nil {
			t.Fatal(err)
		}
		_, err = Create(context.Background(), CreateOptions{
			SourceDir:     source,
			ConfigPath:    configPath,
			BackupDir:     backups,
			Policy:        policy,
			Reason:        reason,
			StoppedProven: true,
			Now:           func() time.Time { return when },
			RandomSuffix:  fixedSuffix(suffix),
			NoRetention:   true,
		})
		if err != nil {
			t.Fatalf("Create(%s) failed: %v", suffix, err)
		}
	}
	createAt("2026-05-20T04:30:00Z", "aaaaaaa1", "manual")
	createAt("2026-05-21T04:30:00Z", "aaaaaaa2", "restore-safety")
	createAt("2026-05-22T04:30:00Z", "aaaaaaa3", "manual")
	createAt("2026-05-23T04:30:00Z", "aaaaaaa4", "manual")

	result, err := Prune(context.Background(), PruneOptions{BackupDir: backups, Policy: policy})
	if err != nil {
		t.Fatalf("Prune failed: %v", err)
	}
	if len(result.Deleted) != 1 || !strings.HasSuffix(result.Deleted[0].BackupID, "aaaaaaa1") {
		t.Fatalf("deleted = %+v, want oldest regular only", result.Deleted)
	}
	if len(result.Protected) != 1 || !strings.Contains(result.Protected[0].Reason, "safety") {
		t.Fatalf("protected = %+v, want safety backup", result.Protected)
	}
	if _, err := os.Stat(filepath.Join(backups, result.Deleted[0].BackupID+".tar.gz")); !os.IsNotExist(err) {
		t.Fatalf("deleted backup still exists or stat failed: %v", err)
	}
}

func TestCreateRetentionCountsCurrentBackup(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "data", "minecraft")
	backups := filepath.Join(root, "backups")
	configPath := filepath.Join(root, "mc-server.toml")
	writeFile(t, filepath.Join(source, "world", "level.dat"), "level")
	writeFile(t, configPath, mcconfig.Render(mcconfig.Defaults()))
	policy := mcconfig.Defaults().Backup
	policy.RetentionCount = 1
	create := func(ts, suffix string) {
		t.Helper()
		when, err := time.Parse(time.RFC3339, ts)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Create(context.Background(), CreateOptions{
			SourceDir:     source,
			ConfigPath:    configPath,
			BackupDir:     backups,
			Policy:        policy,
			StoppedProven: true,
			Now:           func() time.Time { return when },
			RandomSuffix:  fixedSuffix(suffix),
		}); err != nil {
			t.Fatalf("Create(%s) failed: %v", suffix, err)
		}
	}
	create("2026-05-21T04:30:00Z", "retentn1")
	create("2026-05-22T04:30:00Z", "retentn2")
	listed, err := List(context.Background(), backups)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(listed.Valid) != 1 || !strings.HasSuffix(listed.Valid[0].BackupID, "retentn2") {
		t.Fatalf("retained backups = %+v, want only newest current backup", listed.Valid)
	}
}

func TestCreateRetentionMaxBytesPreservesCurrentBackup(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "data", "minecraft")
	backups := filepath.Join(root, "backups")
	configPath := filepath.Join(root, "mc-server.toml")
	writeFile(t, filepath.Join(source, "world", "level.dat"), strings.Repeat("x", 1024))
	writeFile(t, configPath, mcconfig.Render(mcconfig.Defaults()))
	policy := mcconfig.Defaults().Backup
	policy.RetentionCount = 10
	policy.RetentionMaxBytes = 1

	result, err := Create(context.Background(), CreateOptions{
		SourceDir:     source,
		ConfigPath:    configPath,
		BackupDir:     backups,
		Policy:        policy,
		StoppedProven: true,
		Now:           fixedNow,
		RandomSuffix:  fixedSuffix("sizelive"),
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if _, err := os.Stat(result.ArchivePath); err != nil {
		t.Fatalf("created archive missing after retention: %v", err)
	}
	listed, err := List(context.Background(), backups)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(listed.Valid) != 1 || listed.Valid[0].BackupID != result.BackupID {
		t.Fatalf("retained backups = %+v, want current backup %s", listed.Valid, result.BackupID)
	}
	if result.RetentionResult == nil {
		t.Fatal("Create did not report retention result")
	}
	for _, deleted := range result.RetentionResult.Deleted {
		if deleted.BackupID == result.BackupID {
			t.Fatalf("retention deleted current backup: %+v", deleted)
		}
	}
}

func TestRestoreRejectsUnsafeTargetRoot(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "data", "minecraft")
	backups := filepath.Join(root, "backups")
	configPath := filepath.Join(root, "mc-server.toml")
	writeFile(t, filepath.Join(source, "world", "level.dat"), "level")
	writeFile(t, configPath, mcconfig.Render(mcconfig.Defaults()))
	created, err := Create(context.Background(), CreateOptions{
		SourceDir:     source,
		ConfigPath:    configPath,
		BackupDir:     backups,
		Policy:        mcconfig.Defaults().Backup,
		StoppedProven: true,
		Now:           fixedNow,
		RandomSuffix:  fixedSuffix("badroot1"),
		NoRetention:   true,
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	target := filepath.Join(root, "data")
	writeFile(t, filepath.Join(target, "mcbot", "state.json"), "{}")
	if _, err := Restore(context.Background(), RestoreOptions{
		BackupDir:     backups,
		BackupID:      created.BackupID,
		TargetDir:     target,
		ConfigPath:    configPath,
		Policy:        mcconfig.Defaults().Backup,
		StoppedProven: true,
		UID:           os.Getuid(),
		GID:           os.Getgid(),
		Now:           fixedNow,
	}); err == nil || !strings.Contains(err.Error(), "protected path") {
		t.Fatalf("Restore error = %v, want protected target rejection", err)
	}
	if got := readFile(t, filepath.Join(target, "mcbot", "state.json")); got != "{}" {
		t.Fatalf("protected bot data changed: %q", got)
	}
}

func TestRestoreRejectsSymlinkTargetComponent(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	outside := filepath.Join(root, "outside")
	dataLink := filepath.Join(root, "data")
	backups := filepath.Join(root, "backups")
	configPath := filepath.Join(root, "mc-server.toml")
	writeFile(t, filepath.Join(source, "world", "level.dat"), "level")
	writeFile(t, filepath.Join(outside, "minecraft", "old.dat"), "old")
	writeFile(t, configPath, mcconfig.Render(mcconfig.Defaults()))
	if err := os.Symlink(outside, dataLink); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	created, err := Create(context.Background(), CreateOptions{
		SourceDir:     source,
		ConfigPath:    configPath,
		BackupDir:     backups,
		Policy:        mcconfig.Defaults().Backup,
		StoppedProven: true,
		Now:           fixedNow,
		RandomSuffix:  fixedSuffix("targetln"),
		NoRetention:   true,
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if _, err := Restore(context.Background(), RestoreOptions{
		BackupDir:     backups,
		BackupID:      created.BackupID,
		TargetDir:     filepath.Join(dataLink, "minecraft"),
		ConfigPath:    configPath,
		Policy:        mcconfig.Defaults().Backup,
		StoppedProven: true,
		UID:           os.Getuid(),
		GID:           os.Getgid(),
		Now:           fixedNow,
	}); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("Restore error = %v, want symlink component rejection", err)
	}
	if got := readFile(t, filepath.Join(outside, "minecraft", "old.dat")); got != "old" {
		t.Fatalf("escaped target changed: %q", got)
	}
}

func TestRestoreRejectsSymlinkBackupDirectoryBeforeLock(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "data", "minecraft")
	realBackups := filepath.Join(root, "real-backups")
	backupLink := filepath.Join(root, "backup-link")
	configPath := filepath.Join(root, "mc-server.toml")
	writeFile(t, filepath.Join(source, "world", "level.dat"), "new")
	writeFile(t, filepath.Join(target, "old.dat"), "old")
	writeFile(t, configPath, mcconfig.Render(mcconfig.Defaults()))
	created, err := Create(context.Background(), CreateOptions{
		SourceDir:     source,
		ConfigPath:    configPath,
		BackupDir:     realBackups,
		Policy:        mcconfig.Defaults().Backup,
		StoppedProven: true,
		Now:           fixedNow,
		RandomSuffix:  fixedSuffix("backdirl"),
		NoRetention:   true,
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(realBackups, LockDirName)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realBackups, backupLink); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	_, err = Restore(context.Background(), RestoreOptions{
		BackupDir:     backupLink,
		BackupID:      created.BackupID,
		TargetDir:     target,
		ConfigPath:    configPath,
		RepoRoot:      root,
		Policy:        mcconfig.Defaults().Backup,
		StoppedProven: true,
		UID:           os.Getuid(),
		GID:           os.Getgid(),
		Now:           fixedNow,
	})
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("Restore error = %v, want backup directory symlink rejection", err)
	}
	if _, err := os.Lstat(filepath.Join(realBackups, LockDirName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("restore created lock directory through symlink or stat failed: %v", err)
	}
	if got := readFile(t, filepath.Join(target, "old.dat")); got != "old" {
		t.Fatalf("target changed after rejected symlink backup directory: %q", got)
	}
}

func TestRestoreRejectsSymlinkConfigPath(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "data", "minecraft")
	backups := filepath.Join(root, "backups")
	configPath := filepath.Join(root, "mc-server.toml")
	outsideConfig := filepath.Join(root, "outside.toml")
	writeFile(t, filepath.Join(source, "world", "level.dat"), "new")
	writeFile(t, filepath.Join(target, "old.dat"), "old")
	writeFile(t, configPath, mcconfig.Render(mcconfig.Defaults()))
	writeFile(t, outsideConfig, "outside")
	created, err := Create(context.Background(), CreateOptions{
		SourceDir:     source,
		ConfigPath:    configPath,
		BackupDir:     backups,
		Policy:        mcconfig.Defaults().Backup,
		StoppedProven: true,
		Now:           fixedNow,
		RandomSuffix:  fixedSuffix("cfgsymbl"),
		NoRetention:   true,
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if err := os.Remove(configPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideConfig, configPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := Restore(context.Background(), RestoreOptions{
		BackupDir:     backups,
		BackupID:      created.BackupID,
		TargetDir:     target,
		ConfigPath:    configPath,
		RepoRoot:      root,
		Policy:        mcconfig.Defaults().Backup,
		StoppedProven: true,
		UID:           os.Getuid(),
		GID:           os.Getgid(),
		Now:           fixedNow,
	}); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("Restore error = %v, want symlink config rejection", err)
	}
	if got := readFile(t, outsideConfig); got != "outside" {
		t.Fatalf("outside config changed: %q", got)
	}
}

func TestRestoreDoesNotPreserveStaleDataDirectory(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "data", "minecraft")
	backups := filepath.Join(root, "backups")
	configPath := filepath.Join(root, "mc-server.toml")
	writeFile(t, filepath.Join(source, "world", "level.dat"), "world")
	writeFile(t, filepath.Join(source, "data", "scoreboard.dat"), "from-archive")
	writeFile(t, filepath.Join(target, "data", "scoreboard.dat"), "stale-local")
	writeFile(t, configPath, mcconfig.Render(mcconfig.Defaults()))
	created, err := Create(context.Background(), CreateOptions{
		SourceDir:     source,
		ConfigPath:    configPath,
		BackupDir:     backups,
		Policy:        mcconfig.Defaults().Backup,
		StoppedProven: true,
		Now:           fixedNow,
		RandomSuffix:  fixedSuffix("staledat"),
		NoRetention:   true,
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if _, err := Restore(context.Background(), RestoreOptions{
		BackupDir:     backups,
		BackupID:      created.BackupID,
		TargetDir:     target,
		ConfigPath:    configPath,
		RepoRoot:      root,
		Policy:        mcconfig.Defaults().Backup,
		StoppedProven: true,
		UID:           os.Getuid(),
		GID:           os.Getgid(),
		Now:           fixedNow,
	}); err != nil {
		t.Fatalf("Restore failed: %v", err)
	}
	if got := readFile(t, filepath.Join(target, "data", "scoreboard.dat")); got != "from-archive" {
		t.Fatalf("restored data = %q, want archive contents not preserved stale target data", got)
	}
}

func TestCopyPreservedRestoreEntriesRejectsSymlinkFile(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	replacement := filepath.Join(root, "replacement")
	outside := filepath.Join(root, "outside.env")
	writeFile(t, outside, "secret")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(replacement, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(target, ".env")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := copyPreservedRestoreEntries(target, replacement); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("copyPreservedRestoreEntries error = %v, want symlink rejection", err)
	}
	if _, err := os.Lstat(filepath.Join(replacement, ".env")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("replacement .env exists or stat failed after rejected symlink: %v", err)
	}
}

func TestCopyTreeRejectsNestedSymlink(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "src")
	dst := filepath.Join(root, "dst")
	outside := filepath.Join(root, "outside.txt")
	writeFile(t, filepath.Join(src, "state", "ok.json"), "{}")
	writeFile(t, outside, "secret")
	if err := os.Symlink(outside, filepath.Join(src, "state", "linked-secret.txt")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := copyTree(src, dst); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("copyTree error = %v, want symlink rejection", err)
	}
	if _, err := os.Lstat(filepath.Join(dst, "state", "linked-secret.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("destination symlink exists or stat failed after rejected copy: %v", err)
	}
}

func TestCopyFileRejectsDestinationSymlink(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "source.txt")
	dst := filepath.Join(root, "dest.txt")
	outside := filepath.Join(root, "outside.txt")
	writeFile(t, src, "new")
	writeFile(t, outside, "outside")
	if err := os.Symlink(outside, dst); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := copyFile(src, dst, 0o644); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("copyFile error = %v, want destination symlink rejection", err)
	}
	if got := readFile(t, outside); got != "outside" {
		t.Fatalf("outside destination target changed: %q", got)
	}
}

func TestNormalizeTreeRejectsSymlink(t *testing.T) {
	root := t.TempDir()
	replacement := filepath.Join(root, "replacement")
	outside := filepath.Join(root, "outside.txt")
	writeFile(t, filepath.Join(replacement, "world", "level.dat"), "level")
	writeFile(t, outside, "secret")
	if err := os.Symlink(outside, filepath.Join(replacement, "world", "linked-secret.txt")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	before, err := os.Lstat(outside)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := normalizeTree(replacement, os.Getuid(), os.Getgid()); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("normalizeTree error = %v, want symlink rejection", err)
	}
	after, err := os.Lstat(outside)
	if err != nil {
		t.Fatal(err)
	}
	if after.Mode().Perm() != before.Mode().Perm() {
		t.Fatalf("outside mode changed from %v to %v", before.Mode().Perm(), after.Mode().Perm())
	}
}

func TestRestoreSafetyBackupIncludesConfigWhenArchiveRestoresConfig(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "data", "minecraft")
	backups := filepath.Join(root, "backups")
	configPath := filepath.Join(root, "mc-server.toml")
	writeFile(t, filepath.Join(source, "world", "level.dat"), "new")
	writeFile(t, filepath.Join(target, "old.dat"), "old")
	writeFile(t, configPath, mcconfig.Render(mcconfig.Defaults()))
	created, err := Create(context.Background(), CreateOptions{
		SourceDir:     source,
		ConfigPath:    configPath,
		BackupDir:     backups,
		Policy:        mcconfig.Defaults().Backup,
		StoppedProven: true,
		Now:           fixedNow,
		RandomSuffix:  fixedSuffix("cfgsafe1"),
		NoRetention:   true,
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	restorePolicy := mcconfig.Defaults().Backup
	restorePolicy.IncludeMCServerTOML = false
	result, err := Restore(context.Background(), RestoreOptions{
		BackupDir:     backups,
		BackupID:      created.BackupID,
		TargetDir:     target,
		ConfigPath:    configPath,
		RepoRoot:      root,
		Policy:        restorePolicy,
		StoppedProven: true,
		UID:           os.Getuid(),
		GID:           os.Getgid(),
		Now:           fixedNow,
	})
	if err != nil {
		t.Fatalf("Restore failed: %v", err)
	}
	validation, err := ValidateArchive(result.SafetyArchivePath)
	if err != nil {
		t.Fatalf("Validate safety archive failed: %v", err)
	}
	if !validation.Manifest.IncludeMCServerTOML {
		t.Fatal("safety backup did not include current config before config-restoring archive")
	}
}

func TestRestoreConfigArchiveSucceedsWhenCurrentConfigMissing(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "data", "minecraft")
	backups := filepath.Join(root, "backups")
	configPath := filepath.Join(root, "mc-server.toml")
	configContents := mcconfig.Render(mcconfig.Defaults())
	writeFile(t, filepath.Join(source, "world", "level.dat"), "new")
	writeFile(t, filepath.Join(target, "old.dat"), "old")
	writeFile(t, configPath, configContents)
	created, err := Create(context.Background(), CreateOptions{
		SourceDir:     source,
		ConfigPath:    configPath,
		BackupDir:     backups,
		Policy:        mcconfig.Defaults().Backup,
		StoppedProven: true,
		Now:           fixedNow,
		RandomSuffix:  fixedSuffix("cfgmiss1"),
		NoRetention:   true,
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if err := os.Remove(configPath); err != nil {
		t.Fatal(err)
	}
	result, err := Restore(context.Background(), RestoreOptions{
		BackupDir:     backups,
		BackupID:      created.BackupID,
		TargetDir:     target,
		ConfigPath:    configPath,
		RepoRoot:      root,
		Policy:        mcconfig.Defaults().Backup,
		StoppedProven: true,
		UID:           os.Getuid(),
		GID:           os.Getgid(),
		Now:           fixedNow,
	})
	if err != nil {
		t.Fatalf("Restore with missing current config failed: %v", err)
	}
	if !result.RestoredConfig {
		t.Fatal("Restore did not restore archived config")
	}
	if got := readFile(t, configPath); got != configContents {
		t.Fatalf("restored config = %q, want original archive config", got)
	}
	if got := readFile(t, filepath.Join(target, "world", "level.dat")); got != "new" {
		t.Fatalf("restored world = %q", got)
	}
	validation, err := ValidateArchive(result.SafetyArchivePath)
	if err != nil {
		t.Fatalf("Validate safety archive failed: %v", err)
	}
	if validation.Manifest.IncludeMCServerTOML {
		t.Fatal("safety backup included missing current config")
	}
}

func TestRestoreRejectsInvalidStagedConfigBeforeReplacingTarget(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "data", "minecraft")
	backups := filepath.Join(root, "backups")
	configPath := filepath.Join(root, "mc-server.toml")
	archivePath := filepath.Join(backups, "20260522T043000Z-badconf1.tar.gz")
	oldConfig := mcconfig.Render(mcconfig.Defaults())
	newWorld := []byte("new")
	invalidConfig := []byte("unknown_key = true\n")
	writeFile(t, filepath.Join(target, "world", "level.dat"), "old")
	writeFile(t, configPath, oldConfig)
	if err := os.MkdirAll(backups, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{
		FormatVersion:       ManifestFormatVersion,
		BackupID:            "20260522T043000Z-badconf1",
		CreatedAt:           fixedNow(),
		CreatedBy:           "cli",
		BackupReason:        "manual",
		IncludeMCServerTOML: true,
		Files: []FileManifest{
			{Path: "world-data/world/level.dat", Size: int64(len(newWorld)), SHA256: sha256Hex(newWorld)},
			{Path: "mc-server.toml", Size: int64(len(invalidConfig)), SHA256: sha256Hex(invalidConfig)},
		},
	}
	writeCustomArchive(t, archivePath, manifest, func(tw *tar.Writer) {
		if err := tw.WriteHeader(&tar.Header{Name: "world-data/world/level.dat", Typeflag: tar.TypeReg, Size: int64(len(newWorld))}); err != nil {
			t.Fatalf("write world header: %v", err)
		}
		if _, err := tw.Write(newWorld); err != nil {
			t.Fatalf("write world: %v", err)
		}
		if err := tw.WriteHeader(&tar.Header{Name: "mc-server.toml", Typeflag: tar.TypeReg, Size: int64(len(invalidConfig))}); err != nil {
			t.Fatalf("write config header: %v", err)
		}
		if _, err := tw.Write(invalidConfig); err != nil {
			t.Fatalf("write config: %v", err)
		}
	})
	if _, err := Restore(context.Background(), RestoreOptions{
		BackupDir:     backups,
		BackupID:      "20260522T043000Z-badconf1",
		TargetDir:     target,
		ConfigPath:    configPath,
		RepoRoot:      root,
		Policy:        mcconfig.Defaults().Backup,
		StoppedProven: true,
		UID:           os.Getuid(),
		GID:           os.Getgid(),
		Now:           fixedNow,
	}); err == nil || !strings.Contains(err.Error(), "staged mc-server.toml is invalid") {
		t.Fatalf("Restore error = %v, want invalid staged config rejection", err)
	}
	if got := readFile(t, filepath.Join(target, "world", "level.dat")); got != "old" {
		t.Fatalf("target world changed before config validation failure: %q", got)
	}
	if got := readFile(t, configPath); got != oldConfig {
		t.Fatalf("config changed before config validation failure")
	}
}

func TestNormalizePathReportsChownFailureEvenWhenOperatorCanWrite(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "level.dat")
	writeFile(t, path, "level")
	oldChown := chownPath
	chownPath = func(string, int, int) error {
		return fmt.Errorf("operation not permitted")
	}
	t.Cleanup(func() { chownPath = oldChown })

	err := normalizePath(path, 1001, 1001, false)
	if err == nil || !strings.Contains(err.Error(), "normalize ownership") {
		t.Fatalf("normalizePath error = %v, want ownership failure", err)
	}
	if got := readFile(t, path); got != "level" {
		t.Fatalf("normalizePath changed file contents: %q", got)
	}
}

func TestRestoreNormalizationFailureLeavesTargetUnchanged(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "data", "minecraft")
	backups := filepath.Join(root, "backups")
	configPath := filepath.Join(root, "mc-server.toml")
	writeFile(t, filepath.Join(source, "world", "level.dat"), "new")
	writeFile(t, filepath.Join(target, "old-world", "old.dat"), "old")
	writeFile(t, configPath, mcconfig.Render(mcconfig.Defaults()))
	created, err := Create(context.Background(), CreateOptions{
		SourceDir:     source,
		ConfigPath:    configPath,
		BackupDir:     backups,
		Policy:        mcconfig.Defaults().Backup,
		StoppedProven: true,
		Now:           fixedNow,
		RandomSuffix:  fixedSuffix("normfail"),
		NoRetention:   true,
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	oldChown := chownPath
	chownPath = func(string, int, int) error {
		return fmt.Errorf("operation not permitted")
	}
	t.Cleanup(func() { chownPath = oldChown })

	_, err = Restore(context.Background(), RestoreOptions{
		BackupDir:     backups,
		BackupID:      created.BackupID,
		TargetDir:     target,
		ConfigPath:    configPath,
		RepoRoot:      root,
		Policy:        mcconfig.Defaults().Backup,
		StoppedProven: true,
		UID:           1001,
		GID:           1001,
		Now:           fixedNow,
	})
	if err == nil || !strings.Contains(err.Error(), "normalize staged restore tree") {
		t.Fatalf("Restore error = %v, want staged normalization failure", err)
	}
	if got := readFile(t, filepath.Join(target, "old-world", "old.dat")); got != "old" {
		t.Fatalf("target old-world changed after failed normalization: %q", got)
	}
	if _, err := os.Stat(filepath.Join(target, "world", "level.dat")); !os.IsNotExist(err) {
		t.Fatalf("new restored world exists after failed normalization or stat failed: %v", err)
	}
}

func TestRestoreRequiresStoppedServerAndReplacesTarget(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	target := filepath.Join(root, "data", "minecraft")
	backups := filepath.Join(root, "backups")
	configPath := filepath.Join(root, "mc-server.toml")
	writeFile(t, filepath.Join(source, "world", "level.dat"), "new")
	writeFile(t, filepath.Join(target, "old-world", "old.dat"), "old")
	writeFile(t, configPath, mcconfig.Render(mcconfig.Defaults()))
	created, err := Create(context.Background(), CreateOptions{
		SourceDir:     source,
		ConfigPath:    configPath,
		BackupDir:     backups,
		Policy:        mcconfig.Defaults().Backup,
		StoppedProven: true,
		Now:           fixedNow,
		RandomSuffix:  fixedSuffix("restore1"),
		NoRetention:   true,
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}
	if _, err := Restore(context.Background(), RestoreOptions{
		BackupDir:     backups,
		BackupID:      created.BackupID,
		TargetDir:     target,
		ConfigPath:    configPath,
		Policy:        mcconfig.Defaults().Backup,
		StoppedProven: false,
		UID:           os.Getuid(),
		GID:           os.Getgid(),
		Now:           fixedNow,
	}); err == nil {
		t.Fatal("Restore while not stopped succeeded, want error")
	}
	result, err := Restore(context.Background(), RestoreOptions{
		BackupDir:     backups,
		BackupID:      created.BackupID,
		TargetDir:     target,
		ConfigPath:    configPath,
		Policy:        mcconfig.Defaults().Backup,
		StoppedProven: true,
		UID:           os.Getuid(),
		GID:           os.Getgid(),
		Now:           fixedNow,
	})
	if err != nil {
		t.Fatalf("Restore failed: %v", err)
	}
	if result.SafetyBackupID == "" {
		t.Fatal("Restore did not create safety backup")
	}
	if got := readFile(t, filepath.Join(target, "world", "level.dat")); got != "new" {
		t.Fatalf("restored level.dat = %q", got)
	}
	if _, err := os.Stat(filepath.Join(target, "old-world", "old.dat")); !os.IsNotExist(err) {
		t.Fatalf("old overlay file still exists or stat failed: %v", err)
	}
}

func fixedNow() time.Time {
	return time.Date(2026, 5, 22, 4, 30, 0, 0, time.UTC)
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s failed: %v", path, err)
	}
	return string(data)
}

func fixedSuffix(value string) func() (string, error) {
	return func() (string, error) { return value, nil }
}

type fakeQuiescer struct {
	commands []string
}

func (f *fakeQuiescer) Execute(_ context.Context, command string) (string, error) {
	f.commands = append(f.commands, command)
	return "ok", nil
}

type blockingFlushQuiescer struct {
	commands []string
	entered  chan struct{}
	release  chan struct{}
}

func (f *blockingFlushQuiescer) Execute(ctx context.Context, command string) (string, error) {
	f.commands = append(f.commands, command)
	if command == "save-all flush" {
		close(f.entered)
		select {
		case <-f.release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
	}
	return "ok", nil
}

type cancelAfterFlushQuiescer struct {
	commands []string
	cancel   context.CancelFunc
}

func (f *cancelAfterFlushQuiescer) Execute(_ context.Context, command string) (string, error) {
	f.commands = append(f.commands, command)
	if command == "save-all flush" && f.cancel != nil {
		f.cancel()
	}
	return "ok", nil
}

type failingQuiescer struct {
	commands []string
	failures map[string]error
}

func (f *failingQuiescer) Execute(_ context.Context, command string) (string, error) {
	f.commands = append(f.commands, command)
	if err := f.failures[command]; err != nil {
		return "", err
	}
	return "ok", nil
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s failed: %v", path, err)
	}
}

func tarNames(t *testing.T, path string) []string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gzr, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer gzr.Close()
	tr := tar.NewReader(gzr)
	var names []string
	for {
		header, err := tr.Next()
		if err != nil {
			if err == io.EOF {
				break
			}
			t.Fatal(err)
		}
		names = append(names, header.Name)
	}
	return names
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

const emptySHA256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestAcquireLockWithHeartbeatCancelsOnLockLoss(t *testing.T) {
	oldInterval := lockHeartbeatInterval
	lockHeartbeatInterval = 10 * time.Millisecond
	defer func() { lockHeartbeatInterval = oldInterval }()

	dir := filepath.Join(t.TempDir(), "backups")
	now := time.Now().UTC()
	lock, stop, opCtx, err := acquireLockWithHeartbeat(context.Background(), dir, LockMetadata{
		Operation: "create",
		Owner:     "test",
		BackupID:  "20260522T043000Z-heartbeat",
		CreatedAt: now,
		StaleAt:   now.Add(time.Hour),
		Status:    "long-running backup",
	})
	if err != nil {
		t.Fatalf("acquireLockWithHeartbeat failed: %v", err)
	}
	defer lock.Release()
	defer stop()
	if err := os.Remove(lock.path); err != nil {
		t.Fatalf("remove lock file: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for opCtx.Err() == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if opCtx.Err() == nil {
		t.Fatal("opCtx was not cancelled after backup lock heartbeat loss")
	}
}

func TestCreateRetentionFailurePreservesArchive(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "data", "minecraft")
	backups := filepath.Join(root, "backups")
	configPath := filepath.Join(root, "mc-server.toml")
	writeFile(t, filepath.Join(source, "world", "level.dat"), "level")
	writeFile(t, configPath, mcconfig.Render(mcconfig.Defaults()))
	policy := mcconfig.Defaults().Backup
	policy.RetentionCount = 1
	if _, err := Create(context.Background(), CreateOptions{
		SourceDir:     source,
		ConfigPath:    configPath,
		BackupDir:     backups,
		Policy:        policy,
		StoppedProven: true,
		Now:           fixedNow,
		RandomSuffix:  fixedSuffix("rtnold01"),
	}); err != nil {
		t.Fatalf("seed Create failed: %v", err)
	}
	entries, err := os.ReadDir(backups)
	if err != nil {
		t.Fatalf("ReadDir backups: %v", err)
	}
	var oldArchive string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".tar.gz") {
			oldArchive = filepath.Join(backups, entry.Name())
			break
		}
	}
	if oldArchive == "" {
		t.Fatal("seed backup archive missing")
	}
	if runtime.GOOS == "darwin" {
		if err := exec.Command("chflags", "uchg", oldArchive).Run(); err != nil {
			t.Skipf("chflags uchg unavailable: %v", err)
		}
		defer func() { _ = exec.Command("chflags", "nouchg", oldArchive).Run() }()
	} else if err := os.Chmod(backups, 0o555); err != nil {
		t.Fatalf("chmod backup directory: %v", err)
	} else {
		defer func() { _ = os.Chmod(backups, 0o755) }()
	}
	result, err := Create(context.Background(), CreateOptions{
		SourceDir:     source,
		ConfigPath:    configPath,
		BackupDir:     backups,
		Policy:        policy,
		StoppedProven: true,
		Now:           func() time.Time { return fixedNow().Add(time.Minute) },
		RandomSuffix:  fixedSuffix("rtnnew01"),
	})
	if !errors.Is(err, ErrRetentionAfterCreate) {
		t.Fatalf("Create error = %v, want ErrRetentionAfterCreate", err)
	}
	if result.BackupID == "" || !strings.HasSuffix(result.BackupID, "rtnnew01") {
		t.Fatalf("Create result = %+v, want partial success with new backup id", result)
	}
	if _, err := os.Stat(result.ArchivePath); err != nil {
		t.Fatalf("new archive missing after retention failure: %v", err)
	}
}

func TestListReportsInvalidArchives(t *testing.T) {
	backups := filepath.Join(t.TempDir(), "backups")
	if err := os.MkdirAll(backups, 0o755); err != nil {
		t.Fatal(err)
	}
	badPath := filepath.Join(backups, "not-a-valid-id.tar.gz")
	if err := os.WriteFile(badPath, []byte("not a backup"), 0o644); err != nil {
		t.Fatal(err)
	}
	listed, err := List(context.Background(), backups)
	if err != nil {
		t.Fatalf("List failed: %v", err)
	}
	if len(listed.Valid) != 0 {
		t.Fatalf("valid backups = %+v, want none", listed.Valid)
	}
	if len(listed.Invalid) != 1 || listed.Invalid[0].FileName != "not-a-valid-id.tar.gz" {
		t.Fatalf("invalid backups = %+v, want one invalid entry", listed.Invalid)
	}
}

func TestPruneDeleteFailureDoesNotReportDeleted(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "data", "minecraft")
	backups := filepath.Join(root, "backups")
	configPath := filepath.Join(root, "mc-server.toml")
	writeFile(t, filepath.Join(source, "world", "level.dat"), "level")
	writeFile(t, configPath, mcconfig.Render(mcconfig.Defaults()))
	policy := mcconfig.Defaults().Backup
	policy.RetentionCount = 1
	create := func(suffix string, when time.Time) string {
		t.Helper()
		result, err := Create(context.Background(), CreateOptions{
			SourceDir:     source,
			ConfigPath:    configPath,
			BackupDir:     backups,
			Policy:        policy,
			StoppedProven: true,
			Now:           func() time.Time { return when },
			RandomSuffix:  fixedSuffix(suffix),
			NoRetention:   true,
		})
		if err != nil {
			t.Fatalf("Create(%s) failed: %v", suffix, err)
		}
		return result.ArchivePath
	}
	oldPath := create("pruneold", time.Date(2026, 5, 21, 4, 30, 0, 0, time.UTC))
	create("prunenew", time.Date(2026, 5, 22, 4, 30, 0, 0, time.UTC))
	if err := os.Chmod(backups, 0o555); err != nil {
		t.Fatalf("chmod backup directory: %v", err)
	}
	result, err := Prune(context.Background(), PruneOptions{
		BackupDir: backups,
		Policy:    policy,
		Now:       func() time.Time { return time.Date(2026, 5, 22, 4, 30, 0, 0, time.UTC) },
	})
	if err == nil {
		t.Fatal("Prune succeeded, want delete failure")
	}
	if len(result.Deleted) != 0 {
		t.Fatalf("Deleted = %+v, want no reported deletions before successful remove", result.Deleted)
	}
	if _, err := os.Stat(oldPath); err != nil {
		t.Fatalf("old archive missing after failed prune: %v", err)
	}
	if err := os.Chmod(backups, 0o755); err != nil {
		t.Fatalf("restore backup directory permissions: %v", err)
	}
}

func writeCustomArchive(t *testing.T, path string, manifest Manifest, writeEntries func(*tar.Writer)) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	gzw := gzip.NewWriter(file)
	defer gzw.Close()
	tw := tar.NewWriter(gzw)
	defer tw.Close()
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := tw.WriteHeader(&tar.Header{Name: "manifest.json", Typeflag: tar.TypeReg, Mode: 0o640, Size: int64(len(data))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(data); err != nil {
		t.Fatal(err)
	}
	writeEntries(tw)
}
