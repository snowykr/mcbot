//go:build integration

package mcserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

var composeConfigScrubbedEnvKeys = []string{
	"COMPOSE_FILE",
	"COMPOSE_PATH_SEPARATOR",
	"COMPOSE_PROJECT_NAME",
	"COMPOSE_PROFILES",
	"COMPOSE_ENV_FILES",
	"COMPOSE_DISABLE_ENV_FILE",
	"COMPOSE_IGNORE_ORPHANS",
	"COMPOSE_MENU",
	"COMPOSE_CONVERT_WINDOWS_PATHS",
	"MC_CONTAINER_NAME",
	"MCBOT_ROLE_NAME",
	"READY_TIMEOUT_SECONDS",
	"STOP_TIMEOUT_SECONDS",
	"MC_SERVER_RESTART_POLICY",
	"MC_SERVER_PORT_PUBLISH",
	"UID",
	"GID",
	"VERSION",
	"TYPE",
	"DIFFICULTY",
	"MEMORY",
	"INIT_MEMORY",
	"MOTD",
	"VIEW_DISTANCE",
	"SIMULATION_DISTANCE",
	"ENABLE_RCON",
	"RCON_PASSWORD",
	"RCON_CMDS_STARTUP",
}

type composeConfig struct {
	Name     string                    `json:"name"`
	Services map[string]composeService `json:"services"`
}

type composeService struct {
	ContainerName string                 `json:"container_name"`
	Init          bool                   `json:"init"`
	Environment   map[string]any         `json:"environment"`
	Ports         []composePublishedPort `json:"ports"`
	Restart       string                 `json:"restart"`
	EnvFile       []composeEnvFile       `json:"env_file"`
	Volumes       []composeVolume        `json:"volumes"`
}

type composePublishedPort struct {
	Published string `json:"published"`
	Target    int    `json:"target"`
	Protocol  string `json:"protocol"`
	Mode      string `json:"mode"`
}

type composeEnvFile struct {
	Path string `json:"path"`
}

type composeVolume struct {
	Type   string `json:"type"`
	Source string `json:"source"`
	Target string `json:"target"`
}

func renderComposeConfig(t *testing.T, overrides map[string]string) composeConfig {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	envFilePath := composeConfigEnvFile(t, "")

	cmd := exec.CommandContext(
		ctx,
		"docker",
		"compose",
		"--env-file", envFilePath,
		"-f", "docker-compose.yml",
		"config",
		"--no-env-resolution",
		"--format", "json",
	)
	cmd.Dir = composeConfigRepoRoot(t)
	cmd.Env = scrubComposeConfigEnv(os.Environ(), overrides)

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docker compose config failed: %v\nOutput: %s", err, string(output))
	}

	var cfg composeConfig
	if err := json.Unmarshal(output, &cfg); err != nil {
		t.Fatalf("unmarshal compose config json failed: %v\nOutput: %s", err, string(output))
	}

	return cfg
}

func renderComposeConfigWithProjectEnv(t *testing.T, envFileContents string) composeConfig {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	projectDir := composeConfigTempProject(t)
	if err := os.WriteFile(filepath.Join(projectDir, ".env"), []byte(envFileContents), 0o600); err != nil {
		t.Fatalf("write temp .env failed: %v", err)
	}

	cmd := exec.CommandContext(
		ctx,
		"docker",
		"compose",
		"-f", "docker-compose.yml",
		"config",
		"--format", "json",
	)
	cmd.Dir = projectDir
	cmd.Env = scrubComposeConfigEnv(os.Environ(), nil)

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docker compose config failed: %v\nOutput: %s", err, string(output))
	}

	var cfg composeConfig
	if err := json.Unmarshal(output, &cfg); err != nil {
		t.Fatalf("unmarshal compose config json failed: %v\nOutput: %s", err, string(output))
	}

	return cfg
}

func renderComposeConfigWithEnvFile(t *testing.T, envFileContents string) composeConfig {
	t.Helper()

	projectDir := composeConfigTempProject(t)
	return renderComposeConfigFromProjectWithEnvFile(t, projectDir, envFileContents)
}

func renderComposeConfigWithProjectAndExplicitEnvFiles(t *testing.T, projectEnvContents, explicitEnvFileContents string) composeConfig {
	t.Helper()

	projectDir := composeConfigTempProject(t)
	if err := os.WriteFile(filepath.Join(projectDir, ".env"), []byte(projectEnvContents), 0o600); err != nil {
		t.Fatalf("write temp .env failed: %v", err)
	}

	return renderComposeConfigFromProjectWithEnvFile(t, projectDir, explicitEnvFileContents)
}

func renderComposeConfigWithEnvFileMissingProjectEnvErr(t *testing.T, envFileContents string) error {
	t.Helper()

	projectDir := composeConfigTempProjectWithoutEnv(t)
	envFilePath := composeConfigEnvFile(t, envFileContents)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(
		ctx,
		"docker",
		"compose",
		"--env-file", envFilePath,
		"-f", "docker-compose.yml",
		"config",
		"--format", "json",
	)
	cmd.Dir = projectDir
	cmd.Env = scrubComposeConfigEnv(os.Environ(), nil)

	output, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}

	return fmt.Errorf("docker compose config failed: %w: %s", err, strings.TrimSpace(string(output)))
}

func renderComposeConfigFromProjectWithEnvFile(t *testing.T, projectDir, envFileContents string) composeConfig {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	envFilePath := composeConfigEnvFile(t, envFileContents)

	cmd := exec.CommandContext(
		ctx,
		"docker",
		"compose",
		"--env-file", envFilePath,
		"-f", "docker-compose.yml",
		"config",
		"--format", "json",
	)
	cmd.Dir = projectDir
	cmd.Env = scrubComposeConfigEnv(os.Environ(), nil)

	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docker compose config failed: %v\nOutput: %s", err, string(output))
	}

	var cfg composeConfig
	if err := json.Unmarshal(output, &cfg); err != nil {
		t.Fatalf("unmarshal compose config json failed: %v\nOutput: %s", err, string(output))
	}

	return cfg
}

func renderComposeConfigErr(t *testing.T, overrides map[string]string) error {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	envFilePath := composeConfigEnvFile(t, "")

	cmd := exec.CommandContext(
		ctx,
		"docker",
		"compose",
		"--env-file", envFilePath,
		"-f", "docker-compose.yml",
		"config",
		"--no-env-resolution",
		"--format", "json",
	)
	cmd.Dir = composeConfigRepoRoot(t)
	cmd.Env = scrubComposeConfigEnv(os.Environ(), overrides)

	output, err := cmd.CombinedOutput()
	if err == nil {
		return fmt.Errorf("expected docker compose config to fail")
	}

	return fmt.Errorf("docker compose config failed: %w: %s", err, strings.TrimSpace(string(output)))
}

func composeConfigRepoRoot(t *testing.T) string {
	t.Helper()

	_, filePath, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve compose helper file path failed")
	}

	return filepath.Clean(filepath.Join(filepath.Dir(filePath), "..", "..", ".."))
}

func composeConfigTempProject(t *testing.T) string {
	t.Helper()

	projectDir := composeConfigTempProjectWithoutEnv(t)
	if err := os.WriteFile(filepath.Join(projectDir, ".env"), []byte{}, 0o600); err != nil {
		t.Fatalf("write temp .env failed: %v", err)
	}

	return projectDir
}

func composeConfigTempProjectWithoutEnv(t *testing.T) string {
	t.Helper()

	projectDir := t.TempDir()

	composeFile, err := os.ReadFile(filepath.Join(composeConfigRepoRoot(t), "docker-compose.yml"))
	if err != nil {
		t.Fatalf("read docker-compose.yml failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "docker-compose.yml"), composeFile, 0o600); err != nil {
		t.Fatalf("write temp docker-compose.yml failed: %v", err)
	}
	buildContextDir := filepath.Join(projectDir, "mcbot")
	if err := os.Mkdir(buildContextDir, 0o700); err != nil {
		t.Fatalf("create temp mcbot build context failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(buildContextDir, "Dockerfile"), []byte("FROM scratch\n"), 0o600); err != nil {
		t.Fatalf("write temp mcbot Dockerfile failed: %v", err)
	}

	return projectDir
}

func composeConfigEnvFile(t *testing.T, contents string) string {
	t.Helper()

	envFilePath := filepath.Join(t.TempDir(), "compose.env")
	if err := os.WriteFile(envFilePath, []byte(contents), 0o600); err != nil {
		t.Fatalf("create compose env file failed: %v", err)
	}

	return envFilePath
}

func scrubComposeConfigEnv(baseEnv []string, overrides map[string]string) []string {
	scrubbedKeys := make(map[string]struct{}, len(composeConfigScrubbedEnvKeys))
	for _, key := range composeConfigScrubbedEnvKeys {
		scrubbedKeys[key] = struct{}{}
	}

	env := make([]string, 0, len(baseEnv)+len(overrides))
	for _, entry := range baseEnv {
		key, _, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		if _, shouldScrub := scrubbedKeys[key]; shouldScrub {
			continue
		}
		env = append(env, entry)
	}

	for key, value := range overrides {
		env = append(env, key+"="+value)
	}

	return env
}

func TestComposeConfigHelper_RendersDefaults(t *testing.T) {
	t.Setenv("MC_CONTAINER_NAME", "leaked-name")
	t.Setenv("MC_SERVER_PORT_PUBLISH", "9999:25565")
	t.Setenv("RCON_PASSWORD", "leaked-rcon-password")
	t.Setenv("RCON_CMDS_STARTUP", "/say leaked startup command")

	cfg := renderComposeConfig(t, nil)

	mcServer, ok := cfg.Services["mc-server"]
	if !ok {
		t.Fatalf("mc-server service missing from compose config")
	}

	if mcServer.ContainerName != "mc-server" {
		t.Fatalf("container_name = %q, want %q", mcServer.ContainerName, "mc-server")
	}

	if !mcServer.Init {
		t.Fatal("init = false, want true")
	}

	if len(mcServer.Ports) != 1 {
		t.Fatalf("ports length = %d, want 1", len(mcServer.Ports))
	}

	if mcServer.Ports[0].Published != "25565" {
		t.Fatalf("published port = %q, want %q", mcServer.Ports[0].Published, "25565")
	}

	if mcServer.Ports[0].Target != 25565 {
		t.Fatalf("target port = %d, want 25565", mcServer.Ports[0].Target)
	}

	if got := mcServer.Environment["VERSION"]; got != "1.20.1" {
		t.Fatalf("VERSION = %#v, want %q", got, "1.20.1")
	}

	if got := mcServer.Environment["RCON_PASSWORD"]; got != nil {
		t.Fatalf("RCON_PASSWORD = %#v, want nil", got)
	}

	if got := mcServer.Environment["RCON_CMDS_STARTUP"]; got != nil {
		t.Fatalf("RCON_CMDS_STARTUP = %#v, want nil", got)
	}
}

func TestComposeConfigHelper_InvalidPublishStringFails(t *testing.T) {
	err := renderComposeConfigErr(t, map[string]string{
		"MC_SERVER_PORT_PUBLISH": "not-a-port",
	})
	if err == nil {
		t.Fatalf("expected compose render error")
	}

	message := err.Error()
	if !strings.Contains(message, "MC_SERVER_PORT_PUBLISH") && !strings.Contains(message, "published") && !strings.Contains(message, "port") {
		t.Fatalf("unexpected error message: %v", err)
	}
}
