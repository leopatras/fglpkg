package resolver_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/genero"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
	"github.com/4js-mikefolcher/fglpkg/internal/registry"
	"github.com/4js-mikefolcher/fglpkg/internal/resolver"
	"github.com/4js-mikefolcher/fglpkg/internal/semver"
)

// ─── Fake registry ────────────────────────────────────────────────────────────

// dbEntry is one version in the fake registry, with its Genero constraint.
type dbEntry struct {
	info             *registry.PackageInfo
	generoConstraint string // semver constraint on the Genero runtime
}

// packageDB maps package name → version string → dbEntry.
type packageDB map[string]map[string]dbEntry

func (db packageDB) versions(name string) ([]resolver.CandidateVersion, error) {
	pkg, ok := db[name]
	if !ok {
		return nil, errors.New("package not found: " + name)
	}
	out := make([]resolver.CandidateVersion, 0, len(pkg))
	for vs, entry := range pkg {
		out = append(out, resolver.CandidateVersion{
			Version:          semver.MustParse(vs),
			GeneroConstraint: entry.generoConstraint,
		})
	}
	return out, nil
}

func (db packageDB) info(name, version, _ string) (*registry.PackageInfo, error) {
	pkg, ok := db[name]
	if !ok {
		return nil, errors.New("package not found: " + name)
	}
	entry, ok := pkg[version]
	if !ok {
		return nil, errors.New("version not found: " + name + "@" + version)
	}
	return entry.info, nil
}

func (db packageDB) newResolver(gv genero.Version) *resolver.Resolver {
	return resolver.NewWithFetchers(gv, db.versions, db.info)
}

// ─── Builder helpers ──────────────────────────────────────────────────────────

// entry builds a dbEntry. generoConstraint="" means "any version".
func entry(generoConstraint string, info *registry.PackageInfo) dbEntry {
	return dbEntry{generoConstraint: generoConstraint, info: info}
}

func pkg(name, version string, fglDeps map[string]string, javaDeps ...manifest.JavaDependency) *registry.PackageInfo {
	return &registry.PackageInfo{
		Name:        name,
		Version:     version,
		DownloadURL: "https://example.com/" + name + "-" + version + ".zip",
		Checksum:    "deadbeef",
		FGLDeps:     fglDeps,
		JavaDeps:    javaDeps,
	}
}

func jar(groupID, artifactID, version string) manifest.JavaDependency {
	return manifest.JavaDependency{GroupID: groupID, ArtifactID: artifactID, Version: version}
}

var (
	genero401 = genero.MustParse("4.01.12")
	genero320 = genero.MustParse("3.20.05")
)

// recordingPinDeclarer captures the per-dependency registry pins the resolver
// discovers, to assert they are threaded from a package's FGLDepPins.
type recordingPinDeclarer struct{ pins map[string]string }

func (r *recordingPinDeclarer) DeclarePin(name, registry string) error {
	r.pins[name] = registry
	return nil
}

// collisionDB is a fetcher+pin-declarer that mimics the multi-provider routing
// layer: a "colliding" name returns an ErrCollision-wrapping error until a pin
// is declared for it, after which it resolves normally. Used to test that the
// resolver defers a collision and retries it once a pin arrives — independent
// of the order names are discovered in.
type collisionDB struct {
	db        packageDB
	colliding map[string]bool
	pins      map[string]string
}

func (c *collisionDB) versions(name string) ([]resolver.CandidateVersion, error) {
	if c.colliding[name] && c.pins[name] == "" {
		return nil, fmt.Errorf("%q collides: %w", name, resolver.ErrCollision)
	}
	return c.db.versions(name)
}
func (c *collisionDB) info(name, version, gm string) (*registry.PackageInfo, error) {
	return c.db.info(name, version, gm)
}
func (c *collisionDB) DeclarePin(name, registry string) error {
	c.pins[name] = registry
	return nil
}

// TestDeferredCollisionResolvedByPin: `widget` is a direct root dependency that
// collides, but its sibling `helper` declares widget→acme. Resolution must
// succeed regardless of whether widget or helper is dequeued first.
func TestDeferredCollisionResolvedByPin(t *testing.T) {
	helper := pkg("helper", "1.0.0", map[string]string{"widget": "*"})
	helper.FGLDepPins = map[string]string{"widget": "acme"}
	c := &collisionDB{
		db: packageDB{
			"helper": {"1.0.0": entry("", helper)},
			"widget": {"1.0.0": entry("", pkg("widget", "1.0.0", nil))},
		},
		colliding: map[string]bool{"widget": true},
		pins:      map[string]string{},
	}
	root := manifest.New("app", "1.0.0", "", "")
	root.AddFGLDependency("widget", "*")
	root.AddFGLDependency("helper", "*")

	r := resolver.NewWithFetchers(genero401, c.versions, c.info).WithPinDeclarer(c)
	plan, err := r.Resolve(root)
	if err != nil {
		t.Fatalf("expected deferred collision to resolve via declared pin, got: %v", err)
	}
	byName := planByName(plan)
	assertVersion(t, byName, "widget", "1.0.0")
	assertVersion(t, byName, "helper", "1.0.0")
}

// TestUnresolvedCollisionErrors: a colliding name that nothing ever pins must
// still fail, wrapping ErrCollision.
func TestUnresolvedCollisionErrors(t *testing.T) {
	c := &collisionDB{
		db:        packageDB{"widget": {"1.0.0": entry("", pkg("widget", "1.0.0", nil))}},
		colliding: map[string]bool{"widget": true},
		pins:      map[string]string{},
	}
	root := manifest.New("app", "1.0.0", "", "")
	root.AddFGLDependency("widget", "*")

	r := resolver.NewWithFetchers(genero401, c.versions, c.info).WithPinDeclarer(c)
	_, err := r.Resolve(root)
	if err == nil {
		t.Fatal("expected a collision error")
	}
	if !errors.Is(err, resolver.ErrCollision) {
		t.Fatalf("error should wrap ErrCollision, got: %v", err)
	}
}

// ─── Tests ────────────────────────────────────────────────────────────────────

// TestResolveThreadsDeclaredPin verifies that a registry pin declared in a
// resolved package's manifest (FGLDepPins) is handed to the PinDeclarer for its
// transitive dependency, so routing can honour the author's stated source.
func TestResolveThreadsDeclaredPin(t *testing.T) {
	a := pkg("a", "1.0.0", map[string]string{"b": "^1.0.0"})
	a.FGLDepPins = map[string]string{"b": "acme"}
	db := packageDB{
		"a": {"1.0.0": entry("", a)},
		"b": {"1.0.0": entry("", pkg("b", "1.0.0", nil))},
	}
	root := manifest.New("myapp", "1.0.0", "", "")
	root.AddFGLDependency("a", "^1.0.0")

	pd := &recordingPinDeclarer{pins: map[string]string{}}
	_, err := db.newResolver(genero401).WithPinDeclarer(pd).Resolve(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pd.pins["b"] != "acme" {
		t.Fatalf("declared pin not threaded to declarer: %+v", pd.pins)
	}
}

func TestNoDeps(t *testing.T) {
	db := packageDB{}
	plan, err := db.newResolver(genero401).Resolve(manifest.New("myapp", "1.0.0", "", ""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.Packages) != 0 {
		t.Errorf("expected 0 packages, got %d", len(plan.Packages))
	}
}

func TestDirectDeps(t *testing.T) {
	db := packageDB{
		"utils": {
			"1.0.0": entry("", pkg("utils", "1.0.0", nil)),
			"1.1.0": entry("", pkg("utils", "1.1.0", nil)),
			"1.2.0": entry("", pkg("utils", "1.2.0", nil)),
		},
		"dbtools": {
			"2.0.0": entry("", pkg("dbtools", "2.0.0", nil)),
			"2.1.0": entry("", pkg("dbtools", "2.1.0", nil)),
		},
	}

	root := manifest.New("myapp", "1.0.0", "", "")
	root.AddFGLDependency("utils", "^1.0.0")
	root.AddFGLDependency("dbtools", "^2.0.0")

	plan, err := db.newResolver(genero401).Resolve(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	byName := planByName(plan)
	assertVersion(t, byName, "utils", "1.2.0")
	assertVersion(t, byName, "dbtools", "2.1.0")
}

func TestTransitiveDeps(t *testing.T) {
	db := packageDB{
		"a": {"1.0.0": entry("", pkg("a", "1.0.0", map[string]string{"b": "^1.0.0"}))},
		"b": {"1.0.0": entry("", pkg("b", "1.0.0", map[string]string{"c": "^2.0.0"}))},
		"c": {
			"2.0.0": entry("", pkg("c", "2.0.0", nil)),
			"2.1.0": entry("", pkg("c", "2.1.0", nil)),
		},
	}

	root := manifest.New("myapp", "1.0.0", "", "")
	root.AddFGLDependency("a", "^1.0.0")

	plan, err := db.newResolver(genero401).Resolve(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.Packages) != 3 {
		t.Fatalf("expected 3 packages, got %d", len(plan.Packages))
	}
	byName := planByName(plan)
	assertVersion(t, byName, "a", "1.0.0")
	assertVersion(t, byName, "b", "1.0.0")
	assertVersion(t, byName, "c", "2.1.0")
}

func TestSharedDepCompatible(t *testing.T) {
	db := packageDB{
		"a": {"1.0.0": entry("", pkg("a", "1.0.0", map[string]string{"c": "^1.0.0"}))},
		"b": {"1.0.0": entry("", pkg("b", "1.0.0", map[string]string{"c": ">=1.1.0"}))},
		"c": {
			"1.0.0": entry("", pkg("c", "1.0.0", nil)),
			"1.1.0": entry("", pkg("c", "1.1.0", nil)),
			"1.2.0": entry("", pkg("c", "1.2.0", nil)),
		},
	}

	root := manifest.New("myapp", "1.0.0", "", "")
	root.AddFGLDependency("a", "^1.0.0")
	root.AddFGLDependency("b", "^1.0.0")

	plan, err := db.newResolver(genero401).Resolve(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.Packages) != 3 {
		t.Fatalf("expected 3 packages, got %d", len(plan.Packages))
	}
	assertVersion(t, planByName(plan), "c", "1.2.0")
}

func TestSharedDepConflict(t *testing.T) {
	db := packageDB{
		"a": {"1.0.0": entry("", pkg("a", "1.0.0", map[string]string{"c": "^1.0.0"}))},
		"b": {"1.0.0": entry("", pkg("b", "1.0.0", map[string]string{"c": "^2.0.0"}))},
		"c": {
			"1.0.0": entry("", pkg("c", "1.0.0", nil)),
			"2.0.0": entry("", pkg("c", "2.0.0", nil)),
		},
	}

	root := manifest.New("myapp", "1.0.0", "", "")
	root.AddFGLDependency("a", "^1.0.0")
	root.AddFGLDependency("b", "^1.0.0")

	_, err := db.newResolver(genero401).Resolve(root)
	if err == nil {
		t.Fatal("expected conflict error, got nil")
	}
	var cl *resolver.ConflictList
	if !errors.As(err, &cl) {
		t.Fatalf("expected *ConflictList, got %T: %v", err, err)
	}
	found := false
	for _, c := range cl.Conflicts {
		if c.Package == "c" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected conflict for 'c', got: %v", cl.Conflicts)
	}
}

// TestGeneroFilteringExcludesIncompatible: package has two versions; only the
// one compatible with the detected Genero version should be chosen.
func TestGeneroFilteringExcludesIncompatible(t *testing.T) {
	db := packageDB{
		"utils": {
			// 1.0.0 was compiled for Genero 3.x only
			"1.0.0": entry("^3.0.0", pkg("utils", "1.0.0", nil)),
			// 2.0.0 supports Genero 4.x
			"2.0.0": entry("^4.0.0", pkg("utils", "2.0.0", nil)),
		},
	}

	root := manifest.New("myapp", "1.0.0", "", "")
	root.AddFGLDependency("utils", "*")

	// Running Genero 4.01 — should pick 2.0.0, not 1.0.0
	plan, err := db.newResolver(genero401).Resolve(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertVersion(t, planByName(plan), "utils", "2.0.0")
}

// TestGeneroFilteringPicksHighestCompatible: multiple versions compatible with
// the detected Genero; highest should win.
func TestGeneroFilteringPicksHighestCompatible(t *testing.T) {
	db := packageDB{
		"utils": {
			"1.0.0": entry(">=3.0.0", pkg("utils", "1.0.0", nil)),
			"1.1.0": entry(">=3.0.0", pkg("utils", "1.1.0", nil)),
			"1.2.0": entry(">=4.0.0", pkg("utils", "1.2.0", nil)), // only 4.x+
		},
	}

	root := manifest.New("myapp", "1.0.0", "", "")
	root.AddFGLDependency("utils", "^1.0.0")

	// Genero 3.20: 1.2.0 is excluded, highest compatible is 1.1.0
	plan320, err := db.newResolver(genero320).Resolve(root)
	if err != nil {
		t.Fatalf("Genero 3.20: unexpected error: %v", err)
	}
	assertVersion(t, planByName(plan320), "utils", "1.1.0")

	// Genero 4.01: all versions pass, highest is 1.2.0
	plan401, err := db.newResolver(genero401).Resolve(root)
	if err != nil {
		t.Fatalf("Genero 4.01: unexpected error: %v", err)
	}
	assertVersion(t, planByName(plan401), "utils", "1.2.0")
}

// TestGeneroNoCompatibleVersion: no version of the package supports the
// installed Genero — should return a clear error, not a conflict.
func TestGeneroNoCompatibleVersion(t *testing.T) {
	db := packageDB{
		"legacy": {
			"1.0.0": entry("^3.0.0", pkg("legacy", "1.0.0", nil)),
			"1.1.0": entry("^3.0.0", pkg("legacy", "1.1.0", nil)),
		},
	}

	root := manifest.New("myapp", "1.0.0", "", "")
	root.AddFGLDependency("legacy", "^1.0.0")

	_, err := db.newResolver(genero401).Resolve(root)
	if err == nil {
		t.Fatal("expected error for Genero-incompatible package, got nil")
	}
}

// TestRootGeneroConstraintRejected: root manifest's own genero constraint
// doesn't match the detected runtime.
func TestRootGeneroConstraintRejected(t *testing.T) {
	db := packageDB{}
	root := manifest.New("myapp", "1.0.0", "", "")
	root.GeneroConstraint = "^3.0.0" // requires Genero 3.x

	_, err := db.newResolver(genero401).Resolve(root) // but we have 4.x
	if err == nil {
		t.Fatal("expected error for mismatched root genero constraint, got nil")
	}
}

func TestJARCollection(t *testing.T) {
	db := packageDB{
		"a": {"1.0.0": entry("", pkg("a", "1.0.0",
			map[string]string{"b": "^1.0.0"},
			jar("com.google.code.gson", "gson", "2.9.0")))},
		"b": {"1.0.0": entry("", pkg("b", "1.0.0", nil,
			jar("com.google.code.gson", "gson", "2.10.1"),
			jar("org.apache.commons", "commons-lang3", "3.12.0")))},
	}

	root := manifest.New("myapp", "1.0.0", "", "")
	root.AddFGLDependency("a", "^1.0.0")
	root.AddJavaDependency(jar("org.slf4j", "slf4j-api", "2.0.0"))

	plan, err := db.newResolver(genero401).Resolve(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	jarsByKey := make(map[string]manifest.JavaDependency)
	for _, j := range plan.JARs {
		jarsByKey[j.Key()] = j
	}

	gson := jarsByKey["com.google.code.gson:gson"]
	if gson.Version != "2.10.1" {
		t.Errorf("expected gson 2.10.1, got %s", gson.Version)
	}
	if _, ok := jarsByKey["org.apache.commons:commons-lang3"]; !ok {
		t.Error("expected commons-lang3")
	}
	if _, ok := jarsByKey["org.slf4j:slf4j-api"]; !ok {
		t.Error("expected slf4j-api")
	}
	if len(plan.JARs) != 3 {
		t.Errorf("expected 3 JARs, got %d", len(plan.JARs))
	}
}

func TestCycleSafety(t *testing.T) {
	db := packageDB{
		"a": {"1.0.0": entry("", pkg("a", "1.0.0", map[string]string{"b": "^1.0.0"}))},
		"b": {"1.0.0": entry("", pkg("b", "1.0.0", map[string]string{"a": "^1.0.0"}))},
	}

	root := manifest.New("myapp", "1.0.0", "", "")
	root.AddFGLDependency("a", "^1.0.0")

	done := make(chan struct{})
	go func() {
		db.newResolver(genero401).Resolve(root) //nolint:errcheck
		close(done)
	}()
	select {
	case <-done:
	case <-timeoutChan(2):
		t.Fatal("resolver did not terminate — possible infinite loop on cyclic graph")
	}
}

// ─── Scopes: dev + optional ──────────────────────────────────────────────────

// ResolveWithOptions(IncludeDev=false) must skip dev-only root deps and any
// transitive deps reached only through them.
func TestResolveProductionExcludesDevSubtree(t *testing.T) {
	db := packageDB{
		"prodlib":   {"1.0.0": entry("", pkg("prodlib", "1.0.0", map[string]string{"common": "^1.0.0"}))},
		"devhelper": {"1.0.0": entry("", pkg("devhelper", "1.0.0", map[string]string{"devonly": "^1.0.0"}))},
		"common":    {"1.0.0": entry("", pkg("common", "1.0.0", nil))},
		"devonly":   {"1.0.0": entry("", pkg("devonly", "1.0.0", nil))},
	}

	root := manifest.New("app", "1.0.0", "", "")
	root.AddFGLDependencyScoped("prodlib", "^1.0.0", manifest.ScopeProd)
	root.AddFGLDependencyScoped("devhelper", "^1.0.0", manifest.ScopeDev)

	plan, err := db.newResolver(genero401).ResolveWithOptions(root, resolver.ResolveOptions{IncludeDev: false, IncludeOptional: false})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	names := planByName(plan)
	if _, ok := names["prodlib"]; !ok {
		t.Error("prodlib should be installed in production mode")
	}
	if _, ok := names["common"]; !ok {
		t.Error("common (transitive of prodlib) should be installed")
	}
	if _, ok := names["devhelper"]; ok {
		t.Error("devhelper should be skipped in production mode")
	}
	if _, ok := names["devonly"]; ok {
		t.Error("devonly (transitive of devhelper) should be skipped in production mode")
	}
}

// The default Resolve path MUST include dev + optional at the root.
func TestResolveDefaultIncludesDevAndOptional(t *testing.T) {
	db := packageDB{
		"prodlib": {"1.0.0": entry("", pkg("prodlib", "1.0.0", nil))},
		"devlib":  {"1.0.0": entry("", pkg("devlib", "1.0.0", nil))},
		"opt":     {"1.0.0": entry("", pkg("opt", "1.0.0", nil))},
	}

	root := manifest.New("app", "1.0.0", "", "")
	root.AddFGLDependencyScoped("prodlib", "^1.0.0", manifest.ScopeProd)
	root.AddFGLDependencyScoped("devlib", "^1.0.0", manifest.ScopeDev)
	root.AddFGLDependencyScoped("opt", "^1.0.0", manifest.ScopeOptional)

	plan, err := db.newResolver(genero401).Resolve(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	names := planByName(plan)
	if names["prodlib"].Scope != manifest.ScopeProd {
		t.Errorf("prodlib scope: got %q", names["prodlib"].Scope)
	}
	if names["devlib"].Scope != manifest.ScopeDev {
		t.Errorf("devlib scope: got %q", names["devlib"].Scope)
	}
	if names["opt"].Scope != manifest.ScopeOptional {
		t.Errorf("opt scope: got %q", names["opt"].Scope)
	}
}

// When a package is reachable via both a prod and a dev path, scope must
// promote to prod so `--production` still installs it.
func TestResolveScopePromotionProdBeatsDev(t *testing.T) {
	db := packageDB{
		"prodroot": {"1.0.0": entry("", pkg("prodroot", "1.0.0", map[string]string{"shared": "^1.0.0"}))},
		"devroot":  {"1.0.0": entry("", pkg("devroot", "1.0.0", map[string]string{"shared": "^1.0.0"}))},
		"shared":   {"1.0.0": entry("", pkg("shared", "1.0.0", nil))},
	}
	root := manifest.New("app", "1.0.0", "", "")
	root.AddFGLDependencyScoped("prodroot", "^1.0.0", manifest.ScopeProd)
	root.AddFGLDependencyScoped("devroot", "^1.0.0", manifest.ScopeDev)

	plan, err := db.newResolver(genero401).Resolve(root)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	shared := planByName(plan)["shared"]
	if shared.Scope != manifest.ScopeProd {
		t.Errorf("expected shared to promote to prod, got %q", shared.Scope)
	}
}

// An optional root dep that cannot be resolved is skipped with a warning,
// not treated as a hard failure.
func TestResolveOptionalMissingPackageSkipped(t *testing.T) {
	db := packageDB{
		"prodlib": {"1.0.0": entry("", pkg("prodlib", "1.0.0", nil))},
		// "opt" is not in the registry
	}
	root := manifest.New("app", "1.0.0", "", "")
	root.AddFGLDependencyScoped("prodlib", "^1.0.0", manifest.ScopeProd)
	root.AddFGLDependencyScoped("opt", "^1.0.0", manifest.ScopeOptional)

	plan, err := db.newResolver(genero401).Resolve(root)
	if err != nil {
		t.Fatalf("optional failure should not error, got: %v", err)
	}
	if _, ok := planByName(plan)["opt"]; ok {
		t.Error("opt should have been dropped")
	}
	if len(plan.OptionalSkipped) == 0 {
		t.Error("expected OptionalSkipped to record the skip")
	}
}

// An optional dep with no Genero-compatible version is skipped, not errored.
func TestResolveOptionalGeneroIncompatibleSkipped(t *testing.T) {
	db := packageDB{
		"legacy": {"1.0.0": entry("^3.0.0", pkg("legacy", "1.0.0", nil))},
	}
	root := manifest.New("app", "1.0.0", "", "")
	root.AddFGLDependencyScoped("legacy", "^1.0.0", manifest.ScopeOptional)

	plan, err := db.newResolver(genero401).Resolve(root)
	if err != nil {
		t.Fatalf("optional incompat should not error, got: %v", err)
	}
	if _, ok := planByName(plan)["legacy"]; ok {
		t.Error("legacy should have been dropped")
	}
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func planByName(plan *resolver.Plan) map[string]resolver.ResolvedPackage {
	m := make(map[string]resolver.ResolvedPackage, len(plan.Packages))
	for _, p := range plan.Packages {
		m[p.Name] = p
	}
	return m
}

func assertVersion(t *testing.T, byName map[string]resolver.ResolvedPackage, name, want string) {
	t.Helper()
	p, ok := byName[name]
	if !ok {
		t.Errorf("package %q not found in plan", name)
		return
	}
	if p.Version.String() != want {
		t.Errorf("package %q: got %s, want %s", name, p.Version, want)
	}
}

func timeoutChan(seconds int) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		n := seconds * 1_000_000_000
		for i := 0; i < n; i++ {
		}
		close(ch)
	}()
	return ch
}
