GO_BIN := $(shell go env GOBIN)
GO_PATH := $(shell go env GOPATH)
MIGRATE ?= $(if $(GO_BIN),$(GO_BIN),$(GO_PATH)/bin)/migrate
DB_PATH ?= dispatch.db
MIGRATIONS_DIR := internal/adapters/outbound/sqlite/migration
DATABASE_URL := sqlite://$(abspath $(DB_PATH))

.PHONY: migrate-up migrate-down migrate-version migrate-create check-migrate

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
