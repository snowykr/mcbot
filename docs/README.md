# 마크봇 (MCBot)

디스코드 상시 임베드 메시지를 통해 마인크래프트 서버를 제어하는 봇입니다.

## 운영 기준

- CLI is the canonical operational source of truth.
- `mcbot bot run` starts the Discord bot runtime.
- Guided onboarding starts with `mcbot setup` or `make setup`; direct `config ...` and `env ...` commands stay available for surgical edits and automation.
- `mcbot server start|stop|status` do not require Discord credentials.
- server status is Docker-derived.
- Secret values are masked by default, and `--show-secrets` only applies to `env show|get`.
- `--no-input` makes prompt-capable paths fail with exit code 2.

## 기능

### 상시 임베드 메시지
- 지정된 채널에 서버 상태를 실시간으로 표시하는 고정 메시지
- 서버 상태 표시: 🟢 열림 / 🟡 여는중, 닫는중 / 🔴 닫힘, 크래시 / ⚫ 봇 오프라인
- 현재 접속 중인 플레이어 목록 실시간 업데이트
- 메시지가 삭제되거나 누락된 경우 자동으로 새 메시지를 생성하여 복구
- 버튼을 통한 서버 시작/종료 제어
  - `서버 열기` - 서버가 닫혀있을 때 표시, 클릭 시 즉시 서버 시작
  - `서버 닫기` - 서버가 열려있을 때 표시, 클릭 시 **에페머럴 확인 메시지** 발송
    - 확인 메시지는 버튼을 누른 사용자에게만 표시됨
    - 이 메시지에서 `닫기` 버튼을 눌러야 실제로 서버가 종료됨
    - `취소` 버튼으로 안전하게 되돌릴 수 있음
    - 다른 사용자는 해당 확인/취소 버튼을 사용할 수 없음
  - `서버 여는중` - 서버 시작 중일 때 표시 (비활성화)
  - `서버 닫는중` - 서버 종료 중일 때 표시 (비활성화)
  - `봇 오프라인` - 봇이 종료되었을 때 표시 (비활성화, 회색)

### 자동 상태 동기화
- 컨테이너가 이미 실행 중인 상태에서 봇만 재시작돼도 로그 스트림을 재연결
- 재연결된 로그를 기반으로 플레이어 트래커가 즉시 join/leave 이벤트를 수집
- 버튼 인터랙션은 서버 상태 enum(`internal/state`)에 기반해 안전하게 분기

### 봇 오프라인 상태 표시
- 봇이 정상 종료될 때 상시 임베드가 "⚫ 봇 오프라인" 상태로 변경됨
- 버튼이 "봇 오프라인"으로 표시되고 비활성화되어 사용자에게 봇 상태를 명확히 전달
- 봇이 다시 시작되면 자동으로 현재 서버 상태로 업데이트됨

### RCON 명령어 (선택적 기능)
- `/마크봇 rcon <command>` 슬래시 명령어로 마인크래프트 서버에 직접 명령 실행
- `MCBOT_TRUSTED_GUILD_ID`로 지정한 Discord 서버에서만 실행 가능
- `마크봇` 역할을 가진 사용자만 사용 가능
- 서버가 실행 중(Running)일 때만 명령어 실행 가능
- 응답이 길 경우 자동으로 1800자로 잘림
- **활성화 조건**: `RCON_PASSWORD` 환경 변수 설정 시 자동 활성화
- **미설정 시**: 명령어 실행 시 설정 안내 메시지 표시

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

4. 봇을 신뢰할 Discord 서버(길드) ID 확인
   - Discord 개발자 모드 활성화
   - 서버 아이콘을 우클릭하여 "ID 복사"

## 설치 및 실행

### 1. 환경 변수 설정

```bash
cp .env.example .env
```

`.env` 파일을 열고 다음 항목을 입력합니다:
- `DISCORD_TOKEN`: Discord 봇 토큰
- `MCBOT_TRUSTED_GUILD_ID`: 봇의 privileged 기능을 허용할 Discord 서버 snowflake ID (숫자 문자열)
- `EMBED_CHANNEL_ID`: 상시 임베드 메시지의 초기 기본 채널 ID (선택, 런타임 설정이 없을 때 사용하는 fallback)

`EMBED_CHANNEL_ID`는 반드시 `MCBOT_TRUSTED_GUILD_ID`와 같은 서버에 속한 채널이어야 합니다. 다른 서버 채널을 지정하면 버튼은 표시될 수 있어도 실행은 거부됩니다.

상시 임베드 채널은 런타임에도 바꿀 수 있습니다. `/마크봇 채널 설정 channel:<채널>` 명령으로 바꾸면 설정은 `./data/mcbot/runtime-config.json`에 저장되고, 이 값이 `.env`의 `EMBED_CHANNEL_ID`보다 우선합니다. `./data/mcbot`는 기존 `./data` 트리 안에 있는 공유 경로입니다.

런타임 설정은 같은 `MCBOT_TRUSTED_GUILD_ID`에서 저장된 경우에만 `EMBED_CHANNEL_ID`보다 우선합니다. 신뢰 서버를 바꾸면 이전 서버에서 저장된 런타임 설정은 부팅 시 무시/삭제되고 `.env` 기본값으로 되돌아갑니다. 이전 버전의 guild scope가 없는 `runtime-config.json`도 안전을 위해 legacy 설정으로 보고 삭제됩니다.

런타임 채널 override 또는 비활성화 설정이 한 번 저장되면 새 값으로 바꾸거나 `/마크봇 채널 기본값`으로 지우기 전까지 계속 우선합니다. 런타임 파일이 손상되면 경고를 남기고 안전하게 `.env` 기본값으로 되돌아갑니다.

채널을 바꿔도 이전 채널의 상시 임베드 메시지는 자동으로 삭제되지 않습니다.

`mc-server.toml`은 mc-server 관련 설정의 기준 파일입니다. `.env.example`은 봇/배포/비밀값만 다룹니다.

기본 UID/GID는 일반적인 첫 Linux 사용자와 맞는 `1000:1000`입니다. `mcbot setup config`는 가능하면 기존 `./data` 디렉터리 소유자 또는 현재 실행 사용자의 UID/GID를 감지해 추천합니다. 기존 Oracle Cloud/레거시 볼륨처럼 이미 `1001:1001`로 파일이 만들어진 환경에서는 setup의 `Custom UID/GID`에서 `1001:1001`을 선택하거나, 먼저 기존 `/data` 볼륨의 소유권을 새 UID/GID에 맞춰 조정하세요.

이전 버전의 `.env`에 있던 mc-server 설정 키는 마이그레이션 중 읽기 전용으로 허용됩니다. 특히 UID/GID 한 쌍은 `mc-server.toml`이 기본 `1000:1000`인 동안 기존 볼륨 권한을 보호하기 위해 우선 적용됩니다. setup에서 값을 `mc-server.toml`로 옮긴 뒤에는 레거시 키를 `.env`에서 제거하세요.

`mc-server.toml` 값을 바꾼 뒤 운영 반영은 기본적으로 canonical CLI 경로인 `mcbot server stop` 후 `mcbot server start`를 사용하세요. 아래 raw Compose 재생성 명령은 canonical CLI/TOML bridge를 우회하는 low-level/manual escape hatch입니다. CLI가 아닌 Compose 동작 자체를 직접 점검해야 할 때만 사용하세요:

```bash
docker compose stop mc-server
docker compose rm -sf mc-server && docker compose create mc-server
```

### 2. 추천 시작 경로: guided setup

처음 설치한 뒤에는 `setup` 흐름으로 시작하는 것이 권장됩니다. 이 경로는 `.env`와 `mc-server.toml`을 순서대로 안내하고, 직접 `config ...` 나 `env ...` 명령을 써서 한 파일씩 바꾸는 경로는 그대로 남겨 둡니다.

```bash
cd mcbot && ./mcbot setup
cd mcbot && ./mcbot setup env
cd mcbot && ./mcbot setup config

# Make는 얇은 래퍼만 제공합니다.
make setup
make setup-env
make setup-config
```

`setup`은 안내형 워크플로우입니다. `--yes`는 현재값이나 기본값만으로 진행 가능한 단계에서만 빠르게 통과합니다. 새 `mc-server.toml`의 UID/GID는 기존 `./data` 소유자, 현재 non-root 사용자, `1000:1000` fallback 순서로 자동 선택합니다. 기존 유효한 config가 있으면 그 값을 유지합니다. `DISCORD_TOKEN` 같은 필수 값이 없으면 config 단계 전에 검증 실패로 멈춥니다. `--force`는 setup에서도 허용되지만, 실제 프롬프트 입력이 필요한 경로가 비대화식이면 여전히 실패합니다. `--no-input`은 자동화용으로만 쓰고, 프롬프트가 필요해지는 순간 exit code 2로 중단됩니다. `--json`과 `--show-secrets`는 setup에서 지원하지 않습니다.

`mcbot setup`은 터미널에서 섹션 단위로 다음 항목을 안내합니다:

1. **Discord bot**: Discord token, trusted guild, embed channel
2. **Runtime features**: bot runtime/RCON 기본값과 고급 RCON 설정
3. **Minecraft server**: 버전, 서버 타입, 난이도, 메모리, MOTD, 고급 렌더/시뮬레이션 거리
4. **Container behavior**: Docker restart policy, 포트 매핑, 고급 `container.uid`/`container.gid` ownership 선택
5. **Review & write**: `.env`와 `mc-server.toml` 요약, 최종 쓰기 확인, 검증 결과 및 다음 실행 명령

`mcbot setup config`만 실행하면 config 전용 흐름인 **Minecraft server**, **Container behavior**, 선택형 **Container ownership**, **Review & write**를 보여줍니다.

TTY 터미널에서는 setup 헤더와 섹션이 ANSI 색상으로 강조됩니다. 파이프/파일 출력이나 `NO_COLOR=1` 환경에서는 색상을 끄고 순수 텍스트만 출력합니다.

#### Restart policy 선택 기준

| 값 | 추천 상황 | Discord bot/CLI start·stop과의 관계 |
|----|-----------|--------------------------------------|
| `no` | 기본 추천. 서버 시작/중지와 재시작 판단을 Discord 버튼/CLI가 소유해야 할 때 | Docker가 자동으로 다시 켜지지 않으므로 사용자가 누른 stop 의도와 충돌하지 않습니다. |
| `on-failure` | 컨테이너 프로세스가 비정상 종료될 때 Docker가 즉시 살리길 원하는 고급 운영 | Docker가 bot보다 먼저 재시작할 수 있어 crash/log 감지가 덜 정확할 수 있습니다. 현재 기본 흐름에서는 bot-managed 복구와 섞어 쓰기보다 명시적으로 선택하세요. |
| `unless-stopped` | 대부분 24/7로 켜두되, 수동 stop은 Docker daemon 재시작 뒤에도 유지하고 싶을 때 | Discord/CLI stop 뒤 Docker 정책은 대체로 정지를 유지하지만, 장애 복구는 Docker가 소유합니다. |
| `always` | Docker가 항상 서버를 살려두는 상시 운영 | daemon 재시작 뒤 사용자가 꺼둔 서버도 다시 켜질 수 있어 Discord 버튼으로 끄는 UX와 가장 충돌하기 쉽습니다. |

bot runtime은 컨테이너 `StartedAt` 변경을 감지해 log follow와 상태 감시를 새 lifecycle 기준으로 다시 연결합니다. 다만 Docker 정책과 bot의 자동 복구가 동시에 재시작을 결정하면 운영 의도가 복잡해질 수 있으므로 기본값은 여전히 `no`를 권장합니다.

### 3. CLI로 실행하고 운영하기

운영의 기준은 CLI입니다. Make는 편의 래퍼입니다.

```bash
# 봇 실행
cd mcbot && go run ./cmd/mcbot bot run

# 도움말 / 버전
cd mcbot && ./mcbot help
cd mcbot && ./mcbot version

# 이미 빌드한 경우
cd mcbot && ./mcbot bot run

# 서버 운영
cd mcbot && ./mcbot server status
cd mcbot && ./mcbot server start
cd mcbot && ./mcbot server stop

# 설정 확인
cd mcbot && ./mcbot config validate
cd mcbot && ./mcbot env validate

# 파일 내용 확인/수정
cd mcbot && ./mcbot config show
cd mcbot && ./mcbot config get server.version
cd mcbot && ./mcbot config set server.version 1.21.4
cd mcbot && ./mcbot config init
cd mcbot && ./mcbot env show
cd mcbot && ./mcbot env get DISCORD_TOKEN
cd mcbot && ./mcbot env set MCBOT_DEBUG true
cd mcbot && ./mcbot env unset MCBOT_DEBUG
cd mcbot && ./mcbot env init
```

`mcbot server start|stop|status`는 Discord 비밀값이 없어도 동작합니다. `server status`는 Docker inspection만 읽습니다. 외부 CLI stop과 실행 중인 봇은 의도적으로 공존할 수 있고, CLI는 operator-intent 파일로 false crash recovery를 막습니다.

`mcbot config set`과 `mcbot env set`도 같은 CLI 계약의 일부입니다.

전역 옵션은 항상 명령 앞에 둡니다. 형식은 `mcbot [global options] <command> [args]` 입니다.

`config` 명령은 `mc-server.toml`만, `env` 명령은 `.env`만 다룹니다. `config show|get|validate`와 `env show|get|validate`는 `--json` 출력을 지원합니다. `env show|get`는 기본적으로 secret value를 `***MASKED***`로 가리고, 실제 값을 보려면 `--show-secrets`를 사용하세요.

`config init`과 `env init`은 기존 파일을 덮어쓸 수 있으므로, 비대화식 경로에서는 `--yes` 또는 `--force`를 명시해야 합니다. `--no-input`이 켜진 prompt-capable 경로는 exit code 2로 실패합니다. `config ... --file <path>`와 `env ... --file <path>`를 사용하면 기본 경로 대신 다른 파일을 대상으로 실행할 수 있습니다. `--quiet`는 성공 메시지 같은 비필수 출력을 숨깁니다.

`make setup`, `make setup-env`, `make setup-config`, `make up`, `make up-all`, `make up-mc`, `make stop`, `make status`, `make config-validate`, `make env-validate`는 같은 CLI/Compose 운영 경로를 감싼 편의 래퍼입니다.

### 3. 디스코드에서 사용

- 상시 임베드 메시지에 표시되는 버튼으로 서버 시작/종료를 제어합니다.
- 임베드 메시지가 삭제된 경우 자동으로 복구되므로 따로 조치할 필요가 없습니다.
- 신뢰한 서버에서 `/마크봇 채널 설정 channel:<채널>` 명령으로 상시 임베드 메시지 채널을 바꿀 수 있습니다.
- `/마크봇 채널 기본값`은 저장된 런타임 설정을 지우고 `.env`의 `EMBED_CHANNEL_ID`로 되돌립니다. 기본값이 없으면 상태 임베드를 비활성화합니다.
- `/마크봇 채널 끄기`는 같은 신뢰 서버에 비활성화 상태를 저장하고 상태 임베드를 즉시 비활성화합니다. 이 상태는 재시작 후에도 `.env` fallback보다 우선하며, `/마크봇 채널 기본값`으로 해제할 수 있습니다.
- 채널을 바꾼 뒤에는 새 채널이 활성 채널이 되며, 이전 채널 메시지는 자동으로 지우지 않습니다.

---

## 고급 설정

### 수동으로 컨테이너 생성 및 봇 실행 (manual escape hatch)

세부적인 제어가 필요한 경우, 단계별로 실행할 수 있습니다. 이 절의 raw Docker Compose 흐름은 canonical CLI/TOML bridge를 우회하는 low-level/manual escape hatch이며, 일반 운영 경로는 `mcbot server start|stop|status`입니다.

#### 1) 마인크래프트 서버 컨테이너 생성 (최초 1회)

mc-server 컨테이너를 **생성만 하고 실행하지 않습니다**.

**Canonical CLI:**
```bash
mcbot server start
```

**Docker Compose v2 (manual escape hatch):**
```bash
docker compose create mc-server
# 또는
docker compose up --no-start mc-server
```

**Docker Compose v1 (manual escape hatch):**
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

프로젝트는 CLI를 감싼 편의 래퍼만 제공합니다. 운영의 기준은 항상 `mcbot ...` 입니다.

### CLI 래퍼

| 명령어 | 설명 |
|--------|------|
| `make stop` | `mcbot server stop` |
| `make status` | `mcbot server status` |
| `make setup` | `mcbot setup` |
| `make setup-env` | `mcbot setup env` |
| `make setup-config` | `mcbot setup config` |
| `make config-show` | `mcbot config show` |
| `make config-get` | `mcbot config get $(ARGS)` |
| `make config-set` | `mcbot config set $(ARGS)` |
| `make config-init` | `mcbot config init $(ARGS)` |
| `make config-validate` | `mcbot config validate` |
| `make env-show` | `mcbot env show` |
| `make env-get` | `mcbot env get $(ARGS)` |
| `make env-set` | `mcbot env set $(ARGS)` |
| `make env-unset` | `mcbot env unset $(ARGS)` |
| `make env-init` | `mcbot env init $(ARGS)` |
| `make env-validate` | `mcbot env validate` |

### 서버 실행 명령어

| 명령어 | 설명 |
|--------|------|
| `make up` | Discord bot 컨테이너만 빌드 및 실행합니다. Minecraft 서버 컨테이너는 나중에 bot/CLI가 시작할 수 있는 준비 상태로 둡니다. |
| `make up-all` | `mcbot server start`로 `mc-server.toml` 설정을 반영해 Minecraft 서버를 먼저 시작한 뒤 Discord bot 컨테이너를 실행합니다. |
| `make up-mc` | Minecraft 서버만 시작합니다. 내부적으로 `mcbot server start`를 사용해 `mc-server.toml` 설정을 Compose 생성 환경에 반영합니다. |
| `make down` | 모든 컨테이너 중지 및 제거 |
| `make logs` | 실시간 로그 확인 (Ctrl+C로 종료) |

### 고급 명령어

| 명령어 | 설명 |
|--------|------|
| `make nuke` | 모든 컨테이너, 볼륨, 네트워크 제거 (완전 초기화) |

### 개발 명령어

| 명령어 | 설명 |
|--------|------|
| `make go-deps` | Go 의존성 정리 (`go mod tidy`) |
| `make go-build` | mcbot 바이너리 빌드 |

## 환경 변수

### `.env`

| 변수명 | 필수 | 기본값 | 설명 |
|--------|------|--------|------|
| `DISCORD_TOKEN` | ✅ | - | Discord 봇 토큰 |
| `MCBOT_TRUSTED_GUILD_ID` | ✅ | - | privileged 기능을 허용할 Discord 서버(길드) ID |
| `MCBOT_ROLE_NAME` | ❌ | `마크봇` | 봇 사용 권한 역할 이름 |
| `EMBED_CHANNEL_ID` | ❌ | - | 상시 임베드 메시지의 초기 기본 채널 ID |
| `MC_CONTAINER_NAME` | ❌ | `mc-server` | MC 서버 컨테이너 이름 |
| `READY_TIMEOUT_SECONDS` | ❌ | `600` | 서버 시작 타임아웃 |
| `STOP_TIMEOUT_SECONDS` | ❌ | `120` | 서버 종료 타임아웃 |
| `SERVER_OPERATION_TIMEOUT_SECONDS` | ❌ | `720` | 서버 작업 전체 타임아웃 |
| `EMBED_UPDATE_TIMEOUT_SECONDS` | ❌ | `10` | 임베드 메시지 업데이트 타임아웃 |
| `AUTO_RECOVER_ENABLED` | ❌ | `true` | 크래시 후 자동 복구 활성화 여부 |
| `AUTO_RECOVER_INTERVAL_SECONDS` | ❌ | `30` | 자동 복구 시도 간격 |
| `MAX_AUTO_RECOVER_ATTEMPTS` | ❌ | `3` | 최대 자동 복구 시도 횟수 |
| `CRASH_DETECTION_INTERVAL_SECONDS` | ❌ | `2` | 컨테이너 상태 감시 주기 |
| `MAX_INSPECT_FAILURE_ATTEMPTS` | ❌ | `3` | 상태 확인 연속 실패 허용 횟수 |
| `MCBOT_DEBUG` | ❌ | `false` | 디버그 로그 활성화 |
| `MC_JOIN_LOG_PATTERN` | ❌ | `]: (.+) joined the game` | 플레이어 접속 로그 패턴 |
| `MC_LEAVE_LOG_PATTERN` | ❌ | `]: (.+) left the game` | 플레이어 퇴장 로그 패턴 |
| `RCON_HOST` | ❌ | `mc-server` | RCON 서버 호스트 |
| `RCON_PORT` | ❌ | `25575` | RCON 서버 포트 |
| `RCON_PASSWORD` | ❌ | - | RCON 비밀번호 |
| `RCON_TIMEOUT_SECONDS` | ❌ | `10` | RCON 명령 타임아웃 |
| `ENABLE_RCON` | ❌ | `true` | RCON 활성화 여부 |
| `RCON_CMDS_STARTUP` | ❌ | - | RCON 시작 명령어 |

### `mc-server.toml`

| 키 | 설명 |
|----|------|
| `server.version` | 마인크래프트 서버 버전 |
| `server.type` | 서버 타입 |
| `server.difficulty` | 난이도 |
| `server.memory` | 서버 메모리 |
| `server.init_memory` | 초기 메모리 |
| `server.motd` | 서버 MOTD |
| `server.view_distance` | 렌더 거리 |
| `server.simulation_distance` | 시뮬레이션 거리 |
| `container.restart_policy` | mc-server 재시작 정책 |
| `container.port_publish` | 포트 매핑 |
| `container.uid` | 컨테이너 UID |
| `container.gid` | 컨테이너 GID |

`config` 명령은 `mc-server.toml`만 읽고, `env` 명령은 `.env`만 읽습니다. 서로의 키를 거꾸로 쓰면 거부됩니다.

기본 마스킹 문자열은 `***MASKED***` 입니다. `--show-secrets` 없이는 secret value를 노출하지 않습니다.


### Compose 서비스 이름과 컨테이너 이름

`MC_CONTAINER_NAME`은 런타임 컨테이너의 이름만 바꿉니다. Compose의 서비스 키는 계속 `mc-server`이며, 이 이름을 기준으로 CLI와 Make 래퍼가 동작합니다. Minecraft 서버만 켤 때는 `make up-mc`를 사용하고, 운영의 기준은 `mcbot server ...` 입니다.

`RCON_HOST`의 기본값이 `mc-server`인 이유도 동일합니다. Compose 네트워크에서 서비스 DNS는 서비스 키로 고정되므로, 컨테이너 이름을 바꿔도 `mc-server`가 기본입니다.

이미 설치된 환경에서 `MC_CONTAINER_NAME`을 바꾸면 Compose가 새 컨테이너 이름을 적용하지 못합니다. 이 경우 기존 서비스와 컨테이너를 재생성해야 합니다. 이는 mc-server가 Compose로 관리되는 설정을 바꿀 때 필요한 일반 규칙의 한 사례입니다.

### 서버 준비 완료 자동 감지

mcbot은 대표적인 Minecraft 서버 이미지의 "서버 준비 완료" 로그 패턴을 내장하고 있어, 별도 설정 없이 자동으로 서버 시작 완료 시점을 감지합니다.

**지원하는 서버 타입:**
- itzg/minecraft-server (Vanilla, Forge, Fabric 등)
- Paper/Spigot 계열

로그에서 로딩 시간(초 단위)을 자동으로 추출하여 Discord 임베드에 표시합니다.

### RCON 설정

RCON은 선택적 기능입니다. `mc-server` 컨테이너는 기본값으로 RCON이 켜져 있습니다 (`ENABLE_RCON` 기본값 `true`).

`/마크봇 rcon` 명령어는 등록 자체는 글로벌로 유지되지만, 실제 실행은 `MCBOT_TRUSTED_GUILD_ID`로 지정한 서버에서만 허용됩니다.

**활성화 방법**: `.env` 파일에 `MCBOT_TRUSTED_GUILD_ID`와 `RCON_PASSWORD`를 설정한 뒤 봇을 재시작합니다.

```bash
# .env 파일
MCBOT_TRUSTED_GUILD_ID=123456789012345678
RCON_PASSWORD=your_secure_password
```

**동작 방식**:
- `RCON_PASSWORD` 설정 시:
  - `mc-server`: 해당 비밀번호로 RCON 인증
  - `mcbot`: 봇 재시작 후 `/마크봇 rcon` 명령어 정상 작동
- `RCON_PASSWORD` 미설정 시:
  - `mc-server`: 랜덤 비밀번호로 RCON 작동
  - `mcbot`: `/마크봇 rcon` 명령어 실행 시 설정 안내 메시지 표시

`docker-compose.yml`은 `RCON_PASSWORD`와 `RCON_CMDS_STARTUP`을 pass-through로 유지합니다. Compose는 셸에 export된 값뿐 아니라 프로젝트 `.env` 또는 `--env-file` 값도 `mc-server`의 pass-through 키에 반영합니다. 다만 봇 컨테이너는 서비스 설정의 `env_file: .env`로 환경을 받으며, Compose의 `--env-file`은 이 서비스용 `.env` 파일을 대체하지 않습니다. 전체 스택과 `/마크봇 rcon`까지 활성화하려면 프로젝트 `.env`를 유지하고 그 안에 `RCON_PASSWORD`를 두는 구성이 가장 명확합니다. 프로젝트 `.env`와 `--env-file`에 서로 다른 RCON 값을 섞으면 `mc-server`와 `mcbot`이 서로 다른 비밀번호를 사용할 수 있으니 피하세요. 값을 쓰지 않을 때는 사용 중인 env 소스에서 해당 값을 unset하거나 줄 자체를 제거해야 하며, 그 경우 `itzg/minecraft-server`의 기본 동작에 따라 랜덤 비밀번호가 사용됩니다.

보안 주의: `.env`에는 실제 Discord/RCON 비밀번호가 들어갈 수 있으므로 버전 관리에 커밋하지 말고 파일 권한을 제한하세요 (`chmod 600 .env`). Docker 환경 변수는 Docker 접근 권한이 있는 사용자에게 노출될 수 있으므로 공유 호스트에서는 Docker secrets 또는 별도 비밀 관리자를 고려하세요.

`RCON_CMDS_STARTUP`는 계속 지원되는 운영 환경 변수입니다. 다만 기본값은 없습니다. 과거처럼 `keepInventory`가 자동 적용되지 않으니 필요하면 직접 설정하세요.

```bash
# .env 파일 (예시)
RCON_CMDS_STARTUP=gamerule keepInventory true
```

`RCON_CMDS_STARTUP`를 사용하면 `itzg/minecraft-server` 이미지가 시작 시 `rcon-cmds-daemon` 보조 프로세스를 실행합니다. 이 프로세스가 종료된 뒤 zombie로 남지 않도록 `docker-compose.yml`의 `mc-server` 서비스는 `init: true`를 사용합니다. 이 설정을 제거하면 SSH 로그인 또는 `ps`에서 `[rcon-cmds-daemo] <defunct>`가 보일 수 있습니다.

`RCON_PASSWORD`가 공백만 있으면 미설정으로 취급됩니다. 실수로 앞뒤 공백이 붙은 비밀번호는 인증 불일치를 막기 위해 시작 시 에러로 거부됩니다.

**사용 예시**:
- `/마크봇 rcon list` - 접속 중인 플레이어 목록
- `/마크봇 rcon say Hello!` - 서버 채팅에 메시지 전송
- `/마크봇 rcon whitelist add PlayerName` - 화이트리스트 추가

### 타임아웃 변수 간 차이

- **READY_TIMEOUT_SECONDS**
  - 서버 컨테이너를 시작한 뒤, 로그에서 "서버 준비 완료" 패턴이 나타날 때까지 대기하는 최대 시간입니다.
  - `Start` 경로에서만 사용되며, 서버가 정상적으로 뜨는 데 걸릴 수 있는 시간을 정의합니다.

- **STOP_TIMEOUT_SECONDS**
  - `docker stop --time <값>` 에 전달되는 숫자이며, 컨테이너가 SIGTERM 이후 그레이스풀하게 종료될 수 있도록 기다려주는 시간입니다.
  - Docker 레벨의 종료 유예 시간으로, Go 코드의 전체 오퍼레이션 타임아웃과는 별개입니다.

- **SERVER_OPERATION_TIMEOUT_SECONDS**
  - Discord 버튼 클릭으로 시작된 "서버 시작/종료" 오퍼레이션 전체의 상한선입니다.
  - Presence 조회, Start/Stop 실행, 결과 채널 수신 및 후속 임베드 업데이트까지 포함한 상위 타임아웃으로, goroutine leak 방지에도 사용됩니다.

- **EMBED_UPDATE_TIMEOUT_SECONDS**
  - 상태 임베드 메시지를 생성/수정할 때 사용하는 HTTP 호출의 최대 대기 시간입니다.
  - 서버 작업 자체의 길이와는 독립적으로, Discord API 호출 지연에 대한 방어선 역할을 합니다.

## 상태 모델

### 서버 상태 (ServerState)

- **StateStopped**: 서버가 종료된 상태
- **StateStarting**: 서버 시작 중
- **StateRunning**: 서버 실행 중
- **StateStopping**: 서버 종료 중
- **StateError**: 실제 런타임 예외 발생 (docker 명령 실패, 타임아웃 등)
- **StateCrashed**: 런타임 중 비정상 종료 감지 (크래시)

### 상태 전이 규칙

- `Stopped`, `Error`, `Crashed` → `Start` 가능
- `Running`, `Error`, `Crashed` → `Stop` 가능
- 전환 중 상태(`Starting`, `Stopping`)에서는 다른 명령 거부

### 에러 처리 원칙 (2024-12-23 개선)

**컨테이너 미존재 (인프라 미준비)**:
- 상태: `StateStopped`
- `lastError`에 상세 메시지 기록
- 사용자에게 canonical CLI 경로인 `mcbot server start` 또는 Make 래퍼 `make up-mc` 안내

**실제 런타임 예외**:
- 상태: `StateError`
- docker inspect 실패, start/stop 명령 실패, ready 타임아웃 등
- 복구 가능한 오류로, 재시도 가능

**설계 근거**:
- 서버 라이프사이클 상태와 인프라 provisioning 상태를 분리
- Start/Stop 간 일관성 확보: 컨테이너 없음은 모두 Stopped로 처리
- `StateError`는 실제 예외 상황에만 사용하여 의미 명확화

### 크래시 감지 및 reason 규약

런타임 중 서버 크래시가 감지되면 `StateCrashed` 상태로 전이되며, 크래시 원인을 나타내는 `reason` 문자열이 로그에 기록됩니다.

**reason 값은 `internal/mcserver/crash_reason.go`에 상수로 정의**되어 있으며, 로그 분석 및 모니터링 호환성을 위해 값 자체는 안정적으로 유지됩니다.

**정의된 reason 상수:**
- `runtime_container_stopped`: 런타임 중 컨테이너가 예기치 않게 종료됨
- `runtime_inspect_failed_repeatedly`: 컨테이너 상태 확인이 연속으로 실패
- `runtime_failure_*`: 런타임 로그에서 실패 패턴 감지 (예: 메모리 부족)
- `runtime_normal_shutdown`: 서버 내부에서 정상 종료 시작 후 종료됨
- `sync_detected_unexpected_stop`: 동기화 중 예기치 않은 종료 감지
- `sync_log_stream_ended`: 시작 중 로그 스트림이 종료됨
- `sync_container_inspect_failed`: 시작 중 컨테이너 상태 확인 실패
- `sync_container_stopped`: 시작 중 컨테이너가 종료됨
- `sync_timeout`: 시작 중 준비 완료 타임아웃 초과
- `log_stream_ended_unexpectedly`: 로그 스트림이 예기치 않게 종료됨

**규칙**: 새로운 크래시 원인을 추가할 때는 반드시 `crash_reason.go`에 상수로 정의하고, 문자열 리터럴 대신 상수를 참조해야 합니다.

## 프로젝트 구조

```
.
├── Makefile                    # Make 타겟 정의
├── docker-compose.yml          # Docker Compose 설정
├── .env.example                # 환경 변수 템플릿
├── .gitignore
├── data/                       # MC 서버 데이터 (gitignore)
├── docs/
│   ├── README.md               # 프로젝트 문서
│   └── TESTING.md              # 테스트 가이드
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
    │   ├── rcon/                # RCON 클라이언트
    │   │   └── client.go
    │   └── state/              # 서버 상태 관리
    │       ├── state.go
    │       └── state_test.go
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
DISCORD_TOKEN=your_token MCBOT_TRUSTED_GUILD_ID=123456789012345678 EMBED_CHANNEL_ID=123456789012345679 ./mcbot/mcbot bot run
```

### 직접 Go 명령어 사용

```bash
# 의존성 설치
cd mcbot && go mod tidy

# 빌드
cd mcbot && go build -o mcbot ./cmd/mcbot

# 실행 (환경 변수 필요)
cd mcbot && DISCORD_TOKEN=your_token MCBOT_TRUSTED_GUILD_ID=123456789012345678 EMBED_CHANNEL_ID=123456789012345679 ./mcbot bot run
```
