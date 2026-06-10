package audit

import (
	"context"

	"github.com/uptrace/bun"
)

type Repository interface {
	Create(ctx context.Context, db bun.IDB, log *AuditLog) error
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
