package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"

	"github.com/codecoffy/nitip-core/config"
	_ "github.com/go-sql-driver/mysql"
	_ "github.com/lib/pq"
	"github.com/pressly/goose/v3"
)

const migrationsDir = "migrations"

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: migrate <up|down|status|create|reset|version> [name]")
	}

	cfg := config.Load()

	// Open raw sql.DB — goose doesn't need bun, just standard database/sql
	var (
		db     *sql.DB
		err    error
		driver string
	)

	switch cfg.DBDriver {
	case "mysql":
		driver = "mysql"
		db, err = sql.Open("mysql", cfg.DSN())
	default:
		driver = "postgres"
		db, err = sql.Open("postgres", cfg.DSN())
	}

	if err != nil {
		// SECURITY: never log DSN with password — use SafeDSN
		log.Fatalf("failed to open db (dsn: %s): %v", cfg.SafeDSN(), sanitizeDBError(err))
	}
	defer func() { _ = db.Close() }()

	if err := db.Ping(); err != nil {
		log.Fatalf("db ping failed (dsn: %s): %v", cfg.SafeDSN(), sanitizeDBError(err))
	}

	goose.SetDialect(driver) //nolint:errcheck

	command := os.Args[1]

	switch command {
	case "create":
		if len(os.Args) < 3 {
			log.Fatal("usage: migrate create <name>")
		}
		name := os.Args[2]
		if err := goose.Create(db, migrationsDir, name, "sql"); err != nil {
			log.Fatalf("migrate create failed: %v", err)
		}

	default:
		// up, down, status, reset, version, fix, validate ...
		if err := goose.RunContext(context.Background(), command, db, migrationsDir); err != nil {
			log.Fatalf("migrate %s failed (dsn: %s): %v", command, cfg.SafeDSN(), sanitizeDBError(err))
		}
	}

	fmt.Printf("migrate %s: done\n", command)
}

func sanitizeDBError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if len(msg) > 300 {
		msg = msg[:300] + "... (truncated)"
	}
	if len(msg) >= 10 {
		for i := 0; i <= len(msg)-10; i++ {
			if msg[i:i+10] == "postgres:/" {
				return "connection failed (credentials redacted for security)"
			}
		}
	}
	if len(msg) >= 3 {
		for i := 0; i <= len(msg)-3; i++ {
			if msg[i:i+3] == "://" {
				// contains DSN-like string
				return "connection failed (credentials redacted for security)"
			}
		}
	}
	return msg
}
