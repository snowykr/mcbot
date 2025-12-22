package discord

import "testing"

func TestEscapeDiscordText(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "Plain text without special characters",
			input:    "Player123",
			expected: "Player123",
		},
		{
			name:     "Text with asterisks (bold markdown)",
			input:    "Player*Name*",
			expected: "Player\\*Name\\*",
		},
		{
			name:     "Text with underscores (italic markdown)",
			input:    "Player_Name_123",
			expected: "Player\\_Name\\_123",
		},
		{
			name:     "Text with backticks (code markdown)",
			input:    "`code`",
			expected: "\\`code\\`",
		},
		{
			name:     "Text with tildes (strikethrough)",
			input:    "~strikethrough~",
			expected: "\\~strikethrough\\~",
		},
		{
			name:     "Text with pipes (table markdown)",
			input:    "col1|col2",
			expected: "col1\\|col2",
		},
		{
			name:     "Everyone mention",
			input:    "@everyone",
			expected: "@\u200beveryone",
		},
		{
			name:     "Here mention",
			input:    "@here",
			expected: "@\u200bhere",
		},
		{
			name:     "User mention format",
			input:    "<@123456789>",
			expected: "\u200b<@123456789>",
		},
		{
			name:     "Channel mention format",
			input:    "<#987654321>",
			expected: "\u200b<#987654321>",
		},
		{
			name:     "Combined markdown characters",
			input:    "*bold* _italic_ `code`",
			expected: "\\*bold\\* \\_italic\\_ \\`code\\`",
		},
		{
			name:     "Text with backslash",
			input:    "path\\to\\file",
			expected: "path\\\\to\\\\file",
		},
		{
			name:     "Complex player name",
			input:    "Player_*123*",
			expected: "Player\\_\\*123\\*",
		},
		{
			name:     "Everyone in sentence",
			input:    "Hello @everyone!",
			expected: "Hello @\u200beveryone!",
		},
		{
			name:     "Multiple special characters",
			input:    "**__test__**",
			expected: "\\*\\*\\_\\_test\\_\\_\\*\\*",
		},
		{
			name:     "Backslash before @everyone",
			input:    "\\@everyone",
			expected: "\\\\@\u200beveryone",
		},
		{
			name:     "Backslash before @here",
			input:    "\\@here",
			expected: "\\\\@\u200bhere",
		},
		{
			name:     "Username with backslash and @everyone",
			input:    "User\\@everyone",
			expected: "User\\\\@\u200beveryone",
		},
		{
			name:     "Backslash before user mention",
			input:    "\\<@123456789>",
			expected: "\\\\<@123456789>",
		},
		{
			name:     "Multiple backslashes before @everyone",
			input:    "\\\\@everyone",
			expected: "\\\\\\\\@\u200beveryone",
		},
		{
			name:     "Path with @everyone",
			input:    "path\\to\\@everyone",
			expected: "path\\\\to\\\\@\u200beveryone",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := EscapeDiscordText(tt.input)
			if result != tt.expected {
				t.Errorf("EscapeDiscordText(%q) = %q, expected %q", tt.input, result, tt.expected)
			}
		})
	}
}
