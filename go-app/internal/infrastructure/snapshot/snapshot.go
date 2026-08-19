// Package snapshot implements the lite profile's file-snapshot persistence
// for silences and the notification log (wave 6, alertmanager-parity,
// FU-LITE-FILE-SNAPSHOT) — the AMP equivalent of upstream Alertmanager's
// --storage.path snapshot file, scoped to what AMP's lite profile actually
// needs to survive a restart.
//
// Format: plain versioned JSON via the stdlib encoding/json (no new
// dependency), deliberately NOT upstream's protobuf+snappy wire format —
// the brief calls that out explicitly as something to keep simple rather
// than copy. JSON is human-inspectable (an operator can `cat` the file
// during an incident) and the version field lets a future format change
// reject an incompatible file cleanly instead of guessing at a schema.
//
// Groups/alerts are intentionally NOT part of this snapshot: upstream
// Alertmanager doesn't persist them either — alerts re-arrive via
// Prometheus's own resend behavior and groups rebuild from there.
package snapshot

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/infrastructure/grouping"
)

// CurrentVersion is the snapshot file format version written by this build.
// Load rejects a file whose Version differs (rather than guessing at a
// schema it was never tested against) — a corrupt/unreadable file and an
// unsupported version are both treated identically by the caller (log Warn,
// start empty), so this is a safety net, not a migration mechanism.
const CurrentVersion = 1

// Data is the top-level file-snapshot shape written to storage.path.
type Data struct {
	Version   int                    `json:"version"`
	WrittenAt time.Time              `json:"written_at"`
	Silences  []core.APISilence      `json:"silences,omitempty"`
	Nflog     grouping.NflogSnapshot `json:"nflog"`
}

// ErrNotExist is returned by Load when path does not exist — the expected,
// routine state on first boot with snapshotting freshly enabled, or any
// boot before the first successful write. Callers distinguish this from a
// genuinely corrupt file (both still resolve to "start empty", but at
// different log levels) via errors.Is.
var ErrNotExist = errors.New("snapshot: file does not exist")

// Write atomically persists data to path: encode into a temp file in the
// same directory, fsync it, close it, then rename it over path (atomic on
// every platform this runs on, since rename within one directory/filesystem
// is atomic), then fsync the directory so the rename's own metadata survives
// a crash immediately after. path's parent directory is created (mode 0700)
// if missing. The temp file — and therefore the final file — is mode 0600:
// silences carry matcher label values and nflog entries carry group keys and
// targets, neither of which is a secret, but there is no reason to make this
// world/group-readable either.
func Write(path string, data Data) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create snapshot directory %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".snapshot-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp snapshot file: %w", err)
	}
	tmpPath := tmp.Name()
	// Best-effort cleanup: harmless no-op once the rename below has already
	// moved tmpPath to path (Remove then just fails ErrNotExist, ignored).
	defer os.Remove(tmpPath)

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("chmod temp snapshot file: %w", err)
	}

	if err := json.NewEncoder(tmp).Encode(data); err != nil {
		tmp.Close()
		return fmt.Errorf("encode snapshot: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("fsync temp snapshot file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp snapshot file: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("rename temp snapshot file into place: %w", err)
	}

	if err := fsyncDir(dir); err != nil {
		// The rename itself already succeeded and is visible to any reader
		// that opens path now — a directory-fsync failure only risks losing
		// the rename's metadata update across a crash in the narrow window
		// right after, not the content. Not worth failing the whole write
		// for (the caller would just retry on the next periodic tick or the
		// final shutdown write anyway), but worth surfacing.
		return fmt.Errorf("fsync snapshot directory %s (write itself succeeded): %w", dir, err)
	}
	return nil
}

// Load reads and decodes path. Returns ErrNotExist (wrapped) when the file
// is absent. Any other failure — permission error, truncated/corrupt JSON,
// or an unsupported Version — is returned as a plain error; callers must
// treat ALL of these as "start empty," never as fatal (the brief's
// never-crash-on-bad-snapshot requirement).
func Load(path string) (Data, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Data{}, fmt.Errorf("%w: %s", ErrNotExist, path)
		}
		return Data{}, fmt.Errorf("read snapshot file %s: %w", path, err)
	}

	var data Data
	if err := json.Unmarshal(raw, &data); err != nil {
		return Data{}, fmt.Errorf("decode snapshot file %s: %w", path, err)
	}
	if data.Version != CurrentVersion {
		return Data{}, fmt.Errorf("snapshot file %s has unsupported version %d (want %d)", path, data.Version, CurrentVersion)
	}
	return data, nil
}

// fsyncDir opens dir and calls Sync on it. Directories can be fsynced on
// every platform this project targets (Linux containers, macOS dev); if
// that ever changes, this is the single call site to special-case.
func fsyncDir(dir string) error {
	f, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
