//go:build integration

package mcserver

import (
	"strings"
	"testing"
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
