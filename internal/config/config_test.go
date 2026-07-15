package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolve_BuiltinOnly(t *testing.T) {
	regs, err := Resolve(BuiltinGI(""), nil, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(regs) != 1 {
		t.Fatalf("want 1 registry, got %d", len(regs))
	}
	gi := regs[0]
	if gi.Name != GIName || gi.Type != TypeGenero || gi.Priority != 1 || gi.Auth != AuthBearer {
		t.Fatalf("unexpected builtin gi: %+v", gi)
	}
	if gi.URL != defaultGIURL {
		t.Fatalf("gi URL = %q, want %q", gi.URL, defaultGIURL)
	}
}

func TestResolve_FGLPKGRegistryRetargetsGI(t *testing.T) {
	regs, err := Resolve(BuiltinGI("https://mirror.example/reg/"), nil, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if regs[0].URL != "https://mirror.example/reg" { // trailing slash trimmed
		t.Fatalf("gi URL = %q", regs[0].URL)
	}
}

func TestResolve_ProjectAddsArtifactoryAndSorts(t *testing.T) {
	project := []Registry{{
		Name: "acme", Type: TypeArtifactory, URL: "https://art.acme.example/artifactory",
		RepoKey: "fgl-generic", Priority: 2, Auth: AuthBearer, Packages: []string{"acme-*"},
	}}
	regs, err := Resolve(BuiltinGI(""), nil, project)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(regs) != 2 {
		t.Fatalf("want 2, got %d", len(regs))
	}
	// Sorted by priority: gi(1) then acme(2).
	if regs[0].Name != "gi" || regs[1].Name != "acme" {
		t.Fatalf("order = %q,%q", regs[0].Name, regs[1].Name)
	}
}

func TestResolve_ProjectWinsPerName(t *testing.T) {
	global := []Registry{{Name: "acme", Type: TypeArtifactory, URL: "https://old", RepoKey: "k", Priority: 2}}
	project := []Registry{{Name: "acme", Type: TypeArtifactory, URL: "https://new", RepoKey: "k", Priority: 2}}
	regs, err := Resolve(BuiltinGI(""), global, project)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	acme, ok := Find(regs, "acme")
	if !ok || acme.URL != "https://new" {
		t.Fatalf("project should win: %+v (ok=%v)", acme, ok)
	}
}

func TestResolve_RetargetGIByName(t *testing.T) {
	project := []Registry{{Name: "gi", Type: TypeGenero, URL: "https://internal-gi.example"}}
	regs, err := Resolve(BuiltinGI(""), nil, project)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	gi, _ := Find(regs, "gi")
	if gi.URL != "https://internal-gi.example" {
		t.Fatalf("gi URL = %q", gi.URL)
	}
	if gi.Priority != 1 { // inherited default even though project omitted it
		t.Fatalf("gi priority = %d, want 1", gi.Priority)
	}
}

func TestResolve_DuplicatePriorityError(t *testing.T) {
	project := []Registry{{Name: "acme", Type: TypeArtifactory, URL: "https://a", RepoKey: "k", Priority: 1}}
	_, err := Resolve(BuiltinGI(""), nil, project) // collides with gi's priority 1
	if err == nil {
		t.Fatal("expected duplicate-priority error")
	}
}

func TestResolve_MissingRepoKeyError(t *testing.T) {
	project := []Registry{{Name: "acme", Type: TypeArtifactory, URL: "https://a", Priority: 2}}
	_, err := Resolve(BuiltinGI(""), nil, project)
	if err == nil {
		t.Fatal("expected missing-repoKey error")
	}
}

func TestResolve_UnknownTypeError(t *testing.T) {
	project := []Registry{{Name: "acme", Type: "npm", URL: "https://a", Priority: 2}}
	_, err := Resolve(BuiltinGI(""), nil, project)
	if err == nil {
		t.Fatal("expected unknown-type error")
	}
}

func TestResolve_UnknownAuthError(t *testing.T) {
	project := []Registry{{Name: "acme", Type: TypeArtifactory, URL: "https://a", RepoKey: "k", Priority: 2, Auth: "kerberos"}}
	_, err := Resolve(BuiltinGI(""), nil, project)
	if err == nil {
		t.Fatal("expected unknown-auth error")
	}
}

func TestLoadGlobal_MissingIsEmpty(t *testing.T) {
	regs, err := LoadGlobal(t.TempDir())
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}
	if regs != nil {
		t.Fatalf("want nil, got %+v", regs)
	}
}

func TestGlobalDefaultRegistry(t *testing.T) {
	// No file → empty, no error.
	if v, err := GlobalDefaultRegistry(t.TempDir()); err != nil || v != "" {
		t.Fatalf("missing file: got (%q, %v), want (\"\", nil)", v, err)
	}
	// File with defaultRegistry → returned.
	home := t.TempDir()
	body := `{"defaultRegistry":"acme","registries":[{"name":"acme","type":"artifactory","url":"https://a","repoKey":"k","priority":2}]}`
	if err := os.WriteFile(filepath.Join(home, GlobalFilename), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if v, err := GlobalDefaultRegistry(home); err != nil || v != "acme" {
		t.Fatalf("got (%q, %v), want (\"acme\", nil)", v, err)
	}
	// LoadGlobal still returns the registries from the same file.
	if regs, err := LoadGlobal(home); err != nil || len(regs) != 1 || regs[0].Name != "acme" {
		t.Fatalf("LoadGlobal: regs=%+v err=%v", regs, err)
	}
}

func TestLoadGlobal_BlankIsEmpty(t *testing.T) {
	for _, body := range []string{"", "   ", "\n\t \r\n"} {
		home := t.TempDir()
		if err := os.WriteFile(filepath.Join(home, GlobalFilename), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		regs, err := LoadGlobal(home)
		if err != nil {
			t.Fatalf("LoadGlobal(%q): %v", body, err)
		}
		if regs != nil {
			t.Fatalf("LoadGlobal(%q): want nil, got %+v", body, regs)
		}
	}
}

func TestLoadGlobal_ReadsFile(t *testing.T) {
	home := t.TempDir()
	body := `{"registries":[{"name":"acme","type":"artifactory","url":"https://a","repoKey":"k","priority":2}]}`
	if err := os.WriteFile(filepath.Join(home, GlobalFilename), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	regs, err := LoadGlobal(home)
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}
	if len(regs) != 1 || regs[0].Name != "acme" || regs[0].RepoKey != "k" {
		t.Fatalf("unexpected: %+v", regs)
	}
}

func TestLoadGlobal_RejectsUnknownField(t *testing.T) {
	home := t.TempDir()
	body := `{"registries":[{"name":"acme","type":"artifactory","url":"https://a","repoKey":"k","priority":2,"bogus":true}]}`
	if err := os.WriteFile(filepath.Join(home, GlobalFilename), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadGlobal(home); err == nil {
		t.Fatal("expected error on unknown field")
	}
}

func TestLoad_CascadeGlobalThenProject(t *testing.T) {
	home := t.TempDir()
	body := `{"registries":[{"name":"acme","type":"artifactory","url":"https://global","repoKey":"k","priority":2}]}`
	if err := os.WriteFile(filepath.Join(home, GlobalFilename), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	project := []Registry{{Name: "acme", Type: TypeArtifactory, URL: "https://project", RepoKey: "k", Priority: 2}}
	regs, err := Load(home, "", project)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	acme, _ := Find(regs, "acme")
	if acme.URL != "https://project" {
		t.Fatalf("project should override global: %q", acme.URL)
	}
}

func TestAdmits(t *testing.T) {
	r := Registry{Packages: []string{"acme-*", "internal-*"}}
	if !r.Admits("acme-utils") || !r.Admits("internal-x") {
		t.Fatal("should admit matching names")
	}
	if r.Admits("logft") {
		t.Fatal("should not admit non-matching name")
	}
	// No allow-list admits everything.
	if !(Registry{}).Admits("anything") {
		t.Fatal("empty allow-list should admit all")
	}
}
