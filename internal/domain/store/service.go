package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/codecoffy/nitip-core/internal/cache"
	"github.com/codecoffy/nitip-core/internal/storage"
	"github.com/google/uuid"
)

// NearbyStoresCacheTTL is the Redis TTL for proximity query results.
// Stores rarely change; 5 minutes balances freshness and VPS load.
const NearbyStoresCacheTTL = 5 * time.Minute

// AllStoresCacheTTL is the TTL for the full active stores list.
const AllStoresCacheTTL = 10 * time.Minute

type CreateStoreRequest struct {
	Name        string  `json:"name"        validate:"required"`
	Address     string  `json:"address"`
	Lat         float64 `json:"lat"         validate:"required"`
	Lng         float64 `json:"lng"         validate:"required"`
	Category    string  `json:"category"`
	Description string  `json:"description"`
	ImageURL    string  `json:"image_url"`
	IsActive    *bool   `json:"is_active"   validate:"required"`
}

type UpdateStoreRequest struct {
	Name        string  `json:"name"        validate:"required"`
	Address     string  `json:"address"`
	Lat         float64 `json:"lat"         validate:"required"`
	Lng         float64 `json:"lng"         validate:"required"`
	Category    string  `json:"category"`
	Description string  `json:"description"`
	ImageURL    string  `json:"image_url"`
	IsActive    *bool   `json:"is_active"   validate:"required"`
}

type NearbyStoreRequest struct {
	Lat      float64 `query:"lat"    validate:"required"`
	Lng      float64 `query:"lng"    validate:"required"`
	RadiusKm float64 `query:"radius"`
	Limit    int     `query:"limit"`
}

type Service interface {
	GetAllStores(ctx context.Context) ([]Store, error)
	GetActiveStores(ctx context.Context) ([]Store, error)
	GetNearbyStores(ctx context.Context, lat, lng, radiusKm float64, limit int) ([]Store, error)
	CreateStore(ctx context.Context, req CreateStoreRequest) (*Store, error)
	CreateStoresBatch(ctx context.Context, reqs []CreateStoreRequest) (int, error)
	UpdateStore(ctx context.Context, id uuid.UUID, req UpdateStoreRequest) (*Store, error)
	DeleteStore(ctx context.Context, id uuid.UUID) error
}

type service struct {
	repo    Repository
	redis   *cache.Redis
	storage storage.Storage
}

func NewService(repo Repository, redis *cache.Redis, st storage.Storage) Service {
	return &service{repo: repo, redis: redis, storage: st}
}

func (s *service) GetAllStores(ctx context.Context) ([]Store, error) {
	stores, err := s.repo.GetAll(ctx)
	if err != nil {
		return nil, err
	}
	s.signStoreImages(ctx, stores)
	return stores, nil
}

func (s *service) GetActiveStores(ctx context.Context) ([]Store, error) {
	// Cache entire active list — small dataset, rarely mutates
	// Cache raw DB values, sign per request agar signature fresh 1h
	if s.redis != nil {
		cacheKey := "stores:active:all"
		if cached, err := s.redis.Get(ctx, cacheKey); err == nil && cached != "" {
			var stores []Store
			if jsonErr := json.Unmarshal([]byte(cached), &stores); jsonErr == nil {
				s.signStoreImages(ctx, stores)
				return stores, nil
			}
		}

		stores, err := s.repo.GetActive(ctx)
		if err != nil {
			return nil, err
		}
		// Cache raw
		if b, jsonErr := json.Marshal(stores); jsonErr == nil {
			_ = s.redis.Set(ctx, cacheKey, string(b), AllStoresCacheTTL)
		}
		s.signStoreImages(ctx, stores)
		return stores, nil
	}

	stores, err := s.repo.GetActive(ctx)
	if err != nil {
		return nil, err
	}
	s.signStoreImages(ctx, stores)
	return stores, nil
}

// GetNearbyStores returns stores within radiusKm sorted by distance.
// Results are cached per ~1km grid cell to reduce repeated DB queries.
func (s *service) GetNearbyStores(ctx context.Context, lat, lng, radiusKm float64, limit int) ([]Store, error) {
	if radiusKm <= 0 {
		radiusKm = 15
	}
	if limit <= 0 || limit > 20 {
		limit = 20
	}

	if s.redis != nil {
		// Quantize lat/lng to ~1km grid to maximize cache hits for nearby requests
		gridLat := float64(int(lat*10)) / 10 // ~11km precision
		gridLng := float64(int(lng*10)) / 10
		cacheKey := fmt.Sprintf("stores:nearby:%.1f:%.1f:%.0f:%d", gridLat, gridLng, radiusKm, limit)

		if cached, err := s.redis.Get(ctx, cacheKey); err == nil && cached != "" {
			var stores []Store
			if jsonErr := json.Unmarshal([]byte(cached), &stores); jsonErr == nil {
				s.signStoreImages(ctx, stores)
				return stores, nil
			}
		}

		stores, err := s.repo.FindNearby(ctx, lat, lng, radiusKm, limit)
		if err != nil {
			return nil, err
		}
		// Cache raw, sign after
		if b, jsonErr := json.Marshal(stores); jsonErr == nil {
			_ = s.redis.Set(ctx, cacheKey, string(b), NearbyStoresCacheTTL)
		}
		s.signStoreImages(ctx, stores)
		return stores, nil
	}

	stores, err := s.repo.FindNearby(ctx, lat, lng, radiusKm, limit)
	if err != nil {
		return nil, err
	}
	s.signStoreImages(ctx, stores)
	return stores, nil
}

func (s *service) CreateStore(ctx context.Context, req CreateStoreRequest) (*Store, error) {
	isActive := true
	if req.IsActive != nil {
		isActive = *req.IsActive
	}
	store := &Store{
		ID:          uuid.New(),
		Name:        req.Name,
		Address:     req.Address,
		Lat:         req.Lat,
		Lng:         req.Lng,
		Category:    req.Category,
		Description: req.Description,
		ImageURL:    req.ImageURL,
		Items:       json.RawMessage("[]"),
		IsActive:    isActive,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	if err := s.repo.Create(ctx, store); err != nil {
		return nil, err
	}
	s.invalidateCache(ctx)
	return store, nil
}

func (s *service) UpdateStore(ctx context.Context, id uuid.UUID, req UpdateStoreRequest) (*Store, error) {
	existing, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}

	existing.Name = req.Name
	existing.Address = req.Address
	existing.Lat = req.Lat
	existing.Lng = req.Lng
	existing.Category = req.Category
	existing.Description = req.Description
	existing.ImageURL = req.ImageURL
	existing.UpdatedAt = time.Now()
	if req.IsActive != nil {
		existing.IsActive = *req.IsActive
	}

	if err := s.repo.Update(ctx, existing); err != nil {
		return nil, err
	}
	s.invalidateCache(ctx)
	return existing, nil
}

func (s *service) DeleteStore(ctx context.Context, id uuid.UUID) error {
	if err := s.repo.Delete(ctx, id); err != nil {
		return err
	}
	s.invalidateCache(ctx)
	return nil
}

func (s *service) CreateStoresBatch(ctx context.Context, reqs []CreateStoreRequest) (int, error) {
	if len(reqs) == 0 {
		return 0, nil
	}

	successCount := 0
	for _, req := range reqs {
		if req.Name == "" {
			continue
		}

		isActive := true
		if req.IsActive != nil {
			isActive = *req.IsActive
		}

		store := &Store{
			ID:          uuid.New(),
			Name:        req.Name,
			Address:     req.Address,
			Lat:         req.Lat,
			Lng:         req.Lng,
			Category:    req.Category,
			Description: req.Description,
			ImageURL:    req.ImageURL,
			Items:       json.RawMessage("[]"),
			IsActive:    isActive,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}

		if err := s.repo.Create(ctx, store); err == nil {
			successCount++
		}
	}

	s.invalidateCache(ctx)
	return successCount, nil
}

// signStoreImages ensures all image URLs return final base https://upload.nihtip.com/ via SignedURL
func (s *service) signStoreImages(ctx context.Context, stores []Store) {
	if s.storage == nil {
		return
	}
	for i := range stores {
		if stores[i].ImageURL == "" {
			continue
		}
		if strings.HasPrefix(stores[i].ImageURL, "http") {
			// legacy may be myqcloud or localhost — sanitize via storage sign which rewrites to ASSET_BASE_URL
			// but if already https://upload.nihtip.com/ keep, SignedURL will still rewrite to same domain
			// We still attempt to sign if it is not already upload.nihtip.com to enforce
			if strings.HasPrefix(stores[i].ImageURL, "https://upload.nihtip.com/") {
				continue
			}
			// else fallthrough to sanitize + sign
		}
		sanitized := sanitizeStoreKey(stores[i].ImageURL)
		if sanitized == "" {
			continue
		}
		if signed, err := s.storage.SignedURL(ctx, sanitized, 1*3600*1000000000); err == nil {
			stores[i].ImageURL = signed
		}
	}
}

func sanitizeStoreKey(raw string) string {
	if raw == "" {
		return ""
	}
	u := strings.TrimSpace(raw)
	// If full URL e.g. https://bucket.cos.../stores/foo.jpg?sign -> extract stores/foo.jpg
	if strings.HasPrefix(u, "http") {
		// try extract after ".myqcloud.com/" or "/uploads/" or find stores/
		if idx := strings.Index(u, "myqcloud.com/"); idx != -1 {
			u = u[idx+len("myqcloud.com/"):]
		} else if idx := strings.Index(u, "/uploads/"); idx != -1 {
			u = u[idx+len("/uploads/"):]
		} else if idx := strings.Index(u, "/stores/"); idx != -1 {
			u = u[idx+1:] // keep stores/...
			u = strings.TrimPrefix(u, "/")
		}
		// strip query
		if qIdx := strings.Index(u, "?"); qIdx != -1 {
			u = u[:qIdx]
		}
	}
	// remove leading /
	u = strings.TrimPrefix(u, "/")
	u = strings.TrimPrefix(u, "uploads/")
	return u
}

// invalidateCache clears the active stores cache after any mutation.
// Nearby caches will expire naturally via TTL (5 min).
func (s *service) invalidateCache(ctx context.Context) {
	if s.redis != nil {
		_ = s.redis.Del(ctx, "stores:active:all")
	}
}
