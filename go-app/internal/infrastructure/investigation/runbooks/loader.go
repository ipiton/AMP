// Package runbooks loads PHASE-6B investigation runbooks from the filesystem.
package runbooks

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/ipiton/AMP/internal/core/investigation/runbook"
)

// LoadDir walks root recursively and parses every *.md file into a runbook set.
//
// Entries whose name starts with "." are skipped, which also covers the
// "..data" / "..<timestamp>" directories of a mounted Kubernetes ConfigMap.
// Symlinks to files are read (ConfigMap keys are symlinks); symlinks to
// directories are not followed. Unreadable or invalid files are logged and
// skipped. Only an inaccessible root is an error.
func LoadDir(root string, logger *slog.Logger) (*runbook.Set, error) {
	if logger == nil {
		logger = slog.Default()
	}

	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("runbooks path %q: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("runbooks path %q is not a directory", root)
	}

	var (
		loaded  []runbook.Runbook
		skipped int
	)
	skip := func(path string, reason error) {
		skipped++
		logger.Warn("Skipping invalid runbook", "file", path, "error", reason)
	}

	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == root {
				return err
			}
			skip(path, err)
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if path != root && strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() || !strings.EqualFold(filepath.Ext(d.Name()), ".md") {
			return nil
		}

		// Resolve symlinks: accept only regular files.
		fi, err := os.Stat(path)
		if err != nil {
			skip(path, err)
			return nil
		}
		if !fi.Mode().IsRegular() {
			return nil
		}
		if fi.Size() > runbook.MaxFileSize {
			skip(path, fmt.Errorf("file size %d exceeds %d bytes", fi.Size(), runbook.MaxFileSize))
			return nil
		}

		data, err := os.ReadFile(path)
		if err != nil {
			skip(path, err)
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			rel = path
		}
		rb, err := runbook.Parse(filepath.ToSlash(rel), data)
		if err != nil {
			skip(path, err)
			return nil
		}
		loaded = append(loaded, rb)
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("walk runbooks path %q: %w", root, walkErr)
	}

	logger.Info("Runbooks loaded", "path", root, "loaded", len(loaded), "skipped", skipped)
	return runbook.NewSet(loaded), nil
}
