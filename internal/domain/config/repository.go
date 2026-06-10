package systemconfig

import (
	"context"

	"github.com/uptrace/bun"
)

type Repository interface {
	Get(ctx context.Context, key string) (*Config, error)
	GetAll(ctx context.Context) ([]Config, error)
	Set(ctx context.Context, cfg *Config) error
}

type repository struct {
	db *bun.DB
}

func NewRepository(db *bun.DB) Repository {
	return &repository{db: db}
}

func (r *repository) Get(ctx context.Context, key string) (*Config, error) {
	cfg := new(Config)
	err := r.db.NewSelect().Model(cfg).Where("key = ?", key).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

func (r *repository) GetAll(ctx context.Context) ([]Config, error) {
	cfgs := []Config{}
	err := r.db.NewSelect().Model(&cfgs).Order("key ASC").Scan(ctx)
	return cfgs, err
}

func (r *repository) Set(ctx context.Context, cfg *Config) error {
	_, err := r.db.NewInsert().Model(cfg).
		On("CONFLICT (key) DO UPDATE").
		Set("value = EXCLUDED.value").
		Set("description = EXCLUDED.description").
		Set("updated_at = current_timestamp").
		Exec(ctx)
	return err
}
