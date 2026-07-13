package review

import (
	"context"
	"errors"

	"github.com/codecoffy/nitip-core/internal/domain/order"
	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type Service interface {
	SubmitReview(ctx context.Context, orderID, reviewerID uuid.UUID, rating int, comment string) error
	GetReviewByOrder(ctx context.Context, orderID uuid.UUID) (*Review, error)
}

type service struct {
	repo      Repository
	orderRepo order.Repository
	db        *bun.DB
}

func NewService(repo Repository, orderRepo order.Repository, db *bun.DB) Service {
	return &service{repo: repo, orderRepo: orderRepo, db: db}
}

func (s *service) SubmitReview(ctx context.Context, orderID, reviewerID uuid.UUID, rating int, comment string) error {
	if rating < 1 || rating > 5 {
		return errors.New("rating harus antara 1 sampai 5")
	}

	// Wait, we need to make sure the order belongs to them and is completed!
	o, err := s.orderRepo.FindByID(ctx, orderID)
	if err != nil {
		return errors.New("pesanan tidak ditemukan")
	}

	if o.RequesterID != reviewerID {
		return errors.New("anda hanya dapat mengulas pesanan anda sendiri")
	}

	if o.Status != order.StatusCompleted {
		return errors.New("pesanan harus selesai sebelum dapat diulas")
	}

	if o.RunnerID == nil {
		return errors.New("pesanan belum memiliki runner")
	}

	// Check if already reviewed
	existing, _ := s.repo.GetByOrderID(ctx, s.db, orderID)
	if existing != nil {
		return errors.New("pesanan ini sudah diulas")
	}

	return s.repo.RunInTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		// 1. Insert Review
		rv := &Review{
			ID:         uuid.New(),
			OrderID:    orderID,
			ReviewerID: reviewerID,
			RevieweeID: *o.RunnerID,
			Rating:     rating,
			Comment:    comment,
		}
		if err := s.repo.Create(ctx, tx, rv); err != nil {
			return err
		}

		// 2. Fetch Average
		avg, err := s.repo.GetAverageRatingByReviewee(ctx, tx, *o.RunnerID)
		if err != nil {
			return err
		}

		// 3. Sync to User Profile (Trust Score)
		return s.repo.UpdateUserTrustScore(ctx, tx, *o.RunnerID, avg)
	})
}

func (s *service) GetReviewByOrder(ctx context.Context, orderID uuid.UUID) (*Review, error) {
	return s.repo.GetByOrderID(ctx, s.db, orderID)
}
