package runbooks_test

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/ipiton/AMP/internal/core"
	"github.com/ipiton/AMP/internal/infrastructure/investigation/runbooks"
)

var quietLogger = slog.New(slog.NewTextHandler(io.Discard, nil))

func runbookFile(name, alertname string) string {
	return "---\nname: " + name + "\nmatch:\n  alertname: " + alertname + "\n---\nbody of " + name + "\n"
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func matchedNames(t *testing.T, root, alertname string) []string {
	t.Helper()
	set, err := runbooks.LoadDir(root, quietLogger)
	if err != nil {
		t.Fatalf("LoadDir returned error: %v", err)
	}
	var out []string
	for _, rb := range set.Match(&core.Alert{AlertName: alertname}, 0) {
		out = append(out, rb.Name)
	}
	sort.Strings(out)
	return out
}

func TestLoadDir_RecursiveAndFiltering(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "top.md"), runbookFile("top", "A"))
	writeFile(t, filepath.Join(root, "nested", "deep", "Nested.MD"), runbookFile("nested", "A"))
	writeFile(t, filepath.Join(root, "notes.txt"), runbookFile("txt", "A"))
	writeFile(t, filepath.Join(root, ".hidden.md"), runbookFile("hidden-file", "A"))
	writeFile(t, filepath.Join(root, "..data", "cm.md"), runbookFile("configmap-data-dir", "A"))
	writeFile(t, filepath.Join(root, ".git", "x.md"), runbookFile("hidden-dir", "A"))
	writeFile(t, filepath.Join(root, "broken.md"), "no frontmatter here")
	writeFile(t, filepath.Join(root, "huge.md"), runbookFile("huge", "A")+strings.Repeat("x", 64*1024))

	got := matchedNames(t, root, "A")
	if want := []string{"nested", "top"}; !reflect.DeepEqual(got, want) {
		t.Errorf("loaded = %v, want %v", got, want)
	}
}

func TestLoadDir_EmptyDir(t *testing.T) {
	set, err := runbooks.LoadDir(t.TempDir(), quietLogger)
	if err != nil {
		t.Fatalf("LoadDir returned error: %v", err)
	}
	if set.Len() != 0 {
		t.Errorf("Len = %d, want 0", set.Len())
	}
}

func TestLoadDir_RootErrors(t *testing.T) {
	root := t.TempDir()
	if _, err := runbooks.LoadDir(filepath.Join(root, "missing"), quietLogger); err == nil {
		t.Error("missing root: want error")
	}
	file := filepath.Join(root, "file.md")
	writeFile(t, file, runbookFile("f", "A"))
	if _, err := runbooks.LoadDir(file, quietLogger); err == nil {
		t.Error("root is a file: want error")
	}
}

// TestLoadDir_ConfigMapLayout mirrors how Kubernetes mounts a ConfigMap:
// real files live in a hidden "..<timestamp>" dir, "..data" symlinks to it
// and every key is a top-level symlink through "..data".
func TestLoadDir_ConfigMapLayout(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "..2026_09_27", "memory.md"), runbookFile("memory", "A"))
	if err := os.Symlink("..2026_09_27", filepath.Join(root, "..data")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := os.Symlink(filepath.Join("..data", "memory.md"), filepath.Join(root, "memory.md")); err != nil {
		t.Fatal(err)
	}

	got := matchedNames(t, root, "A")
	if want := []string{"memory"}; !reflect.DeepEqual(got, want) {
		t.Errorf("loaded = %v, want %v (exactly once, via the key symlink)", got, want)
	}
}

func TestLoadDir_DirectorySymlinkNotFollowed(t *testing.T) {
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, "outside.md"), runbookFile("outside", "A"))
	root := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if got := matchedNames(t, root, "A"); len(got) != 0 {
		t.Errorf("loaded = %v, want none", got)
	}
}
