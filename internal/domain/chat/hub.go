package chat

import (
	"sync"
	"time"

	"github.com/gofiber/websocket/v2"
	"github.com/google/uuid"
)

type Client struct {
	UserID uuid.UUID
	Conn   *websocket.Conn
}

type Hub struct {
	// orders maps OrderID string to connected clients
	orders map[string][]*Client
	mu     sync.RWMutex
}

func NewHub() *Hub {
	return &Hub{
		orders: make(map[string][]*Client),
	}
}

func (h *Hub) Register(orderID string, client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.orders[orderID] = append(h.orders[orderID], client)
}

func (h *Hub) Unregister(orderID string, userID uuid.UUID) {
	h.mu.Lock()
	defer h.mu.Unlock()

	clients, ok := h.orders[orderID]
	if !ok {
		return
	}

	for i, c := range clients {
		if c.UserID == userID {
			h.orders[orderID] = append(clients[:i], clients[i+1:]...)
			break
		}
	}

	if len(h.orders[orderID]) == 0 {
		delete(h.orders, orderID)
	}
}

func (h *Hub) Broadcast(orderID string, msg interface{}) {
	h.mu.RLock()
	clients, ok := h.orders[orderID]
	h.mu.RUnlock()

	if !ok {
		return
	}

	var deadClients []uuid.UUID
	for _, client := range clients {
		if err := client.Conn.SetWriteDeadline(time.Now().Add(5 * time.Second)); err != nil {
			deadClients = append(deadClients, client.UserID)
			continue
		}
		err := client.Conn.WriteJSON(msg)
		if err != nil {
			deadClients = append(deadClients, client.UserID)
		}
	}

	// Cleanup dead connections
	if len(deadClients) > 0 {
		for _, userID := range deadClients {
			h.Unregister(orderID, userID)
		}
	}
}

func (h *Hub) IsUserOnline(orderID string, userID uuid.UUID) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()

	clients, ok := h.orders[orderID]
	if !ok {
		return false
	}

	for _, c := range clients {
		if c.UserID == userID {
			return true
		}
	}
	return false
}
