package realtime

import (
	"context"
	"time"

	"github.com/codecoffy/nitip-core/internal/cache"
	"github.com/codecoffy/nitip-core/internal/domain/user"
	"github.com/codecoffy/nitip-core/internal/middleware"
	"github.com/codecoffy/nitip-core/pkg/response"
	"github.com/gofiber/fiber/v2"
	"github.com/uptrace/bun"
)

type MetricsHandler struct {
	redis   *cache.Redis
	db      *bun.DB
	poolHub *PoolHub
}

func NewMetricsHandler(redis *cache.Redis, db *bun.DB, hub *PoolHub) *MetricsHandler {
	return &MetricsHandler{redis: redis, db: db, poolHub: hub}
}

func (h *MetricsHandler) RegisterRoutes(router fiber.Router) {
	admin := router.Group("/admin/metrics", middleware.Protected(h.db, h.redis), middleware.Role(user.RoleAdmin))
	admin.Get("/pool", h.GetPoolMetrics)
	admin.Get("/pool/history", h.GetPoolHistory)
}

// GetPoolMetrics godoc
// @Summary      Get pool realtime metrics (no Grafana, Redis + PG only)
// @Tags         [Admin] System Config
// @Security     BearerAuth
// @Produce      json
// @Success      200  {object}  response.envelope
// @Router       /admin/metrics/pool [get]
func (h *MetricsHandler) GetPoolMetrics(c *fiber.Ctx) error {
	ctx := context.Background()

	counters := map[string]int64{}
	if h.redis != nil {
		if cs, err := h.redis.GetAllPoolCounters(ctx); err == nil {
			counters = cs
		}
	}

	// last broadcast info
	var lastBroadcast map[string]string
	if h.redis != nil && h.redis.Client() != nil {
		if vals, err := h.redis.Client().HGetAll(ctx, "pool:last_broadcast").Result(); err == nil {
			lastBroadcast = vals
		}
	}

	sseConns := int64(0)
	totalEvents := int64(0)
	conflicts := int64(0)
	if h.poolHub != nil {
		sseConns = h.poolHub.ConnectionCount()
		totalEvents = h.poolHub.TotalEvents()
		conflicts = h.poolHub.ClaimConflicts()
	}

	// PG stats from pool_metrics table (last 1h)
	type pgStats struct {
		Count      int     `bun:"cnt"`
		AvgLatency float64 `bun:"avg_lat"`
	}
	var stats pgStats
	if h.db != nil {
		_ = h.db.NewSelect().
			Table("pool_metrics").
			ColumnExpr("COUNT(*) as cnt, COALESCE(AVG(latency_ms),0) as avg_lat").
			Where("created_at > ?", time.Now().Add(-1*time.Hour)).
			Scan(ctx, &stats) // ignore error for empty table
	}

	return response.Success(c, "pool metrics", fiber.Map{
		"sse_connections":      sseConns,
		"sse_total_events_mem": totalEvents,
		"claim_conflicts_mem":  conflicts,
		"redis_counters":       counters,
		"last_broadcast":       lastBroadcast,
		"pg_last_1h":           stats,
		"timestamp":            time.Now().Format(time.RFC3339),
	})
}

// GetPoolHistory godoc
// @Summary      Get recent pool_metrics history
// @Tags         [Admin] System Config
// @Security     BearerAuth
// @Produce      json
// @Success      200  {object}  response.envelope
// @Router       /admin/metrics/pool/history [get]
func (h *MetricsHandler) GetPoolHistory(c *fiber.Ctx) error {
	if h.db == nil {
		return response.Success(c, "history", []interface{}{})
	}
	limit := c.QueryInt("limit", 50)
	if limit > 200 {
		limit = 200
	}
	type row struct {
		bun.BaseModel `bun:"table:pool_metrics"`
		ID            string    `json:"id"`
		EventType     string    `json:"event_type"`
		OrderID       *string   `json:"order_id"`
		CellKey       *string   `json:"cell_key"`
		RunnerCnt     int       `json:"runner_count"`
		LatencyMs     int       `json:"latency_ms"`
		CreatedAt     time.Time `json:"created_at"`
	}
	var rows []row
	err := h.db.NewSelect().
		Model(&rows).
		Order("created_at DESC").
		Limit(limit).
		Scan(c.Context())
	if err != nil {
		return response.Success(c, "history", []interface{}{})
	}
	return response.Success(c, "history", rows)
}
