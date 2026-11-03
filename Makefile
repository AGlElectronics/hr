# Developer conveniences. Not required by Docker builds.

GO       ?= go
NPM      ?= npm
COMPOSE  ?= docker compose
ENV_FILE ?= .env

.PHONY: help
help:
	@echo "Targets:"
	@echo "  up            Boot the full Docker Compose stack (base)."
	@echo "  up-local      Boot the stack with the local-dev overlay (adds Adminer)."
	@echo "  down          Stop the stack and remove containers."
	@echo "  logs          Tail backend + frontend logs."
	@echo "  migrate       Run the one-shot migrator against the running Postgres."
	@echo "  tidy          go mod tidy for the backend."
	@echo "  sqlc          Run sqlc generate (requires sqlc on PATH)."
	@echo "  test-backend  Run Go tests."
	@echo "  test-frontend Run frontend tests."
	@echo "  fmt           gofmt + goimports."

.PHONY: up
up:
	$(COMPOSE) --env-file $(ENV_FILE) -f infra/docker-compose.yml up -d --build

.PHONY: up-local
up-local:
	$(COMPOSE) --env-file $(ENV_FILE) \
		-f infra/docker-compose.yml \
		-f infra/docker-compose.local.yml up -d --build

.PHONY: down
down:
	$(COMPOSE) --env-file $(ENV_FILE) -f infra/docker-compose.yml down

.PHONY: logs
logs:
	$(COMPOSE) --env-file $(ENV_FILE) -f infra/docker-compose.yml logs -f backend frontend

.PHONY: migrate
migrate:
	$(COMPOSE) --env-file $(ENV_FILE) -f infra/docker-compose.yml run --rm migrate

.PHONY: tidy
tidy:
	cd backend && $(GO) mod tidy

.PHONY: sqlc
sqlc:
	cd backend && sqlc generate

.PHONY: test-backend
test-backend:
	cd backend && $(GO) test ./...

.PHONY: test-frontend
test-frontend:
	cd frontend && $(NPM) test

.PHONY: fmt
fmt:
	cd backend && gofmt -w . && goimports -w .
