package cli

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"golang.org/x/term"
)

var termIsTerminal = term.IsTerminal
var termReadPassword = term.ReadPassword

type Prompter interface {
	Input(label, defaultValue string) (string, error)
	Confirm(label string, defaultValue bool) (bool, error)
	SecretInput(label string) (string, error)
	Select(label string, options []string, defaultIndex int) (string, error)
}

type StdlibPrompter struct {
	stdin  io.Reader
	reader *bufio.Reader
	writer io.Writer
}

func NewStdlibPrompter(stdin io.Reader, stdout io.Writer) *StdlibPrompter {
	return &StdlibPrompter{stdin: stdin, reader: bufio.NewReader(stdin), writer: stdout}
}

func (p *StdlibPrompter) Input(label, defaultValue string) (string, error) {
	prompt := label
	if defaultValue != "" {
		prompt = fmt.Sprintf("%s [%s]", label, defaultValue)
	}
	line, err := p.readLine(prompt + ": ")
	if err != nil {
		return "", err
	}
	if line == "" {
		return defaultValue, nil
	}
	return line, nil
}

func (p *StdlibPrompter) SecretInput(label string) (string, error) {
	if _, err := fmt.Fprint(p.writer, label+": "); err != nil {
		return "", err
	}
	if file, ok := p.stdin.(*os.File); ok && termIsTerminal(int(file.Fd())) {
		secret, err := termReadPassword(int(file.Fd()))
		if _, newlineErr := fmt.Fprintln(p.writer); newlineErr != nil && err == nil {
			err = newlineErr
		}
		return string(secret), err
	}
	return p.readRawLine()
}

func (p *StdlibPrompter) Confirm(label string, defaultValue bool) (bool, error) {
	suffix := " [y/N]: "
	if defaultValue {
		suffix = " [Y/n]: "
	}
	for {
		line, err := p.readLine(label + suffix)
		if err != nil {
			return false, err
		}
		switch strings.ToLower(line) {
		case "":
			return defaultValue, nil
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		default:
			if _, err := fmt.Fprintln(p.writer, "Please answer yes or no."); err != nil {
				return false, err
			}
		}
	}
}

func (p *StdlibPrompter) Select(label string, options []string, defaultIndex int) (string, error) {
	if len(options) == 0 {
		return "", fmt.Errorf("select prompt %q requires at least one option", label)
	}
	if defaultIndex < 0 || defaultIndex >= len(options) {
		return "", fmt.Errorf("select prompt %q default index %d out of range", label, defaultIndex)
	}
	if _, err := fmt.Fprintln(p.writer, label); err != nil {
		return "", err
	}
	for i, option := range options {
		if _, err := fmt.Fprintf(p.writer, "  %d) %s\n", i+1, option); err != nil {
			return "", err
		}
	}
	for {
		line, err := p.readLine(fmt.Sprintf("Choose 1-%d [%d]: ", len(options), defaultIndex+1))
		if err != nil {
			return "", err
		}
		if line == "" {
			return options[defaultIndex], nil
		}
		choice, err := strconv.Atoi(line)
		if err == nil && choice >= 1 && choice <= len(options) {
			return options[choice-1], nil
		}
		if _, err := fmt.Fprintf(p.writer, "Please choose a number from 1 to %d.\n", len(options)); err != nil {
			return "", err
		}
	}
}

func (p *StdlibPrompter) readLine(prompt string) (string, error) {
	if _, err := fmt.Fprint(p.writer, prompt); err != nil {
		return "", err
	}
	return p.readRawLine()
}

func (p *StdlibPrompter) readRawLine() (string, error) {
	line, err := p.reader.ReadString('\n')
	if err != nil && !(err == io.EOF && line != "") {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}
