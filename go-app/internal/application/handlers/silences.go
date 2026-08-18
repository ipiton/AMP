package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/ipiton/AMP/internal/core"
	infrasilencing "github.com/ipiton/AMP/internal/infrastructure/silencing"
	"github.com/ipiton/AMP/internal/infrastructure/storage/memory"
)

// Silence write path (SPLIT-BRAIN-RISK slice 2): when a persistent repository
// is available, writes are DB-first — the database commit happens before the
// in-memory store is touched, and a database failure leaves memory unchanged
// (returning 5xx). The memory store remains the read path; it is rehydrated
// from the repository on startup (see ServiceRegistry.rehydrateSilenceStore).
// With a nil repository (lite profile) the legacy memory-only behavior is kept.
//
// Cross-replica cache invalidation (task 6.3): after a successful local
// mirror (store.Upsert/store.Delete), the write path also publishes a
// SilenceEvent via registry.SilenceEventPublisher() so OTHER replicas'
// memory.SilenceStore converge without waiting for a restart — see
// internal/infrastructure/silencing/redis_event_bus.go for the pub/sub
// mechanism and ServiceRegistry.applySilenceEvent for the subscriber side.
// A nil publisher (lite profile, or standard profile without a live Redis
// cache backend) makes this a no-op; a publish error is logged and
// otherwise ignored — the database write already committed, and the
// periodic fallback resync (ServiceRegistry.runSilencePeriodicResync) is the
// backstop for a silently-dropped publish.

func SilencesHandler(registry RegistryProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store := registry.SilenceStore()
		switch r.Method {
		case http.MethodGet:
			handleSilencesGet(store, w, r)
		case http.MethodPost:
			handleSilencePost(store, registry.SilenceRepository(), registry.SilenceEventPublisher(), w, r)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func SilenceByIDHandler(registry RegistryProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store := registry.SilenceStore()
		id := strings.TrimPrefix(r.URL.Path, "/api/v2/silence/")
		if id == "" || strings.Contains(id, "/") {
			writeJSON(w, http.StatusNotFound, map[string]any{
				"error": "silence not found",
			})
			return
		}

		switch r.Method {
		case http.MethodGet:
			silence, ok := store.Get(id, time.Now().UTC())
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			writeJSON(w, http.StatusOK, silence)
		case http.MethodDelete:
			handleSilenceDelete(r.Context(), store, registry.SilenceRepository(), registry.SilenceEventPublisher(), id, w)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

// publishSilenceEvent is a best-effort fire-and-forget helper: publish
// failures are logged, never surfaced to the HTTP client (the database
// write already committed) and never retried here (the periodic fallback
// resync is the retry mechanism — see package doc comment above).
func publishSilenceEvent(ctx context.Context, publisher infrasilencing.SilenceEventPublisher, id string, op infrasilencing.SilenceEventOp) {
	if publisher == nil {
		return
	}
	if err := publisher.Publish(ctx, infrasilencing.SilenceEvent{ID: id, Op: op}); err != nil {
		slog.Default().Warn("silence event publish failed; other replicas converge on next fallback resync",
			"silence_id", id, "op", op, "error", err)
	}
}

func handleSilencesGet(store *memory.SilenceStore, w http.ResponseWriter, r *http.Request) {
	filters, err := ParseLabelMatchers(r.URL.Query()["filter"])
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	all := store.List(time.Now().UTC())

	result := make([]core.APISilence, 0, len(all))
	for _, s := range all {
		if !MatchesSilenceMatchers(filters, s.Matchers) {
			continue
		}
		result = append(result, s)
	}

	writeJSON(w, http.StatusOK, result)
}

func handleSilencePost(store *memory.SilenceStore, repo infrasilencing.SilenceRepository, publisher infrasilencing.SilenceEventPublisher, w http.ResponseWriter, r *http.Request) {
	defer func() { _ = r.Body.Close() }()
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1024*1024))
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "payload too large"})
		return
	}

	var in core.SilenceInput
	if err := json.Unmarshal(body, &in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	now := time.Now().UTC()

	if repo == nil {
		// Memory-only fallback (lite profile / repository unavailable). No
		// persistent source of truth exists for other replicas to converge
		// against, so no event is published — matches "lite profile has no
		// publisher anyway" (registry.SilenceEventPublisher() returns nil).
		id, err := store.CreateOrUpdate(&in, now)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"silenceID": id})
		return
	}

	id, status, err := persistSilenceDBFirst(r.Context(), repo, store, &in, now)
	if err != nil {
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	publishSilenceEvent(r.Context(), publisher, id, infrasilencing.SilenceEventUpsert)
	writeJSON(w, http.StatusOK, map[string]string{"silenceID": id})
}

// persistSilenceDBFirst writes a silence to the persistent repository first
// and mirrors it into the in-memory read cache only after the database commit
// succeeded. On any repository error the memory store is left untouched.
//
// The silence ID has a single source of truth: for creates the repository
// generates it (postgres repo assigns a UUID), and memory adopts the same ID;
// for updates the client-provided ID must already exist in the database.
func persistSilenceDBFirst(ctx context.Context, repo infrasilencing.SilenceRepository, store *memory.SilenceStore, in *core.SilenceInput, now time.Time) (string, int, error) {
	domain, err := SilenceInputToDomain(in, now)
	if err != nil {
		return "", http.StatusBadRequest, err
	}

	if domain.ID != "" {
		// Update path: fetch the current row so optimistic locking
		// (UpdatedAt comparison) operates on fresh data.
		existing, err := repo.GetSilenceByID(ctx, domain.ID)
		if err != nil {
			if errors.Is(err, infrasilencing.ErrSilenceNotFound) || errors.Is(err, infrasilencing.ErrInvalidUUID) {
				// Match the memory-only path contract: unknown ID on update is 400.
				return "", http.StatusBadRequest, errors.New("silence not found")
			}
			return "", http.StatusInternalServerError, errors.New("failed to persist silence")
		}
		domain.CreatedAt = existing.CreatedAt
		domain.UpdatedAt = existing.UpdatedAt
		if err := repo.UpdateSilence(ctx, domain); err != nil {
			return "", silenceRepoErrorStatus(err), silenceRepoClientError(err)
		}
	} else {
		created, err := repo.CreateSilence(ctx, domain)
		if err != nil {
			return "", silenceRepoErrorStatus(err), silenceRepoClientError(err)
		}
		domain = created
	}

	// Database commit succeeded — mirror into the memory read cache under the
	// same ID. A cache failure here must not fail the request: the database is
	// the source of truth and rehydration repairs the cache on next restart.
	cacheIn := *in
	cacheIn.ID = domain.ID
	if _, err := store.Upsert(&cacheIn, now); err != nil {
		slog.Default().Error("silence cache update failed after database write; cache repaired on next restart",
			"silence_id", domain.ID, "error", err)
	}

	return domain.ID, http.StatusOK, nil
}

// handleSilenceDelete implements DELETE /api/v2/silence/{id}.
//
// F3 fix (alertmanager-parity amtool audit): upstream Alertmanager's DELETE
// forces the silence into the "expired" state rather than removing it —
// amtool's `silence expire` relies on the expired silence staying queryable
// via GET /api/v2/silences (status.state == "expired") afterwards. Both
// profiles now implement that:
//   - repo == nil (lite profile): memory.SilenceStore.Expire mutates the
//     cached row in place (see its doc comment for the GC/retention
//     posture in this profile — there is none, matching pre-existing
//     lifetime behavior for naturally-elapsed silences).
//   - repo != nil (standard/Postgres profile): SilenceRepository.ExpireSilence
//     does the same UPDATE-not-DELETE in the database; row removal is left
//     exclusively to the GC retention worker (ExpireSilences with
//     deleteExpired=true). This is an UPDATE, not a delete-from-store, so
//     the cross-replica event published on success is SilenceEventUpsert
//     (not Delete) — other replicas' applySilenceEvent re-fetch by ID and
//     mirror the new (expired) state into their own read cache rather than
//     evicting it, since the read path already computes state from
//     timestamps live on every read.
func handleSilenceDelete(ctx context.Context, store *memory.SilenceStore, repo infrasilencing.SilenceRepository, publisher infrasilencing.SilenceEventPublisher, id string, w http.ResponseWriter) {
	now := time.Now().UTC()

	if repo == nil {
		// Memory-only fallback (lite profile / repository unavailable). No
		// persistent source of truth exists for other replicas, so no event
		// is published (same reasoning as handleSilencePost's repo==nil path).
		if !store.Expire(id, now) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	err := repo.ExpireSilence(ctx, id, now)
	switch {
	case err == nil:
		// DB commit succeeded — the row survives, forced into "expired".
		// Re-fetch it (rather than trusting a locally-derived value) so the
		// cache mirrors exactly what's now in the database, same posture as
		// applySilenceEvent's fetch-by-ID-then-mirror flow; this also
		// correctly populates the cache on a replica that never had this
		// silence cached in the first place (e.g. it was created on another
		// replica and this one hasn't resynced yet).
		if fresh, ferr := repo.GetSilenceByID(ctx, id); ferr == nil {
			if _, uerr := store.UpsertFromAPI(DomainSilenceToAPI(fresh, now), now); uerr != nil {
				slog.Default().Warn("silence cache update failed after expire; cache repaired on next resync",
					"silence_id", id, "error", uerr)
			}
		} else {
			slog.Default().Warn("silence expired in db but re-fetch for cache mirror failed; cache repaired on next resync",
				"silence_id", id, "error", ferr)
		}
		publishSilenceEvent(ctx, publisher, id, infrasilencing.SilenceEventUpsert)
		w.WriteHeader(http.StatusOK)
	case errors.Is(err, infrasilencing.ErrSilenceNotFound), errors.Is(err, infrasilencing.ErrInvalidUUID):
		// Not in the database (already hard-deleted by GC retention, or
		// never existed). This genuinely is a deletion, not an expiry: evict
		// a stale cache entry if one exists so memory converges back to the
		// database state, and let other replicas evict their own stale copy
		// too.
		if store.Delete(id) {
			publishSilenceEvent(ctx, publisher, id, infrasilencing.SilenceEventDelete)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to expire silence"})
	}
}

// silenceRepoErrorStatus maps repository errors onto HTTP status codes.
// Client-caused failures map to 4xx; everything else is a 500 so callers can
// retry without the memory store having been touched.
func silenceRepoErrorStatus(err error) int {
	switch {
	case errors.Is(err, infrasilencing.ErrValidation),
		errors.Is(err, infrasilencing.ErrInvalidUUID),
		errors.Is(err, infrasilencing.ErrSilenceExists):
		return http.StatusBadRequest
	case errors.Is(err, infrasilencing.ErrSilenceNotFound):
		return http.StatusBadRequest
	case errors.Is(err, infrasilencing.ErrSilenceConflict):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

// silenceRepoClientError returns an error safe to expose to API clients:
// client-caused errors keep their message, internal failures are masked.
func silenceRepoClientError(err error) error {
	if silenceRepoErrorStatus(err) == http.StatusInternalServerError {
		return errors.New("failed to persist silence")
	}
	return err
}
