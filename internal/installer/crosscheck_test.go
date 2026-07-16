package installer

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/4js-mikefolcher/fglpkg/internal/lockfile"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

func jd(group, artifact, version string) manifest.JavaDependency {
	return manifest.JavaDependency{GroupID: group, ArtifactID: artifact, Version: version}
}

// ─── classify ───────────────────────────────────────────────────────────────

func TestClassify(t *testing.T) {
	tests := []struct {
		name     string
		declared []declaredDep
		install  map[string]manifest.JavaDependency
		want     []divergence
	}{
		{
			name:     "both empty",
			declared: nil,
			install:  map[string]manifest.JavaDependency{},
			want:     nil,
		},
		{
			name:     "exact match — no divergence",
			declared: []declaredDep{{pkg: "p", dep: jd("g", "a", "1.0.0")}},
			install:  map[string]manifest.JavaDependency{"g:a": jd("g", "a", "1.0.0")},
			want:     nil,
		},
		{
			name:     "missing — declared but not installed (poiapi case)",
			declared: []declaredDep{{pkg: "poiapi", dep: jd("org.apache.poi", "poi", "5.3.0")}},
			install:  map[string]manifest.JavaDependency{},
			want: []divergence{
				{class: classMissing, pkg: "poiapi", dep: jd("org.apache.poi", "poi", "5.3.0")},
			},
		},
		{
			name:     "extra — installed but declared by no manifest",
			declared: nil,
			install:  map[string]manifest.JavaDependency{"g:a": jd("g", "a", "2.0.0")},
			want: []divergence{
				{class: classExtra, dep: jd("g", "a", "2.0.0")},
			},
		},
		{
			name:     "version-mismatch — install version is authoritative",
			declared: []declaredDep{{pkg: "p", dep: jd("g", "a", "2.0.0")}},
			install:  map[string]manifest.JavaDependency{"g:a": jd("g", "a", "1.0.0")},
			want: []divergence{
				{class: classVersionMismatch, pkg: "p", dep: jd("g", "a", "2.0.0"), installVersion: "1.0.0"},
			},
		},
		{
			name: "root-fold suppresses extra for a consumer's own JAR",
			declared: []declaredDep{
				{pkg: rootPkgLabel, dep: jd("g", "a", "1.0.0")},
			},
			install: map[string]manifest.JavaDependency{"g:a": jd("g", "a", "1.0.0")},
			want:    nil,
		},
		{
			name: "higher version wins across duplicate declarations",
			declared: []declaredDep{
				{pkg: "pA", dep: jd("g", "a", "5.2.0")},
				{pkg: "pB", dep: jd("g", "a", "5.3.0")},
			},
			install: map[string]manifest.JavaDependency{},
			want: []divergence{
				{class: classMissing, pkg: "pB", dep: jd("g", "a", "5.3.0")},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classify(tt.declared, tt.install)
			if len(got) != len(tt.want) {
				t.Fatalf("classify() returned %d divergences, want %d: %+v", len(got), len(tt.want), got)
			}
			for i := range got {
				w := tt.want[i]
				if got[i].class != w.class || got[i].pkg != w.pkg ||
					got[i].dep.Key() != w.dep.Key() || got[i].dep.Version != w.dep.Version ||
					got[i].installVersion != w.installVersion {
					t.Errorf("divergence[%d] = %+v, want %+v", i, got[i], w)
				}
			}
		})
	}
}

// ─── supplementalJARs ─────────────────────────────────────────────────────────

func TestSupplementalJARsOnlyMissingAndPreservesOverrides(t *testing.T) {
	withURL := manifest.JavaDependency{
		GroupID: "g", ArtifactID: "a", Version: "1.0.0",
		URL: "https://example.test/custom/a.jar", JarFile: "a-custom.jar",
	}
	divs := []divergence{
		{class: classMissing, pkg: "p", dep: withURL},
		{class: classVersionMismatch, pkg: "p", dep: jd("g", "b", "2.0.0"), installVersion: "1.0.0"},
		{class: classExtra, dep: jd("g", "c", "3.0.0")},
	}
	got := supplementalJARs(divs)
	if len(got) != 1 {
		t.Fatalf("supplementalJARs returned %d, want 1 (only the missing one)", len(got))
	}
	if got[0].URL != withURL.URL || got[0].JarFile != withURL.JarFile {
		t.Errorf("url/jar overrides not preserved: got %+v", got[0])
	}
}

func TestHigherVersion(t *testing.T) {
	if !higherVersion("5.3.0", "5.2.9") {
		t.Error("5.3.0 should be higher than 5.2.9")
	}
	if higherVersion("1.0.0", "1.0.0") {
		t.Error("equal versions: higherVersion should be false")
	}
	// Unparseable falls back to lexical without panicking.
	_ = higherVersion("not-semver", "1.0.0")
}

// ─── collectDeclared (reads manifests off disk) ───────────────────────────────

func writePkgManifest(t *testing.T, packagesDir, name, body string) {
	t.Helper()
	dir := filepath.Join(packagesDir, name)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, manifest.Filename), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestCollectDeclared(t *testing.T) {
	home := t.TempDir()
	inst := New(home, "", "")
	if err := inst.ensureDirs(); err != nil {
		t.Fatal(err)
	}

	writePkgManifest(t, inst.packagesDir, "poiapi", `{
		"name":"poiapi","version":"1.4.0",
		"dependencies":{
			"fgl":{"logger":"^1.0.0"},
			"java":[{"groupId":"org.apache.poi","artifactId":"poi","version":"5.3.0"}]
		}
	}`)
	// A directory with no manifest simulates a webcomponent-only package —
	// collectDeclared must skip it, not error.
	if err := os.MkdirAll(filepath.Join(inst.packagesDir, "wc-only"), 0755); err != nil {
		t.Fatal(err)
	}

	root := manifest.New("app", "1.0.0", "", "")
	root.AddJavaDependency(jd("com.root", "own", "9.9.9"))

	set := inst.collectDeclared(root, []string{"poiapi", "wc-only"})

	var sawRoot, sawPoi bool
	for _, d := range set.java {
		if d.pkg == rootPkgLabel && d.dep.Key() == "com.root:own" {
			sawRoot = true
		}
		if d.pkg == "poiapi" && d.dep.Key() == "org.apache.poi:poi" && d.dep.Version == "5.3.0" {
			sawPoi = true
		}
	}
	if !sawRoot {
		t.Error("root's own Java dep not folded into DECLARED")
	}
	if !sawPoi {
		t.Error("poiapi's bundled Java dep not collected")
	}
	if len(set.fgl) != 1 || set.fgl[0].name != "logger" || set.fgl[0].pkg != "poiapi" {
		t.Errorf("expected one FGL declaration (logger from poiapi), got %+v", set.fgl)
	}
}

// ─── warning output ───────────────────────────────────────────────────────────

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	fn()
	w.Close()
	os.Stderr = old
	out, _ := readAll(r)
	return out
}

func readAll(f *os.File) (string, error) {
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := f.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			return sb.String(), nil
		}
	}
}

func TestReportJavaDivergencesFallbackWording(t *testing.T) {
	divs := []divergence{
		{class: classMissing, pkg: "poiapi", pkgVersion: "1.4.0", dep: jd("org.apache.poi", "poi", "5.3.0")},
	}
	on := captureStderr(t, func() { reportJavaDivergences(divs, true) })
	if !strings.Contains(on, "org.apache.poi:poi@5.3.0") {
		t.Errorf("warning missing coordinate: %q", on)
	}
	// The declaring package is named with its version, per spec §6.
	if !strings.Contains(on, "poiapi@1.4.0 declares") {
		t.Errorf("warning should name the package as poiapi@1.4.0: %q", on)
	}
	if !strings.Contains(on, "--no-manifest-fallback to disable") {
		t.Errorf("fallback-on warning should mention the disable flag: %q", on)
	}

	off := captureStderr(t, func() { reportJavaDivergences(divs, false) })
	if !strings.Contains(off, "NOT installed") {
		t.Errorf("fallback-off warning should say NOT installed: %q", off)
	}
}

// A package that omits many JARs has its list truncated with an "(N total)"
// summary rather than flooding the terminal.
func TestReportJavaDivergencesTruncatesLongList(t *testing.T) {
	var divs []divergence
	for i := 0; i < maxCoordsShown+5; i++ {
		divs = append(divs, divergence{
			class: classMissing, pkg: "poiapi", pkgVersion: "1.4.0",
			dep: jd("org.apache.poi", fmt.Sprintf("poi-mod%d", i), "5.3.0"),
		})
	}
	out := captureStderr(t, func() { reportJavaDivergences(divs, true) })
	total := maxCoordsShown + 5
	if !strings.Contains(out, fmt.Sprintf("(%d total)", total)) {
		t.Errorf("long list should be summarised with (%d total): %q", total, out)
	}
	if !strings.Contains(out, "…") {
		t.Errorf("truncated list should show an ellipsis: %q", out)
	}
	// The one-per-package grouping still reports the true count in the header.
	if !strings.Contains(out, fmt.Sprintf("declares %d Java dependencies", total)) {
		t.Errorf("header should state the full count: %q", out)
	}
}

func TestReportFGLDivergencesWarnsOnUnresolved(t *testing.T) {
	fgl := []declaredFGL{{pkg: "poiapi", name: "logger"}}
	out := captureStderr(t, func() { reportFGLDivergences(fgl, map[string]bool{}) })
	if !strings.Contains(out, "logger") || !strings.Contains(out, "cannot recover") {
		t.Errorf("expected an FGL warning mentioning logger: %q", out)
	}
	// Resolved FGL dep → no warning.
	quiet := captureStderr(t, func() { reportFGLDivergences(fgl, map[string]bool{"logger": true}) })
	if quiet != "" {
		t.Errorf("resolved FGL dep should produce no warning, got: %q", quiet)
	}
}

// ─── integration: the poiapi regression through installFromLock ───────────────

// poiapiRegressionFixture stands up a fake registry serving a package zip
// whose bundled manifest declares a JAR the lock omits, plus the JAR itself.
// Returns the installer, projectDir (holding the lock), the saved lock, and
// the URL the bundled manifest points its JAR at.
func poiapiRegressionFixture(t *testing.T) (*Installer, string, *lockfile.LockFile, string) {
	t.Helper()
	tmp := t.TempDir()

	mux := http.NewServeMux()
	mux.HandleFunc("/lib.jar", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("JARDATA"))
	})
	mux.HandleFunc("/pkg.zip", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filepath.Join(tmp, "pkg.zip"))
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	jarURL := ts.URL + "/lib.jar"
	pkgManifest := fmt.Sprintf(`{
		"name":"poiapi","version":"1.4.0",
		"dependencies":{"java":[
			{"groupId":"org.apache.poi","artifactId":"poi","version":"5.3.0","url":%q}
		]}
	}`, jarURL)
	writeTestZip(t, filepath.Join(tmp, "pkg.zip"), map[string]string{
		manifest.Filename: pkgManifest,
		"src/poi.4gl":     "MAIN\nEND MAIN\n",
	})

	home := t.TempDir()
	inst := New(home, "", "")

	projectDir := t.TempDir()
	lf := &lockfile.LockFile{
		Version:       1,
		GeneroVersion: "4.00",
		RootManifest:  lockfile.RootEntry{Name: "app", Version: "1.0.0"},
		// Registry dropped the Java deps: the lock records the package but
		// zero JARs — exactly the poiapi@1.4.0 state.
		Packages: []lockfile.LockedPackage{
			{Name: "poiapi", Version: "1.4.0", DownloadURL: ts.URL + "/pkg.zip"},
		},
	}
	if err := lf.Save(projectDir); err != nil {
		t.Fatal(err)
	}
	return inst, projectDir, lf, jarURL
}

func TestInstallFromLockFallbackRecoversMissingJAR(t *testing.T) {
	inst, projectDir, lf, _ := poiapiRegressionFixture(t)
	root := manifest.New("app", "1.0.0", "", "")

	if err := inst.installFromLock(lf, root, Options{}, projectDir); err != nil {
		t.Fatalf("installFromLock: %v", err)
	}

	// (b) the JAR was installed as a fallback.
	jarPath := filepath.Join(inst.jarsDir, "poi-5.3.0.jar")
	data, err := os.ReadFile(jarPath)
	if err != nil {
		t.Fatalf("fallback JAR not installed: %v", err)
	}
	if string(data) != "JARDATA" {
		t.Errorf("fallback JAR content = %q, want JARDATA", data)
	}

	// (c) the lock records it as manifest-sourced.
	got, err := lockfile.Load(projectDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.JARs) != 1 {
		t.Fatalf("lock should record 1 JAR, got %d", len(got.JARs))
	}
	if got.JARs[0].Key != "org.apache.poi:poi" || got.JARs[0].Source != "manifest" {
		t.Errorf("lock JAR = %+v, want key org.apache.poi:poi source manifest", got.JARs[0])
	}
}

func TestInstallFromLockNoFallbackFlagSkipsRecovery(t *testing.T) {
	inst, projectDir, lf, _ := poiapiRegressionFixture(t)
	root := manifest.New("app", "1.0.0", "", "")

	if err := inst.installFromLock(lf, root, Options{NoManifestFallback: true}, projectDir); err != nil {
		t.Fatalf("installFromLock: %v", err)
	}

	if _, err := os.Stat(filepath.Join(inst.jarsDir, "poi-5.3.0.jar")); !os.IsNotExist(err) {
		t.Error("--no-manifest-fallback should NOT install the missing JAR")
	}
	got, err := lockfile.Load(projectDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.JARs) != 0 {
		t.Errorf("--no-manifest-fallback should leave the lock JAR list empty, got %+v", got.JARs)
	}
}

// No false positives: when the lock and the bundled manifest agree, the
// cross-check must emit no warning and leave the lock's JAR list untouched.
func TestInstallFromLockNoDivergenceNoWarning(t *testing.T) {
	inst, projectDir, lf, jarURL := poiapiRegressionFixture(t)
	// Registry did NOT drop the dep this time — the lock already pins the
	// same coordinate the manifest declares.
	lf.JARs = []lockfile.LockedJAR{{
		Key: "org.apache.poi:poi", GroupID: "org.apache.poi", ArtifactID: "poi",
		Version: "5.3.0", DownloadURL: jarURL,
	}}
	if err := lf.Save(projectDir); err != nil {
		t.Fatal(err)
	}
	root := manifest.New("app", "1.0.0", "", "")

	out := captureStderr(t, func() {
		if err := inst.installFromLock(lf, root, Options{}, projectDir); err != nil {
			t.Fatalf("installFromLock: %v", err)
		}
	})
	if strings.Contains(out, "warning:") {
		t.Errorf("agreeing lock/manifest should produce no warning, got: %q", out)
	}

	got, err := lockfile.Load(projectDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.JARs) != 1 || got.JARs[0].Source != "" {
		t.Errorf("lock JARs should be unchanged (1 registry-sourced entry), got %+v", got.JARs)
	}
}

// Lock fast-path (spec §11): a package already present on disk is skipped for
// download, but its bundled manifest is still scanned, so a stale lock that
// omits a declared JAR is still recovered.
func TestInstallFromLockAlreadyInstalledStillCrossChecked(t *testing.T) {
	inst, projectDir, lf, jarURL := poiapiRegressionFixture(t)
	if err := inst.ensureDirs(); err != nil {
		t.Fatal(err)
	}
	// Pre-install the package on disk so installFromLock's already-installed
	// filter skips the (irrelevant) download entirely — the bundled manifest
	// is what the cross-check must read.
	writePkgManifest(t, inst.packagesDir, "poiapi", fmt.Sprintf(`{
		"name":"poiapi","version":"1.4.0",
		"dependencies":{"java":[
			{"groupId":"org.apache.poi","artifactId":"poi","version":"5.3.0","url":%q}
		]}
	}`, jarURL))

	root := manifest.New("app", "1.0.0", "", "")
	if err := inst.installFromLock(lf, root, Options{}, projectDir); err != nil {
		t.Fatalf("installFromLock: %v", err)
	}

	if _, err := os.Stat(filepath.Join(inst.jarsDir, "poi-5.3.0.jar")); err != nil {
		t.Errorf("already-installed package should still be cross-checked and its JAR recovered: %v", err)
	}
	got, err := lockfile.Load(projectDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.JARs) != 1 || got.JARs[0].Source != "manifest" {
		t.Errorf("stale lock should gain the manifest-sourced JAR, got %+v", got.JARs)
	}
}
