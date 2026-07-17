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
}

func TestDocsDescribeSetupWorkflow(t *testing.T) {
	repo := repoRoot(t)
	readme := readRepoFile(t, repo, "docs", "README.md")
	testingDoc := readRepoFile(t, repo, "docs", "TESTING.md")
	makefile := readRepoFile(t, repo, "Makefile")

	for _, want := range []string{
		"Make is the recommended onboarding and operations interface.",
		"canonical operational source of truth",
		"Guided onboarding starts with `make setup`",
		"mcbot setup",
		"mcbot setup env",
		"mcbot setup config",
		"make setup",
		"make setup-env",
		"make setup-config",
		"make up-all",
		"make up-mc",
		"make status",
		"make stop",
		"`make up`은 Discord bot 컨테이너만 빌드하고 실행하며, `mc-server`를 생성하거나 준비 상태로 만들지 않습니다.",
		"go run ./cmd/mcbot help",
		"make go-build",
		"./mcbot help",
		"수동 Docker Compose escape hatch",
		"direct Go CLI `config ...` and `env ...` commands stay available",
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

	install := strings.Index(readme, "## 설치 및 실행")
	advancedCLI := strings.Index(readme, "## 고급 CLI 사용법")
	manualCompose := strings.Index(readme, "### 수동 Docker Compose escape hatch")
	firstRawCompose := strings.Index(readme, "docker compose create mc-server")
	if install < 0 || advancedCLI < install || manualCompose < advancedCLI || firstRawCompose < manualCompose {
		t.Fatal("README must present Make-first setup before advanced CLI and raw Compose escape-hatch commands")
	}
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

func TestDocsDescribeUnderlyingCLIWorkflow(t *testing.T) {
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
