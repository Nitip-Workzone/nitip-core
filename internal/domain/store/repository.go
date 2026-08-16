package store

import (
	"context"
	"fmt"
	"math"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type Repository interface {
	GetAll(ctx context.Context) ([]Store, error)
	GetActive(ctx context.Context) ([]Store, error)
	FindNearby(ctx context.Context, lat, lng float64, radiusKm float64, limit int) ([]Store, error)
	GetByID(ctx context.Context, id uuid.UUID) (*Store, error)
	Create(ctx context.Context, s *Store) error
	Update(ctx context.Context, s *Store) error
	Delete(ctx context.Context, id uuid.UUID) error
}

type repository struct {
	db *bun.DB
}

func NewRepository(db *bun.DB) Repository {
	return &repository{db: db}
}

func (r *repository) GetAll(ctx context.Context) ([]Store, error) {
	var stores []Store
	err := r.db.NewSelect().Model(&stores).Order("created_at DESC").Limit(500).Scan(ctx)
	return stores, err
}

func (r *repository) GetActive(ctx context.Context) ([]Store, error) {
	var stores []Store
	err := r.db.NewSelect().Model(&stores).
		Where("s.is_active = ?", true).
		Order("s.name ASC").
		Limit(500).
		Scan(ctx)
	return stores, err
}

// FindNearby returns active stores within radiusKm from the given lat/lng,
// sorted ascending by Haversine distance, limited to `limit` results.
//
// Performance notes:
//   - Uses pure SQL Haversine formula — avoids PostGIS geometry overhead for small datasets.
//   - The idx_stores_lat_lng index enables a bounding-box pre-filter before the expensive formula.
//   - limit is enforced at DB level; caller should keep it <= 20 for VPS safety.
//   - Results are cached by the service layer (Redis TTL 5 min) to avoid repeated scans.
func (r *repository) FindNearby(ctx context.Context, lat, lng float64, radiusKm float64, limit int) ([]Store, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	if radiusKm <= 0 || radiusKm > 50 {
		radiusKm = 15
	}

	// Bounding box pre-filter: 1 degree lat ≈ 111 km, 1 degree lng varies by latitude.
	latDelta := radiusKm / 111.0
	lngDelta := radiusKm / (111.0 * math.Cos(lat*math.Pi/180.0))

	// Haversine formula in SQL — computes great-circle distance in km.
	// Only applied after bbox pre-filter to minimize rows evaluated.
	distanceExpr := fmt.Sprintf(`
		(6371 * acos(
			cos(radians(%f)) * cos(radians(s.lat)) * cos(radians(s.lng) - radians(%f)) +
			sin(radians(%f)) * sin(radians(s.lat))
		))`, lat, lng, lat)

	var stores []Store
	err := r.db.NewSelect().
		TableExpr("stores AS s").
		ColumnExpr("s.*").
		ColumnExpr(distanceExpr+" AS distance_km").
		Where("s.is_active = ?", true).
		// Bounding box pre-filter using index
		Where("s.lat BETWEEN ? AND ?", lat-latDelta, lat+latDelta).
		Where("s.lng BETWEEN ? AND ?", lng-lngDelta, lng+lngDelta).
		// Haversine radius filter
		Where(distanceExpr+" <= ?", radiusKm).
		OrderExpr(distanceExpr + " ASC").
		Limit(limit).
		Scan(ctx, &stores)

	return stores, err
}

func (r *repository) GetByID(ctx context.Context, id uuid.UUID) (*Store, error) {
	s := new(Store)
	err := r.db.NewSelect().Model(s).Where("s.id = ?", id).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return s, nil
}

func (r *repository) Create(ctx context.Context, s *Store) error {
	if s.ID == uuid.Nil {
		s.ID = uuid.New()
	}
	_, err := r.db.NewInsert().Model(s).Exec(ctx)
	return err
}

func (r *repository) Update(ctx context.Context, s *Store) error {
	_, err := r.db.NewUpdate().Model(s).WherePK().Exec(ctx)
	return err
}

func (r *repository) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.NewDelete().Model((*Store)(nil)).Where("id = ?", id).Exec(ctx)
	return err
}
