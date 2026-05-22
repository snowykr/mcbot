package cli

import (
	"io"
	"os"

	"golang.org/x/term"
)

type Style struct {
	enabled bool
}

func NewStyle(stdout io.Writer) Style {
	if os.Getenv("NO_COLOR") != "" {
		return Style{}
	}
	file, ok := stdout.(*os.File)
	if !ok {
		return Style{}
	}
	return Style{enabled: term.IsTerminal(int(file.Fd()))}
}

func (s Style) Header(text string) string {
	if !s.enabled {
		return text
	}
	return "\x1b[1;36m" + text + "\x1b[0m"
}

func (s Style) Accent(text string) string {
	if !s.enabled {
		return text
	}
	return "\x1b[36m" + text + "\x1b[0m"
}

func (s Style) Dim(text string) string {
	if !s.enabled {
		return text
	}
	return "\x1b[2m" + text + "\x1b[0m"
}
