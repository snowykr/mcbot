package serverops

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOperationalSourceOwnership(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		key     string
		domain  string
		wantErr bool
	}{
		{name: "env owns discord token", key: "DISCORD_TOKEN", domain: "env"},
		{name: "env owns container name", key: "MC_CONTAINER_NAME", domain: "env"},
		{name: "env owns rcon password", key: "RCON_PASSWORD", domain: "env"},
		{name: "env rejects toml server key", key: "server.version", domain: "env", wantErr: true},
		{name: "config owns server version", key: "server.version", domain: "config"},
		{name: "config owns container port publish", key: "container.port_publish", domain: "config"},
		{name: "config rejects discord token", key: "DISCORD_TOKEN", domain: "config", wantErr: true},
		{name: "config rejects rcon password secret", key: "RCON_PASSWORD", domain: "config", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var err error
			switch tt.domain {
			case "env":
				err = ValidateEnvKey(tt.key)
			case "config":
				err = ValidateConfigKey(tt.key)
			default:
				t.Fatalf("unknown domain %q", tt.domain)
			}
			if tt.wantErr && err == nil {
				t.Fatalf("Validate%sKey(%q) succeeded, want error", tt.domain, tt.key)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("Validate%sKey(%q) error = %v", tt.domain, tt.key, err)
			}
		})
	}
}

func TestServerCommandsDoNotRequireDiscordToken(t *testing.T) {
	t.Setenv("DISCORD_TOKEN", "")

	if ServerCommandsRequireDiscordToken() {
		t.Fatal("server commands unexpectedly require DISCORD_TOKEN")
	}

	dir := t.TempDir()
	cfg, err := LoadSources(Sources{
		EnvFile:    filepath.Join(dir, ".env"),
		ConfigFile: filepath.Join(dir, "mc-server.toml"),
	})
	if err != nil {
		t.Fatalf("LoadSources without Discord env failed: %v", err)
	}
	if got := cfg.MCConfig.Server.Version; got != "1.20.1" {
		t.Fatalf("default server version = %q, want 1.20.1", got)
	}
}

func TestComposeEnvBridgeUsesFileValues(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	configPath := filepath.Join(dir, "mc-server.toml")

	writeFile(t, envPath, strings.Join([]string{
		"DISCORD_TOKEN=not-required-by-serverops",
		"MC_CONTAINER_NAME=snowy-mc",
		"RCON_PASSWORD=file-secret",
		"VERSION=legacy-env-version",
		"MEMORY=legacy-env-memory",
		"MC_SERVER_PORT_PUBLISH=9999:25565",
	}, "\n")+"\n")
	writeFile(t, configPath, strings.Join([]string{
		"[server]",
		`version = "1.21.4"`,
		`type = "PAPER"`,
		`difficulty = "hard"`,
		`memory = "8G"`,
		`init_memory = "4G"`,
		`motd = "Bridge MOTD"`,
		"view_distance = 10",
		"simulation_distance = 6",
		"",
		"[container]",
		`restart_policy = "unless-stopped"`,
		`port_publish = "25570:25565"`,
		"uid = 1234",
		"gid = 5678",
	}, "\n")+"\n")

	cfg, err := LoadSources(Sources{EnvFile: envPath, ConfigFile: configPath})
	if err != nil {
		t.Fatalf("LoadSources failed: %v", err)
	}
	env := ComposeEnvironment(cfg)

	assertEnv(t, env, "MC_CONTAINER_NAME", "snowy-mc")
	assertEnv(t, env, "RCON_PASSWORD", "file-secret")
	assertEnv(t, env, "VERSION", "1.21.4")
	assertEnv(t, env, "TYPE", "PAPER")
	assertEnv(t, env, "DIFFICULTY", "hard")
	assertEnv(t, env, "MEMORY", "8G")
	assertEnv(t, env, "INIT_MEMORY", "4G")
	assertEnv(t, env, "MOTD", "Bridge MOTD")
	assertEnv(t, env, "VIEW_DISTANCE", "10")
	assertEnv(t, env, "SIMULATION_DISTANCE", "6")
	assertEnv(t, env, "MC_SERVER_RESTART_POLICY", "unless-stopped")
	assertEnv(t, env, "MC_SERVER_PORT_PUBLISH", "25570:25565")
	assertEnv(t, env, "UID", "1234")
	assertEnv(t, env, "GID", "5678")
}

func TestMissingFilesFallBackToDefaults(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	cfg, err := LoadSources(Sources{
		EnvFile:    filepath.Join(dir, ".env"),
		ConfigFile: filepath.Join(dir, "mc-server.toml"),
	})
	if err != nil {
		t.Fatalf("LoadSources failed: %v", err)
	}
	env := ComposeEnvironment(cfg)
	assertEnv(t, env, "VERSION", "1.20.1")
	assertEnv(t, env, "TYPE", "FORGE")
	assertEnv(t, env, "MC_SERVER_PORT_PUBLISH", "25565:25565")
}

func TestRejectsMcServerOwnedKeysInEnvDomain(t *testing.T) {
	t.Parallel()

	if err := ValidateEnvKey("server.memory"); err == nil {
		t.Fatal("ValidateEnvKey(server.memory) succeeded, want ownership error")
	}
}

func TestRejectsSecretsInMcServerTomlDomain(t *testing.T) {
	t.Parallel()

	if err := ValidateConfigKey("RCON_PASSWORD"); err == nil {
		t.Fatal("ValidateConfigKey(RCON_PASSWORD) succeeded, want ownership error")
	}
}

func writeFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write %s failed: %v", path, err)
	}
}

func assertEnv(t *testing.T, env map[string]string, key, want string) {
	t.Helper()
	if got := env[key]; got != want {
		t.Fatalf("%s = %q, want %q", key, got, want)
	}
}
