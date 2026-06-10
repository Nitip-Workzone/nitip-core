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
		log.Fatalf("failed to open db: %v", err)
	}
	defer func() { _ = db.Close() }()

	if err := db.Ping(); err != nil {
		log.Fatalf("db ping failed: %v", err)
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
			log.Fatalf("migrate %s failed: %v", command, err)
		}
	}

	fmt.Printf("migrate %s: done\n", command)
}
