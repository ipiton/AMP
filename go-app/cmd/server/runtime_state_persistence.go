package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	runtimeStateFileEnv       = "AMP_STATE_FILE"
	defaultRuntimeStateFile   = "data/runtime-state.json"
	persistedStateFileVersion = 1
)

type persistedRuntimeState struct {
	Version  int          `json:"version"`
	SavedAt  string       `json:"savedAt"`
	Alerts   []apiAlert   `json:"alerts"`
	Silences []apiSilence `json:"silences"`
}

type runtimeStatePersistence struct {
	mu       sync.Mutex
	path     string
	alerts   *alertStore
	silences *silenceStore
}

func setupRuntimeStatePersistence(alerts *alertStore, silences *silenceStore) {
	path := resolveRuntimeStatePath()
	if path == "" {
		slog.Info("Runtime state persistence disabled", "env", runtimeStateFileEnv)
		return
	}

	persistence := &runtimeStatePersistence{
		path:     path,
		alerts:   alerts,
		silences: silences,
	}

	if err := persistence.load(); err != nil {
		slog.Warn("Failed to restore runtime state", "path", path, "error", err)
	} else {
		slog.Info("Runtime state restored", "path", path)
	}

	notify := func(component string) func() {
		return func() {
			if err := persistence.save(); err != nil {
				slog.Warn("Failed to persist runtime state", "path", path, "component", component, "error", err)
			}
		}
	}

	alerts.setOnChange(notify("alerts"))
	silences.setOnChange(notify("silences"))
}

func resolveRuntimeStatePath() string {
	raw := strings.TrimSpace(os.Getenv(runtimeStateFileEnv))
	switch strings.ToLower(raw) {
	case "off", "none", "disabled":
		return ""
	}
	if raw != "" {
		return raw
	}
	return defaultRuntimeStateFile
}

func (p *runtimeStatePersistence) load() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	content, err := os.ReadFile(p.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read state file: %w", err)
	}

	var state persistedRuntimeState
	if err := json.Unmarshal(content, &state); err != nil {
		return fmt.Errorf("decode state file: %w", err)
	}

	now := time.Now().UTC()
	if err := p.alerts.restoreFromPersistence(state.Alerts, now); err != nil {
		return fmt.Errorf("restore alerts: %w", err)
	}
	if err := p.silences.restoreFromPersistence(state.Silences, now); err != nil {
		return fmt.Errorf("restore silences: %w", err)
	}

	return nil
}

func (p *runtimeStatePersistence) save() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	now := time.Now().UTC()
	state := persistedRuntimeState{
		Version:  persistedStateFileVersion,
		SavedAt:  now.Format(time.RFC3339),
		Alerts:   p.alerts.exportForPersistence(),
		Silences: p.silences.exportForPersistence(now),
	}

	content, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state file: %w", err)
	}

	dir := filepath.Dir(p.path)
	if dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create state directory: %w", err)
		}
	}

	tmpPath := p.path + ".tmp"
	if err := os.WriteFile(tmpPath, content, 0o644); err != nil {
		return fmt.Errorf("write temp state file: %w", err)
	}
	if err := os.Rename(tmpPath, p.path); err != nil {
		return fmt.Errorf("replace state file: %w", err)
	}

	return nil
}
