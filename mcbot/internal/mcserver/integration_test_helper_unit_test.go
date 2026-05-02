//go:build integration

package mcserver

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestIntegrationTestHelper_RenderComposeEnvOverride(t *testing.T) {
	got := renderComposeEnvOverride(map[string]string{
		"RCON_CMDS_STARTUP": "gamerule keepInventory true",
		"RCON_PASSWORD":     "test",
	})

	want := `services:
  mc-test:
    environment:
      RCON_CMDS_STARTUP: "gamerule keepInventory true"
      RCON_PASSWORD: "test"
`
	if got != want {
		t.Fatalf("override compose mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestIntegrationTestHelper_WithoutEnvScrubsRCONStartup(t *testing.T) {
	got := withoutEnv(
		[]string{
			"PATH=/usr/bin",
			"RCON_CMDS_STARTUP=say leaked",
			"HOME=/tmp",
		},
		"RCON_CMDS_STARTUP",
	)

	want := []string{"PATH=/usr/bin", "HOME=/tmp"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("filtered env = %#v, want %#v", got, want)
	}
}

func TestIntegrationTestHelper_DockerLogsSinceArgsPreservesSubsecondPrecision(t *testing.T) {
	since := time.Date(2026, time.May, 2, 10, 25, 18, 123456789, time.FixedZone("KST", 9*60*60))

	got := dockerLogsSinceArgs("container-123", since)
	want := []string{"logs", "--tail", "50", "--since", "2026-05-02T01:25:18.123456789Z", "container-123"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("docker logs args = %#v, want %#v", got, want)
	}
}

func TestIntegrationTestHelper_DockerLogsSinceArgsOmitsZeroSince(t *testing.T) {
	got := dockerLogsSinceArgs("container-123", time.Time{})
	want := []string{"logs", "--tail", "50", "container-123"}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("docker logs args = %#v, want %#v", got, want)
	}
}
