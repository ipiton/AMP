// Package cluster provides a Redis-backed peer heartbeat used to populate
// the `cluster` field of /api/v2/status (task 6.5, alertmanager-parity,
// Phase 6 — Redis-based HA).
//
// Unlike upstream Alertmanager's memberlist/gossip cluster, AMP's "cluster"
// concept in the standard profile is intentionally minimal: each replica
// periodically re-registers a short-TTL key in the shared Redis instance
// (amp:cluster:peer:<id>), and the status endpoint lists whichever peer
// keys currently exist. A crashed/stopped replica's key simply expires —
// there is no failure detector, no gossip, no consensus. This is enough to
// answer "how many replicas are alive right now and what are their names"
// for the status endpoint; it is NOT a general-purpose cluster membership
// system and nothing else in AMP depends on it (leader election, task 6.4,
// is a separate SET-NX lock, not layered on top of this).
package cluster

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

// Default heartbeat timings. TTL sits in the same 15-30s window task 6.5's
// brief calls out for peer registration; RefreshInterval is TTL/3 so at
// least two consecutive refreshes can fail before the key actually expires
// (same reasoning as lock.DefaultElectionRenewInterval, task 6.4).
const (
	DefaultHeartbeatTTL             = 20 * time.Second
	DefaultHeartbeatRefreshInterval = DefaultHeartbeatTTL / 3

	peerKeyPrefix = "amp:cluster:peer:"
)

// PeerInfo is a single cluster member, matching upstream Alertmanager's
// cluster.peers[] wire shape ({"name", "address"}).
type PeerInfo struct {
	Name    string
	Address string
}

// peerRecord is the JSON value actually stored under each peer's Redis key.
// Since is informational (surfaced nowhere on the wire today) but kept for
// observability — cheap to store, useful when inspecting Redis directly
// during an incident or the e2e test.
type peerRecord struct {
	Name    string    `json:"name"`
	Address string    `json:"address,omitempty"`
	Since   time.Time `json:"since"`
}

// HeartbeatRegistry registers this replica's presence in Redis
// (amp:cluster:peer:<selfID>, TTL-bound) and lists currently-live peers by
// scanning the same key prefix. Redis's own TTL expiry is the only
// liveness/expiry mechanism — Peers() never needs to filter "stale" entries
// itself; if a key is returned by SCAN it has not expired.
type HeartbeatRegistry struct {
	client  *redis.Client
	logger  *slog.Logger
	selfID  string
	address string
	ttl     time.Duration
	refresh time.Duration
	since   time.Time

	mu     sync.Mutex
	cancel context.CancelFunc
	doneCh chan struct{}

	// registered is true once the first (synchronous, inside Start) SET has
	// succeeded, and set back to false by Stop. This is the "settling" vs
	// "ready" signal task 6.5's brief calls for: the status handler reports
	// "disabled" until this flips true, then "ready".
	registered atomic.Bool
}

// NewHeartbeatRegistry creates a HeartbeatRegistry. selfID empty generates
// one (hostname + random suffix — stable for the process lifetime, unique
// across replicas sharing a hostname prefix in practice because container
// hostnames are already unique). ttl/refresh <= 0 fall back to the
// Default* constants above. Start must be called to begin registering.
func NewHeartbeatRegistry(
	client *redis.Client,
	selfID string,
	address string,
	ttl time.Duration,
	refresh time.Duration,
	logger *slog.Logger,
) *HeartbeatRegistry {
	if logger == nil {
		logger = slog.Default()
	}
	if selfID == "" {
		selfID = generateSelfID()
	}
	if ttl <= 0 {
		ttl = DefaultHeartbeatTTL
	}
	if refresh <= 0 {
		refresh = DefaultHeartbeatRefreshInterval
	}

	return &HeartbeatRegistry{
		client:  client,
		logger:  logger,
		selfID:  selfID,
		address: address,
		ttl:     ttl,
		refresh: refresh,
		since:   time.Now().UTC(),
	}
}

// generateSelfID builds a stable-for-the-process identifier: hostname plus
// a random 4-byte suffix so two replicas sharing a hostname (unlikely, but
// e.g. a scripted test harness that doesn't set one) still get distinct
// peer keys.
func generateSelfID() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "amp"
	}
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return host
	}
	return fmt.Sprintf("%s-%s", host, hex.EncodeToString(buf))
}

// SelfID returns this replica's identifier — the "name" field of the
// status endpoint's cluster object.
func (h *HeartbeatRegistry) SelfID() string {
	return h.selfID
}

// IsRegistered reports whether this replica has an active heartbeat
// registration (true from the moment Start's first SET succeeds until
// Stop is called). The status handler treats false as cluster.status
// "disabled" and true as "ready".
func (h *HeartbeatRegistry) IsRegistered() bool {
	return h.registered.Load()
}

func (h *HeartbeatRegistry) key() string {
	return peerKeyPrefix + h.selfID
}

// Start performs the first heartbeat registration synchronously (so a
// caller can rely on IsRegistered() being accurate the instant Start
// returns successfully), then begins a background refresh loop that
// re-registers every refresh interval until ctx is cancelled or Stop is
// called. Returns an error, without starting the background loop, if the
// initial registration fails or Start has already been called.
func (h *HeartbeatRegistry) Start(ctx context.Context) error {
	h.mu.Lock()
	if h.cancel != nil {
		h.mu.Unlock()
		return fmt.Errorf("cluster heartbeat already started for %q", h.selfID)
	}
	h.mu.Unlock()

	if err := h.register(ctx); err != nil {
		return fmt.Errorf("initial cluster heartbeat registration failed: %w", err)
	}
	h.registered.Store(true)

	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	h.mu.Lock()
	h.cancel = cancel
	h.doneCh = done
	h.mu.Unlock()

	go h.run(runCtx, done)

	h.logger.Info("cluster heartbeat started", "self_id", h.selfID, "ttl", h.ttl)
	return nil
}

// Stop ends the background refresh loop and marks this replica
// unregistered (IsRegistered() reports false from this point on). It
// best-effort deletes the Redis key so other replicas' Peers() converges
// immediately instead of waiting out the TTL, then waits for the refresh
// goroutine to actually exit or ctx to be done, whichever comes first.
// Idempotent: safe to call on a never-started or already-stopped registry.
func (h *HeartbeatRegistry) Stop(ctx context.Context) error {
	h.mu.Lock()
	cancel := h.cancel
	done := h.doneCh
	h.cancel = nil
	h.mu.Unlock()

	h.registered.Store(false)

	if cancel == nil {
		return nil // never started, or already stopped
	}
	cancel()

	delCtx, delCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer delCancel()
	if err := h.client.Del(delCtx, h.key()).Err(); err != nil {
		h.logger.Warn("cluster heartbeat: best-effort key delete on stop failed", "error", err, "key", h.key())
	}

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// run refreshes the heartbeat key every h.refresh until ctx is cancelled. A
// single failed refresh is logged and retried on the next tick — it does
// NOT flip registered back to false; that only happens on Stop. A run of
// failures longer than the TTL will still cause the key to expire in
// Redis, and Peers() (on any replica, including this one) will correctly
// stop listing it — IsRegistered() staying true in that window is a
// "this replica believes it's registered" signal, not a live guarantee,
// same posture as lock.LeaderElector's isLeader between renewal attempts.
func (h *HeartbeatRegistry) run(ctx context.Context, done chan struct{}) {
	defer close(done)

	ticker := time.NewTicker(h.refresh)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := h.register(ctx); err != nil {
				h.logger.Warn("cluster heartbeat: refresh failed, will retry next tick",
					"error", err, "key", h.key())
			}
		}
	}
}

func (h *HeartbeatRegistry) register(ctx context.Context) error {
	rec := peerRecord{Name: h.selfID, Address: h.address, Since: h.since}
	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal peer record: %w", err)
	}
	return h.client.Set(ctx, h.key(), data, h.ttl).Err()
}

// Peers lists all currently-live cluster members (including this replica,
// if registered) by scanning amp:cluster:peer:* and reading each key's
// value. A key that expires between the SCAN and the MGET (or holds a
// malformed value — should never happen given only this package writes
// these keys, but defensive against a stray manual SET during debugging)
// is silently skipped rather than failing the whole call: a slightly
// stale/short peer list beats a hard error on the status endpoint.
func (h *HeartbeatRegistry) Peers(ctx context.Context) ([]PeerInfo, error) {
	var (
		peers  []PeerInfo
		cursor uint64
	)

	for {
		keys, next, err := h.client.Scan(ctx, cursor, peerKeyPrefix+"*", 100).Result()
		if err != nil {
			return nil, fmt.Errorf("cluster heartbeat: scan failed: %w", err)
		}

		if len(keys) > 0 {
			vals, err := h.client.MGet(ctx, keys...).Result()
			if err != nil {
				return nil, fmt.Errorf("cluster heartbeat: mget failed: %w", err)
			}
			for _, v := range vals {
				s, ok := v.(string)
				if !ok {
					continue // expired between SCAN and MGET, or nil
				}
				var rec peerRecord
				if err := json.Unmarshal([]byte(s), &rec); err != nil {
					h.logger.Warn("cluster heartbeat: malformed peer record, skipping", "error", err)
					continue
				}
				peers = append(peers, PeerInfo{Name: rec.Name, Address: rec.Address})
			}
		}

		cursor = next
		if cursor == 0 {
			break
		}
	}

	sort.Slice(peers, func(i, j int) bool { return peers[i].Name < peers[j].Name })
	return peers, nil
}
