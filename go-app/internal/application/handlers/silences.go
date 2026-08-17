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

func SilencesHandler(registry RegistryProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store := registry.SilenceStore()
		switch r.Method {
		case http.MethodGet:
			handleSilencesGet(store, w, r)
		case http.MethodPost:
			handleSilencePost(store, registry.SilenceRepository(), w, r)
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
			handleSilenceDelete(r.Context(), store, registry.SilenceRepository(), id, w)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
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

func handleSilencePost(store *memory.SilenceStore, repo infrasilencing.SilenceRepository, w http.ResponseWriter, r *http.Request) {
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
		// Memory-only fallback (lite profile / repository unavailable).
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

func handleSilenceDelete(ctx context.Context, store *memory.SilenceStore, repo infrasilencing.SilenceRepository, id string, w http.ResponseWriter) {
	if repo == nil {
		// Memory-only fallback (lite profile / repository unavailable).
		if !store.Delete(id) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
		return
	}

	err := repo.DeleteSilence(ctx, id)
	switch {
	case err == nil:
		// DB commit succeeded; evict from the read cache (best effort).
		store.Delete(id)
		w.WriteHeader(http.StatusOK)
	case errors.Is(err, infrasilencing.ErrSilenceNotFound), errors.Is(err, infrasilencing.ErrInvalidUUID):
		// Not in the database. Evict a stale cache entry if one exists so
		// memory converges back to the database state.
		if store.Delete(id) {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to delete silence"})
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
