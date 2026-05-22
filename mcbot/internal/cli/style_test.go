package cli

import (
	"bytes"
	"regexp"
	"strings"
	"testing"

	"github.com/snowy/mcbot/internal/envfile"
	"github.com/snowy/mcbot/internal/mcconfig"
)

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func TestSetupHeaderAlignsWhenStyled(t *testing.T) {
	var stdout bytes.Buffer
	out := Output{stdout: &stdout, style: Style{enabled: true}}

	if err := out.SetupHeader("MCBot Setup", "Discord + Minecraft server wizard"); err != nil {
		t.Fatalf("SetupHeader failed: %v", err)
	}

	lines := nonControlLines(strings.Split(strings.TrimSpace(ansiRE.ReplaceAllString(stdout.String(), "")), "\n"))
	if len(lines) != 4 {
		t.Fatalf("header lines = %d, want 4: %q", len(lines), stdout.String())
	}
	wantWidth := len([]rune(lines[0]))
	for _, line := range lines[1:] {
		if got := len([]rune(line)); got != wantWidth {
			t.Fatalf("misaligned header line width = %d, want %d: %q", got, wantWidth, line)
		}
	}
}

func TestSetupHeaderKeepsStyledBordersAccent(t *testing.T) {
	var stdout bytes.Buffer
	out := Output{stdout: &stdout, style: Style{enabled: true}}

	if err := out.SetupHeader("MCBot Setup", "Discord + Minecraft server wizard"); err != nil {
		t.Fatalf("SetupHeader failed: %v", err)
	}

	rawLines := nonControlLines(strings.Split(strings.TrimSpace(stdout.String()), "\n"))
	if len(rawLines) != 4 {
		t.Fatalf("header lines = %d, want 4: %q", len(rawLines), stdout.String())
	}
	subtitleLine := rawLines[2]
	if !strings.HasPrefix(subtitleLine, "\x1b[36m│") {
		t.Fatalf("subtitle border should start with accent color: %q", subtitleLine)
	}
	if strings.HasPrefix(subtitleLine, "\x1b[2m│") {
		t.Fatalf("subtitle border should not inherit dim color: %q", subtitleLine)
	}
	if !strings.Contains(subtitleLine, "\x1b[2mDiscord + Minecraft server wizard") {
		t.Fatalf("subtitle text should remain dim: %q", subtitleLine)
	}
}

func TestSetupReviewKeepsStyledBordersAccent(t *testing.T) {
	var stdout bytes.Buffer
	out := Output{stdout: &stdout, style: Style{enabled: true}}
	cfg := mcconfig.Defaults()
	entries := []envfile.Entry{{Key: "DISCORD_TOKEN", Value: envfile.MaskedValue}, {Key: "ENABLE_RCON", Value: "true"}}

	if err := out.SetupReview("/tmp/.env", entries, "/tmp/mc-server.toml", cfg); err != nil {
		t.Fatalf("SetupReview failed: %v", err)
	}

	rawLines := nonControlLines(strings.Split(strings.TrimSpace(stdout.String()), "\n"))
	if len(rawLines) < 5 {
		t.Fatalf("review lines = %d, want at least 5: %q", len(rawLines), stdout.String())
	}
	for _, line := range rawLines[1 : len(rawLines)-1] {
		if !strings.HasPrefix(line, "\x1b[36m│") {
			t.Fatalf("review row border should start with accent color: %q", line)
		}
		if strings.HasPrefix(line, "\x1b[2m│") {
			t.Fatalf("review row border should not inherit dim color: %q", line)
		}
	}

	plainLines := nonControlLines(strings.Split(strings.TrimSpace(ansiRE.ReplaceAllString(stdout.String(), "")), "\n"))
	wantWidth := len([]rune(plainLines[0]))
	for _, line := range plainLines[1:] {
		if got := len([]rune(line)); got != wantWidth {
			t.Fatalf("misaligned review line width = %d, want %d: %q", got, wantWidth, line)
		}
	}
}

func nonControlLines(lines []string) []string {
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		if line == "" || line == "\x1b[2J\x1b[H" {
			continue
		}
		filtered = append(filtered, line)
	}
	return filtered
}

func TestSetupQuestionSkipsDuplicateSectionLabel(t *testing.T) {
	var stdout bytes.Buffer
	out := Output{stdout: &stdout, style: Style{enabled: true}, state: &outputState{}}

	if err := out.SetupSection(1, 5, "Discord bot"); err != nil {
		t.Fatalf("SetupSection failed: %v", err)
	}
	stdout.Reset()
	if err := out.SetupQuestion("Discord bot", "Discord bot token", "Paste the bot token."); err != nil {
		t.Fatalf("SetupQuestion failed: %v", err)
	}

	plain := ansiRE.ReplaceAllString(stdout.String(), "")
	if strings.Contains(plain, "\nDiscord bot\n") {
		t.Fatalf("duplicate section label was not suppressed: %q", plain)
	}
	if !strings.Contains(plain, "\n\nDiscord bot token") {
		t.Fatalf("question should keep a blank line after current step: %q", plain)
	}
}
