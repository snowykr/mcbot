package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/snowy/mcbot/internal/mcconfig"
)

const (
	ManifestFormatVersion = 1
	LockDirName           = ".locks"
	DefaultStaleTimeout   = 15 * time.Minute
)

type Quiescer interface {
	Execute(ctx context.Context, command string) (string, error)
}

type CreateOptions struct {
	SourceDir      string
	ConfigPath     string
	BackupDir      string
	Policy         mcconfig.BackupConfig
	CreatedBy      string
	Reason         string
	StoppedProven  bool
	Quiescer       Quiescer
	NoRetention    bool
	Now            func() time.Time
	RandomSuffix   func() (string, error)
	AssumeLockHeld bool
}

type CreateResult struct {
	BackupID        string          `json:"backup_id"`
	ArchivePath     string          `json:"archive_path"`
	Manifest        Manifest        `json:"manifest"`
	Quiesce         QuiesceManifest `json:"quiesce"`
	RetentionResult *PruneResult    `json:"retention_result,omitempty"`
}

type Manifest struct {
	FormatVersion       int             `json:"format_version"`
	BackupID            string          `json:"backup_id"`
	CreatedAt           time.Time       `json:"created_at"`
	CreatedBy           string          `json:"created_by"`
	BackupReason        string          `json:"backup_reason"`
	SourceDataRoot      string          `json:"source_data_root"`
	ConfigPath          string          `json:"config_path"`
	Quiesce             QuiesceManifest `json:"quiesce"`
	ServerState         string          `json:"server_state,omitempty"`
	ExcludedPaths       []string        `json:"excluded_paths"`
	FileCount           int             `json:"file_count"`
	UncompressedBytes   int64           `json:"uncompressed_bytes"`
	Files               []FileManifest  `json:"files"`
	IncludeMCServerTOML bool            `json:"include_mc_server_toml"`
}

type QuiesceManifest struct {
	Attempted bool     `json:"attempted"`
	Succeeded bool     `json:"succeeded"`
	Commands  []string `json:"commands"`
	Error     string   `json:"error,omitempty"`
}

type FileManifest struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Mode   string `json:"mode,omitempty"`
}

type fileEntry struct {
	abs  string
	path string
	info fs.FileInfo
}

func Create(ctx context.Context, opts CreateOptions) (CreateResult, error) {
	if opts.CreatedBy == "" {
		opts.CreatedBy = "cli"
	}
	if opts.Reason == "" {
		opts.Reason = "manual"
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.RandomSuffix == nil {
		opts.RandomSuffix = RandomSuffix
	}
	if err := validateCreateOptions(opts); err != nil {
		return CreateResult{}, err
	}
	if err := os.MkdirAll(opts.BackupDir, 0o755); err != nil {
		return CreateResult{}, fmt.Errorf("create backup directory: %w", err)
	}

	id, archivePath, err := nextBackupID(opts.BackupDir, opts.Now, opts.RandomSuffix)
	if err != nil {
		return CreateResult{}, err
	}
	var lock *Lock
	if !opts.AssumeLockHeld {
		lock, err = AcquireLock(opts.BackupDir, LockMetadata{
			Operation: "create",
			Owner:     opts.CreatedBy,
			BackupID:  id,
			CreatedAt: opts.Now().UTC(),
			StaleAt:   opts.Now().UTC().Add(DefaultStaleTimeout),
			Status:    "creating backup",
		})
		if err != nil {
			return CreateResult{}, err
		}
		defer lock.Release()
	}

	var quiesce QuiesceManifest
	var result CreateResult
	createArchive := func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		manifest, err := buildManifest(ctx, id, opts, quiesce)
		if err != nil {
			return err
		}
		if err := writeArchive(ctx, archivePath, manifest, opts.SourceDir, opts.ConfigPath); err != nil {
			_ = os.Remove(archivePath)
			return err
		}
		validation, err := ValidateArchive(archivePath)
		if err != nil {
			_ = os.Remove(archivePath)
			return fmt.Errorf("validate newly created backup %s: %w", id, err)
		}
		manifest = validation.Manifest
		created := CreateResult{BackupID: id, ArchivePath: archivePath, Manifest: manifest, Quiesce: quiesce}
		if !opts.NoRetention {
			pruned, err := Prune(ctx, PruneOptions{
				BackupDir:            opts.BackupDir,
				Policy:               opts.Policy,
				Now:                  opts.Now,
				AssumeLockHeld:       true,
				IgnoreActiveBackupID: id,
			})
			if err != nil {
				return fmt.Errorf("apply retention after backup %s: %w", id, err)
			}
			created.RetentionResult = &pruned
		}
		result = created
		return nil
	}

	if opts.StoppedProven {
		quiesce = QuiesceManifest{Attempted: false, Succeeded: false}
		if err := createArchive(ctx); err != nil {
			return CreateResult{}, err
		}
	} else {
		if opts.Quiescer == nil {
			return CreateResult{}, fmt.Errorf("backup requires RCON quiesce when server is running or stopped state is not proven")
		}
		err = RunQuiesced(ctx, opts.Quiescer, time.Duration(opts.Policy.QuiesceTimeoutSeconds)*time.Second, func(qctx context.Context, qm QuiesceManifest) error {
			quiesce = qm
			return createArchive(qctx)
		})
		if err != nil {
			return CreateResult{}, err
		}
	}
	if result.BackupID == "" {
		manifest, err := ReadManifest(archivePath)
		if err != nil {
			return CreateResult{}, err
		}
		result = CreateResult{BackupID: id, ArchivePath: archivePath, Manifest: manifest, Quiesce: quiesce}
	}
	return result, nil
}

func validateCreateOptions(opts CreateOptions) error {
	if opts.SourceDir == "" {
		return fmt.Errorf("backup source directory must not be empty")
	}
	if opts.ConfigPath == "" {
		return fmt.Errorf("backup config path must not be empty")
	}
	if opts.BackupDir == "" {
		return fmt.Errorf("backup directory must not be empty")
	}
	if err := mcconfig.ValidateBackupDirectory(opts.Policy.Directory); err != nil {
		return err
	}
	if err := ensureUsableBackupDir(opts.BackupDir, opts.SourceDir); err != nil {
		return err
	}
	sourceAbs, err := filepath.Abs(filepath.Clean(opts.SourceDir))
	if err != nil {
		return fmt.Errorf("resolve game data source: %w", err)
	}
	backupAbs, err := filepath.Abs(filepath.Clean(opts.BackupDir))
	if err != nil {
		return fmt.Errorf("resolve backup directory: %w", err)
	}
	commonRoot := commonPathPrefix(sourceAbs, backupAbs)
	if err := rejectSymlinkPathComponentsUnderRoot(commonRoot, sourceAbs, "game data source"); err != nil {
		return err
	}
	if err := rejectSymlinkPathComponentsUnderRoot(commonRoot, backupAbs, "backup directory"); err != nil {
		return err
	}
	sourceInfo, err := lstatNoSymlink(sourceAbs, "game data source", opts.SourceDir)
	if err != nil {
		return err
	}
	if !sourceInfo.IsDir() {
		return fmt.Errorf("game data source is not a directory: %s", opts.SourceDir)
	}
	if opts.Policy.IncludeMCServerTOML {
		configAbs, err := filepath.Abs(filepath.Clean(opts.ConfigPath))
		if err != nil {
			return fmt.Errorf("resolve mc-server.toml for backup: %w", err)
		}
		if err := rejectSymlinkPathComponentsUnderRoot(commonPathPrefix(commonRoot, configAbs), configAbs, "mc-server.toml backup path"); err != nil {
			return err
		}
		info, err := lstatNoSymlink(configAbs, "mc-server.toml backup path", opts.ConfigPath)
		if err != nil {
			return fmt.Errorf("stat mc-server.toml for backup: %w", err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("mc-server.toml backup path is not a regular file: %s", opts.ConfigPath)
		}
	}
	return nil
}

func ensureUsableBackupDir(backupDir, sourceDir string) error {
	cleanBackup := filepath.Clean(backupDir)
	cleanSource := filepath.Clean(sourceDir)
	if cleanBackup == "." || cleanBackup == string(filepath.Separator) {
		return fmt.Errorf("backup directory must not be repository root")
	}
	if rel, err := filepath.Rel(cleanSource, cleanBackup); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("backup directory must not be inside game data source")
	}
	if info, err := os.Lstat(cleanBackup); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("backup directory must not be a symlink")
	}
	return nil
}

func buildManifest(ctx context.Context, id string, opts CreateOptions, quiesce QuiesceManifest) (Manifest, error) {
	if err := ctx.Err(); err != nil {
		return Manifest{}, err
	}
	files, err := collectEntries(ctx, opts.SourceDir, "world-data")
	if err != nil {
		return Manifest{}, err
	}
	if opts.Policy.IncludeMCServerTOML {
		info, err := os.Lstat(opts.ConfigPath)
		if err != nil {
			return Manifest{}, fmt.Errorf("stat mc-server.toml: %w", err)
		}
		if !info.Mode().IsRegular() {
			return Manifest{}, fmt.Errorf("mc-server.toml must be a regular file")
		}
		files = append(files, fileEntry{abs: opts.ConfigPath, path: "mc-server.toml", info: info})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })

	manifest := Manifest{
		FormatVersion:       ManifestFormatVersion,
		BackupID:            id,
		CreatedAt:           opts.Now().UTC(),
		CreatedBy:           opts.CreatedBy,
		BackupReason:        opts.Reason,
		SourceDataRoot:      "world-data",
		ConfigPath:          "mc-server.toml",
		Quiesce:             quiesce,
		ExcludedPaths:       defaultExcludedPaths(),
		IncludeMCServerTOML: opts.Policy.IncludeMCServerTOML,
	}
	for _, entry := range files {
		if err := ctx.Err(); err != nil {
			return Manifest{}, err
		}
		if entry.info.IsDir() {
			continue
		}
		sum, err := fileSHA256(ctx, entry.abs)
		if err != nil {
			return Manifest{}, err
		}
		manifest.Files = append(manifest.Files, FileManifest{
			Path:   entry.path,
			Size:   entry.info.Size(),
			SHA256: sum,
			Mode:   fmt.Sprintf("%04o", entry.info.Mode().Perm()),
		})
		manifest.FileCount++
		manifest.UncompressedBytes += entry.info.Size()
	}
	return manifest, nil
}

func collectEntries(ctx context.Context, root, prefix string) ([]fileEntry, error) {
	var entries []fileEntry
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		slashRel := filepath.ToSlash(rel)
		if d.Type()&fs.ModeSymlink != 0 {
			return fmt.Errorf("refuse to backup source symlink: %s", slashRel)
		}
		info, err := lstatNoSymlink(path, "backup source entry", slashRel)
		if err != nil {
			return err
		}
		if excludedSourcePath(slashRel, info) {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !info.Mode().IsRegular() && !info.IsDir() {
			return fmt.Errorf("refuse to backup non-regular source entry: %s", slashRel)
		}
		entries = append(entries, fileEntry{
			abs:  path,
			path: pathJoin(prefix, slashRel),
			info: info,
		})
		return nil
	})
	return entries, err
}

func excludedSourcePath(path string, info fs.FileInfo) bool {
	base := filepath.Base(path)
	if excludedPayloadPath(path) {
		return true
	}
	if path == ".env" || strings.HasPrefix(path, "data/mcbot/") || path == "data/mcbot" {
		return true
	}
	if path == "mcbot" || strings.HasPrefix(path, "mcbot/") {
		return true
	}
	if path == "backups" || strings.HasPrefix(path, "backups/") {
		return true
	}
	if path == "logs" || strings.HasPrefix(path, "logs/") || strings.HasSuffix(path, ".log") {
		return true
	}
	if strings.HasPrefix(base, ".setup-env-") && strings.HasSuffix(base, ".tmp") {
		return true
	}
	if strings.HasPrefix(base, ".env-") && strings.HasSuffix(base, ".tmp") {
		return true
	}
	if strings.HasSuffix(base, ".tmp") || strings.HasSuffix(base, ".lock") {
		return true
	}
	return false
}

func writeArchive(ctx context.Context, path string, manifest Manifest, sourceDir, configPath string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	tmp := path + ".tmp"
	file, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		return fmt.Errorf("create backup archive temp file: %w", err)
	}
	defer func() {
		_ = file.Close()
		_ = os.Remove(tmp)
	}()
	gzw := gzip.NewWriter(file)
	tw := tar.NewWriter(gzw)
	if err := writeManifest(ctx, tw, manifest); err != nil {
		return err
	}
	if err := writeTree(ctx, tw, sourceDir, "world-data"); err != nil {
		return err
	}
	if manifest.IncludeMCServerTOML {
		if err := writeFileEntry(ctx, tw, configPath, "mc-server.toml"); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("close backup tar writer: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := gzw.Close(); err != nil {
		return fmt.Errorf("close backup gzip writer: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync backup archive: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close backup archive: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("commit backup archive: %w", err)
	}
	return nil
}

func writeManifest(ctx context.Context, tw *tar.Writer, manifest Manifest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode backup manifest: %w", err)
	}
	data = append(data, '\n')
	header := &tar.Header{Name: "manifest.json", Typeflag: tar.TypeReg, Mode: 0o640, Size: int64(len(data)), ModTime: manifest.CreatedAt}
	if err := tw.WriteHeader(header); err != nil {
		return fmt.Errorf("write manifest header: %w", err)
	}
	if _, err := tw.Write(data); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return nil
}

func writeTree(ctx context.Context, tw *tar.Writer, root, prefix string) error {
	entries, err := collectEntries(ctx, root, prefix)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.info.IsDir() {
			if err := writeDirEntry(ctx, tw, entry.path, entry.info); err != nil {
				return err
			}
			continue
		}
		if err := writeFileEntry(ctx, tw, entry.abs, entry.path); err != nil {
			return err
		}
	}
	return nil
}

func writeDirEntry(ctx context.Context, tw *tar.Writer, name string, info fs.FileInfo) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	header := &tar.Header{Name: cleanArchivePath(name), Typeflag: tar.TypeDir, Mode: 0o755, ModTime: info.ModTime()}
	return tw.WriteHeader(header)
}

func writeFileEntry(ctx context.Context, tw *tar.Writer, src, name string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	file, info, err := openVerifiedRegularFile(src, "archive source")
	if err != nil {
		return err
	}
	defer file.Close()
	header := &tar.Header{Name: cleanArchivePath(name), Typeflag: tar.TypeReg, Mode: 0o644, Size: info.Size(), ModTime: info.ModTime()}
	if err := tw.WriteHeader(header); err != nil {
		return fmt.Errorf("write archive header %s: %w", name, err)
	}
	if _, err := copyWithContext(ctx, tw, file); err != nil {
		return fmt.Errorf("write archive file %s: %w", name, err)
	}
	return nil
}

func ReadManifest(path string) (Manifest, error) {
	file, _, err := openVerifiedRegularFile(path, "backup archive")
	if err != nil {
		return Manifest{}, err
	}
	defer file.Close()
	gzr, err := gzip.NewReader(file)
	if err != nil {
		return Manifest{}, fmt.Errorf("open backup gzip: %w", err)
	}
	defer gzr.Close()
	tr := tar.NewReader(gzr)
	header, err := tr.Next()
	if err != nil {
		return Manifest{}, fmt.Errorf("read backup manifest header: %w", err)
	}
	if header.Name != "manifest.json" || header.Typeflag != tar.TypeReg {
		return Manifest{}, fmt.Errorf("backup archive must start with regular manifest.json")
	}
	var manifest Manifest
	if err := json.NewDecoder(tr).Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode backup manifest: %w", err)
	}
	return manifest, nil
}

type ValidationResult struct {
	Manifest       Manifest `json:"manifest"`
	ArchivePath    string   `json:"archive_path"`
	ArchiveBytes   int64    `json:"archive_bytes"`
	ValidatedFiles int      `json:"validated_files"`
}

func ValidateArchive(path string) (ValidationResult, error) {
	file, result, err := openValidatedArchive(path)
	if err != nil {
		return ValidationResult{}, err
	}
	defer file.Close()
	return result, nil
}

func openValidatedArchive(path string) (*os.File, ValidationResult, error) {
	file, _, err := openVerifiedRegularFile(path, "backup archive")
	if err != nil {
		return nil, ValidationResult{}, err
	}
	result, err := validateOpenArchive(file, path)
	if err != nil {
		_ = file.Close()
		return nil, ValidationResult{}, err
	}
	return file, result, nil
}

func validateOpenArchive(file *os.File, path string) (ValidationResult, error) {
	info, err := file.Stat()
	if err != nil {
		return ValidationResult{}, fmt.Errorf("stat opened backup archive: %w", err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return ValidationResult{}, fmt.Errorf("seek backup archive: %w", err)
	}
	gzr, err := gzip.NewReader(file)
	if err != nil {
		return ValidationResult{}, fmt.Errorf("open backup gzip: %w", err)
	}
	defer gzr.Close()
	tr := tar.NewReader(gzr)
	manifest, err := readAndValidateManifest(tr, filepath.Base(path))
	if err != nil {
		return ValidationResult{}, err
	}
	checksums := make(map[string]FileManifest, len(manifest.Files))
	for _, file := range manifest.Files {
		if err := validateArchivePath(file.Path); err != nil {
			return ValidationResult{}, err
		}
		if _, ok := checksums[file.Path]; ok {
			return ValidationResult{}, fmt.Errorf("duplicate manifest file path %q", file.Path)
		}
		checksums[file.Path] = file
	}
	seen := map[string]struct{}{"manifest.json": {}}
	validated := 0
	configEntryPresent := false
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return ValidationResult{}, fmt.Errorf("read backup archive entry: %w", err)
		}
		if err := validateTarHeader(header); err != nil {
			return ValidationResult{}, err
		}
		if (header.Name == "manifest.json" || header.Name == "mc-server.toml") && header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeRegA {
			return ValidationResult{}, fmt.Errorf("archive entry %q must be a regular file", header.Name)
		}
		if _, ok := seen[header.Name]; ok {
			return ValidationResult{}, fmt.Errorf("duplicate archive entry %q", header.Name)
		}
		seen[header.Name] = struct{}{}
		if header.Typeflag == tar.TypeDir {
			continue
		}
		if header.Name == "mc-server.toml" {
			configEntryPresent = true
		}
		want, ok := checksums[header.Name]
		if !ok {
			return ValidationResult{}, fmt.Errorf("archive file %q missing from manifest checksum list", header.Name)
		}
		if header.Size != want.Size {
			return ValidationResult{}, fmt.Errorf("archive file %q size mismatch", header.Name)
		}
		sum := sha256.New()
		if _, err := io.Copy(sum, tr); err != nil {
			return ValidationResult{}, fmt.Errorf("hash archive file %q: %w", header.Name, err)
		}
		got := hex.EncodeToString(sum.Sum(nil))
		if got != want.SHA256 {
			return ValidationResult{}, fmt.Errorf("archive file %q checksum mismatch", header.Name)
		}
		validated++
	}
	if validated != len(checksums) {
		return ValidationResult{}, fmt.Errorf("archive validated %d files, manifest lists %d", validated, len(checksums))
	}
	_, configManifestPresent := checksums["mc-server.toml"]
	if manifest.IncludeMCServerTOML != configManifestPresent || configManifestPresent != configEntryPresent {
		return ValidationResult{}, fmt.Errorf("archive mc-server.toml content does not match include_mc_server_toml manifest flag")
	}
	return ValidationResult{Manifest: manifest, ArchivePath: path, ArchiveBytes: info.Size(), ValidatedFiles: validated}, nil
}

func readAndValidateManifest(tr *tar.Reader, archiveName string) (Manifest, error) {
	header, err := tr.Next()
	if err != nil {
		return Manifest{}, fmt.Errorf("read backup manifest header: %w", err)
	}
	if err := validateTarHeader(header); err != nil {
		return Manifest{}, err
	}
	if header.Name != "manifest.json" {
		return Manifest{}, fmt.Errorf("backup archive must start with manifest.json")
	}
	var manifest Manifest
	if err := json.NewDecoder(tr).Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode backup manifest: %w", err)
	}
	if manifest.FormatVersion != ManifestFormatVersion {
		return Manifest{}, fmt.Errorf("unsupported backup format_version %d", manifest.FormatVersion)
	}
	wantID := strings.TrimSuffix(archiveName, ".tar.gz")
	if !validBackupID(wantID) {
		return Manifest{}, fmt.Errorf("archive filename does not contain a canonical backup_id")
	}
	if manifest.BackupID == "" || manifest.BackupID != wantID {
		return Manifest{}, fmt.Errorf("manifest backup_id must match archive filename")
	}
	if !validBackupID(manifest.BackupID) {
		return Manifest{}, fmt.Errorf("manifest backup_id is not canonical")
	}
	if manifest.CreatedAt.IsZero() || manifest.CreatedAt.Location() != time.UTC {
		return Manifest{}, fmt.Errorf("manifest created_at must be RFC3339 UTC")
	}
	return manifest, nil
}

func validateTarHeader(header *tar.Header) error {
	if header == nil {
		return fmt.Errorf("nil tar header")
	}
	if err := validateArchivePath(header.Name); err != nil {
		return err
	}
	for key := range header.PAXRecords {
		switch key {
		case "path", "mtime", "atime", "ctime":
		default:
			return fmt.Errorf("archive entry %q uses unsupported PAX record %q", header.Name, key)
		}
	}
	switch header.Typeflag {
	case tar.TypeReg, tar.TypeRegA, tar.TypeDir:
	default:
		return fmt.Errorf("archive entry %q has unsupported type %v", header.Name, header.Typeflag)
	}
	if header.Mode&0o6000 != 0 {
		return fmt.Errorf("archive entry %q has unsafe setuid/setgid bits", header.Name)
	}
	if header.Size < 0 {
		return fmt.Errorf("archive entry %q has negative size", header.Name)
	}
	return nil
}

func validateArchivePath(path string) error {
	if path == "" {
		return fmt.Errorf("archive entry path must not be empty")
	}
	if filepath.IsAbs(path) || strings.Contains(path, `\`) || strings.Contains(path, ":") {
		return fmt.Errorf("archive entry %q is not a safe relative path", path)
	}
	clean := filepath.ToSlash(filepath.Clean(path))
	if clean != path || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("archive entry %q is not a clean relative path", path)
	}
	if clean != "manifest.json" && clean != "mc-server.toml" && !strings.HasPrefix(clean, "world-data/") {
		return fmt.Errorf("archive entry %q uses unexpected top-level path", path)
	}
	if strings.HasPrefix(clean, "world-data/") && excludedPayloadPath(strings.TrimPrefix(clean, "world-data/")) {
		return fmt.Errorf("archive entry %q is excluded", path)
	}
	return nil
}

func excludedPayloadPath(path string) bool {
	clean := filepath.ToSlash(filepath.Clean(path))
	if clean == "." {
		return false
	}
	for _, part := range strings.Split(clean, "/") {
		if part == "" {
			continue
		}
		if strings.HasPrefix(part, ".") {
			return true
		}
		if part == "logs" || part == "backups" || part == "mcbot" {
			return true
		}
		if strings.HasSuffix(part, ".log") || strings.HasSuffix(part, ".tmp") || strings.HasSuffix(part, ".lock") {
			return true
		}
	}
	return false
}

func RunQuiesced(ctx context.Context, q Quiescer, timeout time.Duration, fn func(context.Context, QuiesceManifest) error) error {
	if q == nil {
		return fmt.Errorf("RCON quiescer is required")
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	qctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	manifest := QuiesceManifest{Attempted: true, Commands: []string{"save-off", "save-all flush", "save-on"}}
	saveOffSucceeded := false
	if _, err := q.Execute(qctx, "save-off"); err != nil {
		manifest.Error = err.Error()
		return fmt.Errorf("rcon save-off failed: %w", err)
	}
	saveOffSucceeded = true
	if _, err := q.Execute(qctx, "save-all flush"); err != nil {
		manifest.Error = err.Error()
		if _, onErr := q.Execute(context.Background(), "save-on"); onErr != nil {
			return fmt.Errorf("rcon save-all flush failed: %w; additionally save-on cleanup failed: %v", err, onErr)
		}
		return fmt.Errorf("rcon save-all flush failed: %w", err)
	}
	manifest.Succeeded = true
	err := fn(qctx, manifest)
	if saveOffSucceeded {
		if _, onErr := q.Execute(context.Background(), "save-on"); onErr != nil {
			if err != nil {
				return fmt.Errorf("backup operation failed after save-off: %w; additionally save-on cleanup failed: %v", err, onErr)
			}
			return fmt.Errorf("rcon save-on cleanup failed: %w", onErr)
		}
	}
	return err
}

func nextBackupID(dir string, now func() time.Time, suffix func() (string, error)) (string, string, error) {
	const attempts = 10
	for i := 0; i < attempts; i++ {
		sfx, err := suffix()
		if err != nil {
			return "", "", err
		}
		id := now().UTC().Format("20060102T150405Z") + "-" + sfx
		path := filepath.Join(dir, id+".tar.gz")
		if _, err := os.Stat(path); os.IsNotExist(err) {
			return id, path, nil
		} else if err != nil {
			return "", "", fmt.Errorf("stat backup collision candidate: %w", err)
		}
	}
	return "", "", fmt.Errorf("could not allocate unique backup_id after %d attempts", attempts)
}

func RandomSuffix() (string, error) {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	var b strings.Builder
	for i := 0; i < 8; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		if err != nil {
			return "", fmt.Errorf("generate backup id suffix: %w", err)
		}
		b.WriteByte(alphabet[n.Int64()])
	}
	return b.String(), nil
}

func fileSHA256(ctx context.Context, path string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	file, _, err := openVerifiedRegularFile(path, "checksum source")
	if err != nil {
		return "", err
	}
	defer file.Close()
	sum := sha256.New()
	if _, err := copyWithContext(ctx, sum, file); err != nil {
		return "", fmt.Errorf("checksum file %s: %w", path, err)
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

func copyWithContext(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	buf := make([]byte, 32*1024)
	var written int64
	for {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		nr, er := src.Read(buf)
		if nr > 0 {
			if err := ctx.Err(); err != nil {
				return written, err
			}
			nw, ew := dst.Write(buf[0:nr])
			if nw < 0 || nr < nw {
				nw = 0
				if ew == nil {
					ew = io.ErrShortWrite
				}
			}
			written += int64(nw)
			if ew != nil {
				return written, ew
			}
			if nr != nw {
				return written, io.ErrShortWrite
			}
		}
		if er != nil {
			if errors.Is(er, io.EOF) {
				return written, nil
			}
			return written, er
		}
	}
}

func pathJoin(parts ...string) string {
	return filepath.ToSlash(filepath.Join(parts...))
}

func cleanArchivePath(path string) string {
	return filepath.ToSlash(filepath.Clean(path))
}

func defaultExcludedPaths() []string {
	return []string{".env", "data/mcbot", "mcbot", "logs", "*.log", "*.tmp", "*.lock", "backups"}
}
