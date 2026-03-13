MC_SERVICE=mc-server
GO_DIR=mcbot

.PHONY: up up-all up-mc ensure-mc down nuke go-deps go-build

ensure-mc:
	@if ! docker compose ps -a --format '{{.Service}}' | grep -q '^$(MC_SERVICE)$$'; then \
		echo 'mc-server 컨테이너를 생성합니다...'; \
		docker compose create $(MC_SERVICE); \
	else \
		echo 'mc-server 컨테이너가 이미 존재합니다.'; \
	fi

go-deps:
	cd $(GO_DIR) && go mod tidy

go-build:
	cd $(GO_DIR) && go build -o mcbot ./cmd/mcbot

up: ensure-mc
	docker compose up --build -d mcbot

up-all: ensure-mc
	docker compose up --build -d

up-mc: ensure-mc
	docker compose up --build -d mc-server

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
	cd $(GO_DIR) && go test -tags=integration -v ./internal/mcserver/...

test-integration-verbose:
	cd $(GO_DIR) && go test -tags=integration -v -count=1 ./internal/mcserver/...

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
