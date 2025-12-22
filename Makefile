MC_SERVICE=mc-server
GO_DIR=mcbot

.PHONY: up up-all up-mc ensure-mc down nuke go-deps go-build

ensure-mc:
	@if ! docker ps -a --format '{{.Names}}' | grep -q '^mc-server$$'; then \
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