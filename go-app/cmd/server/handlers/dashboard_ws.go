// Package handlers provides HTTP handlers for the Alert History Service.
// TN-78: Real-time Updates (SSE/WebSocket) - Dashboard WebSocket Hub Enhancement
package handlers

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/ipiton/AMP/internal/realtime"
)

// DashboardWebSocketHub extends WebSocketHub for dashboard events.
// It integrates with EventBus to receive and broadcast dashboard events.
type DashboardWebSocketHub struct {
	*WebSocketHub // Embed existing hub from silence_ws.go
	eventBus      *realtime.DefaultEventBus
	logger        *slog.Logger
	metrics       *realtime.RealtimeMetrics
	subscriber    *WebSocketBroadcastSubscriber
	mu            sync.RWMutex
}

// NewDashboardWebSocketHub creates a new dashboard WebSocket hub.
func NewDashboardWebSocketHub(
	baseHub *WebSocketHub,
	eventBus *realtime.DefaultEventBus,
	logger *slog.Logger,
	metrics *realtime.RealtimeMetrics,
) *DashboardWebSocketHub {
	hub := &DashboardWebSocketHub{
		WebSocketHub: baseHub,
		eventBus:     eventBus,
		logger:       logger.With("component", "dashboard_ws_hub"),
		metrics:      metrics,
	}

	// Create broadcast subscriber that forwards EventBus events to WebSocket clients
	hub.subscriber = NewWebSocketBroadcastSubscriber(hub.WebSocketHub, hub.logger, hub.metrics)

	// Subscribe hub to EventBus
	if eventBus != nil {
		if err := eventBus.Subscribe(hub.subscriber); err != nil {
			hub.logger.Error("Failed to subscribe dashboard hub to EventBus", "error", err)
		} else {
			hub.logger.Info("Dashboard WebSocket hub subscribed to EventBus")
		}
	}

	return hub
}

// HandleDashboardWebSocket handles WebSocket upgrade for dashboard.
// GET /ws/dashboard
func (h *DashboardWebSocketHub) HandleDashboardWebSocket(w http.ResponseWriter, r *http.Request) {
	// Use existing HandleWebSocket from WebSocketHub
	h.HandleWebSocket(w, r)
}

// BroadcastDashboardEvent broadcasts a dashboard event to all WebSocket clients.
// This is a convenience method that wraps EventBus.Publish.
func (h *DashboardWebSocketHub) BroadcastDashboardEvent(eventType string, data map[string]interface{}) {
	if h.eventBus == nil {
		h.logger.Warn("EventBus not initialized, cannot broadcast dashboard event")
		return
	}

	event := realtime.NewEvent(eventType, data, realtime.EventSourceSystem)
	if err := h.eventBus.Publish(*event); err != nil {
		h.logger.Warn("Failed to publish dashboard event",
			"event_type", eventType,
			"error", err,
		)
	}
}

// WebSocketBroadcastSubscriber is a subscriber that broadcasts EventBus events to WebSocket clients.
type WebSocketBroadcastSubscriber struct {
	hub    *WebSocketHub
	logger *slog.Logger
	metrics *realtime.RealtimeMetrics
	id     string
	ctx    context.Context
}

// NewWebSocketBroadcastSubscriber creates a new WebSocket broadcast subscriber.
func NewWebSocketBroadcastSubscriber(
	hub *WebSocketHub,
	logger *slog.Logger,
	metrics *realtime.RealtimeMetrics,
) *WebSocketBroadcastSubscriber {
	return &WebSocketBroadcastSubscriber{
		hub:    hub,
		logger: logger.With("component", "ws_broadcast_subscriber"),
		metrics: metrics,
		id:     "ws-broadcast-subscriber",
		ctx:    context.Background(),
	}
}

// ID returns the subscriber ID.
func (s *WebSocketBroadcastSubscriber) ID() string {
	return s.id
}

// Send sends an event to all WebSocket clients.
func (s *WebSocketBroadcastSubscriber) Send(event realtime.Event) error {
	// Convert realtime.Event to SilenceEvent format (for compatibility with existing WebSocketHub)
	silenceEvent := SilenceEvent{
		Type:      event.Type,
		Data:      event.Data,
		Timestamp: event.Timestamp,
	}

	// Broadcast to all WebSocket clients using existing hub
	s.hub.Broadcast(silenceEvent.Type, silenceEvent.Data)

	if s.metrics != nil {
		s.metrics.EventsTotal.WithLabelValues(event.Type, event.Source).Inc()
	}

	return nil
}

// Close closes the subscriber (no-op for broadcast subscriber).
func (s *WebSocketBroadcastSubscriber) Close() error {
	// No-op: broadcast subscriber doesn't maintain a connection
	return nil
}

// Context returns the subscriber context.
func (s *WebSocketBroadcastSubscriber) Context() context.Context {
	return s.ctx
}

// RateLimiter provides rate limiting for WebSocket connections.
type RateLimiter struct {
	connections map[string][]time.Time
	mu          sync.RWMutex
	maxPerIP    int
	window      time.Duration
}

// NewRateLimiter creates a new rate limiter.
func NewRateLimiter(maxPerIP int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		connections: make(map[string][]time.Time),
		maxPerIP:   maxPerIP,
		window:     window,
	}
}

// Allow checks if a connection from the given IP is allowed.
func (rl *RateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-rl.window)

	// Clean up old entries
	if times, ok := rl.connections[ip]; ok {
		validTimes := make([]time.Time, 0, len(times))
		for _, t := range times {
			if t.After(cutoff) {
				validTimes = append(validTimes, t)
			}
		}
		rl.connections[ip] = validTimes
	} else {
		rl.connections[ip] = make([]time.Time, 0)
	}

	// Check if limit exceeded
	if len(rl.connections[ip]) >= rl.maxPerIP {
		return false
	}

	// Add current connection
	rl.connections[ip] = append(rl.connections[ip], now)
	return true
}

// GetCount returns the number of connections for the given IP.
func (rl *RateLimiter) GetCount(ip string) int {
	rl.mu.RLock()
	defer rl.mu.RUnlock()

	if times, ok := rl.connections[ip]; ok {
		return len(times)
	}
	return 0
}

// RateLimitedWebSocketHandler wraps a WebSocket handler with rate limiting.
func RateLimitedWebSocketHandler(
	handler func(http.ResponseWriter, *http.Request),
	rateLimiter *RateLimiter,
	logger *slog.Logger,
) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Extract IP address
		ip := r.RemoteAddr
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			ip = forwarded
		}

		// Check rate limit
		if !rateLimiter.Allow(ip) {
			logger.Warn("WebSocket connection rate limit exceeded",
				"ip", ip,
				"count", rateLimiter.GetCount(ip),
			)
			http.Error(w, "Too many connections", http.StatusTooManyRequests)
			return
		}

		// Call original handler
		handler(w, r)
	}
}
