package database

import (
	"database/sql"
	"fmt"

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
		db = bun.NewDB(sqldb, mysqldialect.New())
		logger.Info("database driver: mysql",
			zap.String("host", cfg.DBHost),
			zap.String("port", cfg.DBPort),
			zap.String("name", cfg.DBName),
		)

	default: // postgres
		sqldb := sql.OpenDB(pgdriver.NewConnector(pgdriver.WithDSN(cfg.DSN())))
		db = bun.NewDB(sqldb, pgdialect.New())
		logger.Info("database driver: postgres",
			zap.String("host", cfg.DBHost),
			zap.String("port", cfg.DBPort),
			zap.String("name", cfg.DBName),
		)
	}

	// Ping to verify connection on startup
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("database ping failed: %w", err)
	}

	logger.Info("database connected successfully")
	return db, nil
}
