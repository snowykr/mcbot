package envfile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestShowMasksSecrets(t *testing.T) {
	path := writeEnv(t, "DISCORD_TOKEN=super-secret\nRCON_PASSWORD=also-secret\nMC_CONTAINER_NAME=mc-server\n")

	entries, err := Show(path, RevealPolicy{})
	if err != nil {
		t.Fatalf("Show failed: %v", err)
	}
	rendered := Render(entries)
	assertContains(t, rendered, "DISCORD_TOKEN=***MASKED***")
	assertContains(t, rendered, "RCON_PASSWORD=***MASKED***")
	assertContains(t, rendered, "MC_CONTAINER_NAME=mc-server")
	assertNotContains(t, rendered, "super-secret")
	assertNotContains(t, rendered, "also-secret")

	data, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	assertContains(t, string(data), "***MASKED***")
	assertNotContains(t, string(data), "super-secret")
}

func TestSetPreservesComments(t *testing.T) {
	path := writeEnv(t, "# top comment\nDISCORD_TOKEN=old-secret\n\n# container comment\nMC_CONTAINER_NAME=mc-server\n")

	if err := Set(path, "MC_CONTAINER_NAME", "minecraft"); err != nil {
		t.Fatalf("Set existing failed: %v", err)
	}
	if err := Set(path, "RCON_HOST", "mc-server"); err != nil {
		t.Fatalf("Set new failed: %v", err)
	}
	got := readEnv(t, path)
	assertContains(t, got, "# top comment")
	assertContains(t, got, "# container comment")
	assertContains(t, got, "DISCORD_TOKEN=old-secret")
	assertContains(t, got, "MC_CONTAINER_NAME=minecraft")
	assertContains(t, got, "RCON_HOST=mc-server")
}

func TestSetUpdatesLastDuplicateKey(t *testing.T) {
	path := writeEnv(t, "MC_CONTAINER_NAME=old-first\n# keep\nMC_CONTAINER_NAME=old-last\n")

	if err := Set(path, "MC_CONTAINER_NAME", "minecraft"); err != nil {
		t.Fatalf("Set duplicate failed: %v", err)
	}

	got := readEnv(t, path)
	if got != "MC_CONTAINER_NAME=old-first\n# keep\nMC_CONTAINER_NAME=minecraft\n" {
		t.Fatalf("duplicate set output = %q", got)
	}
	entry, err := Get(path, "MC_CONTAINER_NAME", RevealPolicy{ShowSecrets: true})
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if entry.Value != "minecraft" {
		t.Fatalf("Get value = %q, want minecraft", entry.Value)
	}
}

func TestUnsetRemovesKey(t *testing.T) {
	path := writeEnv(t, "# keep\nDISCORD_TOKEN=secret\nMC_CONTAINER_NAME=mc-server\n")

	if err := Unset(path, "MC_CONTAINER_NAME"); err != nil {
		t.Fatalf("Unset failed: %v", err)
	}
	got := readEnv(t, path)
	assertContains(t, got, "# keep")
	assertContains(t, got, "DISCORD_TOKEN=secret")
	assertNotContains(t, got, "MC_CONTAINER_NAME=")
}

func TestInitUsesTemplateDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")

	if err := Init(path); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	got := readEnv(t, path)
	assertContains(t, got, "DISCORD_TOKEN=your_discord_bot_token_here")
	assertContains(t, got, "MCBOT_TRUSTED_GUILD_ID=123456789012345678")
	assertContains(t, got, "# MC_CONTAINER_NAME=mc-server")
	assertContains(t, got, "# RCON_PASSWORD=your_rcon_password_here")
	assertContains(t, got, "# MCBOT_DEBUG=false")
}

func TestValidateRejectsInvalidValues(t *testing.T) {
	t.Setenv("MCBOT_TRUSTED_GUILD_ID", "123456789012345678")
	path := writeEnv(t, "DISCORD_TOKEN=token\nMCBOT_TRUSTED_GUILD_ID=not-digits\n")

	if err := ValidateFile(path); err == nil {
		t.Fatal("ValidateFile error = nil, want invalid file contents")
	} else {
		assertContains(t, err.Error(), "MCBOT_TRUSTED_GUILD_ID")
		assertNotContains(t, err.Error(), "token")
	}
}

func TestRejectsMcServerOwnedKeys(t *testing.T) {
	if err := Set(writeEnv(t, ""), "VERSION", "1.20.1"); err == nil {
		t.Fatal("Set VERSION error = nil, want ownership rejection")
	}
	path := writeEnv(t, "DISCORD_TOKEN=token\nMCBOT_TRUSTED_GUILD_ID=123456789012345678\nVERSION=1.20.1\n")
	if err := ValidateFile(path); err == nil {
		t.Fatal("ValidateFile with VERSION error = nil, want ownership rejection")
	} else {
		assertContains(t, err.Error(), "VERSION is not owned by .env")
	}
}

func TestMCBotDebugIsOwnedAndBooleanValidated(t *testing.T) {
	path := writeEnv(t, "DISCORD_TOKEN=token\nMCBOT_TRUSTED_GUILD_ID=123456789012345678\nMCBOT_DEBUG=true\n")

	if err := ValidateFile(path); err != nil {
		t.Fatalf("ValidateFile with MCBOT_DEBUG failed: %v", err)
	}
	if err := Set(path, "MCBOT_DEBUG", "false"); err != nil {
		t.Fatalf("Set MCBOT_DEBUG failed: %v", err)
	}
	assertContains(t, readEnv(t, path), "MCBOT_DEBUG=false")
	if err := Set(path, "MCBOT_DEBUG", "maybe"); err == nil {
		t.Fatal("Set MCBOT_DEBUG=maybe error = nil, want boolean validation error")
	} else {
		assertContains(t, err.Error(), "MCBOT_DEBUG must be a boolean")
	}
}

func TestShowSecretsRequiresExplicitRevealPolicy(t *testing.T) {
	path := writeEnv(t, "DISCORD_TOKEN=secret-token\nMC_CONTAINER_NAME=mc-server\n")

	masked, err := Get(path, "DISCORD_TOKEN", RevealPolicy{})
	if err != nil {
		t.Fatalf("Get masked failed: %v", err)
	}
	if masked.Value != MaskedValue {
		t.Fatalf("masked value = %q, want %q", masked.Value, MaskedValue)
	}
	revealed, err := Get(path, "DISCORD_TOKEN", RevealPolicy{ShowSecrets: true})
	if err != nil {
		t.Fatalf("Get revealed failed: %v", err)
	}
	if revealed.Value != "secret-token" {
		t.Fatalf("revealed value = %q, want raw secret", revealed.Value)
	}
}

func writeEnv(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}
	return path
}

func readEnv(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	return string(data)
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
