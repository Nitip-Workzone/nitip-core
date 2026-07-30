package database

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/codecoffy/nitip-core/config"
	_ "github.com/go-sql-driver/mysql"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/mysqldialect"
	"github.com/uptrace/bun/dialect/pgdialect"
	"github.com/uptrace/bun/driver/pgdriver"
	"go.uber.org/zap"
)

// New creates a bun.DB instance based on the configured driver.
// The returned *bun.DB interface is identical regardless of the underlying driver,
// meaning all query code is portable between postgres and mysql without modification.
func New(cfg *config.Config, logger *zap.Logger) (*bun.DB, error) {
	var db *bun.DB

	switch cfg.DBDriver {
	case "mysql":
		sqldb, err := sql.Open("mysql", cfg.DSN())
		if err != nil {
			return nil, fmt.Errorf("failed to open mysql connection: %w", err)
		}

		// P1 FIX: tuned pool for 512M — IdleTime 5m + jitter avoidance + higher idle for burst
		sqldb.SetMaxOpenConns(50)
		sqldb.SetMaxIdleConns(20)
		sqldb.SetConnMaxLifetime(30 * time.Minute)
		sqldb.SetConnMaxIdleTime(5 * time.Minute)

		db = bun.NewDB(sqldb, mysqldialect.New())
		logger.Info("database driver: mysql",
			zap.String("host", cfg.DBHost),
			zap.String("port", cfg.DBPort),
			zap.String("name", cfg.DBName),
		)

	default: // postgres
		sqldb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(cfg.DSN())))

		// P1 FIX: tuned pool for 512M — IdleTime 5m + higher idle for burst
		sqldb.SetMaxOpenConns(50)
		sqldb.SetMaxIdleConns(20)
		sqldb.SetConnMaxLifetime(30 * time.Minute)
		sqldb.SetConnMaxIdleTime(5 * time.Minute)

		db = bun.NewDB(sqldb, pgdialect.New())
		logger.Info("database driver: postgres",
			zap.String("host", cfg.DBHost),
			zap.String("port", cfg.DBPort),
			zap.String("name", cfg.DBName),
		)
	}

	// Ping to verify connection on startup — sanitize error to not leak password in GitHub Actions logs
	if err := db.Ping(); err != nil {
		// Never log DSN with password. Sanitize error message that might contain DSN (e.g., parse "postgres://user:password@...")
		sanitized := sanitizeDBError(err)
		return nil, fmt.Errorf("database ping failed: %s (dsn: %s)", sanitized, cfg.SafeDSN())
	}

	logger.Info("database connected successfully")
	return db, nil
}

// sanitizeDBError removes password from error string that might contain DSN
func sanitizeDBError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	// If error contains postgres:// or mysql:// with password, redact
	// Simple heuristic: replace ://user:pass@ with ://user:***@
	// We don't regex entire DSN, just strip anything that looks like "postgres://...pass..."
	// To avoid leaking cr34t3PROD193290 in logs like `parse "postgres://nitip-prod:cr34t3PROD193290": invalid port`
	// Replace password segment with ***
	// Search for "://"
	// This is defensive: if error contains "postgres://", redact until next @ or " or space
	// Quick implementation: if contains "postgres://" or starts with parse
	// Replace by not showing raw err if it contains :// and @
	if len(msg) > 200 {
		// Truncate long errors that might contain full DSN
		msg = msg[:200] + "... (truncated for security)"
	}
	// If message contains password-like DSN, mask
	if contains := func(s, substr string) bool {
		return len(s) >= len(substr) && (func() bool {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
			return false
		})()
	}; contains(msg, "postgres://") || contains(msg, "://") {
		// Mask anything between :// and @ as user:pass -> user:***
		// Simplified: return generic message
		return "connection failed (credentials redacted for security)"
	}
	return msg
}
