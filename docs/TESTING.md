# mcbot 테스트 가이드

## 목차

1. [자동화된 테스트](#자동화된-테스트)
2. [통합 테스트](#통합-테스트)
3. [컴포넌트별 자동화 테스트 전략](#컴포넌트별-자동화-테스트-전략)
4. [수동 검증 시나리오](#수동-검증-시나리오)
5. [회귀 테스트](#회귀-테스트)
6. [테스트 체크리스트](#테스트-체크리스트)
7. [문제 해결](#문제-해결)
8. [참고 사항](#참고-사항)

## 자동화된 테스트

### 단위 테스트 실행

```bash
# 전체 테스트 실행
go test ./...

# 특정 패키지 테스트
go test ./internal/mcserver
go test ./internal/discord
go test ./internal/dockerctl

# 상세 출력과 함께 실행
go test -v ./...
```

### 테스트 커버리지

```bash
# 커버리지 확인
go test -cover ./...

# 상세 커버리지 리포트
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

## 통합 테스트

### 개요

통합 테스트는 실제 Docker 컨테이너를 사용하여 엔드투엔드 시나리오를 검증합니다. 단위 테스트와 달리 실제 마인크래프트 서버 컨테이너와 상호작용하여 워처, 상태 전이, 자동 복구 등의 동작을 검증합니다.

### 격리 및 안전성

통합 테스트는 **운영 환경과 완전히 격리**되어 실행됩니다:

- **프로젝트 격리**: 각 테스트 실행마다 유니크한 프로젝트명(`mcbot_test_<timestamp>`) 사용
- **네트워크 격리**: 테스트 전용 네트워크 생성 (운영의 `mcbot_default`와 분리)
- **리소스 격리**: 컨테이너/볼륨/네트워크가 운영 환경과 충돌하지 않음
- **자동 정리**: 테스트 성공/실패 시 항상 `docker compose down -v --remove-orphans` 수행

### 실행 방법

```bash
# Makefile 사용 (권장)
make test-integration

# 직접 실행
cd mcbot
go test -tags=integration -v ./internal/mcserver/...

# 상세 로그와 함께 실행
make test-integration-verbose
```

### 주의사항

- **Docker 필수**: 통합 테스트는 Docker가 설치되어 있어야 합니다
- **시간 소요**: 각 테스트마다 컨테이너를 시작/종료하므로 5-10분 정도 소요됩니다
- **리소스**: 테스트용 경량 설정(1GB 메모리)을 사용하지만, 여러 테스트가 순차 실행되므로 시스템 리소스를 고려하세요
- **로컬 전용**: CI 통합은 별도 작업이며, 현재는 로컬 수동 실행만 지원합니다
- **운영 환경 안전**: 운영 compose를 띄운 상태에서도 통합 테스트 실행 가능 (프로젝트 격리)

### 테스트 시나리오

#### 1. Runtime Watcher - Shutdown Log 감지
**파일**: `integration_test.go::TestIntegration_RuntimeWatcher_ShutdownLog`

**검증 내용**:
- 서버 내부에서 `/stop` 명령 실행 시 shutdown 로그 감지
- `shutdownIntentFromInside` 플래그가 true로 설정됨
- 컨테이너 종료 후 상태가 `StateStopped`로 전이 (crashed가 아님)
- **이벤트 기반 종료 판별**: container watcher가 로그 watcher의 종료 신호(`logWatcherDoneCh`)를 기다린 후 intent 재확인

**시나리오**:
1. 테스트 컨테이너 시작 및 준비 대기
2. Controller를 `StateRunning`으로 설정
3. Runtime watcher 시작
4. RCON으로 `stop` 명령 실행
5. 컨테이너 종료 대기
6. 최종 상태가 `StateStopped`인지 확인

**Shutdown Intent 판별 메커니즘 (2026-01-11 개선)**:
- 기존: 5회 × 50ms polling 루프 (총 250ms)
- 현재: 이벤트 기반 대기 (`logWatcherDoneCh` close 또는 `ShutdownIntentGracePeriod` 타임아웃, 최대 2초)
- 로그 파이프라인 지연으로 인한 정상 종료 오탐 방지

#### 2. Runtime Watcher - 예기치 않은 종료
**파일**: `integration_test.go::TestIntegration_RuntimeWatcher_UnexpectedStop`

**검증 내용**:
- Shutdown 로그 없이 컨테이너가 강제 종료되면 crashed 처리
- `docker stop`으로 강제 종료 시 `StateCrashed`로 전이

**시나리오**:
1. 테스트 컨테이너 시작 및 준비 대기
2. Controller를 `StateRunning`으로 설정
3. Runtime watcher 시작
4. `docker stop`으로 컨테이너 강제 종료
5. 최종 상태가 `StateCrashed`인지 확인

#### 3. Sync Watcher - 외부 시작 감지
**파일**: `integration_test.go::TestIntegration_SyncWatcher_ExternalStart`

**검증 내용**:
- 외부에서 컨테이너를 시작했을 때 자동 감지
- Ready 패턴 매칭 후 `StateRunning`으로 전이
- Sync watcher가 정상 동작

**시나리오**:
1. 컨테이너를 종료된 상태로 시작
2. `SyncState()` 호출로 초기 상태 확인 (`StateStopped`)
3. 외부에서 `docker start` 실행
4. `SyncState()` 재호출로 `StateStarting` 전이 확인
5. Ready 로그 대기
6. 최종 상태가 `StateRunning`인지 확인

#### 4. Container Watcher - Inspect 연속 실패
**파일**: `integration_test.go::TestIntegration_ContainerWatcher_InspectFailure`

**검증 내용**:
- 존재하지 않는 컨테이너에 대한 inspect 연속 실패 감지
- `MaxInspectFailureAttempts` 임계치 도달 시 crashed 처리

**시나리오**:
1. 존재하지 않는 컨테이너명으로 Controller 생성
2. `StateRunning`으로 설정
3. Runtime watcher 시작 (inspect 실패 반복)
4. 설정된 임계치(2회) 도달 후 `StateCrashed` 전이 확인

#### 5. Watcher 재시작 - 크래시 후 복구
**파일**: `integration_test.go::TestIntegration_WatcherRestart_AfterCrash`

**검증 내용**:
- Watcher가 크래시로 종료된 후에도 재시작 가능
- Supervisor goroutine이 `watchersRunning` 상태를 올바르게 정리
- 동일 Controller 인스턴스에서 여러 번 watcher 시작/종료 가능

**시나리오**:
1. 테스트 컨테이너 시작 및 준비 대기
2. Runtime watcher 시작
3. 컨테이너 강제 종료로 crashed 상태 유발
4. Watcher 자동 종료 대기
5. 컨테이너 재시작
6. Runtime watcher 재시작 시도
7. 정상적으로 재시작되는지 확인

### 테스트 환경 구성

통합 테스트는 `docker-compose.test.yml`을 사용합니다:

- **컨테이너명**: 동적 생성 (프로젝트명 기반, 예: `mcbot_test_<timestamp>-mc-test-1`)
- **포트**: 외부 포트 노출 없음 (docker exec를 통한 RCON 사용)
- **메모리**: 1GB (빠른 시작을 위한 경량 설정)
- **버전**: Minecraft 1.20.1 Vanilla
- **RCON**: 활성화 (테스트 명령 실행용, 컨테이너 내부에서만 접근)

### 문제 해결

#### 테스트 중단 후 잔여 리소스 정리
테스트가 `Ctrl+C` 등으로 중단된 경우, 다음 명령으로 모든 테스트 리소스를 정리할 수 있습니다:
```bash
# Makefile 사용 (권장)
make test-integration-clean

# 수동 정리
docker ps -a --filter "name=mcbot_test_" --format "{{.Names}}" | xargs docker rm -f
docker network ls --filter "name=mcbot_test_" --format "{{.Name}}" | xargs docker network rm
docker volume ls --filter "name=mcbot_test_" --format "{{.Name}}" | xargs docker volume rm
```

#### 포트 관련 참고사항
통합 테스트는 기본적으로 외부 포트를 노출하지 않으므로 포트 충돌이 발생하지 않습니다.
만약 외부 접근이 필요한 경우 `docker-compose.test.yml`에 포트 매핑을 추가할 수 있습니다:
```yaml
ports:
  - "25567:25565"  # 원하는 포트로 설정
```

#### 테스트 실패 시 디버깅
테스트가 실패하면 자동으로 다음 정보가 출력됩니다:
- Docker Compose PS 상태
- 컨테이너 로그 (마지막 100줄)
- 컨테이너 상태 정보 (inspect)

이 정보를 활용하여 실패 원인을 파악할 수 있습니다.

## 컴포넌트별 자동화 테스트 전략

이 섹션은 각 `*_test.go` 파일이 검증하는 내용과 책임을 설명합니다. 수동 시나리오와 자동화 테스트의 연결 고리를 이해하는 데 도움이 됩니다.

### Controller 상태 전이 테스트

**파일**: `internal/mcserver/controller_start_test.go`, `internal/mcserver/controller_stop_test.go`

**책임**:
- 서버 상태 전이 규칙 검증
- 잘못된 상태에서의 명령 거부 확인
- 에러 메시지 정확성 검증

**검증 포인트**:
- `Stopped` → `Start` 시도: 컨테이너 없으면 실패, 에러 메시지 반환, 상태는 `Stopped` 유지 (lastError 기록)
- `Error` → `Start` 시도: 컨테이너 없으면 실패, 상태는 `Stopped`로 전환 (lastError 기록)
- `Running` → `Start` 시도: "서버가 이미 실행 중입니다." 에러, 상태 유지
- `Starting` → `Start` 시도: "서버가 이미 시작 중입니다." 에러, 상태 유지
- `Stopping` → `Start` 시도: "서버가 종료 중입니다. 종료가 완료된 후 다시 시도해주세요." 에러
- `Running` → `Stop` 시도: 컨테이너 없으면 실패, 에러 메시지 반환
- `Stopped` → `Stop` 시도: "서버가 이미 종료되어 있습니다." 에러, 상태 유지
- `Stopping` → `Stop` 시도: "서버가 이미 종료 중입니다." 에러, 상태 유지
- `Starting` → `Stop` 시도: "서버가 시작 중입니다. 시작이 완료된 후 다시 시도해주세요." 에러
- `Error` → `Stop` 시도: 실패 처리

**상태 모델 개선 (2024-12-23)**:
- 컨테이너 미존재는 인프라 미준비 상태로, `StateError`가 아닌 `StateStopped` + `lastError` 조합으로 표현
- `StateError`는 실제 런타임 예외(docker inspect 실패, start/stop 명령 실패, ready 타임아웃 등)에만 사용
- Start/Stop 간 상태 처리 일관성 확보: 컨테이너 없음은 모두 Stopped 상태로 처리
- `SetStoppedWithError` 메서드 추가: Stopped 상태이면서 lastError를 기록 가능

**상태 자기 교정 로직 개선 (2024-12-23 최종)**:
- `Status()` 함수의 자기 교정 로직 완전 재설계:
  - **Running 승격 로직 완전 제거**: 컨테이너가 Running이어도 `Status()`는 절대 `StateRunning`으로 승격시키지 않음
  - **오직 ready 로그(Done 플래그)만이 `StateRunning` 진입 조건**
  - `StateStarting`/`StateStopping`은 컨테이너 상태와 무관하게 유지
  - `StateRunning` → `StateStopped` 강등만 최소한 유지 (컨테이너 다운 감지)
- 이를 통해 "여는중" 상태가 **ready 로그 인식 시점까지 정확히 유지**되어 사용자에게 명확한 피드백 제공
- 상태 전이 책임 명확화:
  - `StateRunning` 진입: **오직 `Controller.Start`의 ready 로그 처리만** 담당
  - 성공/실패 전이: `Controller.Start/Stop` 내부에서만 수행
  - `Status()`는 조회 + Running→Stopped 강등만 담당

**컨텍스트 수명 분리 (2024-12-23)**:
- Discord 인터랙션 컨텍스트와 서버 작업 컨텍스트 완전 분리
- `Controller.Start/Stop`은 외부 컨텍스트를 무시하고 `context.Background()` 사용
- 버튼 클릭 요청이 취소되어도 서버 작업은 계속 진행
- 대기 타임아웃은 Discord 핸들러에서 별도 관리 (`ServerOperationTimeout`)

**handleCrash invariant (2026-01-01)**:
- `handleCrash()`는 **오직 `StateRunning` 상태에서만 호출**되어야 함
- 모든 호출 경로(런타임 워처, 컨테이너 워처, 상태 조회)에서 `StateRunning` 가드가 선행됨
- 함수 내부에서 `currentState != StateRunning`이면 invariant 위반으로 간주하고 로그 후 return
- 이 계약은 코드 리뷰 및 테스트에서 검증되어야 함

**연결된 수동 시나리오**: `1.3 중복 요청 방지`, `3.2 비정상 상태에서의 명령 거부`, `1.1 버튼 클릭 - 서버 시작`

### 상태 임베드 관리 테스트

**파일**: `internal/discord/status_embed_test.go`

**책임**:
- Discord 상태 임베드 업데이트 로직 검증
- Unknown Message 에러 복구 메커니즘 검증
- 재시도 한계 및 에러 처리 확인

**검증 포인트**:
- `isUnknownMessageError`: 다양한 에러 문자열/코드(10008)에 대한 Unknown Message 판별
- 정상 업데이트: `messageID`가 있을 때 `ChannelMessageEditComplex` 한 번 호출
- `messageID` 미설정: "메시지 ID가 설정되지 않음" 에러 반환, 업데이트 시도 안 함
- Unknown Message 복구: 새 메시지 생성 → `messageID` 교체 → 재시도 성공
- 재시도 횟수 초과: 최대 3회(maxRetries=2) 시도 후 "메시지 업데이트 재시도 횟수 초과: unknown message 에러가 지속됨" 에러
- Non-Unknown 에러: 재시도 없이 즉시 실패
- 새 메시지 생성 실패: 에러 반환, 더 이상 재시도 안 함

**연결된 수동 시나리오**: `2.2 임베드 메시지 삭제 시 자동 복구`, `2.3 재시도 횟수 초과`

### 플레이어 트래킹 테스트

**파일**: `internal/mcserver/player_tracker_test.go`

**책임**:
- 로그 기반 플레이어 join/leave 파싱 검증
- 플레이어 목록 정확성 및 동시성 안전성 보장
- 콜백(onChange) 메커니즘 검증

**검증 포인트**:
- **정규식 패턴 매칭**: 다양한 로그 포맷에서 플레이어 이름 추출 (캡처 그룹 필수)
- **중복 join 처리**: 같은 플레이어가 여러 번 join해도 목록에 한 번만 추가
- **존재하지 않는 플레이어 leave**: 목록에 없는 플레이어의 leave는 무시
- **에러 로그 무시**: `Err` 필드가 있는 LogLine은 플레이어 목록에 영향 없음
- **onChange 콜백**: 플레이어 목록 변경 시 콜백 정확히 호출, 올바른 목록 전달
- **동시성 안전성**: 
  - 여러 고루틴에서 join/leave 로그 동시 처리
  - `GetPlayers` 동시 호출 시 race 없음
  - `SetOnChange` 동시 변경 시 안전
  - `Clear` 중에도 안전한 동작
- **잘못된 정규식**: 컴파일 불가능한 패턴 전달 시 에러 반환

**플레이어 목록 관리 규칙**:
1. 로그에서 플레이어 join/leave를 정규식으로 추출
2. 같은 플레이어의 중복 join은 누적되지 않음
3. 존재하지 않는 플레이어의 leave는 무시됨
4. 에러 로그는 플레이어 목록에 영향을 주지 않음
5. 내부적으로 콜백(onChange) 기반으로 상태 변경 전파
6. 다중 고루틴 환경에서 race 없는 동작 보장

**연결된 수동 시나리오**: `2.1 플레이어 접속/퇴장 시 자동 업데이트`

### Docker 로그 팔로우 테스트

**파일**: `internal/dockerctl/dockerctl_test.go`

**책임**:
- `FollowLogs` 함수의 컨텍스트 관리 검증
- 로그 채널 종료 보장 확인
- 동시성 및 빠른 취소 시나리오 안정성 검증

**검증 포인트**:
- **정상 종료**: 존재하지 않는 컨테이너에서도 에러 라인으로 표현, 패닉 없음
- **컨텍스트 취소**: `context.Cancel()` 호출 시 로그 채널 확실히 닫힘
- **즉시 취소**: 이미 취소된 컨텍스트로 호출해도 채널 즉시 닫힘
- **타임아웃 컨텍스트**: `context.WithTimeout` 만료 시 채널 정상 종료
- **동시 취소**: 여러 고루틴에서 동시에 취소/소비해도 데드락 없음
- **빠른 반복 취소**: 50회 연속 생성/취소 반복해도 패닉 없음

**컨텍스트/동시성 보장**:
- 컨텍스트가 취소되거나 타임아웃되면 로그 채널이 확실히 닫힘
- 컨테이너가 존재하지 않아도 패닉 없이 에러 라인으로 표현
- 여러 고루틴에서 동시에 취소/소비해도 데드락/패닉 없음
- 빠른 생성/취소 반복에도 안정적으로 동작

### 로그 멀티플렉서 테스트

**파일**: `internal/mcserver/log_multiplexer_test.go`

**책임**:
- 여러 구독자에게 로그 브로드캐스트 검증
- Start/Stop/Subscribe API 계약 확인
- 컨테이너 재시작 시 재연결 가능성 보장
- 동시성 및 종료 안전성 보장

**검증 포인트**:
- **기본 구독**: Subscribe 호출 시 로그 채널 정상 반환
- **다수 구독자**: 여러 구독자 모두 로그 수신
- **Stop idempotent**: 여러 번 호출해도 패닉 없음
- **Start idempotent**: 이미 running 상태면 무시 (로그만 출력)
- **Stop 후 Subscribe**: 채널은 열려있지만 로그는 전달되지 않음 (재시작 대기 상태)
- **Start 없이 Stop**: 정상 처리
- **동시 Subscribe**: 10개 고루틴에서 동시 구독해도 모두 정상
- **동시 Stop & Subscribe**: Stop과 Subscribe 동시 호출해도 데드락 없음
- **느린 구독자**: 느린 구독자가 있어도 전체 시스템 데드락 없음 (default 분기로 드롭)
- **빠른 Start/Stop**: 50회 연속 Start/Stop 반복해도 패닉 없음
- **재시작 후 재연결**: Stop 후 다시 Start 호출 시 동일 구독자 채널로 새 로그 전달
- **다중 재시작**: 여러 번의 Start/Stop 사이클에서도 구독자 채널 유지

**Start/Stop/Subscribe/Close 규약**:
- 여러 구독자에게 동일한 로그 스트림 브로드캐스트
- **구독자 채널은 LogMultiplexer 수명 동안 유지** (Stop 시에도 닫지 않음, Close 시에만 닫힘)
- Start:
  - 이미 running 상태면 no-op (로그 출력 후 즉시 반환)
  - 아니면 새 context로 FollowLogs 시작, running = true
  - 컨테이너 재시작 시마다 호출 가능 (재연결)
  - Close 이후에는 호출해도 무시됨
- Stop:
  - 현재 FollowLogs context를 취소하고 run goroutine 종료 대기
  - running = false로 설정
  - **구독자 채널은 닫지 않음** (다음 Start에서 재사용)
  - Close 이후에는 호출해도 무시됨
- Subscribe:
  - 언제든 호출 가능, 새 버퍼 채널(cap=100) 생성 및 등록
  - 채널은 LogMultiplexer 수명 동안 유지
  - Start 상태에 따라 로그 전달 여부가 결정됨
  - Close 이후에는 호출 시 이미 닫힌 채널 반환 (즉시 range 종료)
- Close:
  - LogMultiplexer의 수명을 종료하는 메서드
  - 내부적으로 Stop() 호출하여 로그 팔로우 중단
  - 모든 구독자 채널을 닫고 목록 비움
  - 이후 Start/Stop/Subscribe 호출은 무시되거나 닫힌 채널 반환
  - Controller.Shutdown()에서만 호출됨
- Subscribe/Stop/Start 동시 호출해도 데드락/패닉 없음
- 느린 구독자는 default 분기로 드롭되어 전체 시스템 블로킹 방지

## 수동 검증 시나리오

### 1. Interaction 핸들러 검증

#### 1.1 버튼 클릭 - 서버 시작

**전제 조건**: 서버가 종료된 상태

**절차**:
1. Discord 채널에서 상시 임베드 메시지 확인
2. "서버 열기" 버튼 클릭
3. **즉시 임베드가 "🟡 여는중" 상태로 변경되고 버튼이 "서버 여는중"(비활성)으로 변경되는지 확인**
4. **서버 시작 진행 중에도 "여는중" 상태가 유지되는지 확인 (몇 초~수십 초)**
5. 서버 시작 완료 후 임베드가 "🟢 열림" 상태로 변경되고 버튼이 "서버 닫기"로 변경되는지 확인
6. ephemeral 메시지로 성공 알림이 오는지 확인

**예상 결과**:
- **버튼 클릭 즉시 상태가 "여는중"으로 변경되고 이 상태가 서버 로드 완료까지 유지됨**
- **버튼이 "서버 여는중"으로 표시되고 비활성화되어 중복 클릭 방지**
- 서버 시작 완료 시 "열림"으로 변경
- 시작 소요 시간이 표시됨
- 요청자 이름이 포함된 성공 메시지

**실패 케이스**:
- 컨테이너가 없는 경우: 명확한 에러 메시지와 해결 방법 안내, 상태는 "닫힘"으로 복귀
- ReadyTimeout 발생: 타임아웃 메시지와 로그 확인 안내, 상태는 "오류"로 전환

**구현 세부사항 (2024-12-23 최종)**:
- `handleButtonStart`는 `Controller.Start` 호출 직후와 완료 후 각각 `statusEmbed.Update(ctx)` 호출
- `Update` 메서드는 내부적으로 `Controller.Presence(ctx)`를 조회하여 항상 최신 상태 반영
- **`Status()` 자기 교정 로직 완전 재설계**:
  - **컨테이너 Running 여부와 무관하게 `StateStarting` 유지**
  - **오직 ready 로그(Done 플래그) 인식 시에만 `StateRunning`으로 전이**
  - 성공/실패 전이는 `Controller.Start` 내부에서만 수행
- 이를 통해 "여는중" 상태가 **ready 로그 인식 시점까지 정확히 유지**되어 사용자가 원하는 동작 구현

#### 1.2 버튼 클릭 - 서버 종료

**전제 조건**: 서버가 실행 중인 상태

**절차**:
1. Discord 채널에서 상시 임베드 메시지 확인
2. "서버 종료" 버튼 클릭
3. 즉시 임베드가 "종료 중..." 상태로 변경되는지 확인
4. 서버 종료 완료 후 임베드가 "종료됨" 상태로 변경되는지 확인
5. ephemeral 메시지로 성공 알림이 오는지 확인

**예상 결과**:
- 버튼 클릭 즉시 상태가 "종료 중"으로 변경
- 서버 종료 완료 시 "종료됨"으로 변경
- 요청자 이름이 포함된 성공 메시지

#### 1.3 중복 요청 방지

**절차**:
1. 서버가 종료된 상태에서 "서버 시작" 버튼 클릭
2. 시작 중 상태에서 다시 "서버 시작" 버튼 클릭

**예상 결과**:
- "서버가 이미 시작 중입니다." 에러 메시지

**절차**:
1. 서버가 실행 중인 상태에서 "서버 시작" 버튼 클릭

**예상 결과**:
- "서버가 이미 실행 중입니다." 에러 메시지

**절차**:
1. 서버가 실행 중인 상태에서 "서버 종료" 버튼 클릭
2. 종료 중 상태에서 다시 "서버 종료" 버튼 클릭

**예상 결과**:
- "서버가 이미 종료 중입니다." 에러 메시지

> **자동화 테스트**: 이 시나리오의 핵심 로직은 `controller_start_test.go`, `controller_stop_test.go`에서 검증됩니다. 각 상태에서의 명령 거부와 에러 메시지가 단위 테스트로 자동 확인됩니다.

#### 1.4 권한 검증

**절차**:
1. mcbot 역할이 없는 사용자로 버튼 클릭

**예상 결과**:
- ephemeral 메시지로 권한 부족 안내
- 상태 임베드는 변경되지 않음

#### 1.5 슬래시 커맨드 (더 이상 지원하지 않음)

**절차**:
1. 봇 로그를 모니터링
2. (테스트 목적으로) 슬래시 커맨드 입력

**예상 결과**:
- 사용자에게 ephemeral 메시지로 "이 봇은 슬래시 커맨드를 더 이상 지원하지 않습니다. 버튼을 통해 서버를 제어해주세요." 안내
- 로그에 "슬래시 커맨드 수신 (더 이상 지원하지 않음): [커맨드명] (ID: [ID], GuildID: [GuildID], UserID: [UserID])" 기록
- 봇이 크래시하지 않음

#### 1.6 기타 지원하지 않는 Interaction 타입

**절차**:
1. 봇 로그를 모니터링
2. (테스트 목적으로) Modal 등 다른 타입의 interaction 전송

**예상 결과**:
- 사용자에게 ephemeral 메시지로 "지원하지 않는 인터랙션 타입입니다." 안내
- 로그에 "지원하지 않는 인터랙션 타입: [타입] (ID: [ID], GuildID: [GuildID], UserID: [UserID])" 기록
- 봇이 크래시하지 않음

### 2. 상태 임베드 업데이트 검증

#### 2.1 플레이어 접속/퇴장 시 자동 업데이트

**전제 조건**: 서버가 실행 중인 상태

**절차**:
1. 마인크래프트 서버에 플레이어 접속
2. Discord 상시 임베드에서 플레이어 목록 확인
3. 플레이어 퇴장
4. Discord 상시 임베드에서 플레이어 목록 확인

**예상 결과**:
- 플레이어 접속/퇴장 시 자동으로 임베드 업데이트
- 현재 접속 중인 플레이어 목록 정확히 표시
- 플레이어 수 정확히 표시

> **자동화 테스트**: 플레이어 목록 관리 로직은 `player_tracker_test.go`에서 검증됩니다. 로그 파싱, 중복 join 처리, 존재하지 않는 플레이어 leave 무시, 동시성 안전성 등이 단위 테스트로 자동 확인됩니다.

#### 2.2 임베드 메시지 삭제 시 자동 복구

**절차**:
1. Discord에서 상시 임베드 메시지 수동 삭제
2. 서버 상태 변경 또는 플레이어 접속/퇴장 발생

**예상 결과**:
- 로그에 "상시 임베드 메시지가 삭제되었습니다. 새 메시지를 생성합니다." 출력
- 새로운 임베드 메시지 자동 생성
- 최대 3회까지 재시도 (maxUnknownMessageRetries = 2)

**추가 에러 처리 규칙**:
- 메시지 ID가 설정되지 않은 경우: 업데이트 시도 없이 에러 반환
- Unknown Message가 아닌 Discord 에러: 재시도 없이 즉시 실패
- 새 메시지 생성 실패: 더 이상 재시도하지 않고 에러 반환

> **자동화 테스트**: Unknown Message 복구 메커니즘은 `status_embed_test.go`에서 검증됩니다. 재시도 로직, messageID 교체, 다양한 에러 케이스가 단위 테스트로 자동 확인됩니다.

#### 2.3 재시도 횟수 초과

**절차**:
1. (테스트 환경에서) Discord API가 지속적으로 unknown message 에러를 반환하도록 설정
2. 상태 업데이트 시도

**예상 결과**:
- 로그에 재시도 진행 상황 출력
- 최종적으로 "메시지 업데이트 재시도 횟수 초과" 에러 로그
- 봇이 크래시하지 않고 계속 실행

> **자동화 테스트**: 재시도 한계 처리는 `status_embed_test.go`에서 검증됩니다. 최대 3회 시도 후 에러 반환이 단위 테스트로 자동 확인됩니다.

### 3. 상태 전이 검증

#### 3.1 정상적인 상태 전이 흐름

**절차**:
1. 초기 상태: Stopped
2. Start 명령 → Starting → Running
3. Stop 명령 → Stopping → Stopped

**예상 결과**:
- 각 단계에서 상태가 정확히 전이
- 임베드에 현재 상태 정확히 표시
- 로그에 상태 전이 기록

#### 3.2 비정상 상태에서의 명령 거부

**테스트 케이스**:
- Starting 상태에서 Start 명령 → "이미 시작 중" 에러
- Running 상태에서 Start 명령 → "이미 실행 중" 에러
- Stopping 상태에서 Start 명령 → "종료 중, 종료 후 다시 시도" 에러
- Stopped 상태에서 Stop 명령 → "이미 종료됨" 에러
- Starting 상태에서 Stop 명령 → "시작 중, 시작 후 다시 시도" 에러
- Stopping 상태에서 Stop 명령 → "이미 종료 중" 에러

**예상 결과**:
- 모든 케이스에서 명확한 에러 메시지
- 상태는 변경되지 않음

> **자동화 테스트**: 모든 상태 전이 규칙과 명령 거부 로직은 `controller_start_test.go`, `controller_stop_test.go`에서 검증됩니다. 각 상태 조합에 대한 에러 메시지와 상태 유지가 단위 테스트로 자동 확인됩니다.

### 4. 동시성 및 안정성 검증

#### 4.1 동시 버튼 클릭

**절차**:
1. 여러 사용자가 동시에 같은 버튼 클릭

**예상 결과**:
- 첫 번째 요청만 처리
- 나머지는 "이미 [상태]" 에러 메시지
- 상태 불일치 없음

#### 4.2 장시간 실행 안정성

**절차**:
1. 봇을 24시간 이상 실행
2. 주기적으로 서버 시작/종료 반복
3. 플레이어 접속/퇴장 반복

**예상 결과**:
- 메모리 누수 없음
- 고루틴 누수 없음
- 안정적인 상태 관리

#### 4.3 내부 컴포넌트 동시성 보장

이 섹션은 사용자에게 직접 보이지 않지만, 봇의 안정성을 위해 내부적으로 보장되는 동시성 안전성을 설명합니다.

**PlayerTracker 동시성**:
- 여러 고루틴에서 동시에 join/leave 로그 처리 가능
- `GetPlayers()` 동시 호출 시 race condition 없음
- `SetOnChange()` 콜백 변경 중에도 안전
- `Clear()` 호출 중에도 다른 작업 안전하게 진행
- **검증**: `player_tracker_test.go`의 동시성 테스트

**LogMultiplexer 동시성**:
- 여러 구독자가 동시에 Subscribe 호출 가능
- Stop과 Subscribe 동시 호출 시 데드락 없음
- 느린 구독자가 있어도 전체 시스템 블로킹 없음
- Start/Stop 여러 번 호출해도 안전 (idempotent)
- **검증**: `log_multiplexer_test.go`의 동시성 테스트

**FollowLogs 컨텍스트 관리**:
- 컨텍스트 취소 시 로그 채널 확실히 종료
- 타임아웃 발생 시 정상 종료
- 여러 고루틴에서 동시 취소/소비 시 데드락 없음
- 빠른 생성/취소 반복에도 패닉 없음
- **검증**: `dockerctl_test.go`의 컨텍스트/동시성 테스트

## 회귀 테스트

### 전체 테스트 스위트 실행

```bash
# 전체 테스트 실행
go test -v ./...

# 실패 시 즉시 중단
go test -v -failfast ./...

# 병렬 실행 (기본값: GOMAXPROCS)
go test -v -parallel 4 ./...
```

### 성능 테스트

```bash
# 벤치마크 실행
go test -bench=. ./...

# CPU 프로파일링
go test -cpuprofile=cpu.prof -bench=. ./...
go tool pprof cpu.prof

# 메모리 프로파일링
go test -memprofile=mem.prof -bench=. ./...
go tool pprof mem.prof
```

## 테스트 체크리스트

### 코드 변경 후 필수 확인 사항

- [ ] `go build` 성공
- [ ] `go vet ./...` 경고 없음
- [ ] `go test ./...` 모두 통과
- [ ] 관련 수동 시나리오 검증 완료
- [ ] 로그 레벨 적절히 설정
- [ ] 에러 메시지 사용자 친화적
- [ ] 상태 전이 규칙 준수

### 배포 전 최종 확인

- [ ] 전체 자동화 테스트 통과
- [ ] 주요 수동 시나리오 검증
- [ ] 로그 모니터링 정상
- [ ] 메모리/CPU 사용량 정상
- [ ] Discord API rate limit 준수
- [ ] 환경 변수 및 설정 확인

## 문제 해결

### 테스트 실패 시

1. 실패한 테스트 로그 확인
2. 관련 코드 변경 사항 검토
3. 로컬에서 해당 테스트만 실행하여 재현
4. 필요시 디버거 사용
5. 상태 전이 규칙 위반 여부 확인

### 봇 동작 이상 시

1. 로그 파일 확인
2. 현재 상태 확인 (`Status` 메서드)
3. Docker 컨테이너 상태 확인
4. Discord API 상태 확인
5. 환경 변수 및 설정 확인

## 참고 사항

### 테스트 작성 가이드

- 테스트 이름은 명확하고 설명적으로
- 각 테스트는 독립적으로 실행 가능해야 함
- 외부 의존성은 목(mock)으로 대체
- 타임아웃 설정으로 무한 대기 방지
- 동시성 테스트는 race detector와 함께 실행

### 코드 품질 유지

```bash
# 필수: 정적 분석 (프로젝트 표준)
go vet ./...

# 필수: Race detector 활성화 (동시성 버그 검출)
go test -race ./...

# 필수: 코드 포맷팅
go fmt ./...

# 선택: 추가 정적 분석 도구 (별도 설치 필요)
# staticcheck ./...
# goimports -w .
```
