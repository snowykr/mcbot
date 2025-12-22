package discord

import (
	"strings"
	"testing"
)

func TestFormatPlayers(t *testing.T) {
	tests := []struct {
		name     string
		players  []string
		expected string
	}{
		{
			name:     "No players",
			players:  []string{},
			expected: "현재 접속자(0명)\n- 없음",
		},
		{
			name:     "Single player",
			players:  []string{"Player1"},
			expected: "현재 접속자(1명)\n- Player1",
		},
		{
			name:     "Multiple players within limit",
			players:  []string{"Player1", "Player2", "Player3"},
			expected: "현재 접속자(3명)\n- Player1\n- Player2\n- Player3",
		},
		{
			name:     "Exactly at max visible limit",
			players:  []string{"P1", "P2", "P3", "P4", "P5", "P6", "P7", "P8", "P9", "P10"},
			expected: "현재 접속자(10명)\n- P1\n- P2\n- P3\n- P4\n- P5\n- P6\n- P7\n- P8\n- P9\n- P10",
		},
		{
			name:     "Over max visible limit",
			players:  []string{"P1", "P2", "P3", "P4", "P5", "P6", "P7", "P8", "P9", "P10", "P11"},
			expected: "현재 접속자(11명)\n- P1\n- P2\n- P3\n- P4\n- P5\n- P6\n- P7\n- P8\n- P9\n- P10\n_...외 1명_",
		},
		{
			name:     "Many hidden players",
			players:  []string{"P1", "P2", "P3", "P4", "P5", "P6", "P7", "P8", "P9", "P10", "P11", "P12", "P13", "P14", "P15"},
			expected: "현재 접속자(15명)\n- P1\n- P2\n- P3\n- P4\n- P5\n- P6\n- P7\n- P8\n- P9\n- P10\n_...외 5명_",
		},
		{
			name:     "Player with special characters",
			players:  []string{"Player_*123*"},
			expected: "현재 접속자(1명)\n- Player\\_\\*123\\*",
		},
		{
			name:     "Player with markdown characters",
			players:  []string{"**Bold**", "_Italic_", "`Code`"},
			expected: "현재 접속자(3명)\n- \\*\\*Bold\\*\\*\n- \\_Italic\\_\n- \\`Code\\`",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatPlayers(tt.players)
			if result != tt.expected {
				t.Errorf("formatPlayers() mismatch\nGot:\n%s\n\nExpected:\n%s", result, tt.expected)
			}
		})
	}
}

func TestFormatPlayers_NoTrailingNewline(t *testing.T) {
	tests := []struct {
		name    string
		players []string
	}{
		{
			name:    "No players",
			players: []string{},
		},
		{
			name:    "Single player",
			players: []string{"Player1"},
		},
		{
			name:    "Multiple players within limit",
			players: []string{"Player1", "Player2", "Player3"},
		},
		{
			name:    "Over max visible limit",
			players: []string{"P1", "P2", "P3", "P4", "P5", "P6", "P7", "P8", "P9", "P10", "P11"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatPlayers(tt.players)
			if strings.HasSuffix(result, "\n") {
				t.Errorf("formatPlayers() should not end with newline, got: %q", result)
			}
		})
	}
}
