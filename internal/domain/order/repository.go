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
	orders := []Order{}
	query := r.db.NewSelect().
		Model(&orders).
		Where("(merchant_id IS NULL AND status = ?) OR (merchant_id IS NOT NULL AND (status = ? OR status = ? OR status = ? OR status = ?))", StatusPending, StatusMerchantAccepted, StatusAccepted, StatusCooking, StatusReady).
		Where("created_at > ?", params.Cutoff).
		WhereGroup(" AND ", func(q *bun.SelectQuery) *bun.SelectQuery {
			return q.Where("payment_status = ?", PaymentEscrow).
				WhereOr("payment_method = ?", MethodCOD)
		}).
		Order("created_at DESC")

	if len(params.AllowedTypes) > 0 {
		query = query.Where("order_type IN (?)", bun.In(params.AllowedTypes)) // nolint:staticcheck
	}

	// Geolocation Matching - Performance optimized with PostGIS ST_DWithin + parameterized queries
	// Hybrid Logic:
	// 1. Path-based (Trip) if HasActiveTrip
	// 2. Proximity-based (<10km) if IsAcceptingOrders

	hasCondition := false

	if params.HasActiveTrip && params.RadiusKm > 0 {
		radiusM := params.RadiusKm * 1000
		// PostGIS: ST_DWithin(geography, geography, meters) uses GIST index idx_orders_pickup_geom_gist
		// Safe parameterized - no fmt.Sprintf injection
		query = query.WhereGroup(" AND ", func(q *bun.SelectQuery) *bun.SelectQuery {
			// forward leg: pickup near origin AND delivery near destination
			forward := q.Where("ST_DWithin(pickup_geom, ST_SetSRID(ST_MakePoint(?, ?), 4326)::geography, ?)", params.OriginLng, params.OriginLat, radiusM).
				Where("ST_DWithin(delivery_geom, ST_SetSRID(ST_MakePoint(?, ?), 4326)::geography, ?)", params.DestLng, params.DestLat, radiusM)

			if params.IsRoundTrip {
				// round trip: also allow reverse leg
				// Use WhereGroup OR: (forward) OR (reverse)
				// bun doesn't support direct OR group easily, so we build raw with params via Where? Use bun. WhereOr attempt
				// Fallback: combined OR using WhereGroup OR at top level? Do separate path for simplicity: keep forward, then OR reverse via WhereOrGroup
				// To keep SQL correct, we do: (forward) OR (reverse) wrapped in AND for overall query - we achieve via second Where building OR
				// We'll use a raw OR clause with placeholders
				_ = forward
				// Combined condition: (pickup near origin AND delivery near dest) OR (pickup near dest AND delivery near origin)
				return q.WhereGroup(" AND ", func(q2 *bun.SelectQuery) *bun.SelectQuery {
					return q2.Where("(ST_DWithin(pickup_geom, ST_SetSRID(ST_MakePoint(?, ?), 4326)::geography, ?) AND ST_DWithin(delivery_geom, ST_SetSRID(ST_MakePoint(?, ?), 4326)::geography, ?))",
						params.OriginLng, params.OriginLat, radiusM, params.DestLng, params.DestLat, radiusM).
						WhereOr("(ST_DWithin(pickup_geom, ST_SetSRID(ST_MakePoint(?, ?), 4326)::geography, ?) AND ST_DWithin(delivery_geom, ST_SetSRID(ST_MakePoint(?, ?), 4326)::geography, ?))",
							params.DestLng, params.DestLat, radiusM, params.OriginLng, params.OriginLat, radiusM)
				})
			}
			return forward
		})
		hasCondition = true
	}

	if params.IsAcceptingOrders && params.RunnerLat != 0 {
		localRadiusM := 15000.0 // 15km radius
		// For orders <10km distance (distance_km column) AND pickup within 15km of runner - uses GIST index
		query = query.WhereGroup(" OR ", func(q *bun.SelectQuery) *bun.SelectQuery {
			// If we already had trip condition, this adds OR branch
			if hasCondition {
				return q.Where("distance_km < 10 AND ST_DWithin(pickup_geom, ST_SetSRID(ST_MakePoint(?, ?), 4326)::geography, ?)", params.RunnerLng, params.RunnerLat, localRadiusM)
			}
			return q.Where("distance_km < 10 AND ST_DWithin(pickup_geom, ST_SetSRID(ST_MakePoint(?, ?), 4326)::geography, ?)", params.RunnerLng, params.RunnerLat, localRadiusM)
		})
		if !hasCondition {
			hasCondition = true
		} else {
			// When both conditions exist, we need special handling:
			// The WhereGroup above with OR will create: (existing) OR (proximity)
			// But our earlier trip condition was added as AND, so we rebuild query correctly:
			// To avoid complexity, we use a fallback to combined OR in a single WHERE if both present
			// We already added trip as AND, then added proximity as OR -> final is (trip AND proximity?) Actually bun will chain AND then OR -> need to fix
			// Let's just ensure we don't return empty - the current chaining with WhereGroup OR will work as OR appended at top level
		}
	} else if hasCondition {
		// only trip-based, already handled
	}

	// Edge: if both trip and proximity present, ensure OR logic wins
	// Rebuild if both present for correct semantics: (tripCondition) OR (proximityCondition)
	if params.HasActiveTrip && params.IsAcceptingOrders && params.RunnerLat != 0 && params.RadiusKm > 0 {
		// Reset query to use OR between two geo branches (keep other filters)
		radiusM := params.RadiusKm * 1000
		localRadiusM := 15000.0
		// Re-create base query with OR geo filter
		orders = []Order{}
		query = r.db.NewSelect().
			Model(&orders).
			Where("(merchant_id IS NULL AND status = ?) OR (merchant_id IS NOT NULL AND (status = ? OR status = ? OR status = ? OR status = ?))", StatusPending, StatusMerchantAccepted, StatusAccepted, StatusCooking, StatusReady).
			Where("created_at > ?", params.Cutoff).
			WhereGroup(" AND ", func(q *bun.SelectQuery) *bun.SelectQuery {
				return q.Where("payment_status = ?", PaymentEscrow).
					WhereOr("payment_method = ?", MethodCOD)
			}).
			Order("created_at DESC")

		if len(params.AllowedTypes) > 0 {
			query = query.Where("order_type IN (?)", bun.In(params.AllowedTypes))
		}

		if params.IsRoundTrip {
			query = query.Where("( (ST_DWithin(pickup_geom, ST_SetSRID(ST_MakePoint(?, ?), 4326)::geography, ?) AND ST_DWithin(delivery_geom, ST_SetSRID(ST_MakePoint(?, ?), 4326)::geography, ?)) OR (ST_DWithin(pickup_geom, ST_SetSRID(ST_MakePoint(?, ?), 4326)::geography, ?) AND ST_DWithin(delivery_geom, ST_SetSRID(ST_MakePoint(?, ?), 4326)::geography, ?)) OR (distance_km < 10 AND ST_DWithin(pickup_geom, ST_SetSRID(ST_MakePoint(?, ?), 4326)::geography, ?)) )",
				params.OriginLng, params.OriginLat, radiusM, params.DestLng, params.DestLat, radiusM,
				params.DestLng, params.DestLat, radiusM, params.OriginLng, params.OriginLat, radiusM,
				params.RunnerLng, params.RunnerLat, localRadiusM)
		} else {
			query = query.Where("( (ST_DWithin(pickup_geom, ST_SetSRID(ST_MakePoint(?, ?), 4326)::geography, ?) AND ST_DWithin(delivery_geom, ST_SetSRID(ST_MakePoint(?, ?), 4326)::geography, ?)) OR (distance_km < 10 AND ST_DWithin(pickup_geom, ST_SetSRID(ST_MakePoint(?, ?), 4326)::geography, ?)) )",
				params.OriginLng, params.OriginLat, radiusM, params.DestLng, params.DestLat, radiusM,
				params.RunnerLng, params.RunnerLat, localRadiusM)
		}
		hasCondition = true
	}

	if !hasCondition {
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
