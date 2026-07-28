package order

import (
	"github.com/codecoffy/nitip-core/internal/realtime"
)

// poolHubAdapter adapts realtime.Broadcaster to order.service PoolBroadcaster interface
// without import cycle issues (order -> realtime -> order would cycle if broadcaster imported order)
// So we have thin adapter here that uses the hub directly, implemented in order package.

type poolHubAdapter struct {
	hub         *realtime.PoolHub
	broadcaster *realtime.Broadcaster
}

func NewPoolBroadcasterAdapter(hub *realtime.PoolHub, bc *realtime.Broadcaster) PoolBroadcaster {
	return &poolHubAdapter{hub: hub, broadcaster: bc}
}

func (a *poolHubAdapter) BroadcastNewOrder(o *Order) {
	if a.broadcaster != nil {
		a.broadcaster.BroadcastNewOrderFull(o.ID.String(), o.PickupLat, o.PickupLng, o.ItemDetails, o.MerchantID, o.EstimatedCost, o.DeliveryFee, o.DistanceKm)
	}
}

func (a *poolHubAdapter) BroadcastClaimed(orderID string, runnerID string, pickupLat, pickupLng float64) {
	if a.broadcaster != nil {
		a.broadcaster.BroadcastOrderClaimed(orderID, runnerID, pickupLat, pickupLng)
	}
}

func (a *poolHubAdapter) BroadcastCancelled(orderID string, reason string, pickupLat, pickupLng float64) {
	if a.broadcaster != nil {
		a.broadcaster.BroadcastOrderCancelled(orderID, reason, pickupLat, pickupLng)
	}
}

func (a *poolHubAdapter) BroadcastMerchantEvent(merchantID string, eventType string, order *Order) {
	if a.broadcaster != nil {
		data := map[string]interface{}{
			"order_id":     order.ID.String(),
			"item":         order.ItemDetails,
			"status":       order.Status,
			"pickup_lat":   order.PickupLat,
			"pickup_lng":   order.PickupLng,
			"delivery_fee": order.DeliveryFee,
			"merchant_id":  merchantID,
		}
		a.broadcaster.BroadcastMerchant(merchantID, eventType, order.ID.String(), data)
	}
}

func (a *poolHubAdapter) IncrConflict() {
	if a.broadcaster != nil {
		a.broadcaster.IncrClaimConflict()
	}
}
