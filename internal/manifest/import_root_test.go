package manifest_test

import (
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// TestPublishCopy_ImportRootRewritesRoot: when importRoot rebases the archive,
// the shipped manifest must describe the post-strip layout (root relative to
// importRoot) and drop importRoot/include, without mutating the original.
func TestPublishCopy_ImportRootRewritesRoot(t *testing.T) {
	m := &manifest.Manifest{
		Name: "x", Version: "1.0.0",
		Root: "lib/com/fourjs/x", ImportRoot: "lib",
		Include: []string{"dist/app.4st"},
	}
	pc := m.PublishCopy()
	if pc.Root != "com/fourjs/x" {
		t.Errorf("Root = %q, want com/fourjs/x", pc.Root)
	}
	if pc.ImportRoot != "" {
		t.Errorf("ImportRoot = %q, want empty", pc.ImportRoot)
	}
	if pc.Include != nil {
		t.Errorf("Include = %v, want nil", pc.Include)
	}
	if m.Root != "lib/com/fourjs/x" || m.ImportRoot != "lib" || len(m.Include) != 1 {
		t.Errorf("PublishCopy mutated the original: %+v", m)
	}
}

// TestPublishCopy_ImportRootEqualsRoot: root == importRoot collapses to ".".
func TestPublishCopy_ImportRootEqualsRoot(t *testing.T) {
	m := &manifest.Manifest{Name: "x", Version: "1.0.0", Root: "lib", ImportRoot: "lib"}
	pc := m.PublishCopy()
	if pc.Root != "." {
		t.Errorf("Root = %q, want \".\"", pc.Root)
	}
	if pc.ImportRoot != "" {
		t.Errorf("ImportRoot = %q, want empty", pc.ImportRoot)
	}
}

// TestPublishCopy_NoImportRootUnchanged: without importRoot, root is untouched.
func TestPublishCopy_NoImportRootUnchanged(t *testing.T) {
	m := &manifest.Manifest{Name: "x", Version: "1.0.0", Root: "com/fourjs/x"}
	pc := m.PublishCopy()
	if pc.Root != "com/fourjs/x" {
		t.Errorf("Root = %q, want com/fourjs/x (unchanged when importRoot unset)", pc.Root)
	}
}

func TestValidate_ImportRoot(t *testing.T) {
	base := func() *manifest.Manifest {
		return &manifest.Manifest{Name: "x", Version: "1.0.0"}
	}
	cases := []struct {
		name    string
		mutate  func(*manifest.Manifest)
		wantErr bool
	}{
		{"absolute importRoot", func(m *manifest.Manifest) { m.ImportRoot = "/lib" }, true},
		{"escaping importRoot", func(m *manifest.Manifest) { m.ImportRoot = "../lib" }, true},
		{"disjoint root and importRoot", func(m *manifest.Manifest) { m.ImportRoot = "lib"; m.Root = "src" }, true},
		{"root within importRoot", func(m *manifest.Manifest) { m.ImportRoot = "lib"; m.Root = "lib/com/fourjs/x" }, false},
		{"root equals importRoot", func(m *manifest.Manifest) { m.ImportRoot = "lib"; m.Root = "lib" }, false},
		{"root '.' ancestor of importRoot (init default)", func(m *manifest.Manifest) { m.ImportRoot = "lib"; m.Root = "." }, false},
		{"absolute include", func(m *manifest.Manifest) { m.Include = []string{"/etc/passwd"} }, true},
		{"escaping include", func(m *manifest.Manifest) { m.Include = []string{"../secret"} }, true},
		{"valid include", func(m *manifest.Manifest) { m.Include = []string{"dist/app.4st"} }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := base()
			tc.mutate(m)
			err := m.Validate()
			if tc.wantErr && err == nil {
				t.Error("expected an error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}
