// Package handlers provides HTTP handlers for the Alert History Service.
package handlers

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		// TODO: Implement proper origin check based on config
		// For now, allow all origins (development mode)
		return true
	},
}

// WebSocketHub manages WebSocket connections and broadcasts events.
type WebSocketHub struct {
	// Registered clients
	clients map[*websocket.Conn]bool

	// Inbound messages from clients
	broadcast chan SilenceEvent

	// Register requests from clients
	register chan *websocket.Conn

	// Unregister requests from clients
	unregister chan *websocket.Conn

	// Mutex to protect clients map
	mu sync.RWMutex

	// Logger
	logger *slog.Logger

	// Metrics (Phase 14 enhancement)
	metrics *SilenceUIMetrics
}

// SilenceEvent represents a WebSocket event.
type SilenceEvent struct {
	Type      string                 `json:"type"`      // Event type (silence_created, etc.)
	Data      map[string]interface{} `json:"data"`      // Event payload
	Timestamp time.Time              `json:"timestamp"` // Event timestamp
}

// NewWebSocketHub creates a new WebSocketHub.
func NewWebSocketHub(logger *slog.Logger) *WebSocketHub {
	return &WebSocketHub{
		clients:    make(map[*websocket.Conn]bool),
		broadcast:  make(chan SilenceEvent, 256), // Buffered channel
		register:   make(chan *websocket.Conn),
		unregister: make(chan *websocket.Conn),
		logger:     logger,
		metrics:    nil, // Will be set by SetMetrics if needed
	}
}

// SetMetrics sets the metrics instance for the WebSocket hub.
func (h *WebSocketHub) SetMetrics(metrics *SilenceUIMetrics) {
	h.metrics = metrics
}

// Start starts the WebSocket hub (run in goroutine).
func (h *WebSocketHub) Start(ctx context.Context) {
	h.logger.Info("WebSocket hub starting")

	for {
		select {
		case <-ctx.Done():
			h.logger.Info("WebSocket hub stopping")
			h.closeAllConnections()
			return

		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			clientCount := len(h.clients)
			h.mu.Unlock()

			// Update metrics (Phase 14 enhancement)
			if h.metrics != nil {
				h.metrics.UpdateWebSocketConnections(clientCount)
			}

			h.logger.Debug("WebSocket client registered",
				"total_clients", clientCount,
				"remote_addr", client.RemoteAddr().String(),
			)

		case client := <-h.unregister:
			h.mu.Lock()
			clientCount := len(h.clients)
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				client.Close()
				clientCount = len(h.clients)
			}
			h.mu.Unlock()

			// Update metrics (Phase 14 enhancement)
			if h.metrics != nil {
				h.metrics.UpdateWebSocketConnections(clientCount)
			}

			h.logger.Debug("WebSocket client unregistered",
				"total_clients", clientCount,
			)

		case event := <-h.broadcast:
			// Record metrics (Phase 14 enhancement)
			if h.metrics != nil {
				h.metrics.RecordWebSocketMessage(event.Type)
			}

			h.mu.RLock()
			clientCount := len(h.clients)
			h.mu.RUnlock()

			h.logger.Debug("Broadcasting WebSocket event",
				"type", event.Type,
				"clients", clientCount,
			)

			// Send to all clients concurrently
			h.mu.RLock()
			for client := range h.clients {
				go h.sendToClient(client, event)
			}
			h.mu.RUnlock()
		}
	}
}

// sendToClient sends an event to a specific client.
func (h *WebSocketHub) sendToClient(client *websocket.Conn, event SilenceEvent) {
	// Set write deadline
	client.SetWriteDeadline(time.Now().Add(10 * time.Second))

	if err := client.WriteJSON(event); err != nil {
		h.logger.Warn("Failed to send WebSocket message",
			"error", err,
			"remote_addr", client.RemoteAddr().String(),
		)
		// Unregister failed client
		h.unregister <- client
	}
}

// Broadcast broadcasts an event to all connected clients.
func (h *WebSocketHub) Broadcast(eventType string, data map[string]interface{}) {
	event := SilenceEvent{
		Type:      eventType,
		Data:      data,
		Timestamp: time.Now(),
	}

	// Non-blocking send to broadcast channel
	select {
	case h.broadcast <- event:
		h.logger.Debug("Event queued for broadcast", "type", eventType)
	default:
		h.logger.Warn("WebSocket broadcast channel full, dropping event",
			"type", eventType,
		)
	}
}

// HandleWebSocket handles WebSocket upgrade and connection.
// GET /ws/silences
func (h *WebSocketHub) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	// Upgrade HTTP connection to WebSocket
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Error("Failed to upgrade WebSocket connection",
			"error", err,
			"remote_addr", r.RemoteAddr,
		)
		return
	}

	h.logger.Info("WebSocket connection established",
		"remote_addr", conn.RemoteAddr().String(),
	)

	// Register client
	h.register <- conn

	// Start read pump (keeps connection alive)
	go h.readPump(conn)
}

// readPump reads messages from WebSocket connection.
// This mainly handles ping/pong to keep connection alive.
func (h *WebSocketHub) readPump(conn *websocket.Conn) {
	defer func() {
		h.unregister <- conn
	}()

	// Set initial read deadline
	conn.SetReadDeadline(time.Now().Add(60 * time.Second))

	// Set pong handler
	conn.SetPongHandler(func(string) error {
		conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	// Start ping ticker
	ticker := time.NewTicker(54 * time.Second)
	defer ticker.Stop()

	// Read loop
	for {
		select {
		case <-ticker.C:
			// Send ping
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				h.logger.Debug("Ping failed, closing connection", "error", err)
				return
			}

		default:
			// Read message (we don't expect clients to send data, but need to handle close)
			_, _, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err,
					websocket.CloseGoingAway,
					websocket.CloseAbnormalClosure) {
					h.logger.Warn("WebSocket read error", "error", err)
				}
				return
			}
		}
	}
}

// closeAllConnections closes all active WebSocket connections.
func (h *WebSocketHub) closeAllConnections() {
	h.mu.Lock()
	defer h.mu.Unlock()

	for client := range h.clients {
		client.Close()
	}

	h.clients = make(map[*websocket.Conn]bool)

	// Update metrics (Phase 14 enhancement)
	if h.metrics != nil {
		h.metrics.UpdateWebSocketConnections(0)
	}

	h.logger.Info("All WebSocket connections closed")
}

// GetActiveConnections returns the current number of active connections.
func (h *WebSocketHub) GetActiveConnections() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// ============================================================================
// Event Type Constants
// ============================================================================

const (
	// EventSilenceCreated is emitted when a silence is created.
	EventSilenceCreated = "silence_created"

	// EventSilenceUpdated is emitted when a silence is updated.
	EventSilenceUpdated = "silence_updated"

	// EventSilenceDeleted is emitted when a silence is deleted.
	EventSilenceDeleted = "silence_deleted"

	// EventSilenceExpired is emitted when a silence expires (via GC worker).
	EventSilenceExpired = "silence_expired"
)

// ============================================================================
// Helper Functions
// ============================================================================

// NewSilenceCreatedEvent creates a silence_created event payload.
func NewSilenceCreatedEvent(silenceID, creator, status string, startsAt, endsAt time.Time) map[string]interface{} {
	return map[string]interface{}{
		"id":        silenceID,
		"creator":   creator,
		"status":    status,
		"starts_at": startsAt.Format(time.RFC3339),
		"ends_at":   endsAt.Format(time.RFC3339),
	}
}

// NewSilenceUpdatedEvent creates a silence_updated event payload.
func NewSilenceUpdatedEvent(silenceID, updatedBy string, changes map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"id":         silenceID,
		"updated_by": updatedBy,
		"changes":    changes,
	}
}

// NewSilenceDeletedEvent creates a silence_deleted event payload.
func NewSilenceDeletedEvent(silenceID, deletedBy string) map[string]interface{} {
	return map[string]interface{}{
		"id":         silenceID,
		"deleted_by": deletedBy,
	}
}

// NewSilenceExpiredEvent creates a silence_expired event payload.
func NewSilenceExpiredEvent(silenceID string) map[string]interface{} {
	return map[string]interface{}{
		"id": silenceID,
	}
}

// ============================================================================
// JSON Helpers
// ============================================================================

// MarshalEvent marshals a SilenceEvent to JSON.
func MarshalEvent(event SilenceEvent) ([]byte, error) {
	return json.Marshal(event)
}

// UnmarshalEvent unmarshals JSON to SilenceEvent.
func UnmarshalEvent(data []byte) (*SilenceEvent, error) {
	var event SilenceEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return nil, err
	}
	return &event, nil
}
