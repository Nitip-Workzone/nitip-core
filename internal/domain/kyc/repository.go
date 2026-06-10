package kyc

import (
	"context"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type Repository interface {
	Create(ctx context.Context, kyc *KycSubmission) error
	GetByID(ctx context.Context, id uuid.UUID) (*KycSubmission, error)
	GetByUserID(ctx context.Context, userID uuid.UUID) (*KycSubmission, error)
	ListPending(ctx context.Context, offset, limit int) ([]KycSubmission, error)
	Update(ctx context.Context, kyc *KycSubmission) error
}

type repository struct {
	db *bun.DB
}

func NewRepository(db *bun.DB) Repository {
	return &repository{db: db}
}

func (r *repository) Create(ctx context.Context, kyc *KycSubmission) error {
	_, err := r.db.NewInsert().Model(kyc).Exec(ctx)
	return err
}

func (r *repository) GetByID(ctx context.Context, id uuid.UUID) (*KycSubmission, error) {
	kyc := new(KycSubmission)
	err := r.db.NewSelect().Model(kyc).Where("id = ?", id).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return kyc, nil
}

func (r *repository) GetByUserID(ctx context.Context, userID uuid.UUID) (*KycSubmission, error) {
	kyc := new(KycSubmission)
	err := r.db.NewSelect().Model(kyc).Where("user_id = ?", userID).Order("created_at DESC").Limit(1).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return kyc, nil
}

func (r *repository) ListPending(ctx context.Context, offset, limit int) ([]KycSubmission, error) {
	var results []KycSubmission
	err := r.db.NewSelect().
		Model(&results).
		Where("status = ?", StatusPending).
		Offset(offset).
		Limit(limit).
		Scan(ctx)
	return results, err
}

func (r *repository) Update(ctx context.Context, kyc *KycSubmission) error {
	_, err := r.db.NewUpdate().Model(kyc).WherePK().Exec(ctx)
	return err
}
