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
		"처음 설정하고 운영할 때는 Make 명령 사용을 권장합니다.",
		"Go CLI가 내부 동작의 기준입니다.",
		"처음 설정할 때는 `make setup`으로 시작합니다.",
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
		"`make up`은 Discord 봇 컨테이너만 빌드하고 실행하며, `mc-server`를 생성하거나 준비 상태로 만들지 않습니다.",
		"go run ./cmd/mcbot help",
		"make go-build",
		"./mcbot help",
		"수동 Docker Compose 우회 경로",
		"Go CLI의 `config ...`, `env ...` 명령도 계속 사용할 수 있습니다.",
		"--yes",
		"--force",
		"--no-input",
		"--json",
		"--show-secrets",
	} {
		assertContains(t, readme, want)
	}

	for _, want := range []string{
		"안내형 setup 문서와 래퍼 회귀",
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
	manualCompose := strings.Index(readme, "### 수동 Docker Compose 우회 경로")
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
		"Go CLI가 내부 동작의 기준입니다.",
		"mcbot bot run",
		"mcbot server start",
		"mcbot server stop",
		"mcbot server status",
		"Discord 인증 정보 없이 동작합니다.",
		"서버 상태는 Docker 조회 결과를 기준으로 판단합니다.",
		"mc-server.toml",
		"***MASKED***",
		"--show-secrets",
		"--no-input",
		"종료 코드 2",
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
