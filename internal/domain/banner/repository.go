package banner

import (
	"context"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type Repository interface {
	GetAll(ctx context.Context) ([]Banner, error)
	GetActive(ctx context.Context) ([]Banner, error)
	GetByID(ctx context.Context, id uuid.UUID) (*Banner, error)
	Create(ctx context.Context, banner *Banner) error
	Update(ctx context.Context, banner *Banner) error
	Delete(ctx context.Context, id uuid.UUID) error
}

type repository struct {
	db *bun.DB
}

func NewRepository(db *bun.DB) Repository {
	return &repository{db: db}
}

func (r *repository) GetAll(ctx context.Context) ([]Banner, error) {
	var banners []Banner
	err := r.db.NewSelect().Model(&banners).Order("created_at DESC").Scan(ctx)
	return banners, err
}

func (r *repository) GetActive(ctx context.Context) ([]Banner, error) {
	var banners []Banner
	err := r.db.NewSelect().Model(&banners).Where("is_active = ?", true).Order("created_at DESC").Scan(ctx)
	return banners, err
}

func (r *repository) GetByID(ctx context.Context, id uuid.UUID) (*Banner, error) {
	b := new(Banner)
	err := r.db.NewSelect().Model(b).Where("id = ?", id).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func (r *repository) Create(ctx context.Context, banner *Banner) error {
	if banner.ID == uuid.Nil {
		banner.ID = uuid.New()
	}
	_, err := r.db.NewInsert().Model(banner).Exec(ctx)
	return err
}

func (r *repository) Update(ctx context.Context, banner *Banner) error {
	_, err := r.db.NewUpdate().Model(banner).WherePK().Exec(ctx)
	return err
}

func (r *repository) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.NewDelete().Model((*Banner)(nil)).Where("id = ?", id).Exec(ctx)
	return err
}
