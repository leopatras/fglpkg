package lockfile_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/genero"
	"github.com/4js-mikefolcher/fglpkg/internal/lockfile"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
	"github.com/4js-mikefolcher/fglpkg/internal/resolver"
	"github.com/4js-mikefolcher/fglpkg/internal/semver"
)

// ─── Helpers ─────────────────────────────────────────────────────────────────

func makePlan() *resolver.Plan {
	return &resolver.Plan{
		GeneroVersion: genero.MustParse("4.01.12"),
		Packages: []resolver.ResolvedPackage{
			{
				Name:        "utils",
				Version:     semver.MustParse("1.2.3"),
				DownloadURL: "https://registry.fglpkg.dev/utils-1.2.3.zip",
				Checksum:    "aaaa1111",
				RequiredBy:  []string{"<root>"},
			},
			{
				Name:        "dbtools",
				Version:     semver.MustParse("2.1.0"),
				DownloadURL: "https://registry.fglpkg.dev/dbtools-2.1.0.zip",
				Checksum:    "bbbb2222",
				RequiredBy:  []string{"<root>", "utils"},
			},
		},
		JARs: []manifest.JavaDependency{
			{GroupID: "com.google.code.gson", ArtifactID: "gson", Version: "2.10.1"},
			{GroupID: "org.slf4j", ArtifactID: "slf4j-api", Version: "2.0.0"},
		},
	}
}

func makeRoot() *manifest.Manifest {
	return manifest.New("myapp", "1.0.0", "Test application", "Alice")
}

// ─── FromPlan ────────────────────────────────────────────────────────────────

func TestFromPlanPackageCount(t *testing.T) {
	lf := lockfile.FromPlan(makePlan(), makeRoot())
	if len(lf.Packages) != 2 {
		t.Errorf("expected 2 packages, got %d", len(lf.Packages))
	}
	if len(lf.JARs) != 2 {
		t.Errorf("expected 2 JARs, got %d", len(lf.JARs))
	}
}

func TestFromPlanPackagesSortedByName(t *testing.T) {
	lf := lockfile.FromPlan(makePlan(), makeRoot())
	for i := 1; i < len(lf.Packages); i++ {
		if lf.Packages[i].Name < lf.Packages[i-1].Name {
			t.Errorf("packages not sorted: %s before %s",
				lf.Packages[i-1].Name, lf.Packages[i].Name)
		}
	}
}

func TestFromPlanJARsSortedByKey(t *testing.T) {
	lf := lockfile.FromPlan(makePlan(), makeRoot())
	for i := 1; i < len(lf.JARs); i++ {
		if lf.JARs[i].Key < lf.JARs[i-1].Key {
			t.Errorf("JARs not sorted: %s before %s",
				lf.JARs[i-1].Key, lf.JARs[i].Key)
		}
	}
}

func TestFromPlanPreservesChecksums(t *testing.T) {
	lf := lockfile.FromPlan(makePlan(), makeRoot())
	byName := make(map[string]lockfile.LockedPackage)
	for _, p := range lf.Packages {
		byName[p.Name] = p
	}
	if byName["utils"].Checksum != "aaaa1111" {
		t.Errorf("utils checksum = %q, want %q", byName["utils"].Checksum, "aaaa1111")
	}
}

func TestFromPlanGeneroVersion(t *testing.T) {
	lf := lockfile.FromPlan(makePlan(), makeRoot())
	if lf.GeneroVersion != "4.01.12" {
		t.Errorf("GeneroVersion = %q, want %q", lf.GeneroVersion, "4.01.12")
	}
}

func TestFromPlanRootManifest(t *testing.T) {
	root := makeRoot()
	lf := lockfile.FromPlan(makePlan(), root)
	if lf.RootManifest.Name != root.Name {
		t.Errorf("RootManifest.Name = %q, want %q", lf.RootManifest.Name, root.Name)
	}
	if lf.RootManifest.Version != root.Version {
		t.Errorf("RootManifest.Version = %q, want %q", lf.RootManifest.Version, root.Version)
	}
}

func TestFromPlanJARDownloadURL(t *testing.T) {
	lf := lockfile.FromPlan(makePlan(), makeRoot())
	for _, jar := range lf.JARs {
		if jar.DownloadURL == "" {
			t.Errorf("JAR %s has empty DownloadURL", jar.Key)
		}
	}
	// gson URL should follow Maven Central pattern
	for _, jar := range lf.JARs {
		if jar.ArtifactID == "gson" {
			want := "https://repo1.maven.org/maven2/com/google/code/gson/gson/2.10.1/gson-2.10.1.jar"
			if jar.DownloadURL != want {
				t.Errorf("gson DownloadURL = %q, want %q", jar.DownloadURL, want)
			}
		}
	}
}

// ─── Save / Load round-trip ──────────────────────────────────────────────────

func TestSaveAndLoad(t *testing.T) {
	dir := t.TempDir()
	original := lockfile.FromPlan(makePlan(), makeRoot())

	if err := original.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// File must exist.
	if _, err := os.Stat(filepath.Join(dir, lockfile.Filename)); err != nil {
		t.Fatalf("lock file not written: %v", err)
	}

	loaded, err := lockfile.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if loaded.GeneroVersion != original.GeneroVersion {
		t.Errorf("GeneroVersion: got %q, want %q", loaded.GeneroVersion, original.GeneroVersion)
	}
	if len(loaded.Packages) != len(original.Packages) {
		t.Errorf("Packages len: got %d, want %d", len(loaded.Packages), len(original.Packages))
	}
	if len(loaded.JARs) != len(original.JARs) {
		t.Errorf("JARs len: got %d, want %d", len(loaded.JARs), len(original.JARs))
	}
}

func TestSaveProducesValidJSON(t *testing.T) {
	dir := t.TempDir()
	lf := lockfile.FromPlan(makePlan(), makeRoot())
	if err := lf.Save(dir); err != nil {
		t.Fatalf("Save: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, lockfile.Filename))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Errorf("saved file is not valid JSON: %v", err)
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := lockfile.Load(t.TempDir())
	if err == nil {
		t.Error("expected error loading missing lock file, got nil")
	}
}

func TestExists(t *testing.T) {
	dir := t.TempDir()
	if lockfile.Exists(dir) {
		t.Error("Exists() should be false before Save()")
	}
	lf := lockfile.FromPlan(makePlan(), makeRoot())
	lf.Save(dir) //nolint:errcheck
	if !lockfile.Exists(dir) {
		t.Error("Exists() should be true after Save()")
	}
}

// ─── Validate ────────────────────────────────────────────────────────────────

func TestValidateClean(t *testing.T) {
	dir := t.TempDir()
	root := makeRoot()
	lf := lockfile.FromPlan(makePlan(), root)
	lf.Save(dir) //nolint:errcheck

	result := lf.Validate(root, "4.01.12", "", "")
	if !result.IsClean() {
		t.Errorf("expected clean result, got: schema=%v genero=%v manifest=%v missing=%v",
			result.SchemaError, result.GeneroMismatch,
			result.ManifestMismatch, result.MissingPackages)
	}
	if result.NeedsResolve() {
		t.Error("clean lock should not need re-resolve")
	}
}

func TestValidateGeneroMismatch(t *testing.T) {
	root := makeRoot()
	lf := lockfile.FromPlan(makePlan(), root) // locked at 4.01.12

	result := lf.Validate(root, "3.20.05", "", "") // now running 3.20
	if result.GeneroMismatch == nil {
		t.Fatal("expected GeneroMismatch, got nil")
	}
	if result.GeneroMismatch.Locked != "4.01.12" {
		t.Errorf("Locked = %q, want %q", result.GeneroMismatch.Locked, "4.01.12")
	}
	if result.GeneroMismatch.Current != "3.20.05" {
		t.Errorf("Current = %q, want %q", result.GeneroMismatch.Current, "3.20.05")
	}
	// Genero mismatch alone doesn't require re-resolution.
	if result.NeedsResolve() {
		t.Error("genero mismatch alone should not force re-resolve")
	}
}

func TestValidateManifestNameMismatch(t *testing.T) {
	root := makeRoot()
	lf := lockfile.FromPlan(makePlan(), root)

	changedRoot := makeRoot()
	changedRoot.Name = "otherapp"

	result := lf.Validate(changedRoot, "4.01.12", "", "")
	if result.ManifestMismatch == nil {
		t.Fatal("expected ManifestMismatch, got nil")
	}
	if !result.NeedsResolve() {
		t.Error("manifest mismatch should require re-resolve")
	}
}

func TestValidateManifestVersionMismatch(t *testing.T) {
	root := makeRoot()
	lf := lockfile.FromPlan(makePlan(), root)

	changedRoot := makeRoot()
	changedRoot.Version = "2.0.0"

	result := lf.Validate(changedRoot, "4.01.12", "", "")
	if result.ManifestMismatch == nil {
		t.Fatal("expected ManifestMismatch, got nil")
	}
	if !result.NeedsResolve() {
		t.Error("manifest version mismatch should require re-resolve")
	}
}

func TestValidateMissingPackages(t *testing.T) {
	dir := t.TempDir()
	root := makeRoot()
	lf := lockfile.FromPlan(makePlan(), root)

	// packagesDir exists but is empty — all packages are "missing"
	result := lf.Validate(root, "4.01.12", dir, "")
	if len(result.MissingPackages) != 2 {
		t.Errorf("expected 2 missing packages, got %d: %v",
			len(result.MissingPackages), result.MissingPackages)
	}
}

func TestValidatePresentPackages(t *testing.T) {
	dir := t.TempDir()
	root := makeRoot()
	lf := lockfile.FromPlan(makePlan(), root)

	// Create stub package directories to simulate a successful install.
	for _, pkg := range lf.Packages {
		os.MkdirAll(filepath.Join(dir, pkg.Name), 0755) //nolint:errcheck
	}

	result := lf.Validate(root, "4.01.12", dir, "")
	if len(result.MissingPackages) != 0 {
		t.Errorf("expected no missing packages, got: %v", result.MissingPackages)
	}
}

func TestValidateSchemaVersionMismatch(t *testing.T) {
	root := makeRoot()
	lf := lockfile.FromPlan(makePlan(), root)
	lf.Version = 99 // future/unknown schema

	result := lf.Validate(root, "4.01.12", "", "")
	if result.SchemaError == nil {
		t.Fatal("expected SchemaError, got nil")
	}
	if !result.NeedsResolve() {
		t.Error("schema error should require re-resolve")
	}
}

// ─── ToInstallList ────────────────────────────────────────────────────────────

func TestToInstallList(t *testing.T) {
	lf := lockfile.FromPlan(makePlan(), makeRoot())
	pkgs, jars, wcs := lf.ToInstallList()

	if len(pkgs) != 2 {
		t.Errorf("expected 2 packages, got %d", len(pkgs))
	}
	if len(jars) != 2 {
		t.Errorf("expected 2 JARs, got %d", len(jars))
	}
	if len(wcs) != 0 {
		t.Errorf("expected 0 webcomponents in BDL-only plan, got %d", len(wcs))
	}
}

// ─── Scopes ──────────────────────────────────────────────────────────────────

// Scope is written through from Plan to LockedPackage/LockedJAR, and prod
// entries omit the field so existing lock files remain backwards-compatible.
func TestFromPlanCarriesScope(t *testing.T) {
	plan := &resolver.Plan{
		GeneroVersion: genero.MustParse("4.01.12"),
		Packages: []resolver.ResolvedPackage{
			{Name: "a", Version: semver.MustParse("1.0.0"), Scope: manifest.ScopeProd},
			{Name: "b", Version: semver.MustParse("1.0.0"), Scope: manifest.ScopeDev},
			{Name: "c", Version: semver.MustParse("1.0.0"), Scope: manifest.ScopeOptional},
		},
		JARs: []manifest.JavaDependency{
			{GroupID: "g", ArtifactID: "prod-jar", Version: "1"},
			{GroupID: "g", ArtifactID: "dev-jar", Version: "1"},
		},
		JARScopes: map[string]manifest.Scope{
			"g:prod-jar": manifest.ScopeProd,
			"g:dev-jar":  manifest.ScopeDev,
		},
	}
	lf := lockfile.FromPlan(plan, makeRoot())
	want := map[string]string{"a": "", "b": "dev", "c": "optional"}
	for _, p := range lf.Packages {
		if got := want[p.Name]; p.Scope != got {
			t.Errorf("package %s scope: got %q want %q", p.Name, p.Scope, got)
		}
	}
	for _, j := range lf.JARs {
		switch j.ArtifactID {
		case "prod-jar":
			if j.Scope != "" {
				t.Errorf("prod-jar: expected empty scope, got %q", j.Scope)
			}
		case "dev-jar":
			if j.Scope != "dev" {
				t.Errorf("dev-jar: expected dev, got %q", j.Scope)
			}
		}
	}
}

// FilterForProduction drops dev-scoped entries and keeps prod + optional.
func TestFilterForProduction(t *testing.T) {
	plan := &resolver.Plan{
		GeneroVersion: genero.MustParse("4.01.12"),
		Packages: []resolver.ResolvedPackage{
			{Name: "a", Version: semver.MustParse("1.0.0"), Scope: manifest.ScopeProd},
			{Name: "b", Version: semver.MustParse("1.0.0"), Scope: manifest.ScopeDev},
			{Name: "c", Version: semver.MustParse("1.0.0"), Scope: manifest.ScopeOptional},
		},
		JARs: []manifest.JavaDependency{
			{GroupID: "g", ArtifactID: "j1", Version: "1"},
			{GroupID: "g", ArtifactID: "j2", Version: "1"},
		},
		JARScopes: map[string]manifest.Scope{
			"g:j1": manifest.ScopeProd,
			"g:j2": manifest.ScopeDev,
		},
	}
	lf := lockfile.FromPlan(plan, makeRoot())
	pkgs, jars, _ := lf.FilterForProduction()

	if len(pkgs) != 2 {
		t.Errorf("expected 2 packages (prod+optional), got %d", len(pkgs))
	}
	for _, p := range pkgs {
		if p.Scope == "dev" {
			t.Errorf("dev package %q leaked into production filter", p.Name)
		}
	}
	if len(jars) != 1 {
		t.Errorf("expected 1 JAR (prod only), got %d", len(jars))
	}
}
