package discord

import "strings"

func EscapeDiscordText(s string) string {
	if s == "" {
		return s
	}

	escaped := s

	escaped = strings.ReplaceAll(escaped, "@everyone", "@\u200beveryone")
	escaped = strings.ReplaceAll(escaped, "@here", "@\u200bhere")

	if strings.HasPrefix(escaped, "<@") || strings.HasPrefix(escaped, "<#") {
		escaped = "\u200b" + escaped
	}

	replacer := strings.NewReplacer(
		"\\", "\\\\",
		"*", "\\*",
		"_", "\\_",
		"~", "\\~",
		"`", "\\`",
		"|", "\\|",
	)
	escaped = replacer.Replace(escaped)

	return escaped
}
