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

func TestMakeOperationalTargetsAreDeduplicated(t *testing.T) {
	repo := repoRoot(t)
	makefile := readRepoFile(t, repo, "Makefile")

	for _, want := range []string{
		"up:",
		"up-all:",
		"up-mc:",
		"server start",
		"stop:",
		"server stop",
		"status:",
		"server status",
		"setup:",
		"setup env",
		"setup config",
		"setup-env:",
		"setup-config:",
		"config-validate:",
		"config validate",
		"env-validate:",
		"env validate",
		"backup:",
		"backup create",
		"restore:",
		"backup restore --backup-id $(BACKUP)",
		"backup-list:",
		"backup list",
	} {
		assertContains(t, makefile, want)
	}
	upAll := makeTargetBody(t, makefile, "up-all")
	assertContains(t, upAll, "$(MCBOT_CLI) server start")
	assertContains(t, upAll, "docker compose up --build -d mcbot")
	for _, line := range strings.Split(upAll, "\n") {
		if strings.Contains(line, "docker compose up") && (strings.Contains(line, "$(MC_SERVICE)") || strings.Contains(line, "mc-server")) {
			t.Fatalf("up-all should route mc-server through CLI/TOML bridge, got direct Compose startup line %q in body:\n%s", line, upAll)
		}
	}

	for _, forbidden := range []string{
		"start:",
		"ensure" + "-mc:",
		"docker compose create $(MC_SERVICE)",
		"docker compose up --build -d mc-server",
		"\tdocker compose up --build -d\n",
	} {
		if strings.Contains(makefile, forbidden) {
			t.Fatalf("Makefile still contains raw operational server behavior %q", forbidden)
		}
	}
}

func makeTargetBody(t *testing.T, makefile, target string) string {
	t.Helper()
	start := strings.Index(makefile, target+":\n")
	if start < 0 {
		t.Fatalf("Makefile missing target %s", target)
	}
	bodyStart := start + len(target+":\n")
	bodyEnd := len(makefile)
	for i := bodyStart; i < len(makefile); {
		nextNewline := strings.IndexByte(makefile[i:], '\n')
		if nextNewline < 0 {
			break
		}
		lineEnd := i + nextNewline
		nextLineStart := lineEnd + 1
		if nextLineStart >= len(makefile) {
			break
		}
		if makefile[nextLineStart] != '\t' && makefile[nextLineStart] != '\n' {
			bodyEnd = nextLineStart
			break
		}
		i = nextLineStart
	}
	return makefile[bodyStart:bodyEnd]
}

func TestCommittedMcServerConfigMatchesCodeDefaults(t *testing.T) {
	repo := repoRoot(t)
	config := readRepoFile(t, repo, "mc-server.toml")

	assertContains(t, config, `type = "FORGE"`)
	assertContains(t, config, `[backup]`)
	assertContains(t, config, `directory = "backups"`)
	assertContains(t, config, `retention_count = 14`)
}

func TestDocsDescribeSetupWorkflow(t *testing.T) {
	repo := repoRoot(t)
	readme := readRepoFile(t, repo, "docs", "README.md")
	testingDoc := readRepoFile(t, repo, "docs", "TESTING.md")
	makefile := readRepoFile(t, repo, "Makefile")

	for _, want := range []string{
		"Guided onboarding starts with `mcbot setup` or `make setup`",
		"mcbot setup",
		"mcbot setup env",
		"mcbot setup config",
		"make setup",
		"make setup-env",
		"make setup-config",
		"make up-all",
		"make up-mc",
		"direct `config ...` and `env ...` commands stay available",
		"--yes",
		"--force",
		"--no-input",
		"--json",
		"--show-secrets",
	} {
		assertContains(t, readme, want)
	}

	for _, want := range []string{
		"guided setup 문서와 래퍼 회귀",
		"mcbot setup",
		"mcbot --yes setup",
		"mcbot --no-input setup",
		"mcbot --force setup",
		"setup에서는 둘 다 거부",
	} {
		assertContains(t, testingDoc, want)
	}

	for _, want := range []string{
		"setup:",
		"setup-env:",
		"setup-config:",
		"$(MCBOT_CLI) setup",
		"$(MCBOT_CLI) setup env",
		"$(MCBOT_CLI) setup config",
	} {
		assertContains(t, makefile, want)
	}
}

func TestBackupLayoutAndDocs(t *testing.T) {
	repo := repoRoot(t)
	compose := readRepoFile(t, repo, "docker-compose.yml")
	makefile := readRepoFile(t, repo, "Makefile")
	readme := readRepoFile(t, repo, "docs", "README.md")
	testingDoc := readRepoFile(t, repo, "docs", "TESTING.md")
	gitignore := readRepoFile(t, repo, ".gitignore")

	for _, want := range []string{
		"./data/minecraft:/data",
		"./data/mcbot:/app/data/mcbot",
		"./data/minecraft:/app/data/minecraft:ro",
		"./backups:/app/backups",
		"./mc-server.toml:/app/mc-server.toml:ro",
	} {
		assertContains(t, compose, want)
	}
	if strings.Contains(compose, "./data:/data") {
		t.Fatal("docker-compose.yml still mounts the whole ./data tree into mc-server")
	}

	for _, want := range []string{
		"$(MCBOT_CLI) $(GLOBAL_ARGS) backup create $(ARGS)",
		"$(MCBOT_CLI) $(GLOBAL_ARGS) backup restore --backup-id $(BACKUP) $(ARGS)",
		"$(MCBOT_CLI) $(GLOBAL_ARGS) backup list $(ARGS)",
		"$(MCBOT_CLI) $(GLOBAL_ARGS) backup validate $(ARGS)",
		"$(MCBOT_CLI) $(GLOBAL_ARGS) backup prune $(ARGS)",
	} {
		assertContains(t, makefile, want)
	}

	for _, want := range []string{
		"backup.retention_count",
		"data/minecraft",
		"data/mcbot",
		"mcbot backup restore --interactive",
		"quiesce",
		"자동 복원은 없으며",
	} {
		assertContains(t, readme, want)
	}
	assertContains(t, testingDoc, "백업/복원 회귀")
	assertContains(t, gitignore, "/backups/")
}

func TestControllerMissingContainerGuidancePrefersCanonicalCLI(t *testing.T) {
	repo := repoRoot(t)
	controller := readRepoFile(t, repo, "mcbot", "internal", "mcserver", "controller.go")

	assertContains(t, controller, "mcbot server start")
	deprecatedMakeTarget := "make " + "ensure-mc"
	if strings.Contains(controller, deprecatedMakeTarget) {
		t.Fatalf("controller missing-container guidance still recommends %s", deprecatedMakeTarget)
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
		"do not require Discord credentials",
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
