package manifest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// loadWithManifest writes raw as fglpkg.json in a temp dir and loads it,
// returning the load error (which is what these tests inspect).
func loadWithManifest(t *testing.T, raw string) error {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "fglpkg.json"), []byte(raw), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, err := manifest.Load(dir)
	return err
}

// TestLoadFriendlyTypeErrors checks that a type mismatch on a manifest field
// yields a field-named message with the expected/actual types, and never leaks
// the raw "json: cannot unmarshal" text (GIS-269).
func TestLoadFriendlyTypeErrors(t *testing.T) {
	cases := []struct {
		name       string
		raw        string
		wantSubstr []string
	}{
		{
			name:       "docs string instead of array",
			raw:        `{"name":"x","version":"1.0.0","docs":"README.md"}`,
			wantSubstr: []string{"docs", "array", "got a string"},
		},
		{
			name:       "keywords string instead of array",
			raw:        `{"name":"x","version":"1.0.0","keywords":"single"}`,
			wantSubstr: []string{"keywords", "array", "got a string"},
		},
		{
			name:       "files object instead of array",
			raw:        `{"name":"x","version":"1.0.0","files":{}}`,
			wantSubstr: []string{"files", "array", "got an object"},
		},
		{
			name:       "programs number instead of array",
			raw:        `{"name":"x","version":"1.0.0","programs":5}`,
			wantSubstr: []string{"programs", "array", "got a number"},
		},
		{
			name:       "bin array instead of object",
			raw:        `{"name":"x","version":"1.0.0","bin":[]}`,
			wantSubstr: []string{"bin", "object", "got an array"},
		},
		{
			name:       "name number falls back to generic string hint",
			raw:        `{"name":5,"version":"1.0.0"}`,
			wantSubstr: []string{"name", "expected a string", "got a number"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := loadWithManifest(t, tc.raw)
			if err == nil {
				t.Fatalf("expected an error for %s, got nil", tc.name)
			}
			msg := err.Error()
			if strings.Contains(msg, "cannot unmarshal") {
				t.Errorf("message leaks the raw json error: %v", err)
			}
			if !strings.Contains(msg, "invalid fglpkg.json") {
				t.Errorf("message should keep the invalid fglpkg.json prefix, got: %v", err)
			}
			for _, want := range tc.wantSubstr {
				if !strings.Contains(msg, want) {
					t.Errorf("message %q should contain %q", msg, want)
				}
			}
		})
	}
}

// TestLoadUnknownFieldHint checks that DisallowUnknownFields errors still name
// the offending field and now carry a schema hint.
func TestLoadUnknownFieldHint(t *testing.T) {
	err := loadWithManifest(t, `{"name":"x","version":"1.0.0","typoField":true}`)
	if err == nil {
		t.Fatal("expected error for unknown field, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "typoField") {
		t.Errorf("error should name the unknown field, got: %v", err)
	}
	if !strings.Contains(msg, "schema/fglpkg.schema.json") {
		t.Errorf("error should point at the schema, got: %v", err)
	}
}

// TestLoadScriptsHintPreserved guards the legacy "scripts" migration hint,
// which must still take precedence over the generic unknown-field handling.
func TestLoadScriptsHintPreserved(t *testing.T) {
	err := loadWithManifest(t, `{"name":"x","version":"1.0.0","scripts":{"postinstall":"echo hi"}}`)
	if err == nil {
		t.Fatal("expected error for legacy scripts field, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, "hooks") {
		t.Errorf("error should mention the hooks replacement, got: %v", err)
	}
}
