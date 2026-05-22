package composectl

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

type recordingRunner struct {
	invocations []Invocation
	output      []byte
	err         error
}

func (r *recordingRunner) Run(_ context.Context, inv Invocation) ([]byte, error) {
	r.invocations = append(r.invocations, inv)
	return r.output, r.err
}

func TestEnsureServiceCreatedWithoutShell(t *testing.T) {
	dir := t.TempDir()
	paths := DefaultPaths(dir)
	writeComposeFixture(t, paths.ComposeFile)
	writeComposeFixture(t, paths.EnvFile)

	runner := &recordingRunner{}
	client := Client{Runner: runner}
	if err := client.EnsureServiceCreated(context.Background(), paths, MCServerService, map[string]string{"VERSION": "1.21.4"}); err != nil {
		t.Fatalf("EnsureServiceCreated failed: %v", err)
	}
	if len(runner.invocations) != 1 {
		t.Fatalf("invocations = %d, want 1", len(runner.invocations))
	}
	inv := runner.invocations[0]
	if inv.Name != "docker" {
		t.Fatalf("command name = %q, want docker", inv.Name)
	}
	if strings.Contains(strings.Join(inv.Args, " "), "sh -c") || strings.Contains(strings.Join(inv.Args, " "), "bash -c") {
		t.Fatalf("argv unexpectedly shells out: %#v", inv.Args)
	}
	assertArgSequence(t, inv.Args, []string{"compose", "--project-directory", dir, "--env-file", paths.EnvFile, "-f", paths.ComposeFile, "create", MCServerService})
	if inv.Dir != dir {
		t.Fatalf("Dir = %q, want %q", inv.Dir, dir)
	}
	assertEnvEntry(t, inv.Env, "VERSION=1.21.4")
}

func TestEnsureServiceCreatedReturnsOperationalError(t *testing.T) {
	t.Parallel()

	runner := &recordingRunner{output: []byte("compose failed"), err: errors.New("boom")}
	err := (Client{Runner: runner}).EnsureServiceCreated(context.Background(), DefaultPaths(t.TempDir()), MCServerService, nil)
	if err == nil {
		t.Fatal("EnsureServiceCreated succeeded, want error")
	}
	if !strings.Contains(err.Error(), "compose failed") {
		t.Fatalf("error = %v, want captured output", err)
	}
}

func TestWindowsSafeArgumentBuilding(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "repo root with spaces")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	paths := DefaultPaths(dir)
	writeComposeFixture(t, paths.ComposeFile)
	writeComposeFixture(t, paths.EnvFile)

	inv := EnsureServiceCreateInvocation(paths, MCServerService, map[string]string{"MOTD": "hello snowy world"})
	if inv.Name != "docker" {
		t.Fatalf("Name = %q, want docker", inv.Name)
	}
	for _, arg := range inv.Args {
		if strings.Contains(arg, "'") || strings.Contains(arg, "\"") {
			t.Fatalf("arg %q contains shell quoting; args must be raw argv values", arg)
		}
	}
	if runtime.GOOS == "windows" && strings.Contains(strings.Join(inv.Args, " "), "\\ ") {
		t.Fatalf("windows argv contains shell-style escaping: %#v", inv.Args)
	}
	assertEnvEntry(t, inv.Env, "MOTD=hello snowy world")
}

func TestRepoRootFileDiscovery(t *testing.T) {
	dir := t.TempDir()
	repoRoot := filepath.Join(dir, "repo")
	nested := filepath.Join(repoRoot, "mcbot", "internal", "composectl")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatalf("mkdir nested failed: %v", err)
	}
	writeComposeFixture(t, filepath.Join(repoRoot, "docker-compose.yml"))
	if err := os.WriteFile(filepath.Join(repoRoot, ".env"), []byte(""), 0o600); err != nil {
		t.Fatalf("write .env failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "mc-server.toml"), []byte(""), 0o600); err != nil {
		t.Fatalf("write mc-server.toml failed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "mcbot", "go.mod"), []byte("module example\n"), 0o600); err != nil {
		t.Fatalf("write go.mod failed: %v", err)
	}

	paths, err := DiscoverRepoRoot(nested)
	if err != nil {
		t.Fatalf("DiscoverRepoRoot failed: %v", err)
	}
	if paths.RepoRoot != repoRoot {
		t.Fatalf("RepoRoot = %q, want %q", paths.RepoRoot, repoRoot)
	}
	if paths.EnvFile != filepath.Join(repoRoot, ".env") {
		t.Fatalf("EnvFile = %q", paths.EnvFile)
	}
	if paths.ConfigFile != filepath.Join(repoRoot, "mc-server.toml") {
		t.Fatalf("ConfigFile = %q", paths.ConfigFile)
	}
	if paths.ComposeFile != filepath.Join(repoRoot, "docker-compose.yml") {
		t.Fatalf("ComposeFile = %q", paths.ComposeFile)
	}
}

func TestTomlManagedSettingsOverrideLegacyEnvForServerValues(t *testing.T) {
	dir := t.TempDir()
	paths := DefaultPaths(dir)
	writeComposeFixture(t, paths.ComposeFile)
	writeComposeFixture(t, paths.EnvFile)

	inv := EnsureServiceCreateInvocation(paths, MCServerService, map[string]string{
		"VERSION": "1.21.4",
		"MEMORY":  "8G",
	})
	assertEnvEntry(t, inv.Env, "VERSION=1.21.4")
	assertEnvEntry(t, inv.Env, "MEMORY=8G")
}

func writeComposeFixture(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir fixture dir failed: %v", err)
	}
	if err := os.WriteFile(path, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatalf("write fixture %s failed: %v", path, err)
	}
}

func assertArgSequence(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("args[%d] = %q, want %q; full args %#v", i, got[i], want[i], got)
		}
	}
}

func assertEnvEntry(t *testing.T, env []string, want string) {
	t.Helper()
	if slices.Contains(env, want) {
		return
	}
	t.Fatalf("env missing %q in %#v", want, env)
}
