package installer

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeZip writes a zip at path with the given entries (name -> decompressed
// byte count).
func makeZip(t *testing.T, path string, entries []struct {
	name string
	size int
}) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create zip: %v", err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	for _, e := range entries {
		w, err := zw.Create(e.name)
		if err != nil {
			t.Fatalf("zip create entry %s: %v", e.name, err)
		}
		if _, err := w.Write(bytes.Repeat([]byte("a"), e.size)); err != nil {
			t.Fatalf("zip write entry %s: %v", e.name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
}

// TestExtractZipEnforcesDecompressedCaps is the regression for issue #24 M5:
// zip extraction must refuse an archive that decompresses beyond the per-entry
// and per-archive caps (a zip-bomb guard), rather than writing it all to disk.
func TestExtractZipEnforcesDecompressedCaps(t *testing.T) {
	t.Run("per-entry cap", func(t *testing.T) {
		defer withCaps(t, 100, 1<<30)()
		zipPath := filepath.Join(t.TempDir(), "bomb.zip")
		makeZip(t, zipPath, []struct {
			name string
			size int
		}{{"big.txt", 1000}})

		err := extractZip(zipPath, t.TempDir())
		if err == nil {
			t.Fatal("expected per-entry cap error, got nil")
		}
		if !strings.Contains(err.Error(), "too large") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("archive total cap", func(t *testing.T) {
		defer withCaps(t, 1<<30, 100)()
		zipPath := filepath.Join(t.TempDir(), "bomb.zip")
		makeZip(t, zipPath, []struct {
			name string
			size int
		}{{"a.txt", 60}, {"b.txt", 60}})

		err := extractZip(zipPath, t.TempDir())
		if err == nil {
			t.Fatal("expected archive-total cap error, got nil")
		}
		if !strings.Contains(err.Error(), "decompressed-size limit") {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("within caps succeeds", func(t *testing.T) {
		defer withCaps(t, 1<<20, 1<<20)()
		zipPath := filepath.Join(t.TempDir(), "ok.zip")
		makeZip(t, zipPath, []struct {
			name string
			size int
		}{{"a.txt", 60}, {"b.txt", 60}})

		dest := t.TempDir()
		if err := extractZip(zipPath, dest); err != nil {
			t.Fatalf("extraction within caps should succeed, got: %v", err)
		}
		if _, err := os.Stat(filepath.Join(dest, "a.txt")); err != nil {
			t.Errorf("expected a.txt extracted: %v", err)
		}
	})
}

// withCaps temporarily overrides the decompressed-size caps and returns a
// restore function (call via defer).
func withCaps(t *testing.T, perEntry, total int64) func() {
	t.Helper()
	prevEntry, prevTotal := maxDecompressedPerEntry, maxDecompressedTotal
	maxDecompressedPerEntry = perEntry
	maxDecompressedTotal = total
	return func() {
		maxDecompressedPerEntry = prevEntry
		maxDecompressedTotal = prevTotal
	}
}
