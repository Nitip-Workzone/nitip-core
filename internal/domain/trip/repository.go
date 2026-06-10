package trip

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type Repository interface {
	Create(ctx context.Context, trip *Trip) error
	FindByID(ctx context.Context, id uuid.UUID) (*Trip, error)
	FindByRunnerID(ctx context.Context, runnerID uuid.UUID) ([]Trip, error)
	FindActiveByLocation(ctx context.Context, lat, lng float64, radiusKm float64) ([]Trip, error)
	Update(ctx context.Context, trip *Trip) error
	UpdateCapacity(ctx context.Context, db bun.IDB, id uuid.UUID, weight, volume float64) error
	RestoreCapacity(ctx context.Context, db bun.IDB, id uuid.UUID, weight, volume float64) error
}

type repository struct {
	db *bun.DB
}

func NewRepository(db *bun.DB) Repository {
	return &repository{db: db}
}

func (r *repository) Create(ctx context.Context, trip *Trip) error {
	_, err := r.db.NewInsert().Model(trip).Exec(ctx)
	return err
}

func (r *repository) FindByID(ctx context.Context, id uuid.UUID) (*Trip, error) {
	trip := new(Trip)
	err := r.db.NewSelect().Model(trip).Where("id = ?", id).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return trip, nil
}

func (r *repository) FindByRunnerID(ctx context.Context, runnerID uuid.UUID) ([]Trip, error) {
	var trips []Trip
	err := r.db.NewSelect().Model(&trips).
		Where("runner_id = ?", runnerID).
		Order("created_at DESC").
		Scan(ctx)
	return trips, err
}

func (r *repository) FindActiveByLocation(ctx context.Context, lat, lng float64, radiusKm float64) ([]Trip, error) {
	var trips []Trip
	
	// We use a generous candidate radius (20KM) to ensure we catch trips 
	// that can accommodate a 10KM detour even if they don't start exactly at the pickup.
	candidateRadiusMeters := 20000.0

	err := r.db.NewSelect().Model(&trips).
		Where("status IN (?, ?)", StatusActive, StatusStarted).
		Where("departure_time > NOW()").
		Where("ST_DWithin(CAST(ST_SetSRID(ST_MakePoint(origin_lng, origin_lat), 4326) AS geography), CAST(ST_SetSRID(ST_MakePoint(?, ?), 4326) AS geography), ?)", lng, lat, candidateRadiusMeters).
		Scan(ctx)

	if err != nil {
		return nil, err
	}

	return trips, nil
}

func (r *repository) Update(ctx context.Context, trip *Trip) error {
	_, err := r.db.NewUpdate().Model(trip).WherePK().Exec(ctx)
	return err
}
func (r *repository) UpdateCapacity(ctx context.Context, db bun.IDB, id uuid.UUID, weight, volume float64) error {
	res, err := db.NewUpdate().Model((*Trip)(nil)).
		Set("available_weight_kg = available_weight_kg - ?", weight).
		Set("available_volume_liters = available_volume_liters - ?", volume).
		Where("id = ?", id).
		Where("available_weight_kg >= ?", weight).
		Where("available_volume_liters >= ?", volume).
		Exec(ctx)
	
	if err != nil {
		return err
	}

	rows, _ := res.RowsAffected()
	if rows == 0 {
		return errors.New("insufficient trip capacity (atomic check)")
	}
	return nil
}

func (r *repository) RestoreCapacity(ctx context.Context, db bun.IDB, id uuid.UUID, weight, volume float64) error {
	_, err := db.NewUpdate().Model((*Trip)(nil)).
		Set("available_weight_kg = available_weight_kg + ?", weight).
		Set("available_volume_liters = available_volume_liters + ?", volume).
		Where("id = ?", id).
		Exec(ctx)
	return err
}
