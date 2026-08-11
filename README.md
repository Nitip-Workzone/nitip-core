# 🚀 Nitip Core — Backend API

Backend REST API untuk platform **Nitip**, sebuah layanan jastip (titip beli & titip kirim) yang menghubungkan Penitip dengan Runner terdekat.

Dibangun dengan **Go**, **Fiber v2**, **PostgreSQL**, dan **Redis**.

---

## 📦 Tech Stack

| Layer | Technology |
|---|---|
| HTTP Framework | [Fiber v2](https://gofiber.io) |
| ORM | [bun](https://bun.uptrace.dev) |
| Database | PostgreSQL + PostGIS |
| Migration | [golang-migrate](https://github.com/golang-migrate/migrate) |
| Cache & Session | Redis |
| Authentication | JWT (access + refresh token rotation) |
| Notification | Firebase Cloud Messaging (FCM) |
| Storage | Firebase Storage / MinIO / Local |
| Logging | Zap + FiberZap |
| Hot Reload | [Air](https://github.com/air-verse/air) |

---

## 🏗️ Architecture

Mengikuti pola **Clean Architecture / Domain-Driven Design**:

```
nitip-core/
├── cmd/
│   ├── server/main.go          # HTTP server entry point
│   ├── migrate/main.go         # Migration CLI
│   └── admin/main.go           # Admin CLI tools
├── config/config.go            # Environment configuration
├── internal/
│   ├── app/app.go              # Fiber app setup & route registration
│   ├── cache/redis.go          # Redis client wrapper
│   ├── database/database.go    # Database connection (multi-driver)
│   ├── domain/                 # Business domains (each with handler/service/repo/model)
│   │   ├── user/               # User management, auth, PIN, KYC
│   │   ├── order/              # Order lifecycle (create → assign → deliver → complete)
│   │   ├── trip/               # Runner trip management
│   │   ├── wallet/             # Digital wallet & transactions
│   │   ├── chat/               # Real-time chat (WebSocket)
│   │   ├── review/             # Rating & review system
│   │   ├── notification/       # Push notification management
│   │   ├── kyc/                # KYC verification
│   │   ├── config/             # System configuration
│   │   ├── matching/           # Proximity-based order matching
│   │   └── audit/              # Audit logging
│   ├── infrastructure/
│   │   ├── firebase/           # Firebase integration
│   │   └── storage/            # File storage abstraction
│   ├── middleware/              # Auth, rate limit, error handler
│   ├── logger/                 # Structured logging
│   └── notification/           # FCM provider
├── migrations/                 # SQL migration files
├── pkg/                        # Shared packages
│   ├── jwt/                    # JWT generation & validation
│   ├── response/               # Standardized API response
│   ├── validator/              # Request validation
│   ├── geo/                    # Geospatial calculations
│   └── fileutil/               # File utilities
├── docs/                       # Swagger documentation
├── .air.toml                   # Hot reload configuration
├── .env.example                # Environment template
├── docker-compose.yml          # PostgreSQL + Redis containers
├── Makefile                    # Development commands
└── go.mod
```

---

## ⚡ Quick Start

```bash
# 1. Clone & setup
git clone git@github.com:Nitip-Workzone/nitip-core.git
cd nitip-core

# 2. Copy environment file
cp .env.example .env
# Edit .env with your database credentials

# 3. Start dependencies (PostgreSQL + Redis)
docker-compose up -d

# 4. Install dev tools
make install-tools

# 5. Run migrations
make migrate-up

# 6. Start development server (hot reload)
make dev
```

---

## 🛠️ Makefile Commands

```bash
make help                       # Show all available commands
make run                        # Build & start server
make dev                        # Hot reload with Air
make build                      # Build binary to ./bin/server
make clean                      # Remove build artifacts

make migrate-up                 # Run all pending migrations
make migrate-down               # Rollback 1 step
make migrate-status             # Check current migration version
make migrate-create name=xxx    # Create new migration file
make migrate-drop               # Drop all tables (DANGER!)

make test                       # Run unit tests
make test-coverage              # Generate HTML coverage report
make lint                       # Run golangci-lint
make tidy                       # go mod tidy + verify
```

---

## 🔑 Key Features

### Authentication & Security
- JWT with access + refresh token rotation
- Device-based session management
- PIN-based transaction security (setup, change, verify)
- PIN lockout after 5 failed attempts (24h ban)
- Rate limiting on sensitive endpoints
- HMAC grant token for login protection

### Order Management
- **Titip Beli** — Requester asks runner to buy items
- **Titip Kirim** — Requester asks runner to deliver packages
- Order lifecycle: created → assigned → picked up → delivered → completed
- QR code completion verification
- Dispute & cancellation support
- Price adjustment & checking fee

### Geospatial (PostGIS)
- Proximity-based runner matching (< 10km radius)
- Spatial indexing for fast location queries
- Haversine distance calculation

### Wallet & Payments
- Digital wallet with balance management
- Top-up via QRIS integration (mock provider available)
- Withdrawal with multiple channels
- Service fee & system wallet
- Transaction history

### Real-time Features
- WebSocket chat between requester & runner
- Push notifications (FCM)
- Order status updates

### Admin Panel Support
- User management (verify, suspend, trust score)
- Order oversight & dispute resolution
- System configuration management
- Audit logging

---

## 🔧 Database Driver

Switch between PostgreSQL and MySQL via `.env`:

```env
# PostgreSQL (default, recommended)
DB_DRIVER=postgres
DB_PORT=5432

# MySQL
DB_DRIVER=mysql
DB_PORT=3306
```

All query code remains the same — bun handles dialect automatically.

---

## 📡 API Endpoints Overview

### Auth
| Method | Path | Description |
|---|---|---|
| POST | `/auth/grant` | Get HMAC grant token |
| POST | `/auth/login` | Login (email + password) |
| POST | `/auth/refresh` | Refresh access token |
| POST | `/auth/logout` | Logout (invalidate session) | 