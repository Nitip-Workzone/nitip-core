package order

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/uptrace/bun"
)

type Repository interface {
	FindAll(ctx context.Context, offset, limit int) ([]Order, error)
	FindAllWithFilters(ctx context.Context, status string, offset, limit int) ([]Order, error)
	FindAvailable(ctx context.Context, params FindAvailableParams) ([]Order, error)
	ExpireOldOrders(ctx context.Context, cutoff time.Time) (int64, error)
	FindByID(ctx context.Context, id uuid.UUID) (*Order, error)
	FindByRequesterID(ctx context.Context, requesterID uuid.UUID) ([]Order, error)
	FindByRunnerID(ctx context.Context, runnerID uuid.UUID) ([]Order, error)
	FindByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]Order, error)
	Create(ctx context.Context, db bun.IDB, order *Order) error
	Update(ctx context.Context, db bun.IDB, order *Order) error
	UpdateWithStatusCheck(ctx context.Context, db bun.IDB, order *Order, expectedStatus string) (bool, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status string) error
	Delete(ctx context.Context, id uuid.UUID) error
	CountTodayOrders(ctx context.Context, userID uuid.UUID) (int, error)
	CountTodayAcceptances(ctx context.Context, runnerID uuid.UUID) (int, error)
}

type FindAvailableParams struct {
	Cutoff            time.Time
	AllowedTypes      []string
	OriginLat         float64
	OriginLng         float64
	DestLat           float64
	DestLng           float64
	RadiusKm          float64
	IsRoundTrip       bool
	Offset            int
	Limit             int
	RunnerLat         float64
	RunnerLng         float64
	IsAcceptingOrders bool
	HasActiveTrip     bool
}

type repository struct {
	db *bun.DB
}

func NewRepository(db *bun.DB) Repository {
	return &repository{db: db}
}

func (r *repository) FindAll(ctx context.Context, offset, limit int) ([]Order, error) {
	orders := []Order{}
	query := r.db.NewSelect().Model(&orders).Order("created_at DESC")

	if limit > 0 {
		query = query.Limit(limit).Offset(offset)
	}

	err := query.Scan(ctx)
	return orders, err
}

func (r *repository) FindAllWithFilters(ctx context.Context, status string, offset, limit int) ([]Order, error) {
	orders := []Order{}
	query := r.db.NewSelect().Model(&orders).Order("created_at DESC")

	if status != "" {
		query = query.Where("status = ?", status)
	}

	if limit > 0 {
		query = query.Limit(limit).Offset(offset)
	}

	err := query.Scan(ctx)
	return orders, err
}

func (r *repository) FindAvailable(ctx context.Context, params FindAvailableParams) ([]Order, error) {
	orders := []Order{}
	query := r.db.NewSelect().
		Model(&orders).
		Where("status = ? OR (merchant_id IS NOT NULL AND (status = ? OR status = ?))", StatusPending, StatusCooking, StatusReady).
		Where("created_at > ?", params.Cutoff).
		WhereGroup(" AND ", func(q *bun.SelectQuery) *bun.SelectQuery {
			return q.Where("payment_status = ?", PaymentEscrow).
				WhereOr("payment_method = ?", MethodCOD)
		}).
		Order("created_at DESC")

	if len(params.AllowedTypes) > 0 {
		query = query.Where("order_type IN (?)", bun.In(params.AllowedTypes)) // nolint:staticcheck
	}

	// Geolocation Matching
	// Hybrid Logic:
	// 1. Path-based (Trip) if HasActiveTrip
	// 2. Proximity-based (<10km) if IsAcceptingOrders

	var matchingConditions []string

	if params.HasActiveTrip && params.RadiusKm > 0 {
		forwardLeg := fmt.Sprintf(`
			(6371 * acos(LEAST(GREATEST(cos(radians(%f)) * cos(radians(pickup_lat)) * cos(radians(pickup_lng) - radians(%f)) + sin(radians(%f)) * sin(radians(pickup_lat)), -1), 1))) <= %f
			AND
			(6371 * acos(LEAST(GREATEST(cos(radians(%f)) * cos(radians(delivery_lat)) * cos(radians(delivery_lng) - radians(%f)) + sin(radians(%f)) * sin(radians(delivery_lat)), -1), 1))) <= %f
		`, params.OriginLat, params.OriginLng, params.OriginLat, params.RadiusKm, params.DestLat, params.DestLng, params.DestLat, params.RadiusKm)

		if params.IsRoundTrip {
			reverseLeg := fmt.Sprintf(`
				(6371 * acos(LEAST(GREATEST(cos(radians(%f)) * cos(radians(pickup_lat)) * cos(radians(pickup_lng) - radians(%f)) + sin(radians(%f)) * sin(radians(pickup_lat)), -1), 1))) <= %f
				AND
				(6371 * acos(LEAST(GREATEST(cos(radians(%f)) * cos(radians(delivery_lat)) * cos(radians(delivery_lng) - radians(%f)) + sin(radians(%f)) * sin(radians(delivery_lat)), -1), 1))) <= %f
			`, params.DestLat, params.DestLng, params.DestLat, params.RadiusKm, params.OriginLat, params.OriginLng, params.OriginLat, params.RadiusKm)
			matchingConditions = append(matchingConditions, fmt.Sprintf("(%s OR %s)", forwardLeg, reverseLeg))
		} else {
			matchingConditions = append(matchingConditions, forwardLeg)
		}
	}

	if params.IsAcceptingOrders && params.RunnerLat != 0 {
		// Proximity Matching for < 10km orders
		localRadius := 15.0 // Increased to 15km radius to find orders around runner
		proximityCondition := fmt.Sprintf(`
			(distance_km < 10 AND (6371 * acos(LEAST(GREATEST(cos(radians(%f)) * cos(radians(pickup_lat)) * cos(radians(pickup_lng) - radians(%f)) + sin(radians(%f)) * sin(radians(pickup_lat)), -1), 1))) <= %f)
		`, params.RunnerLat, params.RunnerLng, params.RunnerLat, localRadius)
		matchingConditions = append(matchingConditions, proximityCondition)
	}

	if len(matchingConditions) > 0 {
		combined := ""
		for i, cond := range matchingConditions {
			if i == 0 {
				combined = cond
			} else {
				combined += " OR " + cond
			}
		}
		query = query.Where(combined)
	} else {
		// If no matching logic (no trip, no online status, or no location), return empty list
		return []Order{}, nil
	}

	if params.Limit > 0 {
		query = query.Limit(params.Limit).Offset(params.Offset)
	}

	err := query.Scan(ctx)
	return orders, err
}

func (r *repository) ExpireOldOrders(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := r.db.NewUpdate().
		Model((*Order)(nil)).
		Set("status = ?", StatusExpired).
		Set("updated_at = ?", time.Now()).
		Where("status = ?", StatusPending).
		Where("created_at <= ?", cutoff).
		Exec(ctx)

	if err != nil {
		return 0, err
	}

	return res.RowsAffected()
}

func (r *repository) FindByID(ctx context.Context, id uuid.UUID) (*Order, error) {
	order := new(Order)
	err := r.db.NewSelect().Model(order).Where("id = ?", id).Scan(ctx)
	if err != nil {
		return nil, err
	}
	return order, nil
}

func (r *repository) FindByRequesterID(ctx context.Context, requesterID uuid.UUID) ([]Order, error) {
	orders := []Order{}
	err := r.db.NewSelect().Model(&orders).Where("requester_id = ?", requesterID).Order("created_at DESC").Scan(ctx)
	return orders, err
}

func (r *repository) FindByRunnerID(ctx context.Context, runnerID uuid.UUID) ([]Order, error) {
	orders := []Order{}
	err := r.db.NewSelect().Model(&orders).Where("runner_id = ?", runnerID).Order("created_at DESC").Scan(ctx)
	return orders, err
}

func (r *repository) FindByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]Order, error) {
	orders := []Order{}
	query := r.db.NewSelect().
		Model(&orders).
		WhereGroup(" AND ", func(q *bun.SelectQuery) *bun.SelectQuery {
			return q.Where("o.requester_id = ?", userID).
				WhereOr("o.runner_id = ?", userID)
		}).
		Order("o.created_at DESC")

	if limit > 0 {
		query = query.Limit(limit).Offset(offset)
	}

	err := query.Scan(ctx)
	return orders, err
}

func (r *repository) Create(ctx context.Context, db bun.IDB, order *Order) error {
	_, err := db.NewInsert().Model(order).Exec(ctx)
	return err
}

func (r *repository) Update(ctx context.Context, db bun.IDB, order *Order) error {
	_, err := db.NewUpdate().Model(order).WherePK().Exec(ctx)
	return err
}

func (r *repository) UpdateWithStatusCheck(ctx context.Context, db bun.IDB, order *Order, expectedStatus string) (bool, error) {
	res, err := db.NewUpdate().Model(order).
		WherePK().
		Where("status = ?", expectedStatus).
		Exec(ctx)
	if err != nil {
		return false, err
	}
	rows, _ := res.RowsAffected()
	return rows > 0, nil
}

func (r *repository) UpdateStatus(ctx context.Context, id uuid.UUID, status string) error {
	_, err := r.db.NewUpdate().Model((*Order)(nil)).Set("status = ?", status).Where("id = ?", id).Exec(ctx)
	return err
}

func (r *repository) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.NewDelete().Model((*Order)(nil)).Where("id = ?", id).Exec(ctx)
	return err
}

func (r *repository) CountTodayOrders(ctx context.Context, userID uuid.UUID) (int, error) {
	return r.db.NewSelect().
		Model((*Order)(nil)).
		Where("requester_id = ?", userID).
		Where("created_at >= CURRENT_DATE").
		Count(ctx)
}

func (r *repository) CountTodayAcceptances(ctx context.Context, runnerID uuid.UUID) (int, error) {
	return r.db.NewSelect().
		Model((*Order)(nil)).
		Where("runner_id = ?", runnerID).
		Where("status != ?", StatusPending).
		Where("updated_at >= CURRENT_DATE").
		Count(ctx)
}
