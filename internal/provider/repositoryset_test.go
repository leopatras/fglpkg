package provider

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/config"
	"github.com/4js-mikefolcher/fglpkg/internal/registry"
	"github.com/4js-mikefolcher/fglpkg/internal/resolver"
	"github.com/4js-mikefolcher/fglpkg/internal/semver"
)

// fakeProvider is an in-memory Provider for routing tests.
type fakeProvider struct {
	name     string
	versions map[string][]string // package name → version strings ("" absent)
	authErr  bool
}

func (f *fakeProvider) Name() string { return f.name }

func (f *fakeProvider) FetchVersions(name string) ([]resolver.CandidateVersion, error) {
	if f.authErr {
		return nil, errors.New("authentication failed (401)")
	}
	vs, ok := f.versions[name]
	if !ok {
		return nil, ErrNotFound
	}
	out := make([]resolver.CandidateVersion, 0, len(vs))
	for _, s := range vs {
		v, _ := semver.Parse(s)
		out = append(out, resolver.CandidateVersion{Version: v})
	}
	return out, nil
}

func (f *fakeProvider) FetchInfo(name, version, generoMajor string) (*registry.PackageInfo, error) {
	if _, ok := f.versions[name]; !ok {
		return nil, ErrNotFound
	}
	return &registry.PackageInfo{Name: name, Version: version, Source: f.name}, nil
}

func (f *fakeProvider) Search(term string) ([]registry.SearchResult, error) { return nil, nil }

func descriptors() []config.Registry {
	return []config.Registry{
		{Name: "gi", Type: config.TypeGenero, URL: "https://gi", Priority: 1},
		{Name: "acme", Type: config.TypeArtifactory, URL: "https://a", RepoKey: "k", Priority: 2},
	}
}

func TestRoute_SingleHitResolves(t *testing.T) {
	gi := &fakeProvider{name: "gi", versions: map[string][]string{"logft": {"2.0.0"}}}
	acme := &fakeProvider{name: "acme", versions: map[string][]string{"acme-utils": {"1.0.0"}}}
	rs := NewRepositorySet([]Provider{gi, acme}, descriptors(), nil)

	info, err := rs.Info("logft", "2.0.0", "")
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.Source != "gi" {
		t.Fatalf("source = %q, want gi", info.Source)
	}
}

func TestRoute_NotFound(t *testing.T) {
	gi := &fakeProvider{name: "gi", versions: map[string][]string{}}
	acme := &fakeProvider{name: "acme", versions: map[string][]string{}}
	rs := NewRepositorySet([]Provider{gi, acme}, descriptors(), nil)
	_, err := rs.Versions("nope")
	if !errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestVersionsFrom_HonoursRecordedSource(t *testing.T) {
	// "utils" exists in BOTH repos — a normal route() would collide. VersionsFrom
	// pins to the recorded source and returns just that repo's versions.
	gi := &fakeProvider{name: "gi", versions: map[string][]string{"utils": {"1.3.0"}}}
	acme := &fakeProvider{name: "acme", versions: map[string][]string{"utils": {"0.9.0"}}}
	rs := NewRepositorySet([]Provider{gi, acme}, descriptors(), nil)

	vs, err := rs.VersionsFrom("acme", "utils")
	if err != nil {
		t.Fatalf("VersionsFrom: %v", err)
	}
	if len(vs) != 1 || vs[0].Version.String() != "0.9.0" {
		t.Fatalf("want [0.9.0] from acme, got %+v", vs)
	}

	// Empty source name resolves to the built-in GI registry.
	vs, err = rs.VersionsFrom("", "utils")
	if err != nil || len(vs) != 1 || vs[0].Version.String() != "1.3.0" {
		t.Fatalf("empty source should map to gi: %+v err=%v", vs, err)
	}

	// An unconfigured source is a clear error.
	if _, err := rs.VersionsFrom("gone", "utils"); err == nil {
		t.Fatal("expected error for unconfigured registry")
	}
}

func TestRoute_CollisionIsHardError(t *testing.T) {
	gi := &fakeProvider{name: "gi", versions: map[string][]string{"utils": {"1.2.0", "1.3.0"}}}
	acme := &fakeProvider{name: "acme", versions: map[string][]string{"utils": {"0.9.0"}}}
	rs := NewRepositorySet([]Provider{gi, acme}, descriptors(), nil)

	_, err := rs.Versions("utils")
	if err == nil {
		t.Fatal("expected a collision error")
	}
	if errors.Is(err, registry.ErrNotFound) {
		t.Fatalf("collision must not be not-found: %v", err)
	}
	msg := err.Error()
	if !strings.Contains(msg, "more than one repository") || !strings.Contains(msg, "gi") || !strings.Contains(msg, "acme") {
		t.Fatalf("collision message = %q", msg)
	}
}

func TestRoute_PinResolvesToOne(t *testing.T) {
	gi := &fakeProvider{name: "gi", versions: map[string][]string{"utils": {"1.2.0"}}}
	acme := &fakeProvider{name: "acme", versions: map[string][]string{"utils": {"0.9.0"}}}
	rs := NewRepositorySet([]Provider{gi, acme}, descriptors(), map[string]string{"utils": "acme"})

	info, err := rs.Info("utils", "0.9.0", "")
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.Source != "acme" {
		t.Fatalf("pin ignored: source = %q", info.Source)
	}
}

func TestDeclarePin_HonouredOverCollision(t *testing.T) {
	// qrcode exists in BOTH repos — normally a collision — but a depending
	// package declared it comes from acme, so it must resolve from acme.
	gi := &fakeProvider{name: "gi", versions: map[string][]string{"qrcode": {"1.0.0"}}}
	acme := &fakeProvider{name: "acme", versions: map[string][]string{"qrcode": {"0.2.0"}}}
	rs := NewRepositorySet([]Provider{gi, acme}, descriptors(), nil)

	if err := rs.DeclarePin("qrcode", "acme"); err != nil {
		t.Fatalf("DeclarePin: %v", err)
	}
	info, err := rs.Info("qrcode", "0.2.0", "")
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.Source != "acme" {
		t.Fatalf("declared pin ignored: source = %q, want acme", info.Source)
	}
}

// captureStderr redirects os.Stderr for the duration of fn and returns what was
// written, so we can assert on best-effort warnings.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	orig := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = orig }()
	fn()
	w.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("copy: %v", err)
	}
	return buf.String()
}

func TestDeclarePin_TransitivePinWarns(t *testing.T) {
	// A pin declared by a depending package (not the consumer's root pin) is an
	// author-steering event and must warn (spec ISSUE-D, decision: warn).
	gi := &fakeProvider{name: "gi", versions: map[string][]string{"qrcode": {"1.0.0"}}}
	acme := &fakeProvider{name: "acme", versions: map[string][]string{"qrcode": {"0.2.0"}}}
	rs := NewRepositorySet([]Provider{gi, acme}, descriptors(), nil)

	out := captureStderr(t, func() {
		if err := rs.DeclarePin("qrcode", "acme"); err != nil {
			t.Fatalf("DeclarePin: %v", err)
		}
	})
	if !strings.Contains(out, "qrcode") || !strings.Contains(out, "acme") || !strings.Contains(out, "warning") {
		t.Fatalf("expected a warning naming the package and registry, got %q", out)
	}

	// A repeat of the same pin is idempotent and must NOT warn again (noise).
	out2 := captureStderr(t, func() {
		if err := rs.DeclarePin("qrcode", "acme"); err != nil {
			t.Fatalf("idempotent DeclarePin: %v", err)
		}
	})
	if out2 != "" {
		t.Fatalf("repeat pin should not re-warn, got %q", out2)
	}
}

func TestDeclarePin_RootPinDoesNotWarn(t *testing.T) {
	// The consumer's own root pin wins silently — no warning.
	gi := &fakeProvider{name: "gi", versions: map[string][]string{"qrcode": {"1.0.0"}}}
	acme := &fakeProvider{name: "acme", versions: map[string][]string{"qrcode": {"0.2.0"}}}
	rs := NewRepositorySet([]Provider{gi, acme}, descriptors(), map[string]string{"qrcode": "gi"})

	out := captureStderr(t, func() {
		if err := rs.DeclarePin("qrcode", "acme"); err != nil {
			t.Fatalf("DeclarePin: %v", err)
		}
	})
	if out != "" {
		t.Fatalf("root pin should suppress the warning, got %q", out)
	}
}

func TestDeclarePin_RootPinWins(t *testing.T) {
	// The consumer's explicit root pin (gi) overrides a package's declared pin (acme).
	gi := &fakeProvider{name: "gi", versions: map[string][]string{"qrcode": {"1.0.0"}}}
	acme := &fakeProvider{name: "acme", versions: map[string][]string{"qrcode": {"0.2.0"}}}
	rs := NewRepositorySet([]Provider{gi, acme}, descriptors(), map[string]string{"qrcode": "gi"})

	if err := rs.DeclarePin("qrcode", "acme"); err != nil {
		t.Fatalf("DeclarePin should defer to root pin, got %v", err)
	}
	info, err := rs.Info("qrcode", "1.0.0", "")
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.Source != "gi" {
		t.Fatalf("root pin lost: source = %q, want gi", info.Source)
	}
}

func TestDeclarePin_ConflictErrors(t *testing.T) {
	gi := &fakeProvider{name: "gi", versions: map[string][]string{"qrcode": {"1.0.0"}}}
	acme := &fakeProvider{name: "acme", versions: map[string][]string{"qrcode": {"0.2.0"}}}
	rs := NewRepositorySet([]Provider{gi, acme}, descriptors(), nil)

	if err := rs.DeclarePin("qrcode", "acme"); err != nil {
		t.Fatalf("first DeclarePin: %v", err)
	}
	// Same value is idempotent.
	if err := rs.DeclarePin("qrcode", "acme"); err != nil {
		t.Fatalf("idempotent DeclarePin: %v", err)
	}
	// A conflicting declaration is a hard error.
	err := rs.DeclarePin("qrcode", "gi")
	if err == nil || !strings.Contains(err.Error(), "different repositories") {
		t.Fatalf("want conflict error, got %v", err)
	}
}

func TestDeclarePin_UnknownRegistryErrorLists(t *testing.T) {
	gi := &fakeProvider{name: "gi", versions: map[string][]string{"qrcode": {"1.0.0"}}}
	acme := &fakeProvider{name: "acme", versions: map[string][]string{"qrcode": {"0.2.0"}}}
	rs := NewRepositorySet([]Provider{gi, acme}, descriptors(), nil)

	if err := rs.DeclarePin("qrcode", "ghost"); err != nil {
		t.Fatalf("DeclarePin stores the pin regardless: %v", err)
	}
	_, err := rs.Versions("qrcode")
	if err == nil {
		t.Fatal("expected unknown-registry error")
	}
	msg := err.Error()
	// Lists configured registries and gives actionable advice.
	if !strings.Contains(msg, "not configured") || !strings.Contains(msg, "gi, acme") {
		t.Fatalf("error should list configured registries: %q", msg)
	}
}

func TestRoute_PinToUnknownRegistryErrors(t *testing.T) {
	gi := &fakeProvider{name: "gi", versions: map[string][]string{"utils": {"1.2.0"}}}
	rs := NewRepositorySet([]Provider{gi}, descriptors(), map[string]string{"utils": "ghost"})
	_, err := rs.Versions("utils")
	if err == nil || !strings.Contains(err.Error(), "not configured") {
		t.Fatalf("want not-configured error, got %v", err)
	}
}

func TestRoute_AuthErrorAbortsNotDropped(t *testing.T) {
	// acme has the name but returns an auth error: must abort, NOT fall through
	// to gi and silently resolve (that would be a mis-route).
	gi := &fakeProvider{name: "gi", versions: map[string][]string{"utils": {"1.2.0"}}}
	acme := &fakeProvider{name: "acme", authErr: true}
	rs := NewRepositorySet([]Provider{gi, acme}, descriptors(), nil)

	_, err := rs.Versions("utils")
	if err == nil || !strings.Contains(err.Error(), "authentication failed") {
		t.Fatalf("want auth error to abort routing, got %v", err)
	}
}

func TestRoute_PackagesAllowListPrunes(t *testing.T) {
	// acme owns only acme-*; a query for "utils" must not even consult acme,
	// so a name present in both does NOT collide.
	descs := []config.Registry{
		{Name: "gi", Type: config.TypeGenero, URL: "https://gi", Priority: 1},
		{Name: "acme", Type: config.TypeArtifactory, URL: "https://a", RepoKey: "k", Priority: 2, Packages: []string{"acme-*"}},
	}
	gi := &fakeProvider{name: "gi", versions: map[string][]string{"utils": {"1.0.0"}}}
	acme := &fakeProvider{name: "acme", versions: map[string][]string{"utils": {"9.9.9"}, "acme-x": {"1.0.0"}}}
	rs := NewRepositorySet([]Provider{gi, acme}, descs, nil)

	info, err := rs.Info("utils", "1.0.0", "")
	if err != nil {
		t.Fatalf("utils should resolve from gi only: %v", err)
	}
	if info.Source != "gi" {
		t.Fatalf("source = %q, want gi", info.Source)
	}
	// acme-x still routes to acme.
	info2, err := rs.Info("acme-x", "1.0.0", "")
	if err != nil || info2.Source != "acme" {
		t.Fatalf("acme-x should route to acme: %+v %v", info2, err)
	}
}

func TestRoute_RestrictToOneRegistry(t *testing.T) {
	gi := &fakeProvider{name: "gi", versions: map[string][]string{"utils": {"1.2.0"}}}
	acme := &fakeProvider{name: "acme", versions: map[string][]string{"utils": {"0.9.0"}}}
	rs := NewRepositorySet([]Provider{gi, acme}, descriptors(), nil)
	rs.Restrict("acme")

	info, err := rs.Info("utils", "0.9.0", "")
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if info.Source != "acme" {
		t.Fatalf("restrict ignored: source = %q", info.Source)
	}
}
