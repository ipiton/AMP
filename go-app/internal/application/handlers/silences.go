package handlers

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/infrastructure/storage/memory"
)

func SilencesHandler(registry RegistryProvider) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		store := registry.SilenceStore()
		switch r.Method {
		case http.MethodGet:
			handleSilencesGet(store, w, r)
		case http.MethodPost:
			handleSilencePost(store, w, r)
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
			if !store.Delete(id) {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.WriteHeader(http.StatusOK)
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

func handleSilencePost(store *memory.SilenceStore, w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
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

	id, err := store.CreateOrUpdate(&in, time.Now().UTC())
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"silenceID": id})
}
