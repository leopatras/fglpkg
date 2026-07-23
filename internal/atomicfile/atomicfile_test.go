package atomicfile_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/atomicfile"
)

func TestWriteFileCreatesAndReplaces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.json")

	if err := atomicfile.WriteFile(path, []byte("first"), 0600); err != nil {
		t.Fatalf("write new: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "first" {
		t.Errorf("content = %q, want %q", got, "first")
	}

	// Overwrite: the destination is replaced in place, leaving no temp files.
	if err := atomicfile.WriteFile(path, []byte("second"), 0600); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "second" {
		t.Errorf("content = %q, want %q", got, "second")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "data.json" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("expected only data.json to remain, got %v", names)
	}
}

func TestWriteFileAppliesPerm(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes are not meaningful on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "secret")
	if err := atomicfile.WriteFile(path, []byte("x"), 0600); err != nil {
		t.Fatalf("write: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("perm = %o, want 0600", info.Mode().Perm())
	}
}
