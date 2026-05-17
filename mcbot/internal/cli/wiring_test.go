package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRuntimeWiringUsesBotRunEntryPoint(t *testing.T) {
	repo := repoRoot(t)
	dockerfile := readRepoFile(t, repo, "mcbot", "Dockerfile")
	compose := readRepoFile(t, repo, "docker-compose.yml")

	assertContains(t, dockerfile, `CMD ["bot", "run"]`)
	assertContains(t, compose, `command: ["bot", "run"]`)
}

func TestMakeOperationalTargetsDelegateToCLI(t *testing.T) {
	repo := repoRoot(t)
	makefile := readRepoFile(t, repo, "Makefile")

	for _, want := range []string{
		"start:",
		"server start",
		"stop:",
		"server stop",
		"status:",
		"server status",
		"config-validate:",
		"config validate",
		"env-validate:",
		"env validate",
	} {
		assertContains(t, makefile, want)
	}

	for _, forbidden := range []string{
		"docker compose create $(MC_SERVICE)",
		"docker compose up --build -d mc-server",
		"\tdocker compose up --build -d\n",
	} {
		if strings.Contains(makefile, forbidden) {
			t.Fatalf("Makefile still contains raw operational server behavior %q", forbidden)
		}
	}
}

func TestControllerMissingContainerGuidancePrefersCanonicalCLI(t *testing.T) {
	repo := repoRoot(t)
	controller := readRepoFile(t, repo, "mcbot", "internal", "mcserver", "controller.go")

	assertContains(t, controller, "mcbot server start")
	if strings.Contains(controller, "make ensure-mc") {
		t.Fatal("controller missing-container guidance still recommends make ensure-mc")
	}
}

func TestDocsDescribeCLIFirstWorkflow(t *testing.T) {
	repo := repoRoot(t)
	readme := readRepoFile(t, repo, "docs", "README.md")
	testingDoc := readRepoFile(t, repo, "docs", "TESTING.md")

	for _, want := range []string{
		"canonical operational source of truth",
		"mcbot bot run",
		"mcbot server start",
		"mcbot server stop",
		"mcbot server status",
		"server commands do not require Discord credentials",
		"server status is Docker-derived",
		"mc-server.toml",
		"***MASKED***",
		"--show-secrets",
		"--no-input",
		"exit code 2",
	} {
		assertContains(t, readme, want)
	}
	assertContains(t, testingDoc, "mcbot server status")
	assertContains(t, testingDoc, "config validate")
	assertContains(t, testingDoc, "env validate")
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

func readRepoFile(t *testing.T, repo string, parts ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{repo}, parts...)...)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s failed: %v", path, err)
	}
	return string(data)
}
