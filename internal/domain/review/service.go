package review

import (
	"context"
	"errors"
	"strings"

	"github.com/codecoffy/nitip-core/internal/domain/order"
	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type Service interface {
	SubmitReview(ctx context.Context, orderID, reviewerID uuid.UUID, runnerRating int, runnerComment string, merchantRating *int, merchantComment string) (*Review, error)
	SubmitRunnerReview(ctx context.Context, orderID, runnerID uuid.UUID, requesterRating int, requesterComment string) (*Review, error)
	GetReviewByOrder(ctx context.Context, orderID, reviewerID uuid.UUID) (*Review, error)
}

type service struct {
	repo      Repository
	orderRepo order.Repository
	db        *bun.DB
}

func NewService(repo Repository, orderRepo order.Repository, db *bun.DB) Service {
	return &service{repo: repo, orderRepo: orderRepo, db: db}
}

func (s *service) SubmitReview(ctx context.Context, orderID, reviewerID uuid.UUID, runnerRating int, runnerComment string, merchantRating *int, merchantComment string) (*Review, error) {
	if runnerRating < 1 || runnerRating > 5 {
		return nil, errors.New("rating runner harus antara 1 sampai 5")
	}
	if merchantRating != nil && (*merchantRating < 1 || *merchantRating > 5) {
		return nil, errors.New("rating merchant harus antara 1 sampai 5")
	}

	// Wait, we need to make sure the order belongs to them and is completed!
	o, err := s.orderRepo.FindByID(ctx, orderID)
	if err != nil {
		return nil, errors.New("pesanan tidak ditemukan")
	}

	if o.RequesterID != reviewerID {
		return nil, errors.New("anda hanya dapat mengulas pesanan anda sendiri")
	}

	if o.Status != order.StatusCompleted {
		return nil, errors.New("pesanan harus selesai sebelum dapat diulas")
	}

	if o.RunnerID == nil {
		return nil, errors.New("pesanan belum memiliki runner")
	}

	// Check if already reviewed
	existing, _ := s.repo.GetByOrderIDAndReviewerID(ctx, s.db, orderID, reviewerID)
	if existing != nil {
		return nil, errors.New("pesanan ini sudah diulas")
	}

	runnerRatingValue := runnerRating
	var saved *Review
	err = s.repo.RunInTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		// 1. Insert Review
		rv := &Review{
			ID:              uuid.New(),
			OrderID:         orderID,
			ReviewerID:      reviewerID,
			RunnerID:        *o.RunnerID,
			RunnerRating:    &runnerRatingValue,
			RunnerComment:   runnerComment,
			MerchantID:      o.MerchantID,
			MerchantRating:  merchantRating,
			MerchantComment: merchantComment,
			RequesterID:      &o.RequesterID,
		}
		if err := s.repo.Create(ctx, tx, rv); err != nil {
			if isDuplicateReviewError(err) {
				return errors.New("pesanan ini sudah diulas")
			}
			return err
		}

		// 2. Fetch Average Runner Rating
		avgRunner, err := s.repo.GetAverageRatingByReviewee(ctx, tx, *o.RunnerID)
		if err != nil {
			return err
		}

		// 3. Sync to User Profile (Trust Score)
		if err := s.repo.UpdateUserTrustScore(ctx, tx, *o.RunnerID, avgRunner); err != nil {
			return err
		}

		// 4. Fetch and Sync Merchant Rating if applicable
		if o.MerchantID != nil && merchantRating != nil {
			avgMerchant, err := s.repo.GetAverageRatingByMerchant(ctx, tx, *o.MerchantID)
			if err != nil {
				return err
			}
			if err := s.repo.UpdateMerchantRating(ctx, tx, *o.MerchantID, avgMerchant); err != nil {
				return err
			}
		}

		saved = rv
		return nil
	})
	return saved, err
}

func (s *service) SubmitRunnerReview(ctx context.Context, orderID, runnerID uuid.UUID, requesterRating int, requesterComment string) (*Review, error) {
	if requesterRating < 1 || requesterRating > 5 {
		return nil, errors.New("rating penitip harus antara 1 sampai 5")
	}

	o, err := s.orderRepo.FindByID(ctx, orderID)
	if err != nil {
		return nil, errors.New("pesanan tidak ditemukan")
	}
	if o.RunnerID == nil || *o.RunnerID != runnerID {
		return nil, errors.New("anda hanya dapat mengulas pesanan yang anda jalankan")
	}
	if o.Status != order.StatusCompleted {
		return nil, errors.New("pesanan harus selesai sebelum dapat diulas")
	}

	existing, _ := s.repo.GetByOrderIDAndReviewerID(ctx, s.db, orderID, runnerID)
	if existing != nil {
		return nil, errors.New("pesanan ini sudah diulas")
	}

	var saved *Review
	err = s.repo.RunInTx(ctx, func(ctx context.Context, tx bun.Tx) error {
		rv := &Review{
			ID:               uuid.New(),
			OrderID:          orderID,
			ReviewerID:       runnerID,
			RunnerID:         runnerID,
			RequesterID:       &o.RequesterID,
			RequesterRating:  &requesterRating,
			RequesterComment: requesterComment,
		}
		if err := s.repo.Create(ctx, tx, rv); err != nil {
			if isDuplicateReviewError(err) {
				return errors.New("pesanan ini sudah diulas")
			}
			return err
		}

		avgRequester, err := s.repo.GetAverageRatingByRequester(ctx, tx, o.RequesterID)
		if err != nil {
			return err
		}
		if err := s.repo.UpdateUserTrustScore(ctx, tx, o.RequesterID, avgRequester); err != nil {
			return err
		}

		saved = rv
		return nil
	})
	return saved, err
}

func (s *service) GetReviewByOrder(ctx context.Context, orderID, reviewerID uuid.UUID) (*Review, error) {
	return s.repo.GetByOrderIDAndReviewerID(ctx, s.db, orderID, reviewerID)
}

func isDuplicateReviewError(err error) bool {
	if err == nil {
		return false
	}

	low := strings.ToLower(err.Error())
	return strings.Contains(low, "duplicate key") ||
		strings.Contains(low, "unique constraint") ||
		strings.Contains(low, "unique violation") ||
		strings.Contains(low, "reviews_order_id_reviewer_id_key")
}
