package credentials_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/credentials"
)

// TestUnknownFieldsPreserved is the regression test for issue #24 C4: the
// package doc promises unknown fields are preserved, so a read-modify-write
// cycle (as done by login/logout/OAuth-refresh) must not drop keys this build
// does not know about — neither top-level keys nor per-entry keys.
func TestUnknownFieldsPreserved(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "credentials.json")

	// A file written by a hypothetical newer fglpkg: an unknown top-level key
	// and an unknown per-entry key alongside the known ones.
	const raw = `{
  "schemaVersion": 99,
  "registries": {
    "https://registry.fglpkg.dev": {
      "pat": "abc123",
      "savedAt": "2026-01-01T00:00:00Z",
      "futureField": {"nested": true}
    }
  }
}`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Read-modify-write: touch one entry, then persist.
	f, err := credentials.Load(home)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	f.Set("https://registry.fglpkg.dev", "def456", "alice")
	if err := f.Save(home); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// The unknown keys must survive verbatim.
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("parse output: %v\n%s", err, out)
	}
	if _, ok := got["schemaVersion"]; !ok {
		t.Errorf("unknown top-level key \"schemaVersion\" was dropped:\n%s", out)
	}

	var regs map[string]map[string]json.RawMessage
	if err := json.Unmarshal(got["registries"], &regs); err != nil {
		t.Fatalf("parse registries: %v", err)
	}
	entry := regs["https://registry.fglpkg.dev"]
	if _, ok := entry["futureField"]; !ok {
		t.Errorf("unknown per-entry key \"futureField\" was dropped:\n%s", out)
	}
	// The known mutation must still have taken effect.
	if string(entry["pat"]) != `"def456"` {
		t.Errorf("pat = %s, want \"def456\"", entry["pat"])
	}
}
