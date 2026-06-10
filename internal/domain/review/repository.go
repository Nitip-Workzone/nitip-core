package review

import (
	"context"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type Repository interface {
	RunInTx(ctx context.Context, fn func(ctx context.Context, tx bun.Tx) error) error

	Create(ctx context.Context, db bun.IDB, review *Review) error
	GetByOrderID(ctx context.Context, db bun.IDB, orderID uuid.UUID) (*Review, error)
	GetAverageRatingByReviewee(ctx context.Context, db bun.IDB, revieweeID uuid.UUID) (float64, error)

	UpdateUserTrustScore(ctx context.Context, db bun.IDB, userID uuid.UUID, newScore float64) error
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

func (r *repository) GetByOrderID(ctx context.Context, db bun.IDB, orderID uuid.UUID) (*Review, error) {
	rv := new(Review)
	err := db.NewSelect().Model(rv).Where("order_id = ?", orderID).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return rv, nil
}

func (r *repository) GetAverageRatingByReviewee(ctx context.Context, db bun.IDB, revieweeID uuid.UUID) (float64, error) {
	var avg float64
	err := db.NewSelect().Model((*Review)(nil)).
		ColumnExpr("COALESCE(AVG(rating), 0)").
		Where("reviewee_id = ?", revieweeID).
		Scan(ctx, &avg)
	return avg, err
}

// UpdateUserTrustScore cross-updates the user table.
func (r *repository) UpdateUserTrustScore(ctx context.Context, db bun.IDB, userID uuid.UUID, newScore float64) error {
	// NOTE: Depending on architecture, crossing boundaries like this directly in the repo might be frowned upon,
	//       but for MVP performance without creating an event-bus, updating the raw table here inside the transaction is the safest state.
	_, err := db.NewRaw("UPDATE users SET trust_score = ?, updated_at = current_timestamp WHERE id = ?", newScore, userID).Exec(ctx)
	return err
}
