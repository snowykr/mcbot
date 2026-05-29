package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/snowy/mcbot/internal/backup"
	"github.com/snowy/mcbot/internal/composectl"
	"github.com/snowy/mcbot/internal/envfile"
	"github.com/snowy/mcbot/internal/mcconfig"
	"github.com/snowy/mcbot/internal/rcon"
	"github.com/snowy/mcbot/internal/serverops"
)

var dockerExecRCONCommand = runDockerExecRCONCommand

func defaultBackupServerStatus(ctx context.Context, paths backupPaths) (serverops.Result, error) {
	return serverops.Status(ctx, serverops.Options{Paths: composePathsFromBackup(paths)})
}

var backupServerStatus = defaultBackupServerStatus

func SetBackupServerStatusForTest(fn func(context.Context, backupPaths) (serverops.Result, error)) func() {
	old := backupServerStatus
	if fn != nil {
		backupServerStatus = fn
	} else {
		backupServerStatus = defaultBackupServerStatus
	}
	return func() {
		backupServerStatus = old
	}
}

type backupFlags struct {
	opts              Options
	backupDir         string
	sourceDir         string
	configPath        string
	backupID          string
	archivePath       string
	reason            string
	noRetention       bool
	dryRun            bool
	interactive       bool
	deleteAfterDays   *int
	maxTotalSizeBytes *int64
	positionals       []string
}

type backupPaths struct {
	repoRoot    string
	sourceDir   string
	configPath  string
	backupDir   string
	envFile     string
	policy      mcconfig.BackupConfig
	containerID struct{ uid, gid int }
}

func dispatchBackup(opts Options, subcommand string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags, err := parseBackupFlags(opts, args)
	if err != nil {
		return usageError(stderr, "%v", err)
	}
	if flags.opts.JSON && !SupportsJSON([]string{"backup", subcommand}) {
		return usageError(stderr, "--json is not supported for backup %s", subcommand)
	}
	out := NewOutput(stdout, stderr, flags.opts)
	ctx := context.Background()
	paths, err := resolveBackupPaths(flags)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return ExitValidation
	}

	switch subcommand {
	case "create":
		if len(flags.positionals) != 0 {
			return usageError(stderr, "backup create does not accept positional arguments")
		}
		stopped, quiescer, err := backupCreatePreconditions(ctx, paths)
		if err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitOperational
		}
		result, err := backup.Create(ctx, backup.CreateOptions{
			SourceDir:     paths.sourceDir,
			ConfigPath:    paths.configPath,
			BackupDir:     paths.backupDir,
			Policy:        paths.policy,
			CreatedBy:     "cli",
			Reason:        flags.reason,
			StoppedProven: stopped,
			Quiescer:      quiescer,
			NoRetention:   flags.noRetention,
		})
		if err != nil {
			if result.BackupID != "" && errors.Is(err, backup.ErrRetentionAfterCreate) {
				fmt.Fprintf(stderr, "warning: %v\n", err)
				return writeBackupCreateResult(out, flags.opts, result)
			}
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitOperational
		}
		return writeBackupCreateResult(out, flags.opts, result)
	case "list":
		if len(flags.positionals) != 0 {
			return usageError(stderr, "backup list does not accept arguments")
		}
		listed, err := backup.List(ctx, paths.backupDir)
		if err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitOperational
		}
		return writeBackupList(out, flags.opts, listed)
	case "inspect", "validate", "verify":
		if len(flags.positionals) != 0 {
			return usageError(stderr, "backup %s does not accept positional arguments", subcommand)
		}
		archivePath, err := resolveArchiveArg(paths.backupDir, flags)
		if err != nil {
			return usageError(stderr, "%v", err)
		}
		result, err := backup.ValidateArchive(archivePath)
		if err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitValidation
		}
		if flags.opts.JSON {
			if err := out.SuccessJSON(result); err != nil {
				fmt.Fprintf(stderr, "%v\n", err)
				return ExitInternal
			}
			return ExitOK
		}
		if err := out.SuccessLine("backup %s is valid (%d files)", result.Manifest.BackupID, result.ValidatedFiles); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitInternal
		}
		return ExitOK
	case "prune":
		if len(flags.positionals) != 0 {
			return usageError(stderr, "backup prune does not accept arguments")
		}
		result, err := backup.Prune(ctx, backup.PruneOptions{
			BackupDir:         paths.backupDir,
			Policy:            paths.policy,
			DryRun:            flags.dryRun,
			DeleteAfterDays:   flags.deleteAfterDays,
			MaxTotalSizeBytes: flags.maxTotalSizeBytes,
		})
		if err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitOperational
		}
		return writePruneResult(out, flags.opts, result)
	case "restore":
		return dispatchBackupRestore(ctx, flags, paths, stdin, out, stderr)
	default:
		return usageError(stderr, "unknown command %q", "backup "+subcommand)
	}
}

func dispatchBackupRestore(ctx context.Context, flags backupFlags, paths backupPaths, stdin io.Reader, out Output, stderr io.Writer) int {
	promptWriter := out.stdout
	if flags.opts.JSON {
		promptWriter = stderr
	}
	if len(flags.positionals) != 0 {
		return usageError(stderr, "backup restore rejects positional archives; use --backup-id or --interactive")
	}
	if flags.archivePath != "" {
		return usageError(stderr, "backup restore does not accept --archive; use --backup-id")
	}
	backupID := flags.backupID
	var prompter Prompter
	if flags.interactive {
		if flags.opts.NoInput {
			return usageError(stderr, "backup restore --interactive cannot be used with --no-input")
		}
		if !stdinAllowsPrompt(stdin) {
			return usageError(stderr, "prompt required but interactive input is unavailable")
		}
		prompter = NewStdlibPrompter(stdin, promptWriter)
		selected, err := selectBackupID(ctx, paths.backupDir, prompter)
		if err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitOperational
		}
		backupID = selected
	}
	if backupID == "" {
		return usageError(stderr, "backup restore requires --backup-id <id> or --interactive")
	}
	stopped, err := restoreStoppedPrecondition(ctx, paths)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return ExitOperational
	}
	if !stopped {
		fmt.Fprintf(stderr, "restore requires the Minecraft server to be stopped\n")
		return ExitOperational
	}
	archivePath, err := backup.ArchivePathForID(paths.backupDir, backupID)
	if err != nil {
		return usageError(stderr, "%v", err)
	}
	validation, err := backup.ValidateArchive(archivePath)
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return ExitValidation
	}
	if !flags.opts.Yes {
		if flags.opts.NoInput {
			fmt.Fprintf(stderr, "%v\n", UsageError("prompt required but --no-input was set"))
			return ExitUsage
		}
		restoreConfig := "no"
		if validation.Manifest.IncludeMCServerTOML {
			restoreConfig = "yes"
		}
		if err := restoreConfirmationLine(out, flags.opts, "Restore backup %s created at %s", validation.Manifest.BackupID, validation.Manifest.CreatedAt.Format(time.RFC3339)); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitInternal
		}
		if err := restoreConfirmationLine(out, flags.opts, "Archive: %s (%d validated files)", validation.ArchivePath, validation.ValidatedFiles); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitInternal
		}
		if err := restoreConfirmationLine(out, flags.opts, "Target game data: %s", paths.sourceDir); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitInternal
		}
		if err := restoreConfirmationLine(out, flags.opts, "Config restore: %s (%s)", restoreConfig, paths.configPath); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitInternal
		}
		if err := restoreConfirmationLine(out, flags.opts, "Replacement scope: target directory contents; preserved: .env,mcbot,backups,.locks,.git,.omx,.mcbot-restore-*"); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitInternal
		}
		if prompter == nil {
			prompter = NewStdlibPrompter(stdin, promptWriter)
		}
		confirmed, err := prompter.Confirm("Replace current game data and restore included config with this backup", false)
		if err != nil {
			fmt.Fprintf(stderr, "%v\n", OperationalError("read restore confirmation: %v", err))
			return ExitOperational
		}
		if !confirmed {
			fmt.Fprintf(stderr, "%v\n", UsageError("backup restore cancelled before write"))
			return ExitUsage
		}
	}
	result, err := backup.Restore(ctx, backup.RestoreOptions{
		BackupDir:     paths.backupDir,
		BackupID:      backupID,
		TargetDir:     paths.sourceDir,
		ConfigPath:    paths.configPath,
		RepoRoot:      paths.repoRoot,
		Policy:        paths.policy,
		StoppedProven: true,
		UID:           paths.containerID.uid,
		GID:           paths.containerID.gid,
	})
	if err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return ExitOperational
	}
	if flags.opts.JSON {
		if err := out.SuccessJSON(result); err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			return ExitInternal
		}
		return ExitOK
	}
	if err := out.SuccessLine("restored backup %s (safety backup %s)", result.BackupID, result.SafetyBackupID); err != nil {
		fmt.Fprintf(stderr, "%v\n", err)
		return ExitInternal
	}
	return ExitOK
}

func restoreConfirmationLine(out Output, opts Options, format string, args ...any) error {
	if opts.JSON {
		return out.Diagnostic(format, args...)
	}
	return out.SuccessLine(format, args...)
}

func parseBackupFlags(opts Options, args []string) (backupFlags, error) {
	flags := backupFlags{opts: opts}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			flags.opts.JSON = true
		case "--yes":
			flags.opts.Yes = true
		case "--no-input":
			flags.opts.NoInput = true
		case "--backup-dir":
			i++
			if i >= len(args) || args[i] == "" {
				return flags, fmt.Errorf("--backup-dir requires a path")
			}
			flags.backupDir = args[i]
		case "--source-dir":
			i++
			if i >= len(args) || args[i] == "" {
				return flags, fmt.Errorf("--source-dir requires a path")
			}
			flags.sourceDir = args[i]
		case "--config-file":
			i++
			if i >= len(args) || args[i] == "" {
				return flags, fmt.Errorf("--config-file requires a path")
			}
			flags.configPath = args[i]
		case "--backup-id":
			i++
			if i >= len(args) || args[i] == "" {
				return flags, fmt.Errorf("--backup-id requires an id")
			}
			flags.backupID = args[i]
		case "--archive":
			i++
			if i >= len(args) || args[i] == "" {
				return flags, fmt.Errorf("--archive requires a path")
			}
			flags.archivePath = args[i]
		case "--reason":
			i++
			if i >= len(args) {
				return flags, fmt.Errorf("--reason requires text")
			}
			flags.reason = args[i]
		case "--no-retention":
			flags.noRetention = true
		case "--dry-run":
			flags.dryRun = true
		case "--interactive":
			flags.interactive = true
		case "--delete-after-days":
			i++
			if i >= len(args) {
				return flags, fmt.Errorf("--delete-after-days requires a number")
			}
			value, err := strconv.Atoi(args[i])
			if err != nil || value < 0 {
				return flags, fmt.Errorf("--delete-after-days must be a non-negative integer")
			}
			flags.deleteAfterDays = &value
		case "--max-total-size":
			i++
			if i >= len(args) {
				return flags, fmt.Errorf("--max-total-size requires bytes")
			}
			value, err := strconv.ParseInt(args[i], 10, 64)
			if err != nil || value < 0 {
				return flags, fmt.Errorf("--max-total-size must be a non-negative integer")
			}
			flags.maxTotalSizeBytes = &value
		default:
			if strings.HasPrefix(args[i], "--") {
				return flags, fmt.Errorf("unknown backup flag %s", args[i])
			}
			flags.positionals = append(flags.positionals, args[i])
		}
	}
	return flags, nil
}

func resolveBackupPaths(flags backupFlags) (backupPaths, error) {
	paths, err := discoverDefaultPaths()
	if err != nil {
		return backupPaths{}, err
	}
	configPath := paths.ConfigFile
	if flags.configPath != "" {
		configPath = flags.configPath
	} else if configPathOverride != "" {
		configPath = configPathOverride
	}
	if alternatePaths, err := composectl.DiscoverRepoRoot(filepath.Dir(configPath)); err == nil {
		paths = alternatePaths
	}
	cfg, err := mcconfig.Load(configPath)
	if err != nil {
		return backupPaths{}, err
	}
	sourceDir := filepath.Join(paths.RepoRoot, "data", "minecraft")
	if flags.sourceDir != "" {
		sourceDir = flags.sourceDir
	}
	backupDir := filepath.Join(paths.RepoRoot, cfg.Backup.Directory)
	if flags.backupDir != "" {
		backupDir = flags.backupDir
	}
	if err := validateResolvedBackupPaths(paths.RepoRoot, sourceDir, backupDir, configPath); err != nil {
		return backupPaths{}, err
	}
	return backupPaths{
		repoRoot:   paths.RepoRoot,
		sourceDir:  sourceDir,
		configPath: configPath,
		backupDir:  backupDir,
		envFile:    paths.EnvFile,
		policy:     cfg.Backup,
		containerID: struct{ uid, gid int }{
			uid: cfg.Container.UID,
			gid: cfg.Container.GID,
		},
	}, nil
}

func validateResolvedBackupPaths(repoRoot, sourceDir, backupDir, configPath string) error {
	repoAbs, err := filepath.Abs(filepath.Clean(repoRoot))
	if err != nil {
		return fmt.Errorf("resolve repository root: %w", err)
	}
	sourceAbs, err := filepath.Abs(filepath.Clean(sourceDir))
	if err != nil {
		return fmt.Errorf("resolve game data source: %w", err)
	}
	backupAbs, err := filepath.Abs(filepath.Clean(backupDir))
	if err != nil {
		return fmt.Errorf("resolve backup directory: %w", err)
	}
	configAbs, err := filepath.Abs(filepath.Clean(configPath))
	if err != nil {
		return fmt.Errorf("resolve config file path: %w", err)
	}
	if !pathWithin(repoAbs, sourceAbs) || sourceAbs == repoAbs {
		return fmt.Errorf("game data source must be under the repository root")
	}
	if base := filepath.Base(sourceAbs); base == "data" || base == "mcbot" || base == "backups" || base == ".git" || base == ".omx" {
		return fmt.Errorf("game data source %s is a protected path", sourceDir)
	}
	if info, err := os.Lstat(sourceAbs); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("game data source must not be a symlink")
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat game data source: %w", err)
	}
	if err := rejectExistingSymlinkPath(repoAbs, sourceAbs, "game data source"); err != nil {
		return err
	}
	if !pathWithin(repoAbs, backupAbs) || backupAbs == repoAbs {
		return fmt.Errorf("backup directory must be under the repository root and not the root itself")
	}
	protected := []string{
		filepath.Join(repoAbs, ".git"),
		filepath.Join(repoAbs, ".omx"),
		filepath.Join(repoAbs, "mcbot"),
		filepath.Join(repoAbs, "data"),
		filepath.Join(repoAbs, "data", "minecraft"),
		filepath.Join(repoAbs, "data", "mcbot"),
		sourceAbs,
		configAbs,
	}
	for _, protectedPath := range protected {
		if pathsOverlap(protectedPath, backupAbs) {
			return fmt.Errorf("backup directory must not overlap protected path %s", protectedPath)
		}
	}
	if err := rejectExistingSymlinkPath(repoAbs, backupAbs, "backup directory"); err != nil {
		return err
	}
	return nil
}

func rejectExistingSymlinkPath(repoRoot, path, label string) error {
	current := filepath.Clean(path)
	for {
		info, err := os.Lstat(current)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("%s must not be or pass through a symlink: %s", label, current)
			}
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("stat %s %s: %w", label, current, err)
		}
		if current == repoRoot || current == filepath.Dir(current) {
			return nil
		}
		current = filepath.Dir(current)
	}
}

func pathWithin(parent, child string) bool {
	parent = filepath.Clean(parent)
	child = filepath.Clean(child)
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func pathsOverlap(a, b string) bool {
	return pathWithin(a, b) || pathWithin(b, a)
}

func composePathsFromBackup(paths backupPaths) composectl.Paths {
	return composectl.Paths{
		RepoRoot:    paths.repoRoot,
		EnvFile:     paths.envFile,
		ConfigFile:  paths.configPath,
		ComposeFile: filepath.Join(paths.repoRoot, "docker-compose.yml"),
	}
}

func backupCreatePreconditions(ctx context.Context, paths backupPaths) (bool, backup.Quiescer, error) {
	status, err := backupServerStatus(ctx, paths)
	if err == nil && !status.Running {
		return true, nil, nil
	}
	q, qErr := rconClientFromEnv(paths.envFile)
	if qErr != nil {
		return false, nil, qErr
	}
	if q == nil {
		return false, nil, fmt.Errorf("backup requires RCON credentials when server is running or stopped state is not proven")
	}
	return false, q, nil
}

func restoreStoppedPrecondition(ctx context.Context, paths backupPaths) (bool, error) {
	status, err := backupServerStatus(ctx, paths)
	if err != nil {
		return false, err
	}
	return !status.Running, nil
}

func rconClientFromEnv(envPath string) (backup.Quiescer, error) {
	env, err := envfile.Load(envPath)
	if err != nil {
		return nil, err
	}
	password := strings.TrimSpace(env.Values["RCON_PASSWORD"])
	if password == "" {
		return nil, nil
	}
	host := env.Values["RCON_HOST"]
	if host == "" {
		host = "mc-server"
	}
	port := 25575
	if raw := env.Values["RCON_PORT"]; raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 65535 {
			return nil, fmt.Errorf("invalid RCON_PORT in .env")
		}
		port = parsed
	}
	timeout := 10 * time.Second
	if raw := env.Values["RCON_TIMEOUT_SECONDS"]; raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			return nil, fmt.Errorf("invalid RCON_TIMEOUT_SECONDS in .env")
		}
		timeout = time.Duration(parsed) * time.Second
	}
	if host == "mc-server" {
		container := strings.TrimSpace(env.Values["MC_CONTAINER_NAME"])
		if container == "" {
			container = "mc-server"
		}
		return dockerExecRCONQuiescer{container: container, timeout: timeout}, nil
	}
	return rcon.NewClient(host, port, password, timeout), nil
}

type dockerExecRCONQuiescer struct {
	container string
	timeout   time.Duration
}

func (q dockerExecRCONQuiescer) Execute(ctx context.Context, command string) (string, error) {
	timeout := q.timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	qctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	output, err := dockerExecRCONCommand(qctx, q.container, command)
	if err != nil {
		return "", fmt.Errorf("docker exec rcon-cli failed: %w, output: %s", err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

func runDockerExecRCONCommand(ctx context.Context, container, command string) ([]byte, error) {
	return exec.CommandContext(ctx, "docker", "exec", container, "rcon-cli", command).CombinedOutput()
}

func resolveArchiveArg(backupDir string, flags backupFlags) (string, error) {
	if flags.backupID != "" && flags.archivePath != "" {
		return "", fmt.Errorf("use either --backup-id or --archive, not both")
	}
	if flags.backupID != "" {
		return backup.ArchivePathForID(backupDir, flags.backupID)
	}
	if flags.archivePath != "" {
		return flags.archivePath, nil
	}
	return "", fmt.Errorf("backup validation requires --backup-id <id> or --archive <path>")
}

func selectBackupID(ctx context.Context, backupDir string, prompter Prompter) (string, error) {
	listed, err := backup.List(ctx, backupDir)
	if err != nil {
		return "", err
	}
	if len(listed.Valid) == 0 {
		return "", fmt.Errorf("no valid backups found in %s", backupDir)
	}
	options := make([]string, 0, len(listed.Valid))
	for _, item := range listed.Valid {
		options = append(options, fmt.Sprintf("%s %s %s", item.BackupID, item.CreatedAt.Format(time.RFC3339), item.Reason))
	}
	selected, err := prompter.Select("Select backup to restore", options, 0)
	if err != nil {
		return "", err
	}
	id, _, _ := strings.Cut(selected, " ")
	return id, nil
}

func writeBackupCreateResult(out Output, opts Options, result backup.CreateResult) int {
	if opts.JSON {
		if err := out.SuccessJSON(result); err != nil {
			return ExitInternal
		}
		return ExitOK
	}
	if err := out.SuccessLine("created backup %s at %s", result.BackupID, result.ArchivePath); err != nil {
		return ExitInternal
	}
	return ExitOK
}

func writeBackupList(out Output, opts Options, listed backup.ListResult) int {
	if opts.JSON {
		if err := out.SuccessJSON(listed); err != nil {
			return ExitInternal
		}
		return ExitOK
	}
	for _, item := range listed.Valid {
		if err := out.SuccessLine("%s\t%s\t%d bytes", item.BackupID, item.CreatedAt.Format(time.RFC3339), item.SizeBytes); err != nil {
			return ExitInternal
		}
	}
	for _, item := range listed.Invalid {
		if err := out.Diagnostic("invalid backup %s: %s", item.FileName, item.Error); err != nil {
			return ExitInternal
		}
	}
	return ExitOK
}

func writePruneResult(out Output, opts Options, result backup.PruneResult) int {
	if opts.JSON {
		if err := out.SuccessJSON(result); err != nil {
			return ExitInternal
		}
		return ExitOK
	}
	action := "deleted"
	if result.DryRun {
		action = "would delete"
	}
	if err := out.SuccessLine("backup prune %s %d backups, reclaimed %d bytes", action, len(result.Deleted), result.ReclaimedBytes); err != nil {
		return ExitInternal
	}
	return ExitOK
}
