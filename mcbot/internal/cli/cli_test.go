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
	} {
		assertContains(t, stdout, want)
	}

	tree := CommandTree()
	assertCommandTreeContains(t, tree, "bot", "run")
	assertCommandTreeContains(t, tree, "server", "start", "stop", "status")
	assertCommandTreeContains(t, tree, "config", "show", "get", "set", "init", "validate")
	assertCommandTreeContains(t, tree, "env", "show", "get", "set", "unset", "init", "validate")
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
	assertContains(t, stdout, `"value":1001`)
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

func TestForceFlagOnlySupportedForConfigAndEnvInit(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{{"config", "init"}, {"env", "init"}} {
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
	assertContains(t, stderr, "--force is only supported for config init and env init")
}

func runCLI(t *testing.T, args ...string) (string, string, int) {
	t.Helper()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := Run(args, &stdout, &stderr)
	return stdout.String(), stderr.String(), exitCode
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s failed: %v", path, err)
	}
	return string(data)
}

func assertContains(t *testing.T, got, want string) {
	t.Helper()

	if !strings.Contains(got, want) {
		t.Fatalf("expected %q to contain %q", got, want)
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
