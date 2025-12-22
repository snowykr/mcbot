# 마크봇 (MCBot)

디스코드 상시 임베드 메시지를 통해 마인크래프트 서버를 제어하는 봇입니다.

## 기능

### 상시 임베드 메시지
- 지정된 채널에 서버 상태를 실시간으로 표시하는 고정 메시지
- 서버 상태 표시: 🟢 열림 / 🟡 여는중, 닫는중 / 🔴 닫힘
- 현재 접속 중인 플레이어 목록 실시간 업데이트
- 메시지가 삭제되거나 누락된 경우 자동으로 새 메시지를 생성하여 복구
- 버튼을 통한 서버 시작/종료 제어
  - `서버 열기` - 서버가 닫혀있을 때 표시
  - `서버 닫기` - 서버가 열려있을 때 표시
  - `서버 여는중` - 서버 시작 중일 때 표시 (비활성화)

### 자동 상태 동기화
- 컨테이너가 이미 실행 중인 상태에서 봇만 재시작돼도 로그 스트림을 재연결
- 재연결된 로그를 기반으로 플레이어 트래커가 즉시 join/leave 이벤트를 수집
- 버튼 인터랙션은 서버 상태 enum(`internal/state`)에 기반해 안전하게 분기

## 사전 요구사항

1. Discord 봇 생성 및 토큰 발급
   - [Discord Developer Portal](https://discord.com/developers/applications)에서 애플리케이션 생성
   - Bot 섹션에서 토큰 발급
   - OAuth2 > URL Generator에서 `bot`, `applications.commands` 스코프 선택
   - 생성된 URL로 서버에 봇 초대 (https://discord.com/oauth2/authorize?client_id=1444413693097152563&permissions=2147485696&integration_type=0&scope=bot+applications.commands)

2. 디스코드 서버에 `마크봇` 역할 생성
   - 서버 설정 > 역할에서 `마크봇` 역할 생성
   - 봇을 사용할 멤버에게 해당 역할 부여

3. 상시 임베드 메시지를 표시할 채널 ID 확인
   - Discord 개발자 모드 활성화 (사용자 설정 > 고급 > 개발자 모드)
   - 채널을 우클릭하여 "ID 복사"

## 설치 및 실행

### 1. 환경 변수 설정

```bash
cp .env.example .env
```

`.env` 파일을 열고 다음 필수 항목을 입력합니다:
- `DISCORD_TOKEN`: Discord 봇 토큰
- `EMBED_CHANNEL_ID`: 상시 임베드 메시지를 표시할 채널 ID

### 2. 봇 실행 (권장)

가장 간단한 방법은 Make를 사용하는 것입니다:

```bash
make up
```

이 명령은 다음을 자동으로 수행합니다:
- mc-server 컨테이너가 없으면 생성 (실행하지는 않음)
- mcbot 컨테이너를 빌드하고 실행

이후 **디스코드 상시 임베드 메시지의 버튼**으로 서버를 시작/종료할 수 있습니다.

### 3. 디스코드에서 사용

- 상시 임베드 메시지에 표시되는 버튼으로 서버 시작/종료를 제어합니다.
- 임베드 메시지가 삭제된 경우 자동으로 복구되므로 따로 조치할 필요가 없습니다.

---

## 고급 설정

### 수동으로 컨테이너 생성 및 봇 실행

세부적인 제어가 필요한 경우, 단계별로 실행할 수 있습니다.

#### 1) 마인크래프트 서버 컨테이너 생성 (최초 1회)

mc-server 컨테이너를 **생성만 하고 실행하지 않습니다**.

**Make 사용:**
```bash
make ensure-mc
```

**Docker Compose v2:**
```bash
docker compose create mc-server
# 또는
docker compose up --no-start mc-server
```

**Docker Compose v1:**
```bash
docker-compose create mc-server
# 또는
docker-compose up --no-start mc-server
```

#### 2) 봇만 실행

```bash
docker compose up --build -d mcbot
```

### 기존 환경에서 전환하는 방법

이미 `docker compose up -d` 를 사용해 mc-server가 실행 중이라면:

```bash
# 1. 서버 중지 (컨테이너는 유지)
docker compose stop mc-server

# 2. 이후부터는 봇만 실행
docker compose up -d mcbot
```

이후 서버 시작/종료는 상시 임베드 메시지의 버튼을 통해 수행합니다.

## Make 명령어 레퍼런스

프로젝트는 편의를 위해 다양한 Make 타겟을 제공합니다.

### 기본 명령어

| 명령어 | 설명 |
|--------|------|
| `make up` | **(권장)** mc-server 컨테이너 생성 + mcbot 빌드 및 실행 |
| `make down` | 모든 컨테이너 중지 및 제거 |
| `make logs` | 실시간 로그 확인 (Ctrl+C로 종료) |

### 고급 명령어

| 명령어 | 설명 |
|--------|------|
| `make ensure-mc` | mc-server 컨테이너가 없으면 생성 (멱등성 보장) |
| `make up-all` | mcbot과 mc-server를 모두 빌드하고 실행 |
| `make up-mc` | mc-server만 빌드하고 실행 |
| `make nuke` | 모든 컨테이너, 볼륨, 네트워크 제거 (완전 초기화) |

### 개발 명령어

| 명령어 | 설명 |
|--------|------|
| `make go-deps` | Go 의존성 정리 (`go mod tidy`) |
| `make go-build` | mcbot 바이너리 빌드 |

## 환경 변수

| 변수명 | 필수 | 기본값 | 설명 |
|--------|------|--------|------|
| `DISCORD_TOKEN` | ✅ | - | Discord 봇 토큰 |
| `EMBED_CHANNEL_ID` | ✅ | - | 상시 임베드 메시지를 표시할 채널 ID |
| `MC_CONTAINER_NAME` | ❌ | `stardew_create_forge_server` | MC 서버 컨테이너 이름 |
| `MCBOT_ROLE_NAME` | ❌ | `마크봇` | 봇 사용 권한 역할 이름 |
| `READY_LOG_PATTERN` | ❌ | `Dedicated server took` | 서버 준비 완료 로그 패턴 |
| `READY_TIMEOUT_SECONDS` | ❌ | `600` | 서버 시작 타임아웃 (초) |
| `STOP_TIMEOUT_SECONDS` | ❌ | `120` | 서버 종료 타임아웃 (초) |
| `MC_JOIN_LOG_PATTERN` | ❌ | `]: (.+) joined the game` | 플레이어 접속 로그 패턴 (정규식) |
| `MC_LEAVE_LOG_PATTERN` | ❌ | `]: (.+) left the game` | 플레이어 퇴장 로그 패턴 (정규식) |

## 프로젝트 구조

```
.
├── Makefile                    # Make 타겟 정의
├── docker-compose.yml          # Docker Compose 설정
├── .env.example                # 환경 변수 템플릿
├── .gitignore
├── data/                       # MC 서버 데이터 (gitignore)
├── docs/
│   └── README.md               # 프로젝트 문서
└── mcbot/                      # Go 애플리케이션
    ├── cmd/
    │   └── mcbot/
    │       └── main.go         # 애플리케이션 엔트리포인트
    ├── internal/
    │   ├── config/             # 환경 변수 로딩
    │   │   └── config.go
    │   ├── discord/            # Discord 통합
    │   │   ├── embed.go        # 임베드 메시지 생성
    │   │   ├── handler.go      # 버튼 인터랙션 핸들러
    │   │   └── status_embed.go # 상시 임베드 관리
    │   ├── dockerctl/          # Docker CLI 래퍼
    │   │   ├── dockerctl.go
    │   │   └── dockerctl_test.go
    │   ├── mcserver/           # MC 서버 제어 로직
    │   │   ├── controller.go           # 서버 시작/종료 제어
    │   │   ├── log_multiplexer.go      # 로그 스트림 멀티플렉싱
    │   │   ├── log_multiplexer_test.go
    │   │   ├── player_tracker.go       # 플레이어 접속 추적
    │   │   ├── player_tracker_test.go
    │   │   └── presence.go             # 서버 상태 표현
    │   └── state/              # 서버 상태 관리
    │       └── state.go
    ├── Dockerfile              # mcbot 컨테이너 이미지
    ├── go.mod                  # Go 모듈 정의
    └── go.sum                  # Go 의존성 체크섬
```

## 로컬 개발

### Make 사용 (권장)

```bash
# 의존성 정리
make go-deps

# 빌드
make go-build

# 실행 (환경 변수 필요)
DISCORD_TOKEN=your_token ./mcbot/mcbot
```

### 직접 Go 명령어 사용

```bash
# 의존성 설치
cd mcbot && go mod tidy

# 빌드
cd mcbot && go build -o mcbot ./cmd/mcbot

# 실행 (환경 변수 필요)
cd mcbot && DISCORD_TOKEN=your_token ./mcbot
```
