package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/snowy/mcbot/internal/envfile"
	"github.com/snowy/mcbot/internal/mcconfig"
	"github.com/snowy/mcbot/internal/serverops"
)

func TestRootHelp(t *testing.T) {
	stdout, stderr, exitCode := runCLI(t)

	if exitCode != ExitOK {
		t.Fatalf("exit code = %d, want %d", exitCode, ExitOK)
	}
	assertContains(t, stdout, "MCBot operational CLI")
	assertContains(t, stdout, "mcbot [global options] <command> [args]")
	assertContains(t, stdout, "--json")
	assertContains(t, stdout, "--quiet")
	assertContains(t, stdout, "--no-input")
	assertContains(t, stdout, "--yes")
	assertContains(t, stdout, "--force")
	assertContains(t, stdout, "setup")
	assertContains(t, stdout, "setup env")
	assertContains(t, stdout, "setup config")
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestVersionCommand(t *testing.T) {
	stdout, stderr, exitCode := runCLI(t, "version")

	if exitCode != ExitOK {
		t.Fatalf("exit code = %d, want %d", exitCode, ExitOK)
	}
	if got, want := stdout, "mcbot dev\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
}

func TestRunWithIOPreservesRootBehavior(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	exitCode := runWithIO([]string{"help"}, strings.NewReader("unused\n"), &stdout, &stderr)

	if exitCode != ExitOK {
		t.Fatalf("exit code = %d, want %d", exitCode, ExitOK)
	}
	assertContains(t, stdout.String(), "MCBot operational CLI")
	assertContains(t, stdout.String(), "setup env")
	if stderr.String() != "" {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestUnknownCommandExitCode(t *testing.T) {
	stdout, stderr, exitCode := runCLI(t, "wat")

	if exitCode != ExitUsage {
		t.Fatalf("exit code = %d, want %d", exitCode, ExitUsage)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	assertContains(t, stderr, `unknown command "wat"`)
	assertContains(t, stderr, "Run 'mcbot help' for usage.")
}

func TestCommandTreeListsOperationalSubcommands(t *testing.T) {
	stdout, stderr, exitCode := runCLI(t, "help")

	if exitCode != ExitOK {
		t.Fatalf("exit code = %d, want %d", exitCode, ExitOK)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}

	for _, want := range []string{
		"bot run",
		"server start",
		"server stop",
		"server status",
		"config show",
		"config get",
		"config set",
		"config init",
		"config validate",
		"env show",
		"env get",
		"env set",
		"env unset",
		"env init",
		"env validate",
		"setup env",
		"setup config",
	} {
		assertContains(t, stdout, want)
	}

	tree := CommandTree()
	assertCommandTreeContains(t, tree, "bot", "run")
	assertCommandTreeContains(t, tree, "server", "start", "stop", "status")
	assertCommandTreeContains(t, tree, "config", "show", "get", "set", "init", "validate")
	assertCommandTreeContains(t, tree, "env", "show", "get", "set", "unset", "init", "validate")
	assertCommandTreeContains(t, tree, "setup", "env", "config")
}

func TestSetupCommandTree(t *testing.T) {
	stdout, stderr, exitCode := runCLI(t, "help")

	if exitCode != ExitOK {
		t.Fatalf("exit code = %d, want %d", exitCode, ExitOK)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	assertContains(t, stdout, "setup")
	assertContains(t, stdout, "setup env")
	assertContains(t, stdout, "setup config")

	tree := CommandTree()
	assertCommandTreeContains(t, tree, "setup", "env", "config")
}

func TestShowSecretsRejectedOutsideEnvCommands(t *testing.T) {
	stdout, stderr, exitCode := runCLI(t, "--show-secrets", "config", "show")

	if exitCode != ExitUsage {
		t.Fatalf("exit code = %d, want %d", exitCode, ExitUsage)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	assertContains(t, stderr, "--show-secrets is only supported for env commands")
	assertContains(t, stderr, "Run 'mcbot help' for usage.")
}

func TestNoInputRejectsInteractivePromptPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mc-server.toml")
	if err := os.WriteFile(path, []byte("existing"), 0o600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	defer SetConfigPathForTest(path)()

	stdout, stderr, exitCode := runCLI(t, "--no-input", "config", "init")

	if exitCode != ExitUsage {
		t.Fatalf("exit code = %d, want %d", exitCode, ExitUsage)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	assertContains(t, stderr, "config init would overwrite")
	assertContains(t, stderr, "--force or --yes")
	assertContains(t, stderr, "Run 'mcbot help' for usage.")
}

func TestJSONOutputPolicy(t *testing.T) {
	for _, args := range [][]string{
		{"server", "status"},
		{"config", "show"},
		{"config", "get"},
		{"config", "validate"},
		{"env", "show"},
		{"env", "get"},
		{"env", "validate"},
	} {
		if !SupportsJSON(args) {
			t.Fatalf("SupportsJSON(%v) = false, want true", args)
		}
	}

	for _, args := range [][]string{
		{},
		{"help"},
		{"version"},
		{"server", "start"},
		{"server", "stop"},
		{"config", "set"},
		{"config", "init"},
		{"env", "set"},
		{"env", "unset"},
		{"env", "init"},
		{"setup"},
		{"setup", "env"},
		{"setup", "config"},
	} {
		if SupportsJSON(args) {
			t.Fatalf("SupportsJSON(%v) = true, want false", args)
		}
	}

	stdout, stderr, exitCode := runCLI(t, "--json", "version")
	if exitCode != ExitUsage {
		t.Fatalf("exit code = %d, want %d", exitCode, ExitUsage)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	assertContains(t, stderr, "--json is not supported for version")

	server := &fakeServerRunner{status: serverops.Result{Service: "mc-server", Exists: true, Running: true, Status: "running"}}
	defer SetServerRunnerForTest(server)()
	stdout, stderr, exitCode = runCLI(t, "--json", "server", "status")
	if exitCode != ExitOK {
		t.Fatalf("exit code = %d, want %d", exitCode, ExitOK)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	assertContains(t, stdout, `"service":"mc-server"`)
}

func TestSetupNoInputContract(t *testing.T) {
	for _, args := range [][]string{
		{"--no-input", "setup"},
		{"--no-input", "setup", "env"},
		{"--no-input", "setup", "config"},
	} {
		stdout, stderr, exitCode := runCLI(t, args...)
		if exitCode != ExitUsage {
			t.Fatalf("runCLI(%v) exit code = %d, want %d", args, exitCode, ExitUsage)
		}
		if stdout != "" {
			t.Fatalf("runCLI(%v) stdout = %q, want empty", args, stdout)
		}
		assertContains(t, stderr, "prompt required but --no-input was set")
		assertContains(t, stderr, "Run 'mcbot help' for usage.")
	}

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	tempDir := t.TempDir()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	stdout, stderr, exitCode := runCLI(t, "--yes", "setup")
	if exitCode != ExitValidation {
		t.Fatalf("--yes setup exit code = %d, want %d", exitCode, ExitValidation)
	}
	if stdout != "" {
		t.Fatalf("--yes setup stdout = %q, want empty", stdout)
	}
	assertContains(t, stderr, "DISCORD_TOKEN is required")
	assertFileDoesNotExist(t, filepath.Join(tempDir, "mc-server.toml"))

	stdinPath := filepath.Join(tempDir, "stdin.txt")
	if err := os.WriteFile(stdinPath, []byte("ignored\n"), 0o600); err != nil {
		t.Fatalf("WriteFile stdin failed: %v", err)
	}
	stdin, err := os.Open(stdinPath)
	if err != nil {
		t.Fatalf("Open stdin failed: %v", err)
	}
	defer stdin.Close()
	var forceStdout bytes.Buffer
	var forceStderr bytes.Buffer
	exitCode = runWithIO([]string{"--force", "setup"}, stdin, &forceStdout, &forceStderr)
	if exitCode != ExitUsage {
		t.Fatalf("--force setup exit code = %d, want %d", exitCode, ExitUsage)
	}
	if forceStdout.String() != "" {
		t.Fatalf("--force setup stdout = %q, want empty", forceStdout.String())
	}
	assertContains(t, forceStderr.String(), "prompt required but interactive input is unavailable")
}

func TestSetupCombinedFlow(t *testing.T) {
	tempDir := t.TempDir()
	envPath := filepath.Join(tempDir, ".env")
	configPath := filepath.Join(tempDir, "mc-server.toml")
	defer SetEnvPathForTest(envPath)()
	defer SetConfigPathForTest(configPath)()
	defer setOwnershipDetectorsForTest(
		func() (ownershipPair, bool) { return ownershipPair{UID: 1000, GID: 1000}, true },
		func(string) (ownershipPair, bool, error) { return ownershipPair{}, false, nil },
	)()
	input := setupInputLines(
		"combined-discord-token",
		"123456789012345678",
		"",
		"",
		"2",
		"",
		"",
		"",
		"",
		"",
		"",
		"",
		"",
		"",
		"",
		"",
		"",
		"",
		"",
		"",
		"",
		"",
		"",
	)

	stdout, stderr, exitCode := runCLIWithInput(t, input, "setup")

	if exitCode != ExitOK {
		t.Fatalf("exit code = %d, want %d; stderr=%q", exitCode, ExitOK, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	assertContains(t, readTestFile(t, envPath), "DISCORD_TOKEN=combined-discord-token")
	assertContains(t, readTestFile(t, envPath), "ENABLE_RCON=false")
	assertNotContains(t, readTestFile(t, envPath), "RCON_PASSWORD=")
	if err := envfile.ValidateFile(envPath); err != nil {
		t.Fatalf("env ValidateFile failed: %v", err)
	}
	if err := mcconfig.ValidateFile(configPath); err != nil {
		t.Fatalf("config ValidateFile failed: %v", err)
	}
	assertContains(t, stdout, "MCBot Setup")
	assertContains(t, stdout, "[1/5] Discord bot")
	assertContains(t, stdout, "[3/5] Minecraft server")
	assertContains(t, stdout, "[4/5] Container behavior")
	assertContains(t, stdout, "[5/5] Review & write")
	assertContains(t, stdout, "set, masked")
	assertNotContains(t, stdout, "combined-discord-token")
	assertContains(t, stdout, "✓ Wrote "+configPath)
	assertContains(t, stdout, "This wizard will create/update:")
	assertContains(t, stdout, "✓ Wrote "+envPath)
	assertContains(t, stdout, "✓ Wrote "+configPath)
	assertContains(t, stdout, "✓ Validation passed")
	assertContains(t, stdout, "Next:")
	assertContains(t, stdout, "make env-validate")
	assertContains(t, stdout, "make config-validate")
	assertContains(t, stdout, "make up-all")
}

func TestSetupYesUsesDefaults(t *testing.T) {
	tempDir := t.TempDir()
	envPath := filepath.Join(tempDir, ".env")
	configPath := filepath.Join(tempDir, "mc-server.toml")
	defer setOwnershipDetectorsForTest(
		func() (ownershipPair, bool) { return ownershipPair{UID: 1234, GID: 2345}, true },
		func(string) (ownershipPair, bool, error) { return ownershipPair{}, false, nil },
	)()
	if err := os.WriteFile(envPath, []byte(setupInputLines(
		"DISCORD_TOKEN=existing-discord-token",
		"MCBOT_TRUSTED_GUILD_ID=123456789012345678",
	)), 0o600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	defer SetEnvPathForTest(envPath)()
	defer SetConfigPathForTest(configPath)()

	stdout, stderr, exitCode := runCLI(t, "--yes", "setup")

	if exitCode != ExitOK {
		t.Fatalf("--yes setup exit code = %d, want %d; stderr=%q", exitCode, ExitOK, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	assertContains(t, readTestFile(t, envPath), "DISCORD_TOKEN=existing-discord-token")
	assertContains(t, readTestFile(t, envPath), "MC_CONTAINER_NAME=mc-server")
	assertContains(t, readTestFile(t, envPath), "ENABLE_RCON=true")
	assertNotContains(t, readTestFile(t, envPath), "RCON_PASSWORD=")
	assertNotContains(t, stdout, "existing-discord-token")
	assertContains(t, stdout, "set, masked")
	if err := mcconfig.ValidateFile(configPath); err != nil {
		t.Fatalf("config ValidateFile failed: %v", err)
	}
	cfg, err := mcconfig.Load(configPath)
	if err != nil {
		t.Fatalf("Load config failed: %v", err)
	}
	if cfg.Container.UID != 1234 || cfg.Container.GID != 2345 {
		t.Fatalf("--yes setup should use detected current user ownership for new config: %+v", cfg.Container)
	}

	missingEnvPath := filepath.Join(t.TempDir(), ".env")
	missingConfigPath := filepath.Join(t.TempDir(), "mc-server.toml")
	defer SetEnvPathForTest(missingEnvPath)()
	defer SetConfigPathForTest(missingConfigPath)()
	stdout, stderr, exitCode = runCLI(t, "--yes", "setup")
	if exitCode != ExitValidation {
		t.Fatalf("missing candidate exit code = %d, want %d", exitCode, ExitValidation)
	}
	if stdout != "" {
		t.Fatalf("missing candidate stdout = %q, want empty", stdout)
	}
	assertContains(t, stderr, "DISCORD_TOKEN is required")
	assertFileDoesNotExist(t, missingConfigPath)
}

func TestSetupOverwriteConfirmationProtectsExistingEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	initial := setupInputLines(
		"DISCORD_TOKEN=existing-token",
		"MCBOT_TRUSTED_GUILD_ID=123456789012345678",
		"MC_CONTAINER_NAME=existing-container",
	)
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	defer SetEnvPathForTest(path)()

	stdout, stderr, exitCode := runCLIWithInput(t, setupInputLines("n"), "setup", "env")

	if exitCode != ExitUsage {
		t.Fatalf("exit code = %d, want %d", exitCode, ExitUsage)
	}
	assertContains(t, stdout, "MCBot Setup")
	assertContains(t, stdout, "setup env would overwrite "+path)
	assertContains(t, stderr, "setup env would overwrite "+path)
	if got := readTestFile(t, path); got != initial {
		t.Fatalf("declined overwrite changed env file: got %q want %q", got, initial)
	}
}

func TestSetupOverwriteConfirmationProtectsExistingConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mc-server.toml")
	if err := mcconfig.Init(path); err != nil {
		t.Fatalf("mcconfig.Init failed: %v", err)
	}
	initial := readTestFile(t, path)
	defer SetConfigPathForTest(path)()

	stdout, stderr, exitCode := runCLIWithInput(t, setupInputLines("n"), "setup", "config")

	if exitCode != ExitUsage {
		t.Fatalf("exit code = %d, want %d", exitCode, ExitUsage)
	}
	assertContains(t, stdout, "MCBot Setup")
	assertContains(t, stdout, "setup config would overwrite "+path)
	assertContains(t, stderr, "setup config would overwrite "+path)
	if got := readTestFile(t, path); got != initial {
		t.Fatalf("declined overwrite changed config file: got %q want %q", got, initial)
	}
}

func TestSetupCombinedConfigOverwriteDeclineDoesNotCreateEnv(t *testing.T) {
	tempDir := t.TempDir()
	envPath := filepath.Join(tempDir, ".env")
	configPath := filepath.Join(tempDir, "mc-server.toml")
	if err := mcconfig.Init(configPath); err != nil {
		t.Fatalf("mcconfig.Init failed: %v", err)
	}
	initialConfig := readTestFile(t, configPath)
	defer SetEnvPathForTest(envPath)()
	defer SetConfigPathForTest(configPath)()

	stdout, stderr, exitCode := runCLIWithInput(t, setupInputLines("n"), "setup")

	if exitCode != ExitUsage {
		t.Fatalf("exit code = %d, want %d", exitCode, ExitUsage)
	}
	assertContains(t, stdout, "MCBot Setup")
	assertContains(t, stdout, "setup config would overwrite "+configPath)
	assertContains(t, stderr, "setup config would overwrite "+configPath)
	assertFileDoesNotExist(t, envPath)
	if got := readTestFile(t, configPath); got != initialConfig {
		t.Fatalf("declined combined overwrite changed config file: got %q want %q", got, initialConfig)
	}
}

func TestSetupForceBypassesOnlyOverwriteConfirmation(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	initial := setupInputLines("MCBOT_TRUSTED_GUILD_ID=123456789012345678")
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	defer SetEnvPathForTest(path)()
	input := setupInputLines("", "force-token", "", "", "", "2", "", "")

	stdout, stderr, exitCode := runCLIWithInput(t, input, "--force", "setup", "env")

	if exitCode != ExitOK {
		t.Fatalf("exit code = %d, want %d; stderr=%q", exitCode, ExitOK, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	assertNotContains(t, stdout, "would overwrite")
	if strings.Count(stdout, "Secret: ") != 2 {
		t.Fatalf("--force did not require retry for missing DISCORD_TOKEN; stdout=%q", stdout)
	}
	assertContains(t, readTestFile(t, path), "DISCORD_TOKEN=force-token")
	assertNotContains(t, stdout, "force-token")
}

func TestSetupForceYesDoesNotInventMissingRequiredValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("MCBOT_TRUSTED_GUILD_ID=123456789012345678\n"), 0o600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	defer SetEnvPathForTest(path)()

	stdout, stderr, exitCode := runCLIWithInput(t, "", "--force", "--yes", "setup", "env")

	if exitCode != ExitValidation {
		t.Fatalf("exit code = %d, want %d", exitCode, ExitValidation)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	assertContains(t, stderr, "DISCORD_TOKEN is required")
	assertNotContains(t, readTestFile(t, path), "DISCORD_TOKEN=")
}

func TestSetupEnvFailureDoesNotPartiallyMutateOriginalFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	initial := setupInputLines(
		"DISCORD_TOKEN=existing-discord-token",
		"MCBOT_TRUSTED_GUILD_ID=123456789012345678",
		"FOREIGN_KEY=foreign-value",
	)
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	defer SetEnvPathForTest(path)()

	stdout, stderr, exitCode := runCLI(t, "--yes", "setup", "env")

	if exitCode != ExitValidation {
		t.Fatalf("exit code = %d, want %d", exitCode, ExitValidation)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	assertContains(t, stderr, "FOREIGN_KEY is not owned by .env")
	if got := readTestFile(t, path); got != initial {
		t.Fatalf("setup env mutated original file on failure: got %q want %q", got, initial)
	}
	if matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".setup-env-*.tmp")); err != nil {
		t.Fatalf("Glob failed: %v", err)
	} else if len(matches) != 0 {
		t.Fatalf("staging files left behind: %v", matches)
	}
	if matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".env-*.tmp")); err != nil {
		t.Fatalf("Glob failed: %v", err)
	} else if len(matches) != 0 {
		t.Fatalf("env temp files left behind: %v", matches)
	}
}

func TestSetupEnvStageUsesPrivateTempDirOutsideEnvDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("DISCORD_TOKEN=existing-discord-token\n"), 0o600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	stagedPath, cleanup, err := createSetupEnvStage(path)
	if err != nil {
		t.Fatalf("createSetupEnvStage failed: %v", err)
	}
	stageDir := filepath.Dir(stagedPath)
	if stageDir == filepath.Dir(path) {
		cleanup()
		t.Fatalf("staging path %s is inside env directory %s", stagedPath, filepath.Dir(path))
	}
	if info, err := os.Stat(stageDir); err != nil {
		cleanup()
		t.Fatalf("stat staging directory failed: %v", err)
	} else if got := info.Mode().Perm(); got != 0o700 {
		cleanup()
		t.Fatalf("staging directory mode = %v, want 0700", got)
	}

	cleanup()
	assertFileDoesNotExist(t, stageDir)
}

func TestCommitSetupEnvStageUsesAtomicTempFile(t *testing.T) {
	envDir := t.TempDir()
	path := filepath.Join(envDir, ".env")
	initial := "DISCORD_TOKEN=existing-discord-token\n"
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatalf("WriteFile initial env failed: %v", err)
	}

	stageDir := t.TempDir()
	stagedPath := filepath.Join(stageDir, ".env")
	if err := os.WriteFile(stagedPath, []byte("DISCORD_TOKEN=updated-discord-token\n"), 0o600); err != nil {
		t.Fatalf("WriteFile staged env failed: %v", err)
	}

	if err := os.Chmod(envDir, 0o500); err != nil {
		t.Fatalf("Chmod env directory read-only failed: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(envDir, 0o700); err != nil {
			t.Fatalf("restore env directory permissions failed: %v", err)
		}
	})

	err := commitSetupEnvStage(stagedPath, path)
	if err == nil {
		t.Fatalf("commitSetupEnvStage succeeded without permission to create atomic temp file")
	}
	assertContains(t, err.Error(), "replace env file")
	if got := readTestFile(t, path); got != initial {
		t.Fatalf("failed atomic env commit mutated original file: got %q want %q", got, initial)
	}
}

func TestSetupEnvYesTreatsTruthyEnableRCONAsEnabled(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	initial := setupInputLines(
		"DISCORD_TOKEN=existing-discord-token",
		"MCBOT_TRUSTED_GUILD_ID=123456789012345678",
		"ENABLE_RCON=yes",
		"RCON_PASSWORD=existing-rcon-password",
		"RCON_HOST=existing-host",
		"RCON_PORT=25580",
	)
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	defer SetEnvPathForTest(path)()

	stdout, stderr, exitCode := runCLI(t, "--yes", "setup", "env")

	if exitCode != ExitOK {
		t.Fatalf("exit code = %d, want %d; stderr=%q", exitCode, ExitOK, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	got := readTestFile(t, path)
	assertContains(t, got, "ENABLE_RCON=true")
	assertContains(t, got, "RCON_PASSWORD=existing-rcon-password")
	assertContains(t, got, "RCON_HOST=existing-host")
	assertContains(t, got, "RCON_PORT=25580")
	assertNotContains(t, stdout, "existing-rcon-password")
}

func TestSetupEnvSkipsAdvancedRuntimePreservesEnabledRCONValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	initial := setupInputLines(
		"DISCORD_TOKEN=existing-discord-token",
		"MCBOT_TRUSTED_GUILD_ID=123456789012345678",
		"MC_CONTAINER_NAME=existing-container",
		"ENABLE_RCON=true",
		"RCON_PASSWORD=existing-rcon-password",
		"RCON_HOST=existing-host",
		"RCON_PORT=25580",
		"RCON_TIMEOUT_SECONDS=12",
		"RCON_CMDS_STARTUP=say hello",
	)
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	defer SetEnvPathForTest(path)()
	input := setupInputLines("y", "", "", "", "", "", "n", "")

	stdout, stderr, exitCode := runCLIWithInput(t, input, "setup", "env")

	if exitCode != ExitOK {
		t.Fatalf("exit code = %d, want %d; stderr=%q", exitCode, ExitOK, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	got := readTestFile(t, path)
	assertContains(t, got, "ENABLE_RCON=true")
	assertContains(t, got, "RCON_PASSWORD=existing-rcon-password")
	assertContains(t, got, "RCON_HOST=existing-host")
	assertContains(t, got, "RCON_PORT=25580")
	assertContains(t, got, "RCON_TIMEOUT_SECONDS=12")
	assertContains(t, got, "RCON_CMDS_STARTUP=say hello")
	assertContains(t, stdout, "set, masked")
	assertNotContains(t, stdout, "existing-rcon-password")
}

func TestSetupNoTTYFailsPromptRequiredPath(t *testing.T) {
	for _, args := range [][]string{{"setup"}, {"setup", "env"}, {"setup", "config"}} {
		stdinPath := filepath.Join(t.TempDir(), "stdin.txt")
		if err := os.WriteFile(stdinPath, []byte("ignored\n"), 0o600); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}
		stdin, err := os.Open(stdinPath)
		if err != nil {
			t.Fatalf("Open failed: %v", err)
		}
		var stdout bytes.Buffer
		var stderr bytes.Buffer

		exitCode := runWithIO(args, stdin, &stdout, &stderr)
		if closeErr := stdin.Close(); closeErr != nil {
			t.Fatalf("Close failed: %v", closeErr)
		}

		if exitCode != ExitUsage {
			t.Fatalf("runWithIO(%v) exit code = %d, want %d", args, exitCode, ExitUsage)
		}
		if stdout.String() != "" {
			t.Fatalf("runWithIO(%v) stdout = %q, want empty", args, stdout.String())
		}
		assertContains(t, stderr.String(), "prompt required but interactive input is unavailable")
		assertContains(t, stderr.String(), "Run 'mcbot help' for usage.")
	}
}

func TestSetupEmptyInputFailsBeforePromptEOF(t *testing.T) {
	for _, args := range [][]string{{"setup"}, {"setup", "env"}, {"setup", "config"}} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer

		exitCode := runWithIO(args, strings.NewReader(""), &stdout, &stderr)

		if exitCode != ExitUsage {
			t.Fatalf("runWithIO(%v) exit code = %d, want %d", args, exitCode, ExitUsage)
		}
		if stdout.String() != "" {
			t.Fatalf("runWithIO(%v) stdout = %q, want empty", args, stdout.String())
		}
		assertContains(t, stderr.String(), "prompt required but interactive input is unavailable")
		assertNotContains(t, stderr.String(), "EOF")
	}
}

func TestSetupDevNullInputFailsBeforePromptEOF(t *testing.T) {
	for _, args := range [][]string{{"setup"}, {"setup", "env"}, {"setup", "config"}} {
		stdin, err := os.Open(os.DevNull)
		if err != nil {
			t.Fatalf("Open os.DevNull failed: %v", err)
		}
		var stdout bytes.Buffer
		var stderr bytes.Buffer

		exitCode := runWithIO(args, stdin, &stdout, &stderr)
		if closeErr := stdin.Close(); closeErr != nil {
			t.Fatalf("Close os.DevNull failed: %v", closeErr)
		}

		if exitCode != ExitUsage {
			t.Fatalf("runWithIO(%v) exit code = %d, want %d", args, exitCode, ExitUsage)
		}
		if stdout.String() != "" {
			t.Fatalf("runWithIO(%v) stdout = %q, want empty", args, stdout.String())
		}
		assertContains(t, stderr.String(), "prompt required but interactive input is unavailable")
		assertNotContains(t, stderr.String(), "EOF")
	}
}

func TestSetupEnvInteractiveFlow(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	defer SetEnvPathForTest(path)()
	input := setupInputLines(
		"discord-token",
		"123456789012345678",
		"",
		"",
		"1",
		"y",
		"2",
		"rcon-password",
		"",
		"",
		"",
		"gamerule keepInventory true",
		"",
	)

	stdout, stderr, exitCode := runCLIWithInput(t, input, "setup", "env")

	if exitCode != ExitOK {
		t.Fatalf("exit code = %d, want %d; stderr=%q", exitCode, ExitOK, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	got := readTestFile(t, path)
	assertContains(t, got, "DISCORD_TOKEN=discord-token")
	assertContains(t, got, "MCBOT_TRUSTED_GUILD_ID=123456789012345678")
	assertContains(t, got, "MC_CONTAINER_NAME=mc-server")
	assertContains(t, got, "MCBOT_DEBUG=true")
	assertContains(t, got, "ENABLE_RCON=true")
	assertContains(t, got, "RCON_PASSWORD=rcon-password")
	assertContains(t, got, "RCON_HOST=mc-server")
	assertContains(t, got, "RCON_PORT=25575")
	assertContains(t, got, "RCON_TIMEOUT_SECONDS=10")
	assertContains(t, got, "RCON_CMDS_STARTUP=gamerule keepInventory true")
	assertContains(t, stdout, "set, masked")
	assertNotContains(t, stdout, "discord-token")
	assertNotContains(t, stdout, "rcon-password")
}

func TestSetupEnvUsesExistingValuesAsDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	initial := setupInputLines(
		"DISCORD_TOKEN=existing-token",
		"MCBOT_TRUSTED_GUILD_ID=123456789012345678",
		"EMBED_CHANNEL_ID=234567890123456789",
		"MC_CONTAINER_NAME=existing-container",
		"MCBOT_DEBUG=true",
		"ENABLE_RCON=false",
		"RCON_PASSWORD=stale-rcon-password",
		"RCON_HOST=stale-host",
	)
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	defer SetEnvPathForTest(path)()
	input := setupInputLines("y", "", "", "", "", "", "", "")

	stdout, stderr, exitCode := runCLIWithInput(t, input, "setup", "env")

	if exitCode != ExitOK {
		t.Fatalf("exit code = %d, want %d; stderr=%q", exitCode, ExitOK, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	got := readTestFile(t, path)
	assertContains(t, got, "DISCORD_TOKEN=existing-token")
	assertContains(t, got, "MCBOT_TRUSTED_GUILD_ID=123456789012345678")
	assertContains(t, got, "EMBED_CHANNEL_ID=234567890123456789")
	assertContains(t, got, "MC_CONTAINER_NAME=existing-container")
	assertContains(t, got, "MCBOT_DEBUG=true")
	assertContains(t, got, "ENABLE_RCON=false")
	assertNotContains(t, got, "RCON_PASSWORD=")
	assertNotContains(t, got, "RCON_HOST=")
	assertContains(t, stdout, "set, masked")
	assertNotContains(t, stdout, "existing-token")
	assertNotContains(t, stdout, "stale-rcon-password")
}

func TestSetupConfigYesOverwritesMalformedExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mc-server.toml")
	defer setOwnershipDetectorsForTest(
		func() (ownershipPair, bool) { return ownershipPair{UID: 1000, GID: 1000}, true },
		func(string) (ownershipPair, bool, error) { return ownershipPair{}, false, nil },
	)()
	if err := os.WriteFile(path, []byte("[server]\nversion = \"1.20.1\"\nview_distance = \"broken\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	defer SetConfigPathForTest(path)()

	stdout, stderr, exitCode := runCLI(t, "--yes", "setup", "config")

	if exitCode != ExitOK {
		t.Fatalf("exit code = %d, want %d; stderr=%q", exitCode, ExitOK, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	assertContains(t, stdout, "✓ Wrote "+path)
	if err := mcconfig.ValidateFile(path); err != nil {
		t.Fatalf("ValidateFile failed: %v", err)
	}
	cfg, err := mcconfig.Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg != mcconfig.Defaults() {
		t.Fatalf("malformed config was not replaced with defaults: got %+v want %+v", cfg, mcconfig.Defaults())
	}
}

func TestSetupEnvMissingRequiredValueReprompts(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	defer SetEnvPathForTest(path)()
	input := setupInputLines(
		"",
		"discord-token-after-retry",
		"123456789012345678",
		"",
		"",
		"2",
		"",
		"",
	)

	_, stderr, exitCode := runCLIWithInput(t, input, "setup", "env")

	if exitCode != ExitOK {
		t.Fatalf("exit code = %d, want %d; stderr=%q", exitCode, ExitOK, stderr)
	}
	assertContains(t, readTestFile(t, path), "DISCORD_TOKEN=discord-token-after-retry")

	missingPath := filepath.Join(t.TempDir(), ".env")
	defer SetEnvPathForTest(missingPath)()
	stdout, stderr, exitCode := runCLIWithInput(t, "", "--yes", "setup", "env")
	if exitCode != ExitValidation {
		t.Fatalf("--yes exit code = %d, want %d", exitCode, ExitValidation)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	assertContains(t, stderr, "DISCORD_TOKEN is required")
}

func TestSetupEnvMasksSummary(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	defer SetEnvPathForTest(path)()
	input := setupInputLines(
		"masked-discord-token",
		"123456789012345678",
		"",
		"",
		"1",
		"y",
		"",
		"masked-rcon-password",
		"",
		"",
		"",
		"",
		"",
	)

	stdout, stderr, exitCode := runCLIWithInput(t, input, "setup", "env")

	if exitCode != ExitOK {
		t.Fatalf("exit code = %d, want %d; stderr=%q", exitCode, ExitOK, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	assertContains(t, stdout, "set, masked")
	assertNotContains(t, stdout, "masked-discord-token")
	assertNotContains(t, stdout, "masked-rcon-password")
}

func TestSetupConfigInteractiveFlow(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mc-server.toml")
	defer SetConfigPathForTest(path)()
	defer setOwnershipDetectorsForTest(
		func() (ownershipPair, bool) { return ownershipPair{UID: 1000, GID: 1000}, true },
		func(string) (ownershipPair, bool, error) { return ownershipPair{}, false, nil },
	)()

	input := setupInputLines(
		"1.21.1",
		"9", "3",
		"4",
		"8G",
		"Hello from setup",
		"y",
		"4G",
		"10",
		"12",
		"3",
		"25566:25565",
		"y",
		"2",
		"1002",
		"1003",
		"",
	)
	stdout, stderr, exitCode := runCLIWithInput(t, input, "setup", "config")

	if exitCode != ExitOK {
		t.Fatalf("exit code = %d, want %d; stderr=%q", exitCode, ExitOK, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	assertContains(t, stdout, "MCBot Setup")
	assertContains(t, stdout, "[1/3] Minecraft server")
	assertContains(t, stdout, "[2/3] Container behavior")
	assertContains(t, stdout, "Configure advanced container ownership?")
	assertContains(t, stdout, "Minecraft version")
	assertContains(t, stdout, "Server type")
	assertContains(t, stdout, "Please choose a number from 1 to 4.")
	assertContains(t, stdout, "Discord bot/CLI controls start and stop")
	assertContains(t, stdout, "bot crash/log visibility can be less precise")
	assertContains(t, stdout, "Container file ownership")
	assertContains(t, stdout, "✓ Wrote "+path)

	if err := mcconfig.ValidateFile(path); err != nil {
		t.Fatalf("ValidateFile failed: %v", err)
	}
	cfg, err := mcconfig.Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Server.Version != "1.21.1" || cfg.Server.Type != "FABRIC" || cfg.Server.Difficulty != "hard" {
		t.Fatalf("unexpected server enum/string values: %+v", cfg.Server)
	}
	if cfg.Server.Memory != "8G" || cfg.Server.InitMemory != "4G" || cfg.Server.MOTD != "Hello from setup" {
		t.Fatalf("unexpected server text values: %+v", cfg.Server)
	}
	if cfg.Server.ViewDistance != 10 || cfg.Server.SimulationDistance != 12 {
		t.Fatalf("unexpected server distance values: %+v", cfg.Server)
	}
	if cfg.Container.RestartPolicy != "unless-stopped" || cfg.Container.PortPublish != "25566:25565" {
		t.Fatalf("unexpected container string values: %+v", cfg.Container)
	}
	if cfg.Container.UID != 1002 || cfg.Container.GID != 1003 {
		t.Fatalf("unexpected container ids: %+v", cfg.Container)
	}
}

func TestSetupConfigUsesExistingValuesAsDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mc-server.toml")
	existing := mcconfig.Defaults()
	existing.Server.Version = "1.19.4"
	existing.Server.Type = "PAPER"
	existing.Server.Difficulty = "normal"
	existing.Server.Memory = "6G"
	existing.Server.InitMemory = "2G"
	existing.Server.MOTD = "Existing MOTD"
	existing.Server.ViewDistance = 16
	existing.Server.SimulationDistance = 6
	existing.Container.RestartPolicy = "always"
	existing.Container.PortPublish = "25567:25565"
	existing.Container.UID = 2000
	existing.Container.GID = 2001
	if err := mcconfig.Write(path, existing); err != nil {
		t.Fatalf("Write existing config failed: %v", err)
	}
	defer SetConfigPathForTest(path)()

	stdout, stderr, exitCode := runCLIWithInput(t, setupInputLines("y", "", "", "", "", "", "", "", "", "y", "", ""), "setup", "config")

	if exitCode != ExitOK {
		t.Fatalf("exit code = %d, want %d; stderr=%q", exitCode, ExitOK, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	assertContains(t, stdout, "Minecraft version")
	assertContains(t, stdout, "Choose 1-4 [4]: ")
	assertContains(t, stdout, "Keep current config — 2000:2001")

	cfg, err := mcconfig.Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg != existing {
		t.Fatalf("blank setup changed config: got %+v want %+v", cfg, existing)
	}
}

func TestSetupConfigRejectsInvalidTypedValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mc-server.toml")
	defer SetConfigPathForTest(path)()
	defer setOwnershipDetectorsForTest(
		func() (ownershipPair, bool) { return ownershipPair{UID: 1000, GID: 1000}, true },
		func(string) (ownershipPair, bool, error) { return ownershipPair{}, false, nil },
	)()

	input := setupInputLines(
		"",
		"",
		"",
		"lots", "2G",
		"",
		"y",
		"1G",
		"far", "9",
		"near", "7",
		"",
		"25565", "25565:25565",
		"y",
		"2",
		"root", "1001",
		"ops", "1001",
		"",
	)
	stdout, stderr, exitCode := runCLIWithInput(t, input, "setup", "config")

	if exitCode != ExitOK {
		t.Fatalf("exit code = %d, want %d; stderr=%q", exitCode, ExitOK, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	assertContains(t, stdout, "Max memory")
	assertContains(t, stdout, "View distance")
	assertContains(t, stdout, "Port publish")
	if strings.Count(stdout, "Please enter a non-negative integer.") != 2 {
		t.Fatalf("stdout did not explain invalid UID/GID retries twice: %q", stdout)
	}

	cfg, err := mcconfig.Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Server.Memory != "2G" || cfg.Server.ViewDistance != 9 || cfg.Server.SimulationDistance != 7 {
		t.Fatalf("invalid typed retries not applied: %+v", cfg.Server)
	}
	if cfg.Container.PortPublish != "25565:25565" || cfg.Container.UID != 1001 || cfg.Container.GID != 1001 {
		t.Fatalf("invalid container retries not applied: %+v", cfg.Container)
	}
}

func TestSetupConfigOwnershipStatErrorFails(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "mc-server.toml")
	defer SetConfigPathForTest(path)()
	defer setOwnershipDetectorsForTest(
		func() (ownershipPair, bool) { return ownershipPair{UID: 1000, GID: 1000}, true },
		func(string) (ownershipPair, bool, error) {
			return ownershipPair{}, false, errors.New("permission denied")
		},
	)()

	input := setupInputLines("", "", "", "", "", "", "", "", "y")
	stdout, stderr, exitCode := runCLIWithInput(t, input, "setup", "config")

	if exitCode != ExitValidation {
		t.Fatalf("exit code = %d, want %d", exitCode, ExitValidation)
	}
	assertContains(t, stderr, "inspect "+filepath.Join(tempDir, "data")+" ownership: permission denied")
	assertNotContains(t, stdout, "✓ Wrote "+path)
	assertFileDoesNotExist(t, path)
}

func TestSetupConfigOwnershipUsesExistingDataOwner(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "mc-server.toml")
	defer SetConfigPathForTest(path)()
	defer setOwnershipDetectorsForTest(
		func() (ownershipPair, bool) { return ownershipPair{UID: 1000, GID: 1000}, true },
		func(gotPath string) (ownershipPair, bool, error) {
			if gotPath != filepath.Join(tempDir, "data") {
				t.Fatalf("ownership detector path = %q, want data dir under temp dir", gotPath)
			}
			return ownershipPair{UID: 3456, GID: 4567}, true, nil
		},
	)()

	input := setupInputLines("", "", "", "", "", "", "", "", "y", "", "")
	stdout, stderr, exitCode := runCLIWithInput(t, input, "setup", "config")

	if exitCode != ExitOK {
		t.Fatalf("exit code = %d, want %d; stderr=%q", exitCode, ExitOK, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	assertContains(t, stdout, "Use existing ./data owner — 3456:4567")
	cfg, err := mcconfig.Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Container.UID != 3456 || cfg.Container.GID != 4567 {
		t.Fatalf("detected data ownership not applied: %+v", cfg.Container)
	}
}

func TestSetupConfigDefaultOwnershipPromptUsesExistingDataOwner(t *testing.T) {
	tempDir := t.TempDir()
	path := filepath.Join(tempDir, "mc-server.toml")
	defer SetConfigPathForTest(path)()
	defer setOwnershipDetectorsForTest(
		func() (ownershipPair, bool) { return ownershipPair{UID: 1000, GID: 1000}, true },
		func(gotPath string) (ownershipPair, bool, error) {
			if gotPath != filepath.Join(tempDir, "data") {
				t.Fatalf("ownership detector path = %q, want data dir under temp dir", gotPath)
			}
			return ownershipPair{UID: 3456, GID: 4567}, true, nil
		},
	)()

	input := setupInputLines("", "", "", "", "", "", "", "", "", "")
	stdout, stderr, exitCode := runCLIWithInput(t, input, "setup", "config")

	if exitCode != ExitOK {
		t.Fatalf("exit code = %d, want %d; stderr=%q", exitCode, ExitOK, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	assertContains(t, stdout, "Configure advanced container ownership?")
	assertNotContains(t, stdout, "Container file ownership")
	cfg, err := mcconfig.Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if cfg.Container.UID != 3456 || cfg.Container.GID != 4567 {
		t.Fatalf("default ownership prompt did not apply detected data owner: %+v", cfg.Container)
	}
}

func TestNoInputStillFailsPromptRequiredSetupPath(t *testing.T) {
	for _, args := range [][]string{
		{"--no-input", "setup"},
		{"--force", "--no-input", "setup"},
		{"--force", "--no-input", "setup", "env"},
		{"--force", "--no-input", "setup", "config"},
	} {
		var stdout bytes.Buffer
		var stderr bytes.Buffer

		exitCode := runWithIO(args, strings.NewReader("ignored\n"), &stdout, &stderr)

		if exitCode != ExitUsage {
			t.Fatalf("runWithIO(%v) exit code = %d, want %d", args, exitCode, ExitUsage)
		}
		if stdout.String() != "" {
			t.Fatalf("runWithIO(%v) stdout = %q, want empty", args, stdout.String())
		}
		assertContains(t, stderr.String(), "prompt required but --no-input was set")
		assertContains(t, stderr.String(), "Run 'mcbot help' for usage.")
	}
}

func TestSetupJSONRejected(t *testing.T) {
	for _, args := range [][]string{
		{"--json", "setup"},
		{"--json", "setup", "env"},
		{"--json", "setup", "config"},
		{"setup", "--json"},
		{"setup", "env", "--json"},
		{"setup", "config", "--json"},
	} {
		stdout, stderr, exitCode := runCLI(t, args...)
		if exitCode != ExitUsage {
			t.Fatalf("runCLI(%v) exit code = %d, want %d", args, exitCode, ExitUsage)
		}
		if stdout != "" {
			t.Fatalf("runCLI(%v) stdout = %q, want empty", args, stdout)
		}
		assertContains(t, stderr, "--json is not supported for setup")
	}
}

func TestSetupShowSecretsRejected(t *testing.T) {
	for _, args := range [][]string{
		{"--show-secrets", "setup"},
		{"--show-secrets", "setup", "env"},
		{"--show-secrets", "setup", "config"},
		{"setup", "--show-secrets"},
		{"setup", "env", "--show-secrets"},
		{"setup", "config", "--show-secrets"},
	} {
		stdout, stderr, exitCode := runCLI(t, args...)
		if exitCode != ExitUsage {
			t.Fatalf("runCLI(%v) exit code = %d, want %d", args, exitCode, ExitUsage)
		}
		if stdout != "" {
			t.Fatalf("runCLI(%v) stdout = %q, want empty", args, stdout)
		}
		assertContains(t, stderr, "--show-secrets is only supported")
	}
}

func TestServerCommandJSONOutput(t *testing.T) {
	server := &fakeServerRunner{status: serverops.Result{Service: "mc-server", Exists: true, Running: true, Status: "running"}}
	defer SetServerRunnerForTest(server)()

	stdout, stderr, exitCode := runCLI(t, "--json", "server", "status")
	if exitCode != ExitOK {
		t.Fatalf("exit code = %d, want %d", exitCode, ExitOK)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	var payload struct {
		Service string `json:"service"`
		Exists  bool   `json:"exists"`
		Running bool   `json:"running"`
		Status  string `json:"status"`
	}
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("invalid JSON %q: %v", stdout, err)
	}
	if payload.Service != "mc-server" || !payload.Exists || !payload.Running || payload.Status != "running" {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}

func TestServerCommandExitCode4OnDockerFailure(t *testing.T) {
	server := &fakeServerRunner{err: errors.New("docker inspect failed")}
	defer SetServerRunnerForTest(server)()

	stdout, stderr, exitCode := runCLI(t, "server", "status")
	if exitCode != ExitOperational {
		t.Fatalf("exit code = %d, want %d", exitCode, ExitOperational)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	assertContains(t, stderr, "docker inspect failed")
}

func TestConfigShowJSONOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mc-server.toml")
	if err := mcconfig.Init(path); err != nil {
		t.Fatalf("mcconfig.Init failed: %v", err)
	}
	defer SetConfigPathForTest(path)()

	stdout, stderr, exitCode := runCLI(t, "config", "show", "--json")
	if exitCode != ExitOK {
		t.Fatalf("exit code = %d, want %d; stderr=%q", exitCode, ExitOK, stderr)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	var payload mcconfig.Config
	if err := json.Unmarshal([]byte(stdout), &payload); err != nil {
		t.Fatalf("invalid JSON %q: %v", stdout, err)
	}
	if payload.Server.Type != "FORGE" || payload.Container.PortPublish != "25565:25565" {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}

func TestConfigGetOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mc-server.toml")
	if err := mcconfig.Init(path); err != nil {
		t.Fatalf("mcconfig.Init failed: %v", err)
	}
	defer SetConfigPathForTest(path)()

	stdout, stderr, exitCode := runCLI(t, "config", "get", "server.memory")
	if exitCode != ExitOK {
		t.Fatalf("exit code = %d, want %d; stderr=%q", exitCode, ExitOK, stderr)
	}
	if got, want := stdout, "14G\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}

	stdout, stderr, exitCode = runCLI(t, "--json", "config", "get", "container.uid")
	if exitCode != ExitOK {
		t.Fatalf("json exit code = %d, want %d; stderr=%q", exitCode, ExitOK, stderr)
	}
	assertContains(t, stdout, `"key":"container.uid"`)
	assertContains(t, stdout, `"value":1000`)
}

func TestConfigSetValidationFailureLeavesFileUnchanged(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mc-server.toml")
	if err := mcconfig.Init(path); err != nil {
		t.Fatalf("mcconfig.Init failed: %v", err)
	}
	defer SetConfigPathForTest(path)()
	before := readTestFile(t, path)

	stdout, stderr, exitCode := runCLI(t, "config", "set", "server.memory", "many")
	if exitCode != ExitValidation {
		t.Fatalf("exit code = %d, want %d", exitCode, ExitValidation)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	assertContains(t, stderr, "server.memory")
	if got := readTestFile(t, path); got != before {
		t.Fatalf("file changed on invalid set:\nbefore=%q\nafter=%q", before, got)
	}
}

func TestEnvShowAndGetMaskSecretsByDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("DISCORD_TOKEN=secret-token\nMC_CONTAINER_NAME=mc-server\n"), 0o600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	defer SetEnvPathForTest(path)()

	stdout, stderr, exitCode := runCLI(t, "env", "show")
	if exitCode != ExitOK {
		t.Fatalf("exit code = %d, want %d; stderr=%q", exitCode, ExitOK, stderr)
	}
	assertContains(t, stdout, "DISCORD_TOKEN=***MASKED***")
	assertContains(t, stdout, "MC_CONTAINER_NAME=mc-server")
	if strings.Contains(stdout, "secret-token") {
		t.Fatalf("stdout leaked secret: %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}

	stdout, stderr, exitCode = runCLI(t, "--json", "env", "get", "DISCORD_TOKEN")
	if exitCode != ExitOK {
		t.Fatalf("json get exit code = %d, want %d; stderr=%q", exitCode, ExitOK, stderr)
	}
	assertContains(t, stdout, `"value":"***MASKED***"`)
	if strings.Contains(stdout, "secret-token") {
		t.Fatalf("json stdout leaked secret: %q", stdout)
	}
}

func TestEnvShowSecretsAllowedOnlyForShowAndGet(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("DISCORD_TOKEN=secret-token\n"), 0o600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	defer SetEnvPathForTest(path)()

	stdout, stderr, exitCode := runCLI(t, "env", "get", "DISCORD_TOKEN", "--show-secrets")
	if exitCode != ExitOK {
		t.Fatalf("exit code = %d, want %d; stderr=%q", exitCode, ExitOK, stderr)
	}
	if got, want := stdout, "secret-token\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}

	stdout, stderr, exitCode = runCLI(t, "--show-secrets", "env", "validate")
	if exitCode != ExitUsage {
		t.Fatalf("exit code = %d, want %d", exitCode, ExitUsage)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	assertContains(t, stderr, "--show-secrets is only supported for env show and env get")
}

func TestEnvSetUnsetInitValidateCommands(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	defer SetEnvPathForTest(path)()

	stdout, stderr, exitCode := runCLI(t, "env", "init")
	if exitCode != ExitOK {
		t.Fatalf("init exit code = %d, want %d; stderr=%q", exitCode, ExitOK, stderr)
	}
	assertContains(t, stdout, "initialized")
	assertContains(t, readTestFile(t, path), "DISCORD_TOKEN=your_discord_bot_token_here")

	stdout, stderr, exitCode = runCLI(t, "env", "set", "MC_CONTAINER_NAME", "minecraft")
	if exitCode != ExitOK {
		t.Fatalf("set exit code = %d, want %d; stderr=%q", exitCode, ExitOK, stderr)
	}
	assertContains(t, stdout, "set MC_CONTAINER_NAME")
	assertContains(t, readTestFile(t, path), "MC_CONTAINER_NAME=minecraft")

	stdout, stderr, exitCode = runCLI(t, "env", "unset", "MC_CONTAINER_NAME")
	if exitCode != ExitOK {
		t.Fatalf("unset exit code = %d, want %d; stderr=%q", exitCode, ExitOK, stderr)
	}
	assertContains(t, stdout, "unset MC_CONTAINER_NAME")
	if strings.Contains(readTestFile(t, path), "MC_CONTAINER_NAME=minecraft") {
		t.Fatalf("unset did not remove key")
	}

	stdout, stderr, exitCode = runCLI(t, "--json", "env", "validate")
	if exitCode != ExitOK {
		t.Fatalf("validate exit code = %d, want %d; stderr=%q", exitCode, ExitOK, stderr)
	}
	var result envfile.ValidationResult
	if err := json.Unmarshal([]byte(stdout), &result); err != nil {
		t.Fatalf("invalid JSON %q: %v", stdout, err)
	}
	if !result.Valid || result.Path != path {
		t.Fatalf("unexpected validation result: %+v", result)
	}
}

func TestEnvSetUpdatesEffectiveDuplicateAssignment(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("MC_CONTAINER_NAME=old-first\n# keep\nMC_CONTAINER_NAME=old-last\n"), 0o600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	defer SetEnvPathForTest(path)()

	stdout, stderr, exitCode := runCLI(t, "env", "set", "MC_CONTAINER_NAME", "minecraft")
	if exitCode != ExitOK {
		t.Fatalf("set exit code = %d, want %d; stderr=%q", exitCode, ExitOK, stderr)
	}
	assertContains(t, stdout, "set MC_CONTAINER_NAME")

	stdout, stderr, exitCode = runCLI(t, "env", "get", "MC_CONTAINER_NAME")
	if exitCode != ExitOK {
		t.Fatalf("get exit code = %d, want %d; stderr=%q", exitCode, ExitOK, stderr)
	}
	if got, want := stdout, "minecraft\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}
	if got := readTestFile(t, path); got != "MC_CONTAINER_NAME=old-first\n# keep\nMC_CONTAINER_NAME=minecraft\n" {
		t.Fatalf("file contents = %q", got)
	}
}

func TestConfigAndEnvReadCommandsRequireExistingFiles(t *testing.T) {
	missingConfig := filepath.Join(t.TempDir(), "missing-mc-server.toml")
	defer SetConfigPathForTest(missingConfig)()

	for _, args := range [][]string{
		{"config", "show"},
		{"config", "get", "server.memory"},
	} {
		stdout, stderr, exitCode := runCLI(t, args...)
		if exitCode != ExitValidation {
			t.Fatalf("runCLI(%v) exit code = %d, want %d", args, exitCode, ExitValidation)
		}
		if stdout != "" {
			t.Fatalf("runCLI(%v) stdout = %q, want empty", args, stdout)
		}
		assertContains(t, stderr, "mc-server.toml does not exist")
		assertContains(t, stderr, missingConfig)
	}

	missingEnv := filepath.Join(t.TempDir(), "missing.env")
	defer SetEnvPathForTest(missingEnv)()

	for _, args := range [][]string{
		{"env", "show"},
		{"env", "get", "MC_CONTAINER_NAME"},
	} {
		stdout, stderr, exitCode := runCLI(t, args...)
		if exitCode != ExitValidation {
			t.Fatalf("runCLI(%v) exit code = %d, want %d", args, exitCode, ExitValidation)
		}
		if stdout != "" {
			t.Fatalf("runCLI(%v) stdout = %q, want empty", args, stdout)
		}
		assertContains(t, stderr, ".env does not exist")
		assertContains(t, stderr, missingEnv)
	}
}

func TestConfigAndEnvDefaultsResolveRepoRootFromGoSubdir(t *testing.T) {
	repoRoot := t.TempDir()
	goDir := filepath.Join(repoRoot, "mcbot")
	if err := os.MkdirAll(goDir, 0o755); err != nil {
		t.Fatalf("MkdirAll failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "docker-compose.yml"), []byte("services: {}\n"), 0o600); err != nil {
		t.Fatalf("write compose marker failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(goDir, "go.mod"), []byte("module test\n"), 0o600); err != nil {
		t.Fatalf("write go.mod marker failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "mc-server.toml"), []byte(mcconfig.Render(mcconfig.Defaults())), 0o600); err != nil {
		t.Fatalf("write root config failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, ".env"), []byte("MC_CONTAINER_NAME=root-mc\n"), 0o600); err != nil {
		t.Fatalf("write root env failed: %v", err)
	}

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}
	if err := os.Chdir(goDir); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	stdout, stderr, exitCode := runCLI(t, "config", "get", "server.memory")
	if exitCode != ExitOK {
		t.Fatalf("config get exit code = %d, want %d; stderr=%q", exitCode, ExitOK, stderr)
	}
	if got, want := stdout, "14G\n"; got != want {
		t.Fatalf("config stdout = %q, want %q", got, want)
	}

	stdout, stderr, exitCode = runCLI(t, "env", "get", "MC_CONTAINER_NAME")
	if exitCode != ExitOK {
		t.Fatalf("env get exit code = %d, want %d; stderr=%q", exitCode, ExitOK, stderr)
	}
	if got, want := stdout, "root-mc\n"; got != want {
		t.Fatalf("env stdout = %q, want %q", got, want)
	}
}

func TestInitExistingFilesRequireForce(t *testing.T) {
	configFile := filepath.Join(t.TempDir(), "mc-server.toml")
	if err := os.WriteFile(configFile, []byte("existing config\n"), 0o600); err != nil {
		t.Fatalf("write config failed: %v", err)
	}
	defer SetConfigPathForTest(configFile)()

	stdout, stderr, exitCode := runCLI(t, "config", "init")
	if exitCode != ExitUsage {
		t.Fatalf("config init exit code = %d, want %d", exitCode, ExitUsage)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	assertContains(t, stderr, "config init would overwrite")
	assertContains(t, stderr, "--force or --yes")
	if got := readTestFile(t, configFile); got != "existing config\n" {
		t.Fatalf("config init changed existing file: %q", got)
	}

	stdout, stderr, exitCode = runCLI(t, "--force", "config", "init")
	if exitCode != ExitOK {
		t.Fatalf("forced config init exit code = %d, want %d; stderr=%q", exitCode, ExitOK, stderr)
	}
	assertContains(t, stdout, "initialized")
	assertContains(t, readTestFile(t, configFile), "[server]")
	if err := os.WriteFile(configFile, []byte("existing config again\n"), 0o600); err != nil {
		t.Fatalf("rewrite config failed: %v", err)
	}
	stdout, stderr, exitCode = runCLI(t, "--yes", "config", "init")
	if exitCode != ExitOK {
		t.Fatalf("yes config init exit code = %d, want %d; stderr=%q", exitCode, ExitOK, stderr)
	}
	assertContains(t, stdout, "initialized")
	assertContains(t, readTestFile(t, configFile), "[server]")

	envFile := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(envFile, []byte("DISCORD_TOKEN=keep-me\n"), 0o600); err != nil {
		t.Fatalf("write env failed: %v", err)
	}
	defer SetEnvPathForTest(envFile)()

	stdout, stderr, exitCode = runCLI(t, "env", "init")
	if exitCode != ExitUsage {
		t.Fatalf("env init exit code = %d, want %d", exitCode, ExitUsage)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	assertContains(t, stderr, "env init would overwrite")
	assertContains(t, stderr, "--force or --yes")
	if got := readTestFile(t, envFile); got != "DISCORD_TOKEN=keep-me\n" {
		t.Fatalf("env init changed existing file: %q", got)
	}

	stdout, stderr, exitCode = runCLI(t, "--force", "env", "init")
	if exitCode != ExitOK {
		t.Fatalf("forced env init exit code = %d, want %d; stderr=%q", exitCode, ExitOK, stderr)
	}
	assertContains(t, stdout, "initialized")
	assertContains(t, readTestFile(t, envFile), "DISCORD_TOKEN=your_discord_bot_token_here")
	if err := os.WriteFile(envFile, []byte("DISCORD_TOKEN=keep-me-again\n"), 0o600); err != nil {
		t.Fatalf("rewrite env failed: %v", err)
	}
	stdout, stderr, exitCode = runCLI(t, "--yes", "env", "init")
	if exitCode != ExitOK {
		t.Fatalf("yes env init exit code = %d, want %d; stderr=%q", exitCode, ExitOK, stderr)
	}
	assertContains(t, stdout, "initialized")
	assertContains(t, readTestFile(t, envFile), "DISCORD_TOKEN=your_discord_bot_token_here")
}

func TestStdoutStderrSplit(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	out := NewOutput(&stdout, &stderr, Options{})

	if err := out.SuccessLine("server %s", "running"); err != nil {
		t.Fatalf("SuccessLine returned error: %v", err)
	}
	if err := out.SuccessJSON(map[string]string{"status": "running"}); err != nil {
		t.Fatalf("SuccessJSON returned error: %v", err)
	}
	if err := out.Diagnostic("bad %s", "flag"); err != nil {
		t.Fatalf("Diagnostic returned error: %v", err)
	}

	assertContains(t, stdout.String(), "server running\n")
	assertContains(t, stdout.String(), `"status":"running"`)
	if strings.Contains(stdout.String(), "bad flag") {
		t.Fatalf("stdout = %q, want no diagnostics", stdout.String())
	}
	if got, want := stderr.String(), "bad flag\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}

	cliStdout, cliStderr, exitCode := runCLI(t, "wat")
	if exitCode != ExitUsage {
		t.Fatalf("exit code = %d, want %d", exitCode, ExitUsage)
	}
	if cliStdout != "" {
		t.Fatalf("stdout = %q, want empty", cliStdout)
	}
	assertContains(t, cliStderr, `unknown command "wat"`)
}

func TestExitCodeMapping(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want int
	}{
		{name: "nil", err: nil, want: ExitOK},
		{name: "internal default", err: errors.New("boom"), want: ExitInternal},
		{name: "internal", err: InternalError("boom"), want: ExitInternal},
		{name: "usage", err: UsageError("bad flag"), want: ExitUsage},
		{name: "validation", err: ValidationError("invalid file"), want: ExitValidation},
		{name: "docker compose", err: OperationalError("docker compose failed"), want: ExitOperational},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ExitCodeForError(tc.err); got != tc.want {
				t.Fatalf("ExitCodeForError(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}

	if ExitOperational != 4 {
		t.Fatalf("ExitOperational = %d, want Docker/Compose failures to map to 4", ExitOperational)
	}
}

func TestQuietMode(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	out := NewOutput(&stdout, &stderr, Options{Quiet: true})

	if err := out.SuccessLine("created"); err != nil {
		t.Fatalf("SuccessLine returned error: %v", err)
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("stdout = %q, want quiet plaintext success suppressed", got)
	}

	if err := out.SuccessJSON(map[string]bool{"ok": true}); err != nil {
		t.Fatalf("SuccessJSON returned error: %v", err)
	}
	assertContains(t, stdout.String(), `"ok":true`)

	if err := out.Diagnostic("still visible"); err != nil {
		t.Fatalf("Diagnostic returned error: %v", err)
	}
	if got, want := stderr.String(), "still visible\n"; got != want {
		t.Fatalf("stderr = %q, want %q", got, want)
	}
}

func TestConfirmationRules(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{},
		{"help"},
		{"version"},
		{"server", "status"},
		{"config", "show"},
		{"config", "get"},
		{"config", "validate"},
		{"env", "show"},
		{"env", "get"},
		{"env", "validate"},
	} {
		if !NeverPrompts(args) {
			t.Fatalf("NeverPrompts(%v) = false, want true", args)
		}
	}

	for _, args := range [][]string{
		{"config", "set"},
		{"config", "init"},
		{"env", "set"},
		{"env", "unset"},
		{"env", "init"},
		{"setup"},
		{"setup", "env"},
		{"setup", "config"},
	} {
		if NeverPrompts(args) {
			t.Fatalf("NeverPrompts(%v) = true, want false", args)
		}
	}

	confirmed, err := ConfirmPrompt(Options{Yes: true}, true)
	if err != nil || !confirmed {
		t.Fatalf("ConfirmPrompt(--yes, required) = (%v, %v), want (true, nil)", confirmed, err)
	}

	confirmed, err = ConfirmPrompt(Options{Force: true}, true)
	if err != nil || !confirmed {
		t.Fatalf("ConfirmPrompt(--force, required) = (%v, %v), want (true, nil)", confirmed, err)
	}

	confirmed, err = ConfirmPrompt(Options{Force: true, NoInput: true}, true)
	if err == nil || confirmed {
		t.Fatalf("ConfirmPrompt(--force --no-input, required) = (%v, %v), want (false, error)", confirmed, err)
	}

	confirmed, err = ConfirmPrompt(Options{Yes: true, NoInput: true}, true)
	if err != nil || !confirmed {
		t.Fatalf("ConfirmPrompt(--yes --no-input, required) = (%v, %v), want (true, nil)", confirmed, err)
	}

	confirmed, err = ConfirmPrompt(Options{}, true)
	if err != nil || confirmed {
		t.Fatalf("ConfirmPrompt(interactive, required) = (%v, %v), want (false, nil)", confirmed, err)
	}
}

func TestNoInputMode(t *testing.T) {
	confirmed, err := ConfirmPrompt(Options{NoInput: true}, true)
	if err == nil {
		t.Fatal("ConfirmPrompt(--no-input, required) error = nil, want usage error")
	}
	if confirmed {
		t.Fatal("ConfirmPrompt(--no-input, required) confirmed = true, want false")
	}
	if got := ExitCodeForError(err); got != ExitUsage {
		t.Fatalf("ExitCodeForError(prompt error) = %d, want %d", got, ExitUsage)
	}

	confirmed, err = ConfirmPrompt(Options{NoInput: true}, false)
	if err != nil || !confirmed {
		t.Fatalf("ConfirmPrompt(--no-input, not required) = (%v, %v), want (true, nil)", confirmed, err)
	}

	path := filepath.Join(t.TempDir(), "mc-server.toml")
	if err := mcconfig.Init(path); err != nil {
		t.Fatalf("mcconfig.Init failed: %v", err)
	}
	defer SetConfigPathForTest(path)()

	stdout, stderr, exitCode := runCLI(t, "--no-input", "config", "show")
	if exitCode != ExitOK {
		t.Fatalf("exit code = %d, want %d", exitCode, ExitOK)
	}
	assertContains(t, stdout, "[server]")
	if stderr != "" {
		t.Fatalf("stderr = %q, want empty", stderr)
	}

	stdout, stderr, exitCode = runCLI(t, "--no-input", "env", "unset")
	if exitCode != ExitUsage {
		t.Fatalf("exit code = %d, want %d", exitCode, ExitUsage)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	assertContains(t, stderr, "env unset requires exactly one key")
}

func TestForceFlagOnlySupportedForInitAndSetup(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{{"config", "init"}, {"env", "init"}, {"setup"}, {"setup", "env"}, {"setup", "config"}} {
		if !SupportsForce(args) {
			t.Fatalf("SupportsForce(%v) = false, want true", args)
		}
	}

	for _, args := range [][]string{{"server", "status"}, {"config", "set"}, {"env", "unset"}} {
		if SupportsForce(args) {
			t.Fatalf("SupportsForce(%v) = true, want false", args)
		}
	}

	stdout, stderr, exitCode := runCLI(t, "--force", "config", "set", "server.memory", "2G")
	if exitCode != ExitUsage {
		t.Fatalf("exit code = %d, want %d", exitCode, ExitUsage)
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty", stdout)
	}
	assertContains(t, stderr, "--force is only supported for config init, env init, and setup")
}

func runCLI(t *testing.T, args ...string) (string, string, int) {
	t.Helper()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := Run(args, &stdout, &stderr)
	return stdout.String(), stderr.String(), exitCode
}

func runCLIWithInput(t *testing.T, input string, args ...string) (string, string, int) {
	t.Helper()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := runWithIO(args, strings.NewReader(input), &stdout, &stderr)
	return stdout.String(), stderr.String(), exitCode
}

func setupInputLines(values ...string) string {
	return strings.Join(values, "\n") + "\n"
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s failed: %v", path, err)
	}
	return string(data)
}

func assertFileDoesNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("%s exists, want no file mutation", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat %s failed: %v", path, err)
	}
}

func assertContains(t *testing.T, got, want string) {
	t.Helper()

	if !strings.Contains(got, want) {
		t.Fatalf("expected %q to contain %q", got, want)
	}
}

func assertNotContains(t *testing.T, got, want string) {
	t.Helper()

	if strings.Contains(got, want) {
		t.Fatalf("expected %q not to contain %q", got, want)
	}
}

func assertCommandTreeContains(t *testing.T, tree []Command, commandName string, subcommandNames ...string) {
	t.Helper()

	for _, command := range tree {
		if command.Name != commandName {
			continue
		}
		for _, want := range subcommandNames {
			if !hasSubcommand(command, want) {
				t.Fatalf("CommandTree()[%q] missing subcommand %q", commandName, want)
			}
		}
		return
	}

	t.Fatalf("CommandTree() missing command %q", commandName)
}

func hasSubcommand(command Command, name string) bool {
	for _, subcommand := range command.Subcommands {
		if subcommand.Name == name {
			return true
		}
	}
	return false
}

type fakeServerRunner struct {
	start  serverops.Result
	stop   serverops.Result
	status serverops.Result
	err    error
}

func (r *fakeServerRunner) Start(_ context.Context) (serverops.Result, error) {
	return r.start, r.err
}

func (r *fakeServerRunner) Stop(_ context.Context) (serverops.Result, error) {
	return r.stop, r.err
}

func (r *fakeServerRunner) Status(_ context.Context) (serverops.Result, error) {
	return r.status, r.err
}
