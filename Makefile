MC_SERVICE=mc-server
GO_DIR=mcbot
MCBOT_CLI=cd $(GO_DIR) && go run ./cmd/mcbot

.PHONY: stop status setup setup-env setup-config config-show config-get config-set config-init config-validate env-show env-get env-set env-unset env-init env-validate backup restore backup-create backup-list backup-validate backup-prune backup-restore up up-all up-mc down nuke go-deps go-build logs

stop:
	$(MCBOT_CLI) server stop

status:
	$(MCBOT_CLI) server status

setup:
	$(MCBOT_CLI) setup

setup-env:
	$(MCBOT_CLI) setup env

setup-config:
	$(MCBOT_CLI) setup config

config-show:
	$(MCBOT_CLI) config show

config-get:
	$(MCBOT_CLI) config get $(ARGS)

config-set:
	$(MCBOT_CLI) config set $(ARGS)

config-init:
	$(MCBOT_CLI) config init $(ARGS)

config-validate:
	$(MCBOT_CLI) config validate

env-show:
	$(MCBOT_CLI) env show

env-get:
	$(MCBOT_CLI) env get $(ARGS)

env-set:
	$(MCBOT_CLI) env set $(ARGS)

env-unset:
	$(MCBOT_CLI) env unset $(ARGS)

env-init:
	$(MCBOT_CLI) env init $(ARGS)

env-validate:
	$(MCBOT_CLI) env validate

backup:
	$(MCBOT_CLI) $(GLOBAL_ARGS) backup create $(ARGS)

restore:
	$(MCBOT_CLI) $(GLOBAL_ARGS) backup restore --backup-id $(BACKUP) $(ARGS)

backup-create:
	$(MCBOT_CLI) $(GLOBAL_ARGS) backup create $(ARGS)

backup-list:
	$(MCBOT_CLI) $(GLOBAL_ARGS) backup list $(ARGS)

backup-validate:
	$(MCBOT_CLI) $(GLOBAL_ARGS) backup validate $(ARGS)

backup-prune:
	$(MCBOT_CLI) $(GLOBAL_ARGS) backup prune $(ARGS)

backup-restore:
	$(MCBOT_CLI) $(GLOBAL_ARGS) backup restore $(ARGS)

go-deps:
	cd $(GO_DIR) && go mod tidy

go-build:
	cd $(GO_DIR) && go build -o mcbot ./cmd/mcbot

up:
	docker compose up --build -d mcbot

up-all:
	$(MCBOT_CLI) server start
	docker compose up --build -d mcbot

up-mc:
	$(MCBOT_CLI) server start

down:
	docker compose down

nuke:
	docker compose down --volumes --remove-orphans

logs:
	docker compose logs -f

# 테스트 관련 타겟
.PHONY: test test-race test-integration test-integration-verbose test-integration-clean test-all

test:
	cd $(GO_DIR) && go test ./...

test-race:
	cd $(GO_DIR) && go test -race ./...

test-integration:
	cd $(GO_DIR) && go test -tags=integration -timeout=20m -v ./internal/mcserver/...

test-integration-verbose:
	cd $(GO_DIR) && go test -tags=integration -timeout=20m -v -count=1 ./internal/mcserver/...

test-integration-clean:
	@echo "통합 테스트 잔여 리소스 정리 중..."
	@for network in $$(docker network ls --filter "name=mcbot_test_" --format "{{.Name}}"); do \
		project=$$(echo $$network | sed 's/_default$$//'); \
		if [ "$$project" != "$$network" ]; then \
			echo "프로젝트 정리: $$project"; \
			docker compose -p $$project -f docker-compose.test.yml down -v --remove-orphans 2>/dev/null || true; \
		fi; \
	done
	@echo "남은 리소스 강제 정리 중..."
	@docker ps -a --filter "name=mcbot_test_" --format "{{.Names}}" | xargs -r docker rm -f 2>/dev/null || true
	@docker network ls --filter "name=mcbot_test_" --format "{{.Name}}" | xargs -r docker network rm 2>/dev/null || true
	@docker volume ls --filter "name=mcbot_test_" --format "{{.Name}}" | xargs -r docker volume rm 2>/dev/null || true
	@echo "정리 완료"

test-all: test-race test-integration
