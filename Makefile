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
        lint tidy install-tools swagger \
        mock-gen test test-raw test-race test-ci \
        docker-up docker-down docker-logs ngrok push-notification test-fcm

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

## run-local: Start server accessible on LAN for mobile/web testing (0.0.0.0) — FIXED: ensure postgres+redis, bind 0.0.0.0
run-local:
	@which air > /dev/null 2>&1 || go install github.com/air-verse/air@latest
	@IP=$(shell hostname -I | awk '{print $$1}'); \
	PORT=$${APP_PORT:-8000}; \
	if [ -z "$$IP" ]; then IP="192.168.1.5"; fi; \
	BASE_URL="http://$$IP:$$PORT/uploads"; \
	echo ""; \
	echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"; \
	echo "🌐 Local IP: $$IP"; \
	echo "🚀 Server binding 0.0.0.0:$$PORT (Fiber 0.0.0.0)"; \
	echo "   ├─ Health: http://$$IP:$$PORT/health"; \
	echo "   ├─ API:    http://$$IP:$$PORT/api/v1"; \
	echo "   ├─ Uploads:http://$$IP:$$PORT/uploads/..."; \
	echo "   └─ Swagger http://$$IP:$$PORT/docs/index.html"; \
	echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"; \
	echo "🔧 Ensuring postgres & redis running (docker-up)"; \
	docker compose up -d postgres redis > /dev/null 2>&1 || true; \
	echo "⏳ Waiting for postgres..."; \
	timeout 15 bash -c 'until docker compose exec -T postgres pg_isready -U postgres -d nitip 2>/dev/null || pg_isready -h localhost -p 5432 -U postgres 2>/dev/null; do sleep 1; done' || echo "⚠️  postgres not ready yet, air will retry"; \
	echo "✅ Starting air with BASE_URL=$$BASE_URL"; \
	LOCAL_STORAGE_BASE_URL=$$BASE_URL APP_PORT=$$PORT STORAGE_BASE_URL=http://$$IP:$$PORT CORS_ALLOWED_ORIGINS=* air

## run-local-host host=<IP> port=<PORT>: Run with specified IP (custom)
run-local-host:
	@which air > /dev/null 2>&1 || go install github.com/air-verse/air@latest
	@if [ -z "$(host)" ]; then echo "❌ usage: make run-local-host host=192.168.1.5 [port=8000]"; exit 1; fi
	@PORT=$(if $(port),$(port),8000); \
	echo "🌐 Using HOST=$(host) PORT=$$PORT"; \
	echo "   health: http://$(host):$$PORT/health"; \
	docker compose up -d postgres redis > /dev/null 2>&1 || true; \
	LOCAL_STORAGE_BASE_URL=http://$(host):$$PORT/uploads STORAGE_BASE_URL=http://$(host):$$PORT APP_PORT=$$PORT CORS_ALLOWED_ORIGINS=* air

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

## migrate-up: Apply all pending migrations (Local Environment)
migrate-up:
	go run $(CMD_MIGRATE) up

## docker-migrate-up: Apply all pending migrations (Inside Docker / Production)
docker-migrate-up:
	docker compose run --rm app /app/migrate up

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

# ── Quality / Testing ───────────────────────────

AWK_TEST_REPORT = awk '\''\
    /\[no test files\]/ { next } \
    /=== RUN/ { runName=$$0; sub(/^=== RUN[[:space:]]+/, "", runName); all[runName]=1; next } \
    /^[[:space:]]*--- (PASS|FAIL):/ { \
        isFail = ($$0 ~ /--- FAIL:/); \
        line=$$0; \
        if (isFail) sub(/^[[:space:]]*--- FAIL:[[:space:]]*/, "", line); else sub(/^[[:space:]]*--- PASS:[[:space:]]*/, "", line); \
        sub(/[[:space:]]+\(.*/, "", line); \
        testName=line; \
        isParent=0; for (k in all) if (k != testName && index(k, testName "/") == 1) { isParent=1; break } \
        if (isParent) next; \
        cat="UNCATEGORIZED"; catUpper="UNCATEGORIZED"; \
        n=split(testName, parts, "/"); \
        for (i=1; i<=n; i++) if (parts[i]=="positive") { cat="positive"; catUpper="POSITIVE"; break } else if (parts[i]=="negative" || parts[i] ~ /^negative/) { cat="negative"; catUpper="NEGATIVE"; break } \
        scen=parts[n]; \
        if (cat != "UNCATEGORIZED") { scen=""; for (i=1; i<=n; i++) if (parts[i]=="positive" || parts[i] ~ /^negative/) { for (j=i+1; j<=n; j++) { if (scen!="") scen=scen "/"; scen=scen parts[j] } break } gsub(/_/, " ", scen) } else { gsub(/_/, " ", scen) } \
        top=parts[1]; \
        if (top ~ /^TestAuth/) top="AUTH"; else if (top ~ /^TestProfile/) top="PROFILE"; else if (top=="TestOrder") top="ORDER"; else if (top=="TestWallet") top="WALLET"; else if (top=="TestKYC") top="KYC"; else if (top=="TestMerchantDiscovery") top="MERCHANT DISCOVERY"; \
        catIdx=0; for (i=2; i<=n; i++) if (parts[i]=="positive" || parts[i] ~ /^negative/) { catIdx=i; break } \
        op=""; if (catIdx>0) { for (i=2; i<catIdx; i++) { if (op!="") op=op "/"; op=op parts[i] } } else if (n>=2) { op=parts[2]; for (i=3; i<=n; i++) { if (op!="") op=op "/"; op=op parts[i]; break } } \
        gsub(/_/, " ", op); hdr=top " - " op; if (op=="") hdr=top; \
        if (hdr != lastHdr && op != "") { if (lastHdr!="") printf "\n"; printf "%s - %s\n", top, op; lastHdr=hdr } else if (op=="" && top != lastTop) { if (lastHdr!="") printf "\n"; lastTop=top; lastHdr=top } \
        if (isFail) printf "  \xE2\x9C\x97  [%s] %s\n", catUpper, scen; else printf "  \xE2\x9C\x93  [%s] %s\n", catUpper, scen; \
        next \
    } \
    /^PASS$$/ { next } \
    /^ok[[:space:]]+/ { sub(/^ok[[:space:]]+/, "\xE2\x9C\x93  "); print; next } \
    /^FAIL[[:space:]]+/ { sub(/^FAIL[[:space:]]+/, "\xE2\x9C\x97  "); print; next } \
    { print }'\''


.PHONY: mock-gen test test-raw test-race test-ci

# ── Test: domain-aware via domain= (no manual registration) ──
#   make test                         → ./... (full)
#   make test domain=auth             → auth + user(AUTH) + middleware(Protected Gate)
#   make test domain=order            → ./internal/domain/order/...
#   make test domain=wallet           → ./internal/domain/wallet/...
#   make test domain=auth,order       → gabungan (koma)
#   make test domain=/domain/auth     → normalisasi: /domain/auth → auth
# Baru: cukup taruh *_test.go dengan func Test<Domain>*, tanpa edit Makefile.
DOMAIN_INPUT ?= $(domain)
AUTH_PKGS    := ./internal/domain/auth/... ./internal/domain/user/... ./internal/middleware/...
PROFILE_PKGS := ./internal/domain/user/...
DOMAIN_ARGS  = $(strip $(DOMAIN_INPUT))
DOMAIN_CSV   = $(subst $(space),$(comma),$(DOMAIN_ARGS))
comma        := ,
space        := $(empty) $(empty)
empty        :=
# Filter per domain to isolate TestAuth vs TestProfile when user package hosts both
DOMAIN_TEST_FILTER := $(strip $(if $(filter profile,$(DOMAIN_ARGS)),$(if $(filter auth,$(DOMAIN_ARGS)),-run "^(TestAuth|TestProfile)",-run "^TestProfile"),$(if $(filter auth,$(DOMAIN_ARGS)),-run "^TestAuth",)))
define resolve_pkgs
$(strip $(if $(DOMAIN_ARGS),\
  $(foreach d,$(subst $(comma), ,$(DOMAIN_CSV)),\
    $(if $(filter auth,$(d)),$(AUTH_PKGS),\
    $(if $(filter /domain/auth domain/auth,$(d)),$(AUTH_PKGS),\
    $(if $(filter profile,$(d)),$(PROFILE_PKGS),\
    $(if $(filter order,$(d)),./internal/domain/order/..., \
    $(if $(filter wallet,$(d)),./internal/domain/wallet/..., \
    ./internal/domain/$(strip $(patsubst /domain/%,%,$(patsubst domain/%,%,$(d))))/...)))))),\
  ./...))
endef

test-profile:
	@bash -o pipefail -c 'go test -v -count=1 -run "^TestProfile" ./internal/domain/user/... | $(AWK_TEST_REPORT)'

test-auth:
	@bash -o pipefail -c 'go test -v -count=1 -run "^TestAuth" ./internal/domain/user/... ./internal/middleware/... | $(AWK_TEST_REPORT)'

mock-gen:
	go generate ./internal/domain/...

## test: Full regression or filtered by domain= (curated auth, comma-separated)
test:
	@bash -o pipefail -c 'go test -v -count=1 $(DOMAIN_TEST_FILTER) $(call resolve_pkgs) | $(AWK_TEST_REPORT)'

## test-<domain>: Generic — auto (no manual registration)
##    Example: make test domain=banner  →  ./internal/domain/banner/...
test-%:
	@bash -o pipefail -c 'go test -v -count=1 ./internal/domain/$*/... 2>/dev/null | $(AWK_TEST_REPORT); st=$$?; if [ "$$st" -ne 0 ]; then echo "tip: domain '\''$*'\'' belum punya *_test.go atau Test$*"; fi; exit $$st'

test-kyc:
	@bash -o pipefail -c 'go test -v -count=1 -run "^TestKYC" ./internal/domain/kyc/... 2>&1 | $(AWK_TEST_REPORT); st=$${PIPESTATUS[0]}; if [ $$st -ne 0 ] && ! grep -q "--- FAIL" <<< "$$(go test -v -count=1 -run "^TestKYC" ./internal/domain/kyc/... 2>&1)"; then echo "tip: domain '\''kyc'\'' belum punya *_test.go atau TestKYC"; fi; exit $$st'

test-merchant-discovery:
	@bash -o pipefail -c 'go test -v -count=1 -run "^TestMerchantDiscovery" ./internal/domain/merchant/... ./internal/domain/order/... 2>&1 | $(AWK_TEST_REPORT); st=$${PIPESTATUS[0]}; exit $$st'

test-payment-qris:
	@bash -o pipefail -c 'go test -v -count=1 -run "^TestPaymentQRIS" ./internal/domain/order/... 2>&1 | $(AWK_TEST_REPORT); st=$${PIPESTATUS[0]}; exit $$st'

test-order-create:
	@bash -o pipefail -c 'go test -v -count=1 -run "^TestOrderCreate" ./internal/domain/order/... 2>&1 | $(AWK_TEST_REPORT); st=$${PIPESTATUS[0]}; exit $$st'

.PHONY: test-order-lifecycle test-order-cancel

test-order-lifecycle:
	@bash -o pipefail -c 'go test -v -count=1 -run "^(TestOrderLifecycle|TestMatching)" ./internal/domain/order/... | $(AWK_TEST_REPORT)'

test-order-cancel:
	@bash -o pipefail -c 'go test -v -count=1 -run "^TestOrderCancel" ./internal/domain/order/... | $(AWK_TEST_REPORT)'

test-raw:
	go test -v -count=1 ./...

test-race:
	go test -race -count=1 ./...

test-ci:
	go test -race -cover -count=1 ./...

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

## push-notification: Interactive menu to test push notification (token/broadcast)
push-notification:
	@echo ""
	@echo "  ╔══════════════════════════════════════╗"
	@echo "  ║     Test Push Notification (Nitip)   ║"
	@echo "  ╚══════════════════════════════════════╝"
	@echo ""
	@read -p "  🔑 FCM Token (Kosongkan jika Broadcast): " FCM_TOKEN; \
	read -p "  🔔 Judul Pesan                         : " MSG_TITLE; \
	read -p "  💬 Isi Pesan                           : " MSG_BODY; \
	echo ""; \
	echo "  ⏳ Mengirim push notification..."; \
	go run scripts/test_fcm.go "$$FCM_TOKEN" "$$MSG_TITLE" "$$MSG_BODY"

## test-fcm: Alias for push-notification (interactive test)
test-fcm: push-notification


