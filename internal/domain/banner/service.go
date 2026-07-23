package banner

import (
	"context"
	"time"

	"github.com/google/uuid"
)

type Service interface {
	GetAllBanners(ctx context.Context) ([]Banner, error)
	GetActiveBanners(ctx context.Context) ([]Banner, error)
	GetBannerByID(ctx context.Context, id uuid.UUID) (*Banner, error)
	CreateBanner(ctx context.Context, title, imageURL string, redirectURL *string, isActive bool) (*Banner, error)
	UpdateBanner(ctx context.Context, id uuid.UUID, title, imageURL string, redirectURL *string, isActive bool) (*Banner, error)
	DeleteBanner(ctx context.Context, id uuid.UUID) error
}

type service struct {
	repo Repository
}

func NewService(repo Repository) Service {
	return &service{repo: repo}
}

func (s *service) GetAllBanners(ctx context.Context) ([]Banner, error) {
	return s.repo.GetAll(ctx)
}

func (s *service) GetActiveBanners(ctx context.Context) ([]Banner, error) {
	return s.repo.GetActive(ctx)
}

func (s *service) GetBannerByID(ctx context.Context, id uuid.UUID) (*Banner, error) {
	return s.repo.GetByID(ctx, id)
}

func (s *service) CreateBanner(ctx context.Context, title, imageURL string, redirectURL *string, isActive bool) (*Banner, error) {
	banner := &Banner{
		ID:          uuid.New(),
		Title:       title,
		ImageURL:    imageURL,
		RedirectURL: redirectURL,
		IsActive:    isActive,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	err := s.repo.Create(ctx, banner)
	if err != nil {
		return nil, err
	}
	return banner, nil
}

func (s *service) UpdateBanner(ctx context.Context, id uuid.UUID, title, imageURL string, redirectURL *string, isActive bool) (*Banner, error) {
	banner, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	banner.Title = title
	banner.ImageURL = imageURL
	banner.RedirectURL = redirectURL
	banner.IsActive = isActive
	banner.UpdatedAt = time.Now()

	err = s.repo.Update(ctx, banner)
	if err != nil {
		return nil, err
	}
	return banner, nil
}

func (s *service) DeleteBanner(ctx context.Context, id uuid.UUID) error {
	return s.repo.Delete(ctx, id)
}
