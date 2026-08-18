package banner

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/codecoffy/nitip-core/internal/storage"
	"github.com/google/uuid"
)

func sanitizeStorageKey(urlStr string) string {
	if urlStr == "" {
		return ""
	}
	if strings.HasPrefix(urlStr, "http://") || strings.HasPrefix(urlStr, "https://") {
		temp := urlStr
		if strings.HasPrefix(temp, "https://") {
			temp = strings.TrimPrefix(temp, "https://")
		} else {
			temp = strings.TrimPrefix(temp, "http://")
		}

		slashIdx := strings.Index(temp, "/")
		if slashIdx != -1 {
			path := temp[slashIdx+1:]
			path = strings.TrimPrefix(path, "uploads/")

			// Strip query parameters (e.g. ?q-sign-algorithm=...)
			if qIdx := strings.Index(path, "?"); qIdx != -1 {
				path = path[:qIdx]
			}
			return path
		}
	}
	return urlStr
}

type Service interface {
	GetAllBanners(ctx context.Context) ([]Banner, error)
	GetActiveBanners(ctx context.Context) ([]Banner, error)
	GetBannerByID(ctx context.Context, id uuid.UUID) (*Banner, error)
	CreateBanner(ctx context.Context, title, imageURL string, redirectURL *string, isActive bool) (*Banner, error)
	UpdateBanner(ctx context.Context, id uuid.UUID, title, imageURL string, redirectURL *string, isActive bool) (*Banner, error)
	DeleteBanner(ctx context.Context, id uuid.UUID) error
	UploadImage(ctx context.Context, filename string, content io.Reader, size int64, contentType string) (string, error)
}

type service struct {
	repo    Repository
	storage storage.Storage
}

func NewService(repo Repository, storage storage.Storage) Service {
	return &service{repo: repo, storage: storage}
}

func (s *service) GetAllBanners(ctx context.Context) ([]Banner, error) {
	banners, err := s.repo.GetAll(ctx)
	if err != nil {
		return nil, err
	}
	for i := range banners {
		if signed, err := s.storage.SignedURL(ctx, banners[i].ImageURL, 1*time.Hour); err == nil {
			banners[i].ImageURL = signed
		}
	}
	return banners, nil
}

func (s *service) GetActiveBanners(ctx context.Context) ([]Banner, error) {
	banners, err := s.repo.GetActive(ctx)
	if err != nil {
		return nil, err
	}
	for i := range banners {
		if signed, err := s.storage.SignedURL(ctx, banners[i].ImageURL, 1*time.Hour); err == nil {
			banners[i].ImageURL = signed
		}
	}
	return banners, nil
}

func (s *service) GetBannerByID(ctx context.Context, id uuid.UUID) (*Banner, error) {
	banner, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if signed, err := s.storage.SignedURL(ctx, banner.ImageURL, 1*time.Hour); err == nil {
		banner.ImageURL = signed
	}
	return banner, nil
}

func (s *service) CreateBanner(ctx context.Context, title, imageURL string, redirectURL *string, isActive bool) (*Banner, error) {
	banner := &Banner{
		ID:          uuid.New(),
		Title:       title,
		ImageURL:    sanitizeStorageKey(imageURL),
		RedirectURL: redirectURL,
		IsActive:    isActive,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	err := s.repo.Create(ctx, banner)
	if err != nil {
		return nil, err
	}
	if signed, err := s.storage.SignedURL(ctx, banner.ImageURL, 1*time.Hour); err == nil {
		banner.ImageURL = signed
	}
	return banner, nil
}

func (s *service) UpdateBanner(ctx context.Context, id uuid.UUID, title, imageURL string, redirectURL *string, isActive bool) (*Banner, error) {
	banner, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	banner.Title = title
	banner.ImageURL = sanitizeStorageKey(imageURL)
	banner.RedirectURL = redirectURL
	banner.IsActive = isActive
	banner.UpdatedAt = time.Now()

	err = s.repo.Update(ctx, banner)
	if err != nil {
		return nil, err
	}
	if signed, err := s.storage.SignedURL(ctx, banner.ImageURL, 1*time.Hour); err == nil {
		banner.ImageURL = signed
	}
	return banner, nil
}

func (s *service) DeleteBanner(ctx context.Context, id uuid.UUID) error {
	return s.repo.Delete(ctx, id)
}

func (s *service) UploadImage(ctx context.Context, filename string, content io.Reader, size int64, contentType string) (string, error) {
	// Cache-busting: tiap upload unik agar CDN https://upload.nihtip.com/ tidak cache lama saat update image
	objectKey := "banners/" + uuid.New().String() + "_" + fmt.Sprintf("%d", time.Now().UnixNano()) + "_" + filename
	return s.storage.Upload(ctx, objectKey, content, size, contentType)
}
