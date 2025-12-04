# 마크봇 (MCBot)

디스코드 슬래시 명령어로 마인크래프트 서버를 제어하는 봇입니다.

## 기능

- `/마크봇 action:켜기` - 마인크래프트 서버 시작
- `/마크봇 action:끄기` - 마인크래프트 서버 종료 (graceful shutdown)
- `/마크봇 action:상태` - 서버 상태 확인

## 사전 요구사항

1. Discord 봇 생성 및 토큰 발급
   - [Discord Developer Portal](https://discord.com/developers/applications)에서 애플리케이션 생성
   - Bot 섹션에서 토큰 발급
   - OAuth2 > URL Generator에서 `bot`, `applications.commands` 스코프 선택
   - 생성된 URL로 서버에 봇 초대 (https://discord.com/oauth2/authorize?client_id=1444413693097152563&permissions=2147485696&integration_type=0&scope=bot+applications.commands)

2. 디스코드 서버에 `마크봇` 역할 생성
   - 서버 설정 > 역할에서 `마크봇` 역할 생성
   - 봇을 사용할 멤버에게 해당 역할 부여

## 설치 및 실행

### 1. 환경 변수 설정

```bash
cp .env.example .env
```

`.env` 파일을 열고 `DISCORD_TOKEN`에 봇 토큰을 입력합니다.

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

이 명령은 **mcbot만 실행**합니다. mc-server는 자동으로 시작되지 않습니다.

### 4. 디스코드에서 사용

- `/마크봇 action:켜기` - 서버 시작
- `/마크봇 action:끄기` - 서버 종료
- `/마크봇 action:상태` - 상태 확인

### 기존 환경에서 전환하는 방법

이미 `docker compose up -d` 를 사용해 mc-server가 실행 중이라면:

```bash
# 1. 서버 중지 (컨테이너는 유지)
docker compose stop mc-server

# 2. 이후부터는 봇만 실행
docker compose up -d mcbot
```

이후 서버 시작/종료는 항상 `/마크봇 action:켜기/끄기` 명령어를 사용합니다.

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
| `MC_CONTAINER_NAME` | ❌ | `stardew_create_forge_server` | MC 서버 컨테이너 이름 |
| `MCBOT_ROLE_NAME` | ❌ | `마크봇` | 봇 사용 권한 역할 이름 |
| `READY_LOG_PATTERN` | ❌ | `Dedicated server took` | 서버 준비 완료 로그 패턴 |
| `READY_TIMEOUT_SECONDS` | ❌ | `600` | 서버 시작 타임아웃 (초) |
| `STOP_TIMEOUT_SECONDS` | ❌ | `120` | 서버 종료 타임아웃 (초) |

## 프로젝트 구조

```
mcbot/
├── cmd/mcbot/          # 엔트리포인트
│   └── main.go
├── internal/
│   ├── config/         # 환경 변수 로딩
│   ├── discord/        # 디스코드 명령어 및 핸들러
│   ├── dockerctl/      # Docker CLI 래퍼
│   ├── mcserver/       # MC 서버 제어 로직
│   └── state/          # 서버 상태 관리
├── Dockerfile
├── go.mod
└── README.md
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
