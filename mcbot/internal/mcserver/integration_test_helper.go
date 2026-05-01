//go:build integration

package mcserver

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	testServiceName = "mc-test"
	testComposeFile = "../../../docker-compose.test.yml"
)

type IntegrationTestHelper struct {
	t            *testing.T
	serviceName  string
	composeFile  string
	projectName  string
	teardownOnce sync.Once
}

func NewIntegrationTestHelper(t *testing.T) *IntegrationTestHelper {
	projectName := fmt.Sprintf("mcbot_test_%d", time.Now().UnixNano())

	return &IntegrationTestHelper{
		t:           t,
		serviceName: testServiceName,
		composeFile: testComposeFile,
		projectName: projectName,
	}
}

func (h *IntegrationTestHelper) Setup() {
	h.t.Helper()
	h.t.Logf("[SETUP] Docker Compose 환경 시작 (project: %s)", h.projectName)

	h.PreClean()

	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "compose", "-p", h.projectName, "-f", h.composeFile, "up", "-d")
	output, err := cmd.CombinedOutput()
	if err != nil {
		h.t.Fatalf("docker compose up 실패: %v\nOutput: %s", err, string(output))
	}

	h.t.Logf("[SETUP] 컨테이너 시작 완료, 서버 준비 대기 중...")
	h.WaitForServerReady(120 * time.Second)
	h.t.Logf("[SETUP] 서버 준비 완료")

	h.t.Cleanup(func() {
		h.Teardown()
	})
}

func (h *IntegrationTestHelper) PreClean() {
	h.t.Helper()
	h.t.Logf("[PRE-CLEAN] 이전 테스트 잔여물 정리 (project: %s)", h.projectName)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "compose", "-p", h.projectName, "-f", h.composeFile, "down", "-v", "--remove-orphans")
	output, err := cmd.CombinedOutput()
	if err != nil {
		h.t.Logf("[PRE-CLEAN] 경고: %v\nOutput: %s", err, string(output))
	}
}

func (h *IntegrationTestHelper) Teardown() {
	h.t.Helper()

	h.teardownOnce.Do(func() {
		h.t.Logf("[TEARDOWN] Docker Compose 환경 정리 시작 (project: %s)", h.projectName)

		if h.t.Failed() {
			h.DumpArtifactsIfFailed()
		}

		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()

		cmd := exec.CommandContext(ctx, "docker", "compose", "-p", h.projectName, "-f", h.composeFile, "down", "-v", "--remove-orphans")
		output, err := cmd.CombinedOutput()
		if err != nil {
			h.t.Logf("[TEARDOWN] 경고: %v\nOutput: %s", err, string(output))
		} else {
			h.t.Logf("[TEARDOWN] 정리 완료")
		}
	})
}

func (h *IntegrationTestHelper) Cleanup() {
	h.Teardown()
}

func (h *IntegrationTestHelper) ContainerID() (string, error) {
	h.t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	labelFilter := fmt.Sprintf("label=com.docker.compose.project=%s", h.projectName)
	serviceFilter := fmt.Sprintf("label=com.docker.compose.service=%s", h.serviceName)

	cmd := exec.CommandContext(ctx, "docker", "ps", "-aq", "--filter", labelFilter, "--filter", serviceFilter)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("컨테이너 ID 조회 실패: %w, output: %s", err, string(output))
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")

	var validIDs []string
	for _, line := range lines {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			validIDs = append(validIDs, trimmed)
		}
	}

	if len(validIDs) == 0 {
		return "", fmt.Errorf("컨테이너가 존재하지 않음 (service: %s, project: %s)", h.serviceName, h.projectName)
	}

	if len(validIDs) > 1 {
		return "", fmt.Errorf("환경 오염: 여러 컨테이너가 존재함 (service: %s, project: %s, count: %d, IDs: %v)",
			h.serviceName, h.projectName, len(validIDs), validIDs)
	}

	return validIDs[0], nil
}

func shortID(id string) string {
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}

func (h *IntegrationTestHelper) ContainerName() (string, error) {
	h.t.Helper()

	containerID, err := h.ContainerID()
	if err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{.Name}}", containerID)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("컨테이너 이름 조회 실패: %w", err)
	}

	containerName := strings.TrimSpace(string(output))
	containerName = strings.TrimPrefix(containerName, "/")

	return containerName, nil
}

func (h *IntegrationTestHelper) WaitForServerReady(timeout time.Duration) {
	h.WaitForServerReadySince(timeout, time.Time{})
}

func (h *IntegrationTestHelper) WaitForServerReadySince(timeout time.Duration, since time.Time) {
	h.t.Helper()

	readyPatterns, err := newReadyMatchers()
	if err != nil {
		h.t.Fatalf("ready 패턴 초기화 실패: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			h.t.Fatalf("서버 준비 타임아웃 (%v)", timeout)
		case <-ticker.C:
			containerID, err := h.ContainerID()
			if err != nil {
				h.t.Logf("컨테이너 ID 조회 실패: %v", err)
				continue
			}

			args := []string{"logs", "--tail", "50"}
			if !since.IsZero() {
				args = append(args, "--since", fmt.Sprintf("%d", since.Unix()))
			}
			args = append(args, containerID)

			cmd := exec.CommandContext(context.Background(), "docker", args...)
			output, err := cmd.CombinedOutput()
			if err != nil {
				h.t.Logf("로그 확인 실패: %v", err)
				continue
			}

			logs := string(output)
			for _, line := range strings.Split(logs, "\n") {
				if matched, _ := matchesReadyPattern(line, readyPatterns); matched {
					return
				}
				if strings.Contains(line, "Time elapsed:") {
					return
				}
			}
		}
	}
}

func (h *IntegrationTestHelper) StopContainer() error {
	h.t.Helper()

	containerID, err := h.ContainerID()
	if err != nil {
		return fmt.Errorf("컨테이너 ID 조회 실패: %w", err)
	}

	h.t.Logf("[ACTION] 컨테이너 중지: %s", shortID(containerID))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "stop", containerID)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker stop 실패: %w, output: %s", err, string(output))
	}
	return nil
}

func (h *IntegrationTestHelper) KillContainer() error {
	h.t.Helper()

	containerID, err := h.ContainerID()
	if err != nil {
		return fmt.Errorf("컨테이너 ID 조회 실패: %w", err)
	}

	h.t.Logf("[ACTION] 컨테이너 강제 종료: %s", shortID(containerID))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "kill", containerID)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker kill 실패: %w, output: %s", err, string(output))
	}
	return nil
}

func (h *IntegrationTestHelper) StartContainer() error {
	h.t.Helper()

	h.t.Logf("[ACTION] 컨테이너 시작: %s (compose start)", h.serviceName)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "compose", "-p", h.projectName, "-f", h.composeFile, "start", h.serviceName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker compose start 실패: %w, output: %s", err, string(output))
	}
	return nil
}

func (h *IntegrationTestHelper) ExecuteRconCommand(command string) (string, error) {
	h.t.Helper()

	containerID, err := h.ContainerID()
	if err != nil {
		return "", fmt.Errorf("컨테이너 ID 조회 실패: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "exec", containerID,
		"rcon-cli", "--password", "test", command)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("rcon 명령 실패: %w, output: %s", err, string(output))
	}
	return string(output), nil
}

func (h *IntegrationTestHelper) DumpArtifactsIfFailed() {
	h.t.Helper()
	h.t.Logf("[ARTIFACTS] 테스트 실패 - 디버깅 정보 수집 중...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	h.t.Logf("=== Docker Compose PS ===")
	psCmd := exec.CommandContext(ctx, "docker", "compose", "-p", h.projectName, "-f", h.composeFile, "ps", "-a")
	if psOutput, err := psCmd.CombinedOutput(); err == nil {
		h.t.Logf("%s", string(psOutput))
	} else {
		h.t.Logf("ps 실패: %v", err)
	}

	containerID, err := h.ContainerID()
	if err != nil {
		h.t.Logf("컨테이너 ID 조회 실패: %v (로그/inspect 수집 불가)", err)
		return
	}

	h.t.Logf("=== Container Logs (last 100 lines) ===")
	logsCmd := exec.CommandContext(ctx, "docker", "logs", "--tail", "100", containerID)
	if logsOutput, err := logsCmd.CombinedOutput(); err == nil {
		h.t.Logf("%s", string(logsOutput))
	} else {
		h.t.Logf("logs 실패: %v", err)
	}

	h.t.Logf("=== Container Inspect (State) ===")
	inspectCmd := exec.CommandContext(ctx, "docker", "inspect", "--format", "{{json .State}}", containerID)
	if inspectOutput, err := inspectCmd.CombinedOutput(); err == nil {
		h.t.Logf("%s", string(inspectOutput))
	} else {
		h.t.Logf("inspect 실패: %v", err)
	}
}

func (h *IntegrationTestHelper) TriggerServerStop() error {
	h.t.Helper()
	h.t.Logf("[ACTION] 서버 내부 종료 트리거 (stop 명령)")

	_, err := h.ExecuteRconCommand("stop")
	return err
}

func (h *IntegrationTestHelper) WaitForContainerState(running bool, timeout time.Duration) error {
	h.t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("컨테이너 상태 대기 타임아웃 (expected running=%v)", running)
		case <-ticker.C:
			containerID, err := h.ContainerID()
			if err != nil {
				continue
			}

			cmd := exec.CommandContext(context.Background(), "docker", "inspect",
				"-f", "{{.State.Running}}", containerID)
			output, err := cmd.Output()
			if err != nil {
				continue
			}

			isRunning := strings.TrimSpace(string(output)) == "true"
			if isRunning == running {
				return nil
			}
		}
	}
}

func (h *IntegrationTestHelper) GetRecentLogs(lines int) (string, error) {
	h.t.Helper()

	containerID, err := h.ContainerID()
	if err != nil {
		return "", fmt.Errorf("컨테이너 ID 조회 실패: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "docker", "logs", "--tail", fmt.Sprintf("%d", lines), containerID)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("로그 조회 실패: %w", err)
	}
	return string(output), nil
}
