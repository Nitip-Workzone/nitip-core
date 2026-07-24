package review

import (
	"context"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type Repository interface {
	RunInTx(ctx context.Context, fn func(ctx context.Context, tx bun.Tx) error) error

	Create(ctx context.Context, db bun.IDB, review *Review) error
	GetByOrderIDAndReviewerID(ctx context.Context, db bun.IDB, orderID, reviewerID uuid.UUID) (*Review, error)
	GetAverageRatingByReviewee(ctx context.Context, db bun.IDB, revieweeID uuid.UUID) (float64, error)
	GetAverageRatingByMerchant(ctx context.Context, db bun.IDB, merchantID uuid.UUID) (float64, error)
	GetAverageRatingByRequester(ctx context.Context, db bun.IDB, requesterID uuid.UUID) (float64, error)

	UpdateUserTrustScore(ctx context.Context, db bun.IDB, userID uuid.UUID, newScore float64) error
	UpdateMerchantRating(ctx context.Context, db bun.IDB, merchantID uuid.UUID, newRating float64) error
}

type repository struct {
	db *bun.DB
}

func NewRepository(db *bun.DB) Repository {
	return &repository{db: db}
}

func (r *repository) RunInTx(ctx context.Context, fn func(ctx context.Context, tx bun.Tx) error) error {
	return r.db.RunInTx(ctx, nil, fn)
}

func (r *repository) Create(ctx context.Context, db bun.IDB, review *Review) error {
	_, err := db.NewInsert().Model(review).Exec(ctx)
	return err
}

func (r *repository) GetByOrderIDAndReviewerID(ctx context.Context, db bun.IDB, orderID, reviewerID uuid.UUID) (*Review, error) {
	rv := new(Review)
	err := db.NewSelect().Model(rv).Where("order_id = ?", orderID).Where("reviewer_id = ?", reviewerID).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return rv, nil
}

func (r *repository) GetAverageRatingByReviewee(ctx context.Context, db bun.IDB, revieweeID uuid.UUID) (float64, error) {
	var avg float64
	err := db.NewSelect().Model((*Review)(nil)).
		ColumnExpr("COALESCE(AVG(runner_rating), 0)").
		Where("runner_id = ?", revieweeID).
		Where("runner_rating IS NOT NULL").
		Scan(ctx, &avg)
	return avg, err
}

func (r *repository) GetAverageRatingByMerchant(ctx context.Context, db bun.IDB, merchantID uuid.UUID) (float64, error) {
	var avg float64
	err := db.NewSelect().Model((*Review)(nil)).
		ColumnExpr("COALESCE(AVG(merchant_rating), 0)").
		Where("merchant_id = ?", merchantID).
		Where("merchant_rating IS NOT NULL").
		Scan(ctx, &avg)
	return avg, err
}

func (r *repository) GetAverageRatingByRequester(ctx context.Context, db bun.IDB, requesterID uuid.UUID) (float64, error) {
	var avg float64
	err := db.NewSelect().Model((*Review)(nil)).
		ColumnExpr("COALESCE(AVG(requester_rating), 0)").
		Where("requester_id = ?", requesterID).
		Where("requester_rating IS NOT NULL").
		Scan(ctx, &avg)
	return avg, err
}

// UpdateUserTrustScore cross-updates the user table.
func (r *repository) UpdateUserTrustScore(ctx context.Context, db bun.IDB, userID uuid.UUID, newScore float64) error {
	_, err := db.NewRaw("UPDATE users SET trust_score = ?, updated_at = current_timestamp WHERE id = ?", newScore, userID).Exec(ctx)
	return err
}

// UpdateMerchantRating cross-updates the merchants table.
func (r *repository) UpdateMerchantRating(ctx context.Context, db bun.IDB, merchantID uuid.UUID, newRating float64) error {
	_, err := db.NewRaw("UPDATE merchants SET rating = ?, updated_at = current_timestamp WHERE id = ?", newRating, merchantID).Exec(ctx)
	return err
}
