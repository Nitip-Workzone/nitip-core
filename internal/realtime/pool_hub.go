package realtime

import (
	"bufio"
	"encoding/json"
	"fmt"
	"math"
	"sync"
	"time"
)

// Event types for pool realtime
const (
	EventOrderCreated   = "order_created"
	EventOrderClaimed   = "order_claimed"
	EventOrderCancelled = "order_cancelled"
	EventOrderExpired   = "order_expired"
	EventOrderCompleted = "order_completed"
	EventOrderReady     = "order_ready"
	EventOrderStatus    = "order_status"
	EventPoolHeartbeat  = "heartbeat"
)

// PoolEvent is the payload broadcast to runners/merchants
type PoolEvent struct {
	Type      string      `json:"type"`
	OrderID   string      `json:"order_id,omitempty"`
	CellKey   string      `json:"cell_key,omitempty"`
	Data      interface{} `json:"data,omitempty"`
	Timestamp int64       `json:"ts"`
}

// SSEClient holds a channel for server-sent events
type SSEClient struct {
	ID       string
	CellKeys []string // subscribed geohash cells (or "merchant:{merchantId}")
	Lat      float64
	Lng      float64
	RadiusKm float64
	Ch       chan PoolEvent
	Done     chan struct{}
}

// PoolHub manages SSE clients for order pool realtime
// Reuses pattern from chat.Hub but for geohash cells
type PoolHub struct {
	mu sync.RWMutex
	// cellKey -> []*SSEClient
	clientsByCell map[string][]*SSEClient
	// clientID -> *SSEClient
	clients map[string]*SSEClient

	// metrics
	connections    int64
	totalEvents    int64
	claimConflicts int64
}

func NewPoolHub() *PoolHub {
	return &PoolHub{
		clientsByCell: make(map[string][]*SSEClient),
		clients:       make(map[string]*SSEClient),
	}
}

// CellFromLatLng converts lat/lng to geohash-ish cell key (0.1 deg grid ~11km)
// Simple grid for MVP - can be replaced by H3/S2 later
func CellFromLatLng(lat, lng float64) string {
	// Round to 0.1 degree
	cellLat := math.Floor(lat*10) / 10
	cellLng := math.Floor(lng*10) / 10
	return fmt.Sprintf("%.1f:%.1f", cellLat, cellLng)
}

// NeighborCells returns 9 cells including the center (for 15km radius overlap)
func NeighborCells(lat, lng float64) []string {
	baseLat := math.Floor(lat*10) / 10
	baseLng := math.Floor(lng*10) / 10
	var cells []string
	for dLat := -0.1; dLat <= 0.1; dLat += 0.1 {
		for dLng := -0.1; dLng <= 0.1; dLng += 0.1 {
			cells = append(cells, fmt.Sprintf("%.1f:%.1f", baseLat+dLat, baseLng+dLng))
		}
	}
	return cells
}

// RegisterSSE adds an SSE client subscribed to cells
func (h *PoolHub) RegisterSSE(client *SSEClient) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.clients[client.ID] = client
	h.connections++

	// Ensure Done channel exists
	if client.Done == nil {
		client.Done = make(chan struct{})
	}

	for _, cell := range client.CellKeys {
		h.clientsByCell[cell] = append(h.clientsByCell[cell], client)
	}
}

// UnregisterSSE removes client
func (h *PoolHub) UnregisterSSE(clientID string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	client, ok := h.clients[clientID]
	if !ok {
		return
	}

	// Close channels
	select {
	case <-client.Done:
		// already closed
	default:
		close(client.Done)
	}

	// Remove from cell buckets — fix backing array leak retain pointer
	for _, cell := range client.CellKeys {
		clients := h.clientsByCell[cell]
		for i, c := range clients {
			if c.ID == clientID {
				// avoid mem leak: copy + nil last
				copy(clients[i:], clients[i+1:])
				clients[len(clients)-1] = nil
				clients = clients[:len(clients)-1]
				h.clientsByCell[cell] = clients
				break
			}
		}
		if len(h.clientsByCell[cell]) == 0 {
			delete(h.clientsByCell, cell)
		}
	}

	delete(h.clients, clientID)
	if h.connections > 0 {
		h.connections--
	}
}

// BroadcastToCell sends event to all clients in a cell (non-blocking)
func (h *PoolHub) BroadcastToCell(cellKey string, ev PoolEvent) {
	h.mu.RLock()
	clients := h.clientsByCell[cellKey]
	// copy slice to avoid holding lock during send
	cp := make([]*SSEClient, len(clients))
	copy(cp, clients)
	h.mu.RUnlock()

	if len(cp) == 0 {
		return
	}

	ev.CellKey = cellKey
	if ev.Timestamp == 0 {
		ev.Timestamp = time.Now().UnixMilli()
	}

	for _, c := range cp {
		select {
		case c.Ch <- ev:
		default:
			// channel full, skip to avoid blocking
		}
	}

	h.mu.Lock()
	h.totalEvents++
	h.mu.Unlock()
}

// BroadcastToCells broadcasts to multiple cells
func (h *PoolHub) BroadcastToCells(cellKeys []string, ev PoolEvent) {
	seen := make(map[string]bool) // clientID dedup
	if ev.Timestamp == 0 {
		ev.Timestamp = time.Now().UnixMilli()
	}

	h.mu.RLock()
	// collect unique clients
	var uniqueClients []*SSEClient
	for _, cell := range cellKeys {
		for _, c := range h.clientsByCell[cell] {
			if !seen[c.ID] {
				seen[c.ID] = true
				uniqueClients = append(uniqueClients, c)
			}
		}
	}
	h.mu.RUnlock()

	for _, c := range uniqueClients {
		// set cell hint to first matched? keep empty for multi
		select {
		case c.Ch <- ev:
		default:
		}
	}

	h.mu.Lock()
	h.totalEvents++
	h.mu.Unlock()
}

// BroadcastToAll sends to all connected clients (merchant pool, etc)
func (h *PoolHub) BroadcastToAll(ev PoolEvent) {
	h.mu.RLock()
	cp := make([]*SSEClient, 0, len(h.clients))
	for _, c := range h.clients {
		cp = append(cp, c)
	}
	h.mu.RUnlock()

	if ev.Timestamp == 0 {
		ev.Timestamp = time.Now().UnixMilli()
	}

	for _, c := range cp {
		select {
		case c.Ch <- ev:
		default:
		}
	}

	h.mu.Lock()
	h.totalEvents++
	h.mu.Unlock()
}

// BroadcastMerchant sends to merchant-specific cells (merchant:{uuid})
func (h *PoolHub) BroadcastMerchant(merchantID string, ev PoolEvent) {
	h.BroadcastToCell("merchant:"+merchantID, ev)
}

// Stats helpers
func (h *PoolHub) ConnectionCount() int64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.connections
}

func (h *PoolHub) TotalEvents() int64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.totalEvents
}

func (h *PoolHub) IncrClaimConflict() {
	h.mu.Lock()
	h.claimConflicts++
	h.mu.Unlock()
}

func (h *PoolHub) ClaimConflicts() int64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.claimConflicts
}

// Write SSE formatted message to bufio.Writer
func WriteSSEEvent(w *bufio.Writer, ev PoolEvent) error {
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	// SSE format - use Fprintf to avoid QF1012
	if _, err := fmt.Fprintf(w, "event: %s\n", ev.Type); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", string(b)); err != nil {
		return err
	}
	return w.Flush()
}

func WriteSSEHeartbeat(w *bufio.Writer) error {
	if _, err := w.WriteString(": heartbeat\n\n"); err != nil {
		return err
	}
	return w.Flush()
}
