# ────────────────────────────────────────────────
# nitip-core Makefile
# ────────────────────────────────────────────────
APP_NAME    := nitip-core
SHELL       := /bin/bash
BIN_DIR     := bin
BIN         := $(BIN_DIR)/server
CMD_SERVER  := ./cmd/server
CMD_MIGRATE := ./cmd/migrate

# Load .env if it exists
-include .env
export

.PHONY: help run dev build clean \
        migrate-up migrate-down migrate-status migrate-create migrate-reset migrate-fix \
        test test-coverage lint tidy install-tools swagger \
        docker-up docker-down docker-logs ngrok

## help: Show this help
help:
	@echo ""
	@echo "  $(APP_NAME) — available commands:"
	@echo ""
	@grep -E '^##' Makefile | sed 's/## /  /' | column -t -s ":"
	@echo ""

# ── Server ──────────────────────────────────────

## run: Start server with hot-reload (air)
run:
	@which air > /dev/null 2>&1 || go install github.com/air-verse/air@latest
	@echo "✓ Server starting on: http://$(shell hostname -I | awk '{print $$1}'):$(APP_PORT)"
	air

## host: Show local IP for mobile connection
host:
	@echo "Local IP: $(shell hostname -I | awk '{print $$1}')"
	@echo "Mobile API BaseURL: http://$(shell hostname -I | awk '{print $$1}'):8000/api/v1"

## dev: Start server with hot-reload (air)
dev:
	@which air > /dev/null 2>&1 || go install github.com/air-verse/air@latest
	air

## build: Build the server binary to ./bin/server
build:
	@mkdir -p $(BIN_DIR)
	go build -ldflags="-s -w" -o $(BIN) $(CMD_SERVER)
	@echo "✓ built: ./$(BIN)"

## swagger: Generate swagger docs from annotations (requires swag CLI)
swagger:
	@which swag > /dev/null 2>&1 || go install github.com/swaggo/swag/cmd/swag@latest
	swag init -g cmd/server/main.go --output docs --parseDependency --parseInternal --dir ./
	@echo "✓ swagger docs generated at ./docs"

## clean: Remove build artefacts
clean:
	@rm -rf $(BIN_DIR) tmp
	@echo "✓ cleaned"

# ── Docker ──────────────────────────────────────

## docker-up: Start postgres & redis containers
docker-up:
	docker compose up -d
	@echo "✓ containers started"

## docker-down: Stop and remove containers
docker-down:
	docker compose down

## docker-logs: Tail container logs
docker-logs:
	docker compose logs -f

# ── Database Migrations (goose) ──────────────────

## migrate-up: Apply all pending migrations
migrate-up:
	go run $(CMD_MIGRATE) up

## migrate-down: Rollback the last migration step
migrate-down:
	go run $(CMD_MIGRATE) down

## migrate-status: Show migration status
migrate-status:
	go run $(CMD_MIGRATE) status

## migrate-reset: Rollback ALL migrations (DANGER)
migrate-reset:
	go run $(CMD_MIGRATE) reset

## migrate-fix: Fix goose migration sequence numbering
migrate-fix:
	go run $(CMD_MIGRATE) fix

## migrate-create name=<name>: Create a new goose migration file
migrate-create:
ifndef name
	$(error ❌  usage: make migrate-create name=create_something)
endif
	go run $(CMD_MIGRATE) create $(name)

# ── Quality ──────────────────────────────────────

## test: Run all unit tests
test:
	go test ./... -v -race

## test-coverage: Run tests and open HTML coverage report
test-coverage:
	go test ./... -coverprofile=coverage.out
	go tool cover -html=coverage.out

## admin-list: List all system configs
admin-list:
	go run ./cmd/admin list

## admin-set: Set a system config. Usage: make admin-set key=foo value=bar
admin-set:
	go run ./cmd/admin set $(key) $(value) "$(desc)"

## admin-create: Scaffolds a new backend admin (interactive prompt)
admin-create:
	@echo ""
	@echo "  ╔══════════════════════════════════════╗"
	@echo "  ║       Buat Akun Admin Nitip          ║"
	@echo "  ╚══════════════════════════════════════╝"
	@echo ""
	@read -p "  📧 Email    : " ADMIN_EMAIL; \
	read -s -p "  🔑 Password : " ADMIN_PWD; echo ""; \
	read -p "  👤 Nama     : " ADMIN_NAME; \
	echo ""; \
	echo "  ⏳ Membuat admin..."; \
	go run ./cmd/admin create-admin "$$ADMIN_EMAIL" "$$ADMIN_PWD" "$$ADMIN_NAME"

## lint: Run golangci-lint
lint:
	@which golangci-lint > /dev/null 2>&1 || go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	golangci-lint run ./...

## tidy: Tidy and verify go modules
tidy:
	go mod tidy
	go mod verify

## install-tools: Install all dev tools (air, golangci-lint)
install-tools:
	go install github.com/air-verse/air@latest
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	@echo "✓ tools installed"
	
## register-client: Register API client (interactive prompt)
register-client:
	@echo ""
	@echo "  ╔══════════════════════════════════════╗"
	@echo "  ║     Register API Client (Nitip)      ║"
	@echo "  ╚══════════════════════════════════════╝"
	@echo ""
	@read -p "  📱 App Name   : " APP_NAME; \
	read -p "  💻 Platform   : " PLATFORM; \
	read -p "  📝 Description: " DESC; \
	read -p "  🔑 Admin Pass : " ADMIN_PWD; \
	echo ""; \
	echo "  ⏳ Registering client..."; \
	go run ./cmd/admin register-client "$$APP_NAME" "$$PLATFORM" "$$ADMIN_PWD" "$$DESC"

## list-clients: List all registered API clients
list-clients:
	go run ./cmd/admin list-clients

## grant-token: Generate grant token for testing (interactive menu)
grant-token:
	@echo ""
	@echo "  ╔══════════════════════════════════════╗"
	@echo "  ║     Generate Grant Token (Nitip)     ║"
	@echo "  ╚══════════════════════════════════════╝"
	@echo ""
	@echo "  📋 Registered API Clients:"; \
	echo "  ─────────────────────────────────────"; \
	go run ./cmd/admin list-clients 2>/dev/null; \
	echo "  ─────────────────────────────────────"; \
	echo ""; \
	echo "  🔑 Masukkan client yang akan digunakan:"; \
	echo "     (salin App Name & Platform dari daftar di atas)"; \
	echo ""; \
	read -p "  📱 App Name  : " APP_NAME; \
	read -p "  💻 Platform  : " PLATFORM; \
	echo ""; \
	echo "  👤 Generate JWT for user?"; \
	echo "     1) Budi Penitip (requester) — budi@nitip.id"; \
	echo "     2) Andi Runner  (runner)    — andi@nitip.id"; \
	echo "     3) Admin        (admin)     — admin@nitip.id"; \
	echo "     4) Skip (grant token only)"; \
	echo ""; \
	read -p "  Pilih [1-4]: " USER_CHOICE; \
	case "$$USER_CHOICE" in \
		1) JWT_EMAIL="budi@nitip.id" ;; \
		2) JWT_EMAIL="andi@nitip.id" ;; \
		3) JWT_EMAIL="admin@nitip.id" ;; \
		*) JWT_EMAIL="" ;; \
	esac; \
	echo ""; \
	echo "  ⏳ Generating token for $$APP_NAME ($$PLATFORM)..."; \
	if [ -n "$$JWT_EMAIL" ]; then \
		go run ./cmd/admin grant-token "$$APP_NAME" "$$PLATFORM" --jwt "$$JWT_EMAIL"; \
	else \
		go run ./cmd/admin grant-token "$$APP_NAME" "$$PLATFORM"; \
	fi

## test-track: Start SSE tracking monitor. Usage: make test-track id=<order_id> [token=<jwt>] [url=<base_url>]
test-track:
ifndef id
	$(error ❌  usage: make test-track id=ORDER_UUID [token=JWT] [url=URL])
endif
	go run ../scripts/track_monitor.go -id $(id) -token "$(token)" -url "$(url)"

## ngrok: Start ngrok tunnel for local webhook testing on port 8000
ngrok:
	@echo "🚀 Starting ngrok tunnel on port $(APP_PORT)..."
	ngrok http $(APP_PORT)

