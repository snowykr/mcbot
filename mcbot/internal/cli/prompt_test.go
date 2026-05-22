package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestStdlibPrompterInput(t *testing.T) {
	var stdout bytes.Buffer
	prompter := NewStdlibPrompter(strings.NewReader("\nAlex\nsecret-token\n"), &stdout)

	value, err := prompter.Input("Name", "Steve")
	if err != nil {
		t.Fatalf("Input default returned error: %v", err)
	}
	if value != "Steve" {
		t.Fatalf("Input default = %q, want %q", value, "Steve")
	}

	value, err = prompter.Input("Name", "Steve")
	if err != nil {
		t.Fatalf("Input value returned error: %v", err)
	}
	if value != "Alex" {
		t.Fatalf("Input value = %q, want %q", value, "Alex")
	}

	secret, err := prompter.SecretInput("Discord token")
	if err != nil {
		t.Fatalf("SecretInput returned error: %v", err)
	}
	if secret != "secret-token" {
		t.Fatalf("SecretInput = %q, want %q", secret, "secret-token")
	}
	if strings.Contains(stdout.String(), "secret-token") {
		t.Fatalf("prompt output leaked secret value: %q", stdout.String())
	}
	assertContains(t, stdout.String(), "Name [Steve]: ")
	assertContains(t, stdout.String(), "Discord token: ")
}

func TestStdlibPrompterSecretInputUsesHiddenTTYPath(t *testing.T) {
	oldIsTerminal := termIsTerminal
	oldReadPassword := termReadPassword
	termIsTerminal = func(int) bool { return true }
	termReadPassword = func(int) ([]byte, error) { return []byte("hidden-secret"), nil }
	defer func() {
		termIsTerminal = oldIsTerminal
		termReadPassword = oldReadPassword
	}()

	stdin, err := os.CreateTemp(t.TempDir(), "prompt-stdin")
	if err != nil {
		t.Fatalf("CreateTemp failed: %v", err)
	}
	defer stdin.Close()

	var stdout bytes.Buffer
	prompter := NewStdlibPrompter(stdin, &stdout)

	secret, err := prompter.SecretInput("Discord token")
	if err != nil {
		t.Fatalf("SecretInput returned error: %v", err)
	}
	if secret != "hidden-secret" {
		t.Fatalf("SecretInput = %q, want %q", secret, "hidden-secret")
	}
	if got := stdout.String(); got != "Discord token: \n" {
		t.Fatalf("stdout = %q, want prompt plus newline", got)
	}
	if strings.Contains(stdout.String(), "hidden-secret") {
		t.Fatalf("TTY prompt output leaked secret value: %q", stdout.String())
	}
}

func TestStdlibPrompterConfirm(t *testing.T) {
	var stdout bytes.Buffer
	prompter := NewStdlibPrompter(strings.NewReader("maybe\ny\n\nno\n"), &stdout)

	confirmed, err := prompter.Confirm("Continue", false)
	if err != nil {
		t.Fatalf("Confirm yes returned error: %v", err)
	}
	if !confirmed {
		t.Fatal("Confirm yes = false, want true")
	}

	confirmed, err = prompter.Confirm("Use default", true)
	if err != nil {
		t.Fatalf("Confirm default returned error: %v", err)
	}
	if !confirmed {
		t.Fatal("Confirm default = false, want true")
	}

	confirmed, err = prompter.Confirm("Continue", true)
	if err != nil {
		t.Fatalf("Confirm no returned error: %v", err)
	}
	if confirmed {
		t.Fatal("Confirm no = true, want false")
	}
	assertContains(t, stdout.String(), "Please answer yes or no.")
}

func TestStdlibPrompterSelect(t *testing.T) {
	var stdout bytes.Buffer
	prompter := NewStdlibPrompter(strings.NewReader("9\n2\n\n"), &stdout)

	choice, err := prompter.Select("Debug logging", []string{"false", "true"}, 0)
	if err != nil {
		t.Fatalf("Select explicit returned error: %v", err)
	}
	if choice != "true" {
		t.Fatalf("Select explicit = %q, want %q", choice, "true")
	}

	choice, err = prompter.Select("Debug logging", []string{"false", "true"}, 0)
	if err != nil {
		t.Fatalf("Select default returned error: %v", err)
	}
	if choice != "false" {
		t.Fatalf("Select default = %q, want %q", choice, "false")
	}
	assertContains(t, stdout.String(), "  1) false")
	assertContains(t, stdout.String(), "Choose 1-2 [1]: ")
	assertContains(t, stdout.String(), "Please choose a number from 1 to 2.")
}
