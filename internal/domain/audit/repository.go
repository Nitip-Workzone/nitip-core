package audit

import (
	"context"

	"github.com/uptrace/bun"
)

type Repository interface {
	Create(ctx context.Context, db bun.IDB, log *AuditLog) error
	List(ctx context.Context, db bun.IDB, offset, limit int, action string) ([]AuditLog, int, error)
}

type repository struct {
	db *bun.DB
}

func NewRepository(db *bun.DB) Repository {
	return &repository{db: db}
}

func (r *repository) Create(ctx context.Context, db bun.IDB, log *AuditLog) error {
	_, err := db.NewInsert().Model(log).Exec(ctx)
	return err
}

func (r *repository) List(ctx context.Context, db bun.IDB, offset, limit int, action string) ([]AuditLog, int, error) {
	var logs []AuditLog
	q := db.NewSelect().Model(&logs)

	if action != "" {
		q = q.Where("action = ?", action)
	}

	total, err := q.Order("created_at DESC").Limit(limit).Offset(offset).ScanAndCount(ctx)
	return logs, total, err
}
