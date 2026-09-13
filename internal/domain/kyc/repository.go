package kyc

//go:generate mockgen -source=repository.go -destination=mocks/repository.go -package=mocks

import (
	"context"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type Repository interface {
	Create(ctx context.Context, kyc *KycSubmission) error
	GetByID(ctx context.Context, id uuid.UUID) (*KycSubmission, error)
	GetByUserID(ctx context.Context, userID uuid.UUID) (*KycSubmission, error)
	GetByUserIDAndTarget(ctx context.Context, userID uuid.UUID, target string) ([]KycSubmission, error)
	GetPendingByUserAndTarget(ctx context.Context, userID uuid.UUID, target string) (*KycSubmission, error)
	CountRejectionsByTarget(ctx context.Context, userID uuid.UUID, target string) (int, error)
	CountRetryUnlocks(ctx context.Context, userID uuid.UUID, target string) (int, error)
	CountRetryUnlocksForUpdate(ctx context.Context, tx bun.Tx, userID uuid.UUID, target string) (int, error)
	CreateRetryUnlock(ctx context.Context, userID uuid.UUID, target string, actorID uuid.UUID) error
	CreateRetryUnlockInTx(ctx context.Context, tx bun.Tx, userID uuid.UUID, target string, actorID uuid.UUID) error
	ListPending(ctx context.Context, offset, limit int) ([]KycSubmission, error)
	Update(ctx context.Context, kyc *KycSubmission) error
	// Transactional helpers
	GetByIDForUpdate(ctx context.Context, tx bun.Tx, id uuid.UUID) (*KycSubmission, error)
	UpdateInTx(ctx context.Context, tx bun.Tx, kyc *KycSubmission) error
	GetUserForUpdate(ctx context.Context, tx bun.Tx, userID uuid.UUID) (*UserLevelRow, error)
	UpdateUserLevelInTx(ctx context.Context, tx bun.Tx, userID uuid.UUID, level string, isVerified bool) error
	RunInTx(ctx context.Context, fn func(ctx context.Context, tx bun.Tx) error) error
}

type UserLevelRow struct {
	KycLevel   string `bun:"kyc_level"`
	IsVerified bool   `bun:"is_verified"`
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

func (r *repository) GetByUserIDAndTarget(ctx context.Context, userID uuid.UUID, target string) ([]KycSubmission, error) {
	var out []KycSubmission
	err := r.db.NewSelect().Model(&out).Where("user_id = ?", userID).Where("target_level = ?", target).Order("created_at DESC").Scan(ctx)
	return out, err
}

func (r *repository) GetPendingByUserAndTarget(ctx context.Context, userID uuid.UUID, target string) (*KycSubmission, error) {
	kyc := new(KycSubmission)
	err := r.db.NewSelect().Model(kyc).Where("user_id = ?", userID).Where("target_level = ?", target).Where("status = ?", StatusPending).Limit(1).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return kyc, nil
}

func (r *repository) CountRejectionsByTarget(ctx context.Context, userID uuid.UUID, target string) (int, error) {
	cnt, err := r.db.NewSelect().Model((*KycSubmission)(nil)).Where("user_id = ?", userID).Where("target_level = ?", target).Where("status = ?", StatusRejected).Count(ctx)
	return cnt, err
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

func (r *repository) GetByIDForUpdate(ctx context.Context, tx bun.Tx, id uuid.UUID) (*KycSubmission, error) {
	kyc := new(KycSubmission)
	err := tx.NewSelect().Model(kyc).Where("id = ?", id).For("UPDATE").Scan(ctx)
	if err != nil {
		return nil, err
	}
	return kyc, nil
}

func (r *repository) UpdateInTx(ctx context.Context, tx bun.Tx, kyc *KycSubmission) error {
	_, err := tx.NewUpdate().Model(kyc).WherePK().Exec(ctx)
	return err
}

func (r *repository) GetUserForUpdate(ctx context.Context, tx bun.Tx, userID uuid.UUID) (*UserLevelRow, error) {
	row := new(UserLevelRow)
	err := tx.NewSelect().Table("users").Column("kyc_level", "is_verified").Where("id = ?", userID).For("UPDATE").Scan(ctx, row)
	if err != nil {
		return nil, err
	}
	return row, nil
}

func (r *repository) UpdateUserLevelInTx(ctx context.Context, tx bun.Tx, userID uuid.UUID, level string, isVerified bool) error {
	_, err := tx.NewUpdate().Table("users").Set("kyc_level = ?", level).Set("is_verified = ?", isVerified).Set("verified_at = CASE WHEN ? THEN NOW() ELSE verified_at END", isVerified).Set("updated_at = NOW()").Where("id = ?", userID).Exec(ctx)
	return err
}

func (r *repository) CountRetryUnlocks(ctx context.Context, userID uuid.UUID, target string) (int, error) {
	cnt, err := r.db.NewSelect().Table("kyc_retry_unlocks").Where("user_id = ?", userID).Where("target_level = ?", target).Count(ctx)
	return cnt, err
}

func (r *repository) CountRetryUnlocksForUpdate(ctx context.Context, tx bun.Tx, userID uuid.UUID, target string) (int, error) {
	cnt, err := tx.NewSelect().Table("kyc_retry_unlocks").Where("user_id = ?", userID).Where("target_level = ?", target).For("UPDATE").Count(ctx)
	return cnt, err
}

func (r *repository) CreateRetryUnlock(ctx context.Context, userID uuid.UUID, target string, actorID uuid.UUID) error {
	_, err := r.db.NewInsert().Table("kyc_retry_unlocks").Model(&struct {
		bun.BaseModel `bun:"table:kyc_retry_unlocks"`
		ID            string `bun:"id,pk,type:uuid"`
		UserID        string `bun:"user_id,type:uuid"`
		TargetLevel   string `bun:"target_level"`
		UnlockedBy    string `bun:"unlocked_by,type:uuid"`
	}{ID: uuid.New().String(), UserID: userID.String(), TargetLevel: target, UnlockedBy: actorID.String()}).Exec(ctx)
	return err
}

func (r *repository) CreateRetryUnlockInTx(ctx context.Context, tx bun.Tx, userID uuid.UUID, target string, actorID uuid.UUID) error {
	_, err := tx.NewInsert().Table("kyc_retry_unlocks").Model(&struct {
		bun.BaseModel `bun:"table:kyc_retry_unlocks"`
		ID            string `bun:"id,pk,type:uuid"`
		UserID        string `bun:"user_id,type:uuid"`
		TargetLevel   string `bun:"target_level"`
		UnlockedBy    string `bun:"unlocked_by,type:uuid"`
	}{ID: uuid.New().String(), UserID: userID.String(), TargetLevel: target, UnlockedBy: actorID.String()}).Exec(ctx)
	return err
}

func (r *repository) RunInTx(ctx context.Context, fn func(ctx context.Context, tx bun.Tx) error) error {
	return r.db.RunInTx(ctx, nil, fn)
}
