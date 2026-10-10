GO_BIN := $(shell go env GOBIN)
GO_PATH := $(shell go env GOPATH)
MIGRATE ?= $(if $(GO_BIN),$(GO_BIN),$(GO_PATH)/bin)/migrate
DB_PATH ?= dispatch.db
MIGRATIONS_DIR := internal/adapters/outbound/sqlite/migration
DATABASE_URL := sqlite://$(abspath $(DB_PATH))
IMAGE_NAME = dispatch
SERVICE_NAME = dispatch-service
COMPOSE ?= podman compose
BUILD ?= 0
RECREATE ?= 0
COMPOSE_FLAGS ?=

.PHONY: migrate-up migrate-down migrate-version migrate-create \
	check-migrate go-test go-dev go-lint container-build container-up \
	container-stop compose-up

check-migrate:
	@test -x "$(MIGRATE)" || { \
		echo "migrate is required. Install a binary built with the sqlite driver:"; \
		echo "  go install -tags 'sqlite' github.com/golang-migrate/migrate/v4/cmd/migrate@latest"; \
		exit 1; \
	}

migrate-up: check-migrate
	@mkdir -p $(dir $(DB_PATH))
	$(MIGRATE) -path $(MIGRATIONS_DIR) -database "$(DATABASE_URL)" up

migrate-down: check-migrate
	$(MIGRATE) -path $(MIGRATIONS_DIR) -database "$(DATABASE_URL)" down 1

migrate-version: check-migrate
	$(MIGRATE) -path $(MIGRATIONS_DIR) -database "$(DATABASE_URL)" version

migrate-create: check-migrate
	@test -n "$(name)" || { echo "usage: make migrate-create name=create_attempt_index"; exit 1; }
	$(MIGRATE) create -ext sql -dir $(MIGRATIONS_DIR) -seq -digits 3 "$(name)"

go-test:
	go test -v ./...

go-dev:
	go run ./cmd/dispatch/main.go

go-lint:
	golangci-lint run

container-build:
	podman build -t $(IMAGE_NAME) .

container-up:
	podman run -d --name $(SERVICE_NAME) -p 8080:8080 $(IMAGE_NAME)

container-stop:
	podman stop $(SERVICE_NAME)

# Examples:
#   make compose-up BUILD=1 RECREATE=1
#   make compose-up COMPOSE_FLAGS='--build --force-recreate'
#   make compose-up COMPOSE='docker compose' BUILD=1
compose-up:
	$(COMPOSE) up -d $(if $(filter 1,$(BUILD)),--build) $(if $(filter 1,$(RECREATE)),--force-recreate) $(COMPOSE_FLAGS)
