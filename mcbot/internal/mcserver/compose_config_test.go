//go:build integration

package mcserver

import "testing"

func TestComposeConfig_Defaults(t *testing.T) {
	t.Parallel()

	cfg := renderComposeConfig(t, nil)
	mcServer := requireComposeService(t, cfg, "mc-server")

	if mcServer.ContainerName != "mc-server" {
		t.Fatalf("container_name = %q, want %q", mcServer.ContainerName, "mc-server")
	}
	if mcServer.Restart != "no" {
		t.Fatalf("restart = %q, want %q", mcServer.Restart, "no")
	}
	if !mcServer.Init {
		t.Fatal("init = false, want true")
	}
	if len(mcServer.Ports) != 1 {
		t.Fatalf("ports length = %d, want 1", len(mcServer.Ports))
	}

	port := mcServer.Ports[0]
	if port.Published != "25565" {
		t.Fatalf("published port = %q, want %q", port.Published, "25565")
	}
	if port.Target != 25565 {
		t.Fatalf("target port = %d, want 25565", port.Target)
	}
	if port.Protocol != "tcp" {
		t.Fatalf("protocol = %q, want %q", port.Protocol, "tcp")
	}
	if port.Mode != "ingress" {
		t.Fatalf("mode = %q, want %q", port.Mode, "ingress")
	}

	assertComposeEnvValue(t, mcServer.Environment, "EULA", "TRUE")
	assertComposeEnvValue(t, mcServer.Environment, "UID", "1000")
	assertComposeEnvValue(t, mcServer.Environment, "GID", "1000")
	assertComposeEnvValue(t, mcServer.Environment, "VERSION", "1.20.1")
	assertComposeEnvValue(t, mcServer.Environment, "TYPE", "FORGE")
	assertComposeEnvValue(t, mcServer.Environment, "DIFFICULTY", "easy")
	assertComposeEnvValue(t, mcServer.Environment, "MEMORY", "14G")
	assertComposeEnvValue(t, mcServer.Environment, "INIT_MEMORY", "14G")
	assertComposeEnvValue(t, mcServer.Environment, "MOTD", "SNOWY'S SERVER")
	assertComposeEnvValue(t, mcServer.Environment, "VIEW_DISTANCE", "8")
	assertComposeEnvValue(t, mcServer.Environment, "SIMULATION_DISTANCE", "8")
	assertComposeEnvValue(t, mcServer.Environment, "ENABLE_RCON", "true")
	assertComposeEnvValue(t, mcServer.Environment, "USE_AIKAR_FLAGS", "true")
	assertComposeEnvNil(t, mcServer.Environment, "RCON_PASSWORD")
	assertComposeEnvNil(t, mcServer.Environment, "RCON_CMDS_STARTUP")

	if len(mcServer.EnvFile) != 0 {
		t.Fatalf("env_file length = %d, want 0", len(mcServer.EnvFile))
	}
	if len(mcServer.Volumes) != 1 {
		t.Fatalf("volumes length = %d, want 1", len(mcServer.Volumes))
	}

	volume := mcServer.Volumes[0]
	if volume.Type != "bind" {
		t.Fatalf("volume type = %q, want %q", volume.Type, "bind")
	}
	if volume.Source == "" {
		t.Fatal("volume source is empty")
	}
	if volume.Target != "/data" {
		t.Fatalf("volume target = %q, want %q", volume.Target, "/data")
	}
}

func TestComposeConfig_Overrides(t *testing.T) {
	t.Parallel()

	cfg := renderComposeConfig(t, map[string]string{
		"MC_CONTAINER_NAME":        "snowy-mc",
		"MC_SERVER_RESTART_POLICY": "always",
		"MC_SERVER_PORT_PUBLISH":   "25570:25565",
		"UID":                      "1234",
		"GID":                      "5678",
		"TYPE":                     "PAPER",
		"ENABLE_RCON":              "false",
	})
	mcServer := requireComposeService(t, cfg, "mc-server")

	if mcServer.ContainerName != "snowy-mc" {
		t.Fatalf("container_name = %q, want %q", mcServer.ContainerName, "snowy-mc")
	}
	if mcServer.Restart != "always" {
		t.Fatalf("restart = %q, want %q", mcServer.Restart, "always")
	}
	if len(mcServer.Ports) != 1 {
		t.Fatalf("ports length = %d, want 1", len(mcServer.Ports))
	}

	port := mcServer.Ports[0]
	if port.Published != "25570" {
		t.Fatalf("published port = %q, want %q", port.Published, "25570")
	}
	if port.Target != 25565 {
		t.Fatalf("target port = %d, want 25565", port.Target)
	}

	assertComposeEnvValue(t, mcServer.Environment, "UID", "1234")
	assertComposeEnvValue(t, mcServer.Environment, "GID", "5678")
	assertComposeEnvValue(t, mcServer.Environment, "TYPE", "PAPER")
	assertComposeEnvValue(t, mcServer.Environment, "ENABLE_RCON", "false")
}

func TestComposeConfig_RCONStartupPassThrough(t *testing.T) {
	t.Parallel()

	t.Run("unset", func(t *testing.T) {
		t.Parallel()

		cfg := renderComposeConfig(t, nil)
		mcServer := requireComposeService(t, cfg, "mc-server")

		assertComposeEnvNil(t, mcServer.Environment, "RCON_PASSWORD")
		assertComposeEnvNil(t, mcServer.Environment, "RCON_CMDS_STARTUP")
	})

	t.Run("set", func(t *testing.T) {
		t.Parallel()

		cfg := renderComposeConfig(t, map[string]string{
			"RCON_PASSWORD":     "super-secret",
			"RCON_CMDS_STARTUP": "/gamerule keepInventory true",
		})
		mcServer := requireComposeService(t, cfg, "mc-server")

		assertComposeEnvValue(t, mcServer.Environment, "RCON_PASSWORD", "super-secret")
		assertComposeEnvValue(t, mcServer.Environment, "RCON_CMDS_STARTUP", "/gamerule keepInventory true")
	})
}

func requireComposeService(t *testing.T, cfg composeConfig, serviceName string) composeService {
	t.Helper()

	service, ok := cfg.Services[serviceName]
	if !ok {
		t.Fatalf("%s service missing from compose config", serviceName)
	}

	return service
}

func assertComposeEnvValue(t *testing.T, env map[string]any, key string, want string) {
	t.Helper()

	got, ok := env[key]
	if !ok {
		t.Fatalf("%s missing from environment", key)
	}
	if got != want {
		t.Fatalf("%s = %#v, want %q", key, got, want)
	}
}

func assertComposeEnvNil(t *testing.T, env map[string]any, key string) {
	t.Helper()

	got, ok := env[key]
	if !ok {
		t.Fatalf("%s missing from environment", key)
	}
	if got != nil {
		t.Fatalf("%s = %#v, want nil", key, got)
	}
}
