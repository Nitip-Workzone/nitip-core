package order

import (
	"context"
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
	FindByIDForUpdate(ctx context.Context, db bun.IDB, id uuid.UUID) (*Order, error)
	CancelAtomic(ctx context.Context, db bun.IDB, id uuid.UUID, reason string) (bool, error)
	CompleteAtomic(ctx context.Context, db bun.IDB, id uuid.UUID, runnerID uuid.UUID, deliveryImg string) (bool, error)
	FindByRequesterID(ctx context.Context, requesterID uuid.UUID) ([]Order, error)
	FindByRunnerID(ctx context.Context, runnerID uuid.UUID) ([]Order, error)
	FindByUserID(ctx context.Context, userID uuid.UUID, limit, offset int, startDate, endDate string) ([]Order, error)
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
	IDs               []uuid.UUID
}

type repository struct {
	db *bun.DB
}

func NewRepository(db *bun.DB) Repository {
	return &repository{db: db}
}

func (r *repository) FindAll(ctx context.Context, offset, limit int) ([]Order, error) {
	orders := []Order{}
	// P2 heavy query guard: always limit, default 50 max 100 to prevent OOM 512M
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	query := r.db.NewSelect().Model(&orders).Order("created_at DESC").Limit(limit).Offset(offset)

	err := query.Scan(ctx)
	return orders, err
}

func (r *repository) FindAllWithFilters(ctx context.Context, status string, offset, limit int) ([]Order, error) {
	orders := []Order{}
	if limit <= 0 {
		limit = 50
	}
	if limit > 100 {
		limit = 100
	}
	query := r.db.NewSelect().Model(&orders).Order("created_at DESC").Limit(limit).Offset(offset)

	if status != "" {
		query = query.Where("status = ?", status)
	}

	err := query.Scan(ctx)
	return orders, err
}

func (r *repository) FindAvailable(ctx context.Context, params FindAvailableParams) ([]Order, error) {
	// CRITICAL: Only allow statuses that are actually actionable for runner pool
	// COMPLETED, CANCELLED, EXPIRED, etc must never be returned
	orders := []Order{}
	baseQuery := r.db.NewSelect().
		Model(&orders).
		Where("status IN (?)", bun.List([]string{StatusPending, StatusMerchantAccepted, StatusAccepted, StatusCooking, StatusReady})).
		Where("created_at > ?", params.Cutoff).
		WhereGroup(" AND ", func(q *bun.SelectQuery) *bun.SelectQuery {
			return q.Where("payment_status = ?", PaymentEscrow).
				WhereOr("payment_method = ?", MethodCOD)
		}).
		Order("created_at DESC")

	if len(params.AllowedTypes) > 0 {
		baseQuery = baseQuery.Where("order_type IN (?)", bun.List(params.AllowedTypes))
	}

	if len(params.IDs) > 0 {
		baseQuery = baseQuery.Where("id IN (?)", bun.List(params.IDs))
		if params.Limit > 0 {
			baseQuery = baseQuery.Limit(params.Limit).Offset(params.Offset)
		}
		err := baseQuery.Scan(ctx)
		return orders, err
	}

	// Build geo condition as single AND group containing OR branches
	// Previous bug: returned empty when online but no trip/loc → broke realtime pool UX (needs pull).
	// Fix low-burden: if online without geo, fallback to status-only (no geo filter) so SSE + fetch works.
	hasTrip := params.HasActiveTrip && params.RadiusKm > 0
	hasProximity := params.IsAcceptingOrders && params.RunnerLat != 0 && params.RunnerLng != 0
	hasOnlineNoGeo := params.IsAcceptingOrders && !hasProximity && !hasTrip

	if !hasTrip && !hasProximity && !hasOnlineNoGeo {
		// Offline & no trip → empty to save DB
		return []Order{}, nil
	}

	var query *bun.SelectQuery
	if hasOnlineNoGeo {
		// Online fallback: no geo, just status/payment – allows realtime pool to show orders immediately
		// PostGIS skipped, low burden (indexed columns, limit 100)
		query = baseQuery
	} else {
		// With geo
		query = baseQuery.WhereGroup(" AND ", func(q *bun.SelectQuery) *bun.SelectQuery {
			return q.WhereGroup(" OR ", func(q2 *bun.SelectQuery) *bun.SelectQuery {
				if hasTrip {
					radiusM := params.RadiusKm * 1000
					if params.IsRoundTrip {
						q2 = q2.WhereGroup(" OR ", func(q3 *bun.SelectQuery) *bun.SelectQuery {
							return q3.Where("(ST_DWithin(pickup_geom, ST_SetSRID(ST_MakePoint(?, ?), 4326)::geography, ?) AND ST_DWithin(delivery_geom, ST_SetSRID(ST_MakePoint(?, ?), 4326)::geography, ?))",
								params.OriginLng, params.OriginLat, radiusM, params.DestLng, params.DestLat, radiusM).
								WhereOr("(ST_DWithin(pickup_geom, ST_SetSRID(ST_MakePoint(?, ?), 4326)::geography, ?) AND ST_DWithin(delivery_geom, ST_SetSRID(ST_MakePoint(?, ?), 4326)::geography, ?))",
									params.DestLng, params.DestLat, radiusM, params.OriginLng, params.OriginLat, radiusM)
						})
					} else {
						q2 = q2.WhereGroup(" AND ", func(q3 *bun.SelectQuery) *bun.SelectQuery {
							return q3.Where("ST_DWithin(pickup_geom, ST_SetSRID(ST_MakePoint(?, ?), 4326)::geography, ?)", params.OriginLng, params.OriginLat, radiusM).
								Where("ST_DWithin(delivery_geom, ST_SetSRID(ST_MakePoint(?, ?), 4326)::geography, ?)", params.DestLng, params.DestLat, radiusM)
						})
					}
				}
				if hasProximity {
					localRadiusM := 15000.0
					q2 = q2.WhereOr("distance_km < 10 AND ST_DWithin(pickup_geom, ST_SetSRID(ST_MakePoint(?, ?), 4326)::geography, ?)", params.RunnerLng, params.RunnerLat, localRadiusM)
				}
				return q2
			})
		})
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

func (r *repository) FindByIDForUpdate(ctx context.Context, db bun.IDB, id uuid.UUID) (*Order, error) {
	order := new(Order)
	err := db.NewSelect().Model(order).Where("id = ?", id).For("UPDATE").Scan(ctx)
	if err != nil {
		return nil, err
	}
	return order, nil
}

func (r *repository) CancelAtomic(ctx context.Context, db bun.IDB, id uuid.UUID, reason string) (bool, error) {
	// Atomic cancel only if status IN allowed cancellable statuses, prevents race
	res, err := db.NewUpdate().Model((*Order)(nil)).
		Set("status = ?", StatusCancelled).
		Set("dispute_reason = ?", reason).
		Set("updated_at = ?", time.Now()).
		Where("id = ?", id).
		Where("status NOT IN (?, ?, ?)", StatusCompleted, StatusCancelled, StatusExpired).
		Exec(ctx)
	if err != nil {
		return false, err
	}
	aff, _ := res.RowsAffected()
	return aff > 0, nil
}

func (r *repository) CompleteAtomic(ctx context.Context, db bun.IDB, id uuid.UUID, runnerID uuid.UUID, deliveryImg string) (bool, error) {
	// Atomic complete only if status = delivering and runner matches, prevent double release
	res, err := db.NewUpdate().Model((*Order)(nil)).
		Set("status = ?", StatusCompleted).
		Set("delivery_image_url = ?", deliveryImg).
		Set("payment_status = ?", PaymentReleased).
		Set("updated_at = ?", time.Now()).
		Where("id = ?", id).
		Where("runner_id = ?", runnerID).
		Where("status = ?", StatusDelivering).
		Exec(ctx)
	if err != nil {
		return false, err
	}
	aff, _ := res.RowsAffected()
	return aff > 0, nil
}

func (r *repository) FindByRequesterID(ctx context.Context, requesterID uuid.UUID) ([]Order, error) {
	orders := []Order{}
	err := r.db.NewSelect().Model(&orders).Where("requester_id = ?", requesterID).Order("created_at DESC").Limit(100).Scan(ctx)
	return orders, err
}

func (r *repository) FindByRunnerID(ctx context.Context, runnerID uuid.UUID) ([]Order, error) {
	orders := []Order{}
	err := r.db.NewSelect().Model(&orders).Where("runner_id = ?", runnerID).Order("created_at DESC").Limit(100).Scan(ctx)
	return orders, err
}

func (r *repository) FindByUserID(ctx context.Context, userID uuid.UUID, limit, offset int, startDate, endDate string) ([]Order, error) {
	orders := []Order{}
	query := r.db.NewSelect().
		Model(&orders).
		WhereGroup(" AND ", func(q *bun.SelectQuery) *bun.SelectQuery {
			return q.Where("o.requester_id = ?", userID).
				WhereOr("o.runner_id = ?", userID)
		}).
		Order("o.created_at DESC")

	if startDate != "" {
		query = query.Where("o.created_at >= ?", startDate)
	}
	if endDate != "" {
		query = query.Where("o.created_at <= ?", endDate)
	}

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
