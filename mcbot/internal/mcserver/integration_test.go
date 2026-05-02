//go:build integration

package mcserver

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/snowy/mcbot/internal/config"
	"github.com/snowy/mcbot/internal/state"
)

func TestIntegration_RuntimeWatcher_ShutdownLog(t *testing.T) {
	helper := NewIntegrationTestHelper(t)
	helper.Setup()

	containerName, err := helper.ContainerName()
	if err != nil {
		t.Fatalf("컨테이너 이름 조회 실패: %v", err)
	}

	cfg := &config.Config{
		MCContainerName:           containerName,
		ReadyTimeout:              60 * time.Second,
		CrashDetectionInterval:    1 * time.Second,
		MaxInspectFailureAttempts: 3,
	}

	stateManager := state.NewManager()
	controller, err := NewController(cfg, stateManager)
	if err != nil {
		t.Fatalf("Controller 생성 실패: %v", err)
	}
	defer controller.Shutdown()

	stateManager.SetRunning(10 * time.Second)

	controller.logMux.Start(time.Now().Add(-10 * time.Second))
	controller.StartRuntimeWatchers(context.Background())

	time.Sleep(2 * time.Second)

	t.Log("서버 내부 종료 트리거")
	if err := helper.TriggerServerStop(); err != nil {
		t.Fatalf("서버 종료 트리거 실패: %v", err)
	}

	if err := helper.WaitForContainerState(false, 30*time.Second); err != nil {
		t.Fatalf("컨테이너 종료 대기 실패: %v", err)
	}

	time.Sleep(3 * time.Second)

	finalState := stateManager.GetState()
	if finalState != state.StateStopped {
		t.Errorf("예상 상태: StateStopped, 실제: %s", finalState.Korean())
	}

	t.Log("✓ shutdown 로그 감지 후 정상 종료 처리 확인")
}

func TestIntegration_RuntimeWatcher_UnexpectedStop(t *testing.T) {
	helper := NewIntegrationTestHelper(t)
	helper.Setup()

	containerName, err := helper.ContainerName()
	if err != nil {
		t.Fatalf("컨테이너 이름 조회 실패: %v", err)
	}

	cfg := &config.Config{
		MCContainerName:           containerName,
		ReadyTimeout:              60 * time.Second,
		CrashDetectionInterval:    1 * time.Second,
		MaxInspectFailureAttempts: 3,
	}

	stateManager := state.NewManager()
	controller, err := NewController(cfg, stateManager)
	if err != nil {
		t.Fatalf("Controller 생성 실패: %v", err)
	}
	defer controller.Shutdown()

	stateManager.SetRunning(10 * time.Second)

	controller.logMux.Start(time.Now().Add(-10 * time.Second))
	controller.StartRuntimeWatchers(context.Background())

	time.Sleep(2 * time.Second)

	t.Log("컨테이너 강제 종료 (docker kill로 즉시 종료)")
	if err := helper.KillContainer(); err != nil {
		t.Fatalf("컨테이너 kill 실패: %v", err)
	}

	time.Sleep(5 * time.Second)

	finalState := stateManager.GetState()
	if finalState != state.StateCrashed {
		t.Errorf("예상 상태: StateCrashed, 실제: %s", finalState.Korean())
	}

	t.Log("✓ 예기치 않은 종료 감지 후 crashed 처리 확인")
}

func TestIntegration_RCONStartupCommands_DoNotLeaveZombieProcess(t *testing.T) {
	helper := NewIntegrationTestHelper(t)
	helper.SetupWithEnv(map[string]string{
		"RCON_CMDS_STARTUP": "gamerule keepInventory true",
	})

	initEnabled, err := helper.ContainerInitEnabled()
	if err != nil {
		t.Fatalf("container init 설정 확인 실패: %v", err)
	}
	if !initEnabled {
		t.Fatal("mc-test container init = false, want true to reap RCON startup helper processes")
	}

	helper.WaitForLogSubstring("No addition rcon commands are given, stopping rcon cmd service", 30*time.Second)

	processTable, err := helper.ProcessTable()
	if err != nil {
		t.Fatalf("process table 조회 실패: %v", err)
	}
	if hasRCONZombieProcess(processTable) {
		t.Fatalf("rcon-cmds-daemo zombie process detected after RCON_CMDS_STARTUP completed:\n%s", processTable)
	}

	t.Log("✓ RCON_CMDS_STARTUP 실행 후 rcon-cmds-daemo zombie 미발생 확인")
}

func hasRCONZombieProcess(processTable string) bool {
	for _, line := range strings.Split(processTable, "\n") {
		if !strings.Contains(line, "rcon-cmds-daemo") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 5 && (fields[3] == "Z" || strings.HasPrefix(fields[4], "Z")) {
			return true
		}
		if strings.Contains(line, "<defunct>") {
			return true
		}
	}
	return false
}

func TestIntegration_SyncWatcher_ExternalStart(t *testing.T) {
	helper := NewIntegrationTestHelper(t)
	helper.Setup()

	containerName, err := helper.ContainerName()
	if err != nil {
		t.Fatalf("컨테이너 이름 조회 실패: %v", err)
	}

	if err := helper.StopContainer(); err != nil {
		t.Fatalf("초기 컨테이너 종료 실패: %v", err)
	}

	time.Sleep(2 * time.Second)

	cfg := &config.Config{
		MCContainerName:           containerName,
		ReadyTimeout:              120 * time.Second,
		CrashDetectionInterval:    1 * time.Second,
		MaxInspectFailureAttempts: 3,
	}

	stateManager := state.NewManager()
	controller, err := NewController(cfg, stateManager)
	if err != nil {
		t.Fatalf("Controller 생성 실패: %v", err)
	}
	defer controller.Shutdown()

	ctx := context.Background()
	if err := controller.SyncState(ctx); err != nil {
		t.Fatalf("SyncState 실패: %v", err)
	}

	currentState := stateManager.GetState()
	if currentState != state.StateStopped {
		t.Logf("초기 상태: %s", currentState.Korean())
	}

	t.Log("외부에서 컨테이너 시작")
	startedAt := time.Now()
	if err := helper.StartContainer(); err != nil {
		t.Fatalf("컨테이너 시작 실패: %v", err)
	}

	time.Sleep(2 * time.Second)

	if err := controller.SyncState(ctx); err != nil {
		t.Fatalf("SyncState (시작 후) 실패: %v", err)
	}

	time.Sleep(3 * time.Second)

	afterSyncState := stateManager.GetState()
	if afterSyncState != state.StateStarting && afterSyncState != state.StateRunning {
		t.Errorf("예상 상태: StateStarting 또는 StateRunning, 실제: %s", afterSyncState.Korean())
	}

	t.Log("서버 ready 로그 대기 중...")
	helper.WaitForServerReadySince(120*time.Second, startedAt)

	maxWait := 30
	for i := 0; i < maxWait; i++ {
		time.Sleep(1 * time.Second)
		currentState := stateManager.GetState()
		if currentState == state.StateRunning {
			t.Logf("Ready 후 %d초 만에 StateRunning 전이 확인", i+1)
			break
		}
		if i == maxWait-1 {
			t.Logf("경고: %d초 대기 후에도 StateRunning 전이 안됨, 현재: %s", maxWait, currentState.Korean())
		}
	}

	finalState := stateManager.GetState()
	if finalState != state.StateRunning {
		t.Errorf("예상 최종 상태: StateRunning, 실제: %s", finalState.Korean())
	}

	t.Log("✓ 외부 시작 감지 및 ready 패턴 매칭 후 Running 전이 확인")
}

func TestIntegration_ContainerWatcher_InspectFailure(t *testing.T) {
	cfg := &config.Config{
		MCContainerName:           "nonexistent-container-12345",
		ReadyTimeout:              60 * time.Second,
		CrashDetectionInterval:    500 * time.Millisecond,
		MaxInspectFailureAttempts: 2,
	}

	stateManager := state.NewManager()
	controller, err := NewController(cfg, stateManager)
	if err != nil {
		t.Fatalf("Controller 생성 실패: %v", err)
	}
	defer controller.Shutdown()

	stateManager.SetRunning(10 * time.Second)

	controller.StartRuntimeWatchers(context.Background())

	time.Sleep(3 * time.Second)

	finalState := stateManager.GetState()
	if finalState != state.StateCrashed {
		t.Errorf("예상 상태: StateCrashed (inspect 연속 실패), 실제: %s", finalState.Korean())
	}

	t.Log("✓ inspect 연속 실패 임계치 도달 후 crashed 처리 확인")
}

func TestIntegration_WatcherRestart_AfterCrash(t *testing.T) {
	helper := NewIntegrationTestHelper(t)
	helper.Setup()

	containerName, err := helper.ContainerName()
	if err != nil {
		t.Fatalf("컨테이너 이름 조회 실패: %v", err)
	}

	cfg := &config.Config{
		MCContainerName:           containerName,
		ReadyTimeout:              60 * time.Second,
		CrashDetectionInterval:    1 * time.Second,
		MaxInspectFailureAttempts: 3,
	}

	stateManager := state.NewManager()
	controller, err := NewController(cfg, stateManager)
	if err != nil {
		t.Fatalf("Controller 생성 실패: %v", err)
	}
	defer controller.Shutdown()

	stateManager.SetRunning(10 * time.Second)
	controller.logMux.Start(time.Now().Add(-10 * time.Second))
	controller.StartRuntimeWatchers(context.Background())

	time.Sleep(2 * time.Second)

	t.Log("컨테이너 강제 종료로 크래시 유발")
	if err := helper.KillContainer(); err != nil {
		t.Fatalf("컨테이너 kill 실패: %v", err)
	}

	time.Sleep(5 * time.Second)

	if stateManager.GetState() != state.StateCrashed {
		t.Fatalf("크래시 상태가 아님: %s", stateManager.GetState().Korean())
	}

	t.Log("컨테이너 재시작 후 워처 재시작 시도")
	restartedAt := time.Now()
	if err := helper.StartContainer(); err != nil {
		t.Fatalf("컨테이너 재시작 실패: %v", err)
	}

	helper.WaitForServerReadySince(180*time.Second, restartedAt)

	stateManager.SetRunning(10 * time.Second)
	controller.logMux.Start(time.Now().Add(-5 * time.Second))

	controller.StartRuntimeWatchers(context.Background())

	time.Sleep(3 * time.Second)

	if stateManager.GetState() != state.StateRunning {
		t.Errorf("재시작 후 상태가 Running이 아님: %s", stateManager.GetState().Korean())
	}

	t.Log("✓ 크래시 후 워처 재시작 가능 확인 (supervisor 정리 동작)")
}
