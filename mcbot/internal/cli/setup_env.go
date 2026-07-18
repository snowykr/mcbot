package cli

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/snowy/mcbot/internal/atomicfile"
	"github.com/snowy/mcbot/internal/envfile"
	"github.com/snowy/mcbot/internal/mcconfig"
)

const (
	defaultMCContainerName = "mc-server"
	defaultMCBotDebug      = "false"
	defaultEnableRCON      = "true"
	defaultRCONHost        = "mc-server"
	defaultRCONPort        = "25575"
	defaultRCONTimeout     = "10"
)

var rconEnvKeys = []string{"RCON_PASSWORD", "RCON_HOST", "RCON_PORT", "RCON_TIMEOUT_SECONDS", "RCON_CMDS_STARTUP"}

type envQuestion struct {
	section string
	title   string
	help    string
}

type setupEnvDisplay struct {
	sectionIndex  int
	totalSections int
}

type setupEnvResult struct {
	stagedPath string
	cleanup    func()
	entries    []envfile.Entry
	path       string
}

func runSetupEnv(opts Options, prompter Prompter, out Output, path string) error {
	return runSetupEnvWithDisplay(opts, prompter, out, path, setupEnvDisplay{sectionIndex: 1, totalSections: 3})
}

func runSetupEnvWithDisplay(opts Options, prompter Prompter, out Output, path string, display setupEnvDisplay) error {
	result, err := collectSetupEnv(opts, prompter, out, path, display)
	if err != nil {
		return err
	}
	defer result.cleanup()
	if !opts.Yes {
		reviewIndex := display.sectionIndex + 2
		if display.totalSections < reviewIndex {
			display.totalSections = reviewIndex
		}
		if err := out.SetupSection(reviewIndex, display.totalSections, "Review & write"); err != nil {
			return InternalError("%v", err)
		}
		if err := out.SetupReview(path, result.entries, "", mcconfig.Config{}); err != nil {
			return InternalError("%v", err)
		}
		confirmed, err := prompter.Confirm("Write .env", true)
		if err != nil {
			return OperationalError("read setup env write confirmation: %v", err)
		}
		if !confirmed {
			return UsageError("setup env cancelled before write")
		}
	}
	if err := commitSetupEnvStage(result.stagedPath, path); err != nil {
		return ValidationError("%v", err)
	}
	if _, err := envfile.Validate(path); err != nil {
		return ValidationError("%v", err)
	}
	if err := out.SetupComplete(path, ""); err != nil {
		return InternalError("%v", err)
	}
	return nil
}

func collectSetupEnv(opts Options, prompter Prompter, out Output, path string, display setupEnvDisplay) (setupEnvResult, error) {
	stagedPath, cleanup, err := createSetupEnvStage(path)
	if err != nil {
		return setupEnvResult{}, ValidationError("%v", err)
	}
	result := setupEnvResult{stagedPath: stagedPath, cleanup: cleanup, path: path}

	file, err := envfile.Load(stagedPath)
	if err != nil {
		cleanup()
		return setupEnvResult{}, ValidationError("%v", err)
	}
	values := copyEnvValues(file.Values)
	defaults := map[string]string{
		"MC_CONTAINER_NAME":    defaultMCContainerName,
		"MCBOT_DEBUG":          defaultMCBotDebug,
		"ENABLE_RCON":          defaultEnableRCON,
		"RCON_HOST":            defaultRCONHost,
		"RCON_PORT":            defaultRCONPort,
		"RCON_TIMEOUT_SECONDS": defaultRCONTimeout,
	}

	if !opts.Yes {
		if display.sectionIndex <= 0 {
			display.sectionIndex = 1
		}
		if display.totalSections <= 0 {
			display.totalSections = display.sectionIndex
		}
		if err := out.SetupSection(display.sectionIndex, display.totalSections, "Discord bot"); err != nil {
			cleanup()
			return setupEnvResult{}, InternalError("%v", err)
		}
		if err := out.SetupHint("Secrets stay masked in summaries; press Enter to keep existing values."); err != nil {
			cleanup()
			return setupEnvResult{}, InternalError("%v", err)
		}
	}

	if err := promptRequiredEnvValue(opts, prompter, out, stagedPath, values, defaults, "DISCORD_TOKEN", true); err != nil {
		cleanup()
		return setupEnvResult{}, err
	}
	if err := promptRequiredEnvValue(opts, prompter, out, stagedPath, values, defaults, "MCBOT_TRUSTED_GUILD_ID", false); err != nil {
		cleanup()
		return setupEnvResult{}, err
	}
	if err := promptOptionalEnvValue(opts, prompter, out, stagedPath, values, defaults, "EMBED_CHANNEL_ID"); err != nil {
		cleanup()
		return setupEnvResult{}, err
	}

	if !opts.Yes {
		if err := out.SetupSection(display.sectionIndex+1, display.totalSections, "Runtime features"); err != nil {
			cleanup()
			return setupEnvResult{}, InternalError("%v", err)
		}
	}
	if err := promptRequiredEnvValue(opts, prompter, out, stagedPath, values, defaults, "MC_CONTAINER_NAME", false); err != nil {
		cleanup()
		return setupEnvResult{}, err
	}
	if err := promptBooleanEnvValue(opts, prompter, out, stagedPath, values, defaults, "ENABLE_RCON", []string{"true", "false"}); err != nil {
		cleanup()
		return setupEnvResult{}, err
	}

	advancedRuntime := opts.Yes
	if !opts.Yes {
		var err error
		advancedRuntime, err = promptAdvancedSettings(prompter, out, "Runtime features", "Configure advanced runtime settings?", false)
		if err != nil {
			cleanup()
			return setupEnvResult{}, err
		}
	}
	if advancedRuntime {
		if err := promptBooleanEnvValue(opts, prompter, out, stagedPath, values, defaults, "MCBOT_DEBUG", []string{"false", "true"}); err != nil {
			cleanup()
			return setupEnvResult{}, err
		}
	} else if _, ok := values["MCBOT_DEBUG"]; !ok {
		if err := envfile.Set(stagedPath, "MCBOT_DEBUG", defaultMCBotDebug); err != nil {
			cleanup()
			return setupEnvResult{}, ValidationError("%v", err)
		}
		values["MCBOT_DEBUG"] = defaultMCBotDebug
	}

	enabled, err := parseSetupEnvBool(values["ENABLE_RCON"])
	if err != nil {
		cleanup()
		return setupEnvResult{}, ValidationError("ENABLE_RCON is invalid for setup env: %v", err)
	}
	if enabled {
		if advancedRuntime {
			if err := promptOptionalEnvValue(opts, prompter, out, stagedPath, values, defaults, "RCON_PASSWORD"); err != nil {
				cleanup()
				return setupEnvResult{}, err
			}
			for _, key := range []string{"RCON_HOST", "RCON_PORT", "RCON_TIMEOUT_SECONDS", "RCON_CMDS_STARTUP"} {
				if err := promptOptionalEnvValue(opts, prompter, out, stagedPath, values, defaults, key); err != nil {
					cleanup()
					return setupEnvResult{}, err
				}
			}
		}
	} else {
		for _, key := range rconEnvKeys {
			if err := envfile.Unset(stagedPath, key); err != nil {
				cleanup()
				return setupEnvResult{}, ValidationError("%v", err)
			}
			delete(values, key)
		}
	}

	if _, err := envfile.Validate(stagedPath); err != nil {
		cleanup()
		return setupEnvResult{}, ValidationError("%v", err)
	}
	entries, err := envfile.Show(stagedPath, envfile.RevealPolicy{})
	if err != nil {
		cleanup()
		return setupEnvResult{}, ValidationError("%v", err)
	}
	result.entries = entries
	return result, nil
}

func createSetupEnvStage(path string) (string, func(), error) {
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", nil, fmt.Errorf("refusing to replace symlinked env file: %s", path)
	} else if err != nil && !os.IsNotExist(err) {
		return "", nil, fmt.Errorf("inspect env file: %w", err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", nil, fmt.Errorf("create env staging directory: %w", err)
	}
	stageDir, err := os.MkdirTemp("", "mcbot-setup-env-*")
	if err != nil {
		return "", nil, fmt.Errorf("create env staging directory: %w", err)
	}
	cleanup := func() {
		_ = os.RemoveAll(stageDir)
	}
	if err := os.Chmod(stageDir, 0o700); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("secure env staging directory: %w", err)
	}
	stagedPath := filepath.Join(stageDir, ".env")
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			cleanup()
			return "", nil, fmt.Errorf("read env file: %w", err)
		}
		data = nil
	}
	if err := os.WriteFile(stagedPath, data, 0o600); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("seed env staging file: %w", err)
	}
	return stagedPath, cleanup, nil
}

func commitSetupEnvStage(stagedPath, path string) error {
	data, err := os.ReadFile(stagedPath)
	if err != nil {
		return fmt.Errorf("read env staging file: %w", err)
	}
	mode := os.FileMode(0o600)
	if info, statErr := os.Stat(stagedPath); statErr == nil {
		mode = info.Mode().Perm()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create env file directory: %w", err)
	}
	if err := atomicReplaceSetupEnv(path, data, mode); err != nil {
		return fmt.Errorf("replace env file: %w", err)
	}
	return nil
}

type setupEnvRollback struct {
	path           string
	backupPath     string
	originalExists bool
	expectedData   []byte
	expectedMode   os.FileMode
}

func prepareSetupEnvRollback(path, stagedPath string) (*setupEnvRollback, error) {
	expectedData, expectedMode, _, err := readSetupEnvFile(stagedPath)
	if err != nil {
		return nil, fmt.Errorf("read staged env before combined setup: %w", err)
	}
	backupPath, err := unusedSetupEnvPath(filepath.Dir(path), ".env-backup-*.tmp")
	if err != nil {
		return nil, err
	}
	rollback := &setupEnvRollback{path: path, backupPath: backupPath, expectedData: expectedData, expectedMode: expectedMode}
	if err := os.Link(path, backupPath); err != nil {
		if os.IsNotExist(err) {
			placeholder, createErr := os.OpenFile(backupPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
			if createErr != nil {
				return nil, fmt.Errorf("create env rollback placeholder: %w", createErr)
			}
			if closeErr := placeholder.Close(); closeErr != nil {
				_ = os.Remove(backupPath)
				return nil, fmt.Errorf("close env rollback placeholder: %w", closeErr)
			}
			return rollback, nil
		}
		if copyErr := copySetupEnvBackup(path, backupPath); copyErr != nil {
			_ = os.Remove(backupPath)
			return nil, copyErr
		}
	}
	rollback.originalExists = true
	return rollback, nil
}

func (r *setupEnvRollback) restore() error {
	if err := atomicfile.Exchange(r.backupPath, r.path); err != nil {
		return fmt.Errorf("exchange env with rollback backup: %w", err)
	}
	data, mode, _, err := readSetupEnvFile(r.backupPath)
	if err != nil {
		_ = atomicfile.Exchange(r.backupPath, r.path)
		return err
	}
	if mode != r.expectedMode || !bytes.Equal(data, r.expectedData) {
		if exchangeErr := atomicfile.Exchange(r.backupPath, r.path); exchangeErr != nil {
			return fmt.Errorf("env changed concurrently and exchange-back failed: %w", exchangeErr)
		}
		return fmt.Errorf("env changed concurrently; refusing rollback")
	}
	if !r.originalExists {
		if err := os.Remove(r.path); err != nil {
			return fmt.Errorf("remove env rollback placeholder: %w", err)
		}
	}
	if err := os.Remove(r.backupPath); err != nil {
		return fmt.Errorf("remove rolled-back env: %w", err)
	}
	r.backupPath = ""
	return nil
}

func (r *setupEnvRollback) restoreAfterFailedCommit() error {
	r.cleanup()
	return nil
}

func (r *setupEnvRollback) cleanup() {
	if r.backupPath != "" {
		_ = os.Remove(r.backupPath)
	}
}

func readSetupEnvFile(path string) ([]byte, os.FileMode, os.FileInfo, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, 0, nil, err
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, 0, nil, err
	}
	return data, info.Mode().Perm(), info, nil
}

func copySetupEnvBackup(path, backupPath string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect original env for rollback: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		if err != nil {
			return fmt.Errorf("read original env symlink for rollback: %w", err)
		}
		if err := os.Symlink(target, backupPath); err != nil {
			return fmt.Errorf("copy original env symlink for rollback: %w", err)
		}
		return nil
	}
	data, mode, _, err := readSetupEnvFile(path)
	if err != nil {
		return fmt.Errorf("read original env for rollback: %w", err)
	}
	file, err := os.OpenFile(backupPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("create original env rollback copy: %w", err)
	}
	defer file.Close()
	if err := file.Chmod(mode); err != nil {
		return fmt.Errorf("chmod original env rollback copy: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		return fmt.Errorf("write original env rollback copy: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync original env rollback copy: %w", err)
	}
	return nil
}

func unusedSetupEnvPath(dir, pattern string) (string, error) {
	file, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return "", fmt.Errorf("create env rollback path: %w", err)
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("close env rollback placeholder: %w", err)
	}
	if err := os.Remove(path); err != nil {
		return "", fmt.Errorf("remove env rollback placeholder: %w", err)
	}
	return path, nil
}

func atomicReplaceSetupEnv(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tempFile, err := os.CreateTemp(dir, ".env-*.tmp")
	if err != nil {
		return fmt.Errorf("create env temp file: %w", err)
	}
	tempPath := tempFile.Name()
	defer func() {
		_ = tempFile.Close()
		_ = os.Remove(tempPath)
	}()
	if err := tempFile.Chmod(mode); err != nil {
		return fmt.Errorf("chmod env temp file: %w", err)
	}
	if _, err := tempFile.Write(data); err != nil {
		return fmt.Errorf("write env temp file: %w", err)
	}
	if err := tempFile.Sync(); err != nil {
		return fmt.Errorf("sync env temp file: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("close env temp file: %w", err)
	}
	if err := atomicfile.Replace(tempPath, path); err != nil {
		return fmt.Errorf("rename env temp file: %w", err)
	}
	if dirFile, err := os.Open(dir); err == nil {
		_ = dirFile.Sync()
		_ = dirFile.Close()
	}
	return nil
}

func copyEnvValues(values map[string]string) map[string]string {
	copied := make(map[string]string, len(values))
	for key, value := range values {
		copied[key] = value
	}
	return copied
}

func promptRequiredEnvValue(opts Options, prompter Prompter, out Output, path string, values, defaults map[string]string, key string, secret bool) error {
	for {
		value, err := promptEnvValue(opts, prompter, out, values, defaults, key, secret)
		if err != nil {
			return err
		}
		if err := envfile.Set(path, key, value); err != nil {
			if opts.Yes || opts.NoInput {
				return ValidationError("%s is required for setup env: %v", key, err)
			}
			continue
		}
		values[key] = value
		return nil
	}
}

func promptOptionalEnvValue(opts Options, prompter Prompter, out Output, path string, values, defaults map[string]string, key string) error {
	for {
		value, err := promptEnvValue(opts, prompter, out, values, defaults, key, envfile.IsSecretKey(key))
		if err != nil {
			return err
		}
		if value == "" {
			if err := envfile.Unset(path, key); err != nil {
				return ValidationError("%v", err)
			}
			delete(values, key)
			return nil
		}
		if err := envfile.Set(path, key, value); err != nil {
			if opts.Yes || opts.NoInput {
				return ValidationError("%s is invalid for setup env: %v", key, err)
			}
			continue
		}
		values[key] = value
		return nil
	}
}

func promptBooleanEnvValue(opts Options, prompter Prompter, out Output, path string, values, defaults map[string]string, key string, options []string) error {
	current := envCandidate(values, defaults, key)
	if current == "" {
		current = options[0]
	}
	canonical, err := envfile.CanonicalBoolString(current)
	if err != nil {
		if opts.Yes || opts.NoInput {
			return ValidationError("%s is invalid for setup env: %v", key, err)
		}
		canonical = options[0]
	}
	if opts.Yes || opts.NoInput {
		if err := envfile.Set(path, key, canonical); err != nil {
			return ValidationError("%s is invalid for setup env: %v", key, err)
		}
		values[key] = canonical
		return nil
	}
	defaultIndex := indexOf(options, canonical)
	if defaultIndex < 0 {
		defaultIndex = 0
	}
	for {
		meta := envQuestionMeta(key)
		if err := out.SetupQuestion(meta.section, meta.title, meta.help); err != nil {
			return InternalError("%v", err)
		}
		value, err := prompter.Select("Choose", options, defaultIndex)
		if err != nil {
			return OperationalError("read setup env input for %s: %v", key, err)
		}
		canonicalValue, err := envfile.CanonicalBoolString(value)
		if err != nil {
			continue
		}
		if err := envfile.Set(path, key, canonicalValue); err != nil {
			continue
		}
		values[key] = canonicalValue
		return nil
	}
}

func promptSelectEnvValue(opts Options, prompter Prompter, out Output, path string, values, defaults map[string]string, key string, options []string) error {
	current := envCandidate(values, defaults, key)
	if current == "" {
		current = options[0]
	}
	if opts.Yes || opts.NoInput {
		if err := envfile.Set(path, key, current); err != nil {
			return ValidationError("%s is invalid for setup env: %v", key, err)
		}
		values[key] = current
		return nil
	}
	defaultIndex := indexOf(options, current)
	if defaultIndex < 0 {
		defaultIndex = 0
	}
	for {
		meta := envQuestionMeta(key)
		if err := out.SetupQuestion(meta.section, meta.title, meta.help); err != nil {
			return InternalError("%v", err)
		}
		value, err := prompter.Select("Choose", options, defaultIndex)
		if err != nil {
			return OperationalError("read setup env input for %s: %v", key, err)
		}
		if err := envfile.Set(path, key, value); err != nil {
			continue
		}
		values[key] = value
		return nil
	}
}

func promptEnvValue(opts Options, prompter Prompter, out Output, values, defaults map[string]string, key string, secret bool) (string, error) {
	candidate := envCandidate(values, defaults, key)
	if opts.Yes || opts.NoInput {
		return candidate, nil
	}
	meta := envQuestionMeta(key)
	if err := out.SetupQuestion(meta.section, meta.title, meta.help); err != nil {
		return "", InternalError("%v", err)
	}
	if secret {
		label := "Secret"
		if candidate != "" {
			label += " [current value kept if blank]"
		}
		value, err := prompter.SecretInput(label)
		if err != nil {
			return "", OperationalError("read setup env input for %s: %v", key, err)
		}
		if value == "" {
			return candidate, nil
		}
		return value, nil
	}
	value, err := prompter.Input("Value", candidate)
	if err != nil {
		return "", OperationalError("read setup env input for %s: %v", key, err)
	}
	return value, nil
}

func envCandidate(values, defaults map[string]string, key string) string {
	if value, ok := values[key]; ok {
		return value
	}
	return defaults[key]
}

func envQuestionMeta(key string) envQuestion {
	switch key {
	case "DISCORD_TOKEN":
		return envQuestion{section: "Discord bot", title: "Discord bot token", help: "Paste the bot token from the Discord developer portal. It stays masked in summaries."}
	case "MCBOT_TRUSTED_GUILD_ID":
		return envQuestion{section: "Discord bot", title: "Trusted Discord server ID", help: "Only this Discord server can use privileged bot controls."}
	case "EMBED_CHANNEL_ID":
		return envQuestion{section: "Discord bot", title: "Default status embed channel", help: "Optional. You can change it later with the Discord slash command."}
	case "MC_CONTAINER_NAME":
		return envQuestion{section: "Runtime features", title: "Minecraft container name", help: "Usually keep mc-server unless your Compose container name differs."}
	case "ENABLE_RCON":
		return envQuestion{section: "Runtime features", title: "Enable RCON", help: "RCON allows startup commands and server commands from the bot."}
	case "MCBOT_DEBUG":
		return envQuestion{section: "Runtime features · advanced", title: "Debug logging", help: "Verbose logs for troubleshooting. Keep disabled for normal use."}
	case "RCON_PASSWORD":
		return envQuestion{section: "Runtime features · advanced", title: "RCON password", help: "Optional secret used when RCON is enabled."}
	case "RCON_HOST":
		return envQuestion{section: "Runtime features · advanced", title: "RCON host", help: "Usually the Compose service DNS name mc-server."}
	case "RCON_PORT":
		return envQuestion{section: "Runtime features · advanced", title: "RCON port", help: "Minecraft RCON port inside the Compose network."}
	case "RCON_TIMEOUT_SECONDS":
		return envQuestion{section: "Runtime features · advanced", title: "RCON timeout", help: "Seconds to wait for RCON commands before failing."}
	case "RCON_CMDS_STARTUP":
		return envQuestion{section: "Runtime features · advanced", title: "RCON startup commands", help: "Optional commands to run after server startup."}
	default:
		return envQuestion{section: "Runtime features", title: key}
	}
}

func promptAdvancedSettings(prompter Prompter, out Output, section, title string, defaultValue bool) (bool, error) {
	if err := out.SetupQuestion(section, title, "Choose no to keep recommended defaults and existing values."); err != nil {
		return false, InternalError("%v", err)
	}
	confirmed, err := prompter.Confirm("Configure advanced settings", defaultValue)
	if err != nil {
		return false, OperationalError("read advanced setup confirmation: %v", err)
	}
	return confirmed, nil
}

func indexOf(values []string, target string) int {
	for i, value := range values {
		if strings.EqualFold(value, target) {
			return i
		}
	}
	return -1
}

func parseSetupEnvBool(value string) (bool, error) {
	parsed, err := envfile.ParseBoolString(value)
	if err != nil {
		return false, err
	}
	return parsed, nil
}
