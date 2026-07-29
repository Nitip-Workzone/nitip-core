package realtime

import (
	"context"
	"encoding/json"
	"time"

	"github.com/codecoffy/nitip-core/internal/cache"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type Broadcaster struct {
	hub    *PoolHub
	redis  *cache.Redis
	logger *zap.Logger
}

func NewBroadcaster(hub *PoolHub, redis *cache.Redis, logger *zap.Logger) *Broadcaster {
	return &Broadcaster{hub: hub, redis: redis, logger: logger}
}

// BroadcastNewOrder – new order must be instant for ALL runners, not just nearby cells
// Low burden: BroadcastToAll is non-blocking, <1ms for 100 conns, 0 DB
func (b *Broadcaster) BroadcastOrderCreated(orderID string, pickupLat, pickupLng float64, itemDetails string, merchantID *uuid.UUID, extra map[string]interface{}) {
	if b.hub == nil {
		return
	}
	start := time.Now()

	data := map[string]interface{}{
		"order_id":   orderID,
		"pickup_lat": pickupLat,
		"pickup_lng": pickupLng,
		"item":       itemDetails,
	}
	if merchantID != nil {
		data["merchant_id"] = merchantID.String()
	}
	for k, v := range extra {
		data[k] = v
	}

	ev := PoolEvent{
		Type:      EventOrderCreated,
		OrderID:   orderID,
		Timestamp: time.Now().UnixMilli(),
		Data:      data,
	}

	// Strategy: instant for ALL – geo filtering done by GET /available fallback, but new order must appear <2s
	b.hub.BroadcastToAll(ev)

	// Also broadcast to merchant cells if merchant order
	if merchantID != nil {
		b.hub.BroadcastMerchant(merchantID.String(), PoolEvent{
			Type:      EventOrderCreated,
			OrderID:   orderID,
			Timestamp: time.Now().UnixMilli(),
			Data:      data,
		})
		// also global merchant feed
		b.hub.BroadcastToCell("merchant:global", ev)
	}

	latency := time.Since(start).Milliseconds()
	cells := NeighborCells(pickupLat, pickupLng)

	if b.redis != nil {
		_, _ = b.redis.IncrCounter(context.Background(), "events:total")
		_, _ = b.redis.IncrCounter(context.Background(), "orders:created")
		_ = b.redis.Client().HSet(context.Background(), "pool:last_broadcast", map[string]interface{}{
			"order_id":   orderID,
			"latency_ms": latency,
			"ts":         time.Now().Unix(),
			"cells":      len(cells),
		}).Err()
	}

	if b.logger != nil {
		b.logger.Info("[POOL] broadcast new order",
			zap.String("order_id", orderID),
			zap.Int("cells", len(cells)),
			zap.Int64("latency_ms", latency),
		)
	}

	// Async insert to pool_metrics Postgres via redis? We do async goroutine if needed
	// Postgres insert deferred to admin metrics endpoint reading pool_metrics table
	// To avoid import cycle, we store JSON in redis list for later flush?
	// For MVP, log only + redis counters sufficient
	_ = json.RawMessage{} // keep import
}

// BroadcastOrderClaimed – instant for all runners (not geo-filtered) for <1s removal, low burden
func (b *Broadcaster) BroadcastOrderClaimed(orderID string, runnerID string, pickupLat, pickupLng float64) {
	if b.hub == nil {
		return
	}
	ev := PoolEvent{
		Type:      EventOrderClaimed,
		OrderID:   orderID,
		Timestamp: time.Now().UnixMilli(),
		Data: map[string]interface{}{
			"order_id":  orderID,
			"runner_id": runnerID,
		},
	}
	// Broadcast to ALL runners instantly – geo-agnostic, non-blocking, <1ms for 100 conns, 0 DB
	b.hub.BroadcastToAll(ev)
	b.hub.BroadcastToCell("order:"+orderID, PoolEvent{
		Type:      EventOrderStatus,
		OrderID:   orderID,
		Timestamp: time.Now().UnixMilli(),
		Data:      map[string]interface{}{"order_id": orderID, "status": "claimed", "runner_id": runnerID},
	})
	if b.redis != nil {
		_, _ = b.redis.IncrCounter(context.Background(), "events:total")
		_, _ = b.redis.IncrCounter(context.Background(), "orders:claimed")
		_, _ = b.redis.IncrCounter(context.Background(), "claim:success")
	}
}

// BroadcastOrderCancelled – instant for all runners, low burden (BroadcastToAll, 0 DB)
func (b *Broadcaster) BroadcastOrderCancelled(orderID string, reason string, pickupLat, pickupLng float64) {
	if b.hub == nil {
		return
	}
	ev := PoolEvent{
		Type:      EventOrderCancelled,
		OrderID:   orderID,
		Timestamp: time.Now().UnixMilli(),
		Data: map[string]interface{}{
			"order_id": orderID,
			"reason":   reason,
		},
	}
	b.hub.BroadcastToAll(ev)
	b.hub.BroadcastToCell("order:"+orderID, PoolEvent{
		Type:      EventOrderCancelled,
		OrderID:   orderID,
		Timestamp: time.Now().UnixMilli(),
		Data:      map[string]interface{}{"order_id": orderID, "status": "cancelled", "reason": reason},
	})
	if b.redis != nil {
		_, _ = b.redis.IncrCounter(context.Background(), "events:total")
	}
}

// Direct simple wrappers used by order service if it passes full order
func (b *Broadcaster) BroadcastNewOrderFull(orderIDStr string, lat, lng float64, item string, merchantID *uuid.UUID, cost float64, fee float64, dist float64) {
	extra := map[string]interface{}{
		"estimated_cost": cost,
		"delivery_fee":   fee,
		"distance_km":    dist,
	}
	b.BroadcastOrderCreated(orderIDStr, lat, lng, item, merchantID, extra)
}

func (b *Broadcaster) BroadcastMerchant(merchantID string, eventType string, orderID string, data map[string]interface{}) {
	if b.hub == nil {
		return
	}
	ev := PoolEvent{
		Type:      eventType,
		OrderID:   orderID,
		Timestamp: time.Now().UnixMilli(),
		Data:      data,
	}
	b.hub.BroadcastMerchant(merchantID, ev)
	b.hub.BroadcastToCell("merchant:global", ev)
}

// BroadcastOrderStatus sends status update to order-specific channel (order:{id})
// Low burden: 0 DB queries, in-memory hub broadcast only
func (b *Broadcaster) BroadcastOrderStatus(orderID string, status string, eventType string) {
	if b.hub == nil {
		return
	}
	if eventType == "" {
		eventType = EventOrderStatus
	}
	ev := PoolEvent{
		Type:      eventType,
		OrderID:   orderID,
		Timestamp: time.Now().UnixMilli(),
		Data: map[string]interface{}{
			"order_id": orderID,
			"status":   status,
		},
	}
	// Order-specific cell for detail page SSE
	b.hub.BroadcastToCell("order:"+orderID, ev)
}

// IncrClaimConflict increments redis + hub counter
func (b *Broadcaster) IncrClaimConflict() {
	if b.hub != nil {
		b.hub.IncrClaimConflict()
	}
	if b.redis != nil {
		_, _ = b.redis.IncrCounter(context.Background(), "claim:conflict")
	}
}
