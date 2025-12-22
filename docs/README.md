# 마크봇 (MCBot)

디스코드 상시 임베드 메시지를 통해 마인크래프트 서버를 제어하는 봇입니다.  
슬래시 명령어는 단계적으로 제거할 예정이며, 상시 임베드 메시지와 버튼만으로 서버를 제어하는 구조를 목표로 하고 있습니다.

## 기능

### 상시 임베드 메시지
- 지정된 채널에 서버 상태를 실시간으로 표시하는 고정 메시지
- 서버 상태 표시: 🟢 열림 / 🟡 여는중 / 🔴 닫힘
- 현재 접속 중인 플레이어 목록 실시간 업데이트
- 메시지가 삭제되거나 누락된 경우 자동으로 새 메시지를 생성하여 복구
- 버튼을 통한 서버 시작/종료 제어
  - `서버 열기` - 서버가 닫혀있을 때 표시
  - `서버 닫기` - 서버가 열려있을 때 표시
  - `서버 여는중` - 서버 시작 중일 때 표시 (비활성화)

### 자동 상태 동기화
- 컨테이너가 이미 실행 중인 상태에서 봇만 재시작돼도 로그 스트림을 재연결
- 재연결된 로그를 기반으로 플레이어 트래커가 즉시 join/leave 이벤트를 수집
- 슬래시 명령어/버튼 인터랙션은 서버 상태 enum(`internal/state`)에 기반해 안전하게 분기

### 레거시 슬래시 명령어 (제거 예정)
- 상시 임베드 버튼 방식이 기본 제어 수단입니다.
- 기존 슬래시 명령어(`/마크봇 action:켜기/끄기/상태`)는 완전 제거 전까지 한시적으로만 유지됩니다.
- 새 기능 개발 시 슬래시 명령어를 더 이상 고려하지 않습니다.

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

### 2. 마인크래프트 서버 컨테이너 생성 (최초 1회)

mc-server 컨테이너를 **생성만 하고 실행하지 않습니다**.  
이후 서버 시작/종료는 항상 디스코드 명령어로만 제어합니다.

```bash
docker compose create mc-server
```

> **참고**: `docker compose up --no-start mc-server` 도 동일한 효과입니다.

### 3. 봇 실행

```bash
docker compose up --build -d mcbot
```

이 명령은 **mcbot만 실행**합니다. `mc-server` 는 자동으로 시작되지 않습니다.

### 4. 디스코드에서 사용

- 상시 임베드 메시지에 표시되는 버튼으로 서버 시작/종료를 제어합니다.
- 임베드 메시지가 삭제된 경우 자동으로 복구되므로 따로 조치할 필요가 없습니다.
- (레거시) `/마크봇 action:*` 명령어는 완전 제거 시점까지 임시 지원됩니다.

### 기존 환경에서 전환하는 방법

이미 `docker compose up -d` 를 사용해 mc-server가 실행 중이라면:

```bash
# 1. 서버 중지 (컨테이너는 유지)
docker compose stop mc-server

# 2. 이후부터는 봇만 실행
docker compose up -d mcbot
```

이후 서버 시작/종료는 상시 임베드 메시지의 버튼을 통해 수행합니다. (레거시: `/마크봇 action:켜기/끄기` 명령어는 제거 예정)

### (참고) 전체 서비스 실행

아래 명령은 mcbot과 mc-server를 **모두 실행**합니다.  
서버를 디스코드 명령으로만 제어하고 싶다면 이 명령은 사용하지 않는 것을 권장합니다.

```bash
docker compose up -d
```

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
├── docs/
│   └── README.md        # 문서
├── mcbot/               # Go 애플리케이션
│   ├── cmd/mcbot/       # 엔트리포인트
│   │   └── main.go
│   ├── internal/
│   │   ├── config/      # 환경 변수 로딩
│   │   ├── discord/     # 디스코드 명령어 및 핸들러
│   │   ├── dockerctl/   # Docker CLI 래퍼
│   │   ├── mcserver/    # MC 서버 제어 로직
│   │   └── state/       # 서버 상태 관리
│   ├── Dockerfile
│   ├── go.mod
│   └── go.sum
├── docker-compose.yml
├── .env.example
└── .gitignore
```

## 로컬 개발

```bash
# 의존성 설치
go mod tidy

# 빌드
go build -o mcbot ./cmd/mcbot

# 실행 (환경 변수 필요)
DISCORD_TOKEN=your_token ./mcbot
```
