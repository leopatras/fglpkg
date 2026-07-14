package installer

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/4js-mikefolcher/fglpkg/internal/checksum"
	"github.com/4js-mikefolcher/fglpkg/internal/genero"
	gh "github.com/4js-mikefolcher/fglpkg/internal/github"
	"github.com/4js-mikefolcher/fglpkg/internal/lockfile"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
	"github.com/4js-mikefolcher/fglpkg/internal/registry"
	"github.com/4js-mikefolcher/fglpkg/internal/resolver"
)

// InstalledPackage is a summary of an installed BDL package.
type InstalledPackage struct {
	Name    string
	Version string
}

// Installer manages package installation into the fglpkg home directory.
type Installer struct {
	home             string // e.g. ~/.fglpkg
	packagesDir      string // ~/.fglpkg/packages
	jarsDir          string // ~/.fglpkg/jars
	webcomponentsDir string // ~/.fglpkg/webcomponents
	githubToken      string // GitHub PAT for downloading from private GitHub Releases
	registryToken    string // bearer for the consumer registry when it serves zips directly
	repoAuth         []RepoAuth
	// versionFetcher/infoFetcher, when non-nil, replace the default live GI
	// registry fetchers with a multi-provider routing layer (RepositorySet).
	versionFetcher resolver.VersionFetcher
	infoFetcher    resolver.InfoFetcher
	// pinDeclarer, when non-nil, receives per-dependency registry pins found in
	// resolved packages' manifests so transitive deps route to the author's
	// stated source. Set alongside the multi-provider fetchers.
	pinDeclarer resolver.PinDeclarer
}

// RepoAuth maps a repository URL prefix to the HTTP headers that authenticate
// downloads from it. Used for secondary (Artifactory) repositories, whose auth
// scheme may be bearer/basic/apikey. Matched by longest URL prefix.
type RepoAuth struct {
	URLPrefix string
	Headers   map[string]string
}

// New creates an Installer rooted at home.
//
//   - githubToken: authenticates downloads from private GitHub Releases
//     (used by the legacy fglpkg-registry.fly.dev flow). Pass "" if not needed.
//   - registryToken: bearer for non-GitHub download URLs (the new
//     service.generointelligence.ai flow serves zips itself, possibly
//     behind auth). Pass "" for anonymous fetches.
func New(home, githubToken, registryToken string) *Installer {
	return &Installer{
		home:             home,
		packagesDir:      filepath.Join(home, "packages"),
		jarsDir:          filepath.Join(home, "jars"),
		webcomponentsDir: filepath.Join(home, "webcomponents"),
		githubToken:      githubToken,
		registryToken:    registryToken,
	}
}

// WithRepoAuth attaches per-repository download auth (for Artifactory
// secondary repositories) and returns the installer for chaining.
func (i *Installer) WithRepoAuth(ra []RepoAuth) *Installer {
	i.repoAuth = ra
	return i
}

// WithFetchers replaces the default live GI registry fetchers with a
// multi-provider routing layer (e.g. a RepositorySet's Versions/Info). Pass
// nil,nil to keep the default single-registry behaviour.
func (i *Installer) WithFetchers(fv resolver.VersionFetcher, fi resolver.InfoFetcher) *Installer {
	i.versionFetcher = fv
	i.infoFetcher = fi
	return i
}

// WithPinDeclarer attaches a PinDeclarer (typically the same RepositorySet
// backing the fetchers) so declared per-dependency registry pins are honoured
// during resolution. Returns the installer for chaining.
func (i *Installer) WithPinDeclarer(pd resolver.PinDeclarer) *Installer {
	i.pinDeclarer = pd
	return i
}

// newResolver builds the resolver, using injected multi-provider fetchers when
// configured (still honouring any workspace), else the default live resolver.
func (i *Installer) newResolver(gv genero.Version) (*resolver.Resolver, error) {
	if i.versionFetcher != nil && i.infoFetcher != nil {
		r := resolver.NewWithFetchers(gv, i.versionFetcher, i.infoFetcher)
		if i.pinDeclarer != nil {
			r = r.WithPinDeclarer(i.pinDeclarer)
		}
		if err := r.DetectWorkspace(); err != nil {
			return nil, err
		}
		return r, nil
	}
	return resolver.New()
}

// matchRepoAuth returns the headers for the configured repo whose URL prefix
// best (longest) matches url, or nil if none match.
func (i *Installer) matchRepoAuth(url string) map[string]string {
	var best RepoAuth
	for _, ra := range i.repoAuth {
		if strings.HasPrefix(url, ra.URLPrefix) && len(ra.URLPrefix) > len(best.URLPrefix) {
			best = ra
		}
	}
	return best.Headers
}

// Options controls optional install behaviour.
type Options struct {
	// Production skips dev-scoped packages and JARs. Optional entries are
	// still attempted.
	Production bool
	// NoManifestFallback disables the fallback half of the dependency
	// cross-check: when set, the installer still diffs each package's
	// bundled manifest against the install set and warns on divergence, but
	// it does NOT install Java coordinates the manifest declares and the
	// install set omits. Default (false) means fallback is on.
	NoManifestFallback bool
}

// InstallAll resolves or reads from the lock file, then installs every
// BDL package and Java JAR. If a valid lock file exists and matches the
// current environment, it is used directly (no network resolution needed).
// Pass forceResolve=true to bypass the lock and re-resolve from scratch
// (used by `fglpkg update`).
func (i *Installer) InstallAll(m *manifest.Manifest, projectDir string, forceResolve bool) error {
	return i.InstallAllWithOptions(m, projectDir, forceResolve, Options{})
}

// InstallAllWithOptions is InstallAll with caller-controlled options.
func (i *Installer) InstallAllWithOptions(m *manifest.Manifest, projectDir string, forceResolve bool, opts Options) error {
	if err := i.ensureDirs(); err != nil {
		return err
	}

	// Detect Genero version once — used for both lock validation and resolution.
	gv, err := genero.Detect()
	if err != nil {
		return fmt.Errorf("cannot detect Genero version: %w", err)
	}

	// ── Try to use an existing lock file ────────────────────────────────────
	if !forceResolve && lockfile.Exists(projectDir) {
		lf, err := lockfile.Load(projectDir)
		if err != nil {
			fmt.Printf("warning: cannot read lock file: %v — re-resolving\n", err)
		} else {
			vr := lf.Validate(m, gv.String(), i.packagesDir, i.webcomponentsDir)
			if vr.NeedsResolve() {
				fmt.Printf("Lock file is stale (%v) — re-resolving...\n", vr.ManifestMismatch)
			} else {
				if vr.GeneroMismatch != nil {
					fmt.Printf("warning: %v\n", vr.GeneroMismatch)
				}
				if vr.IsClean() {
					fmt.Printf("Lock file is up to date (Genero %s). Nothing to install.\n", gv)
					return nil
				}
				// Lock is valid but some packages are missing on disk — install them.
				fmt.Printf("Installing from lock file (Genero %s)...\n", gv)
				return i.installFromLock(lf, m, opts, projectDir)
			}
		}
	}

	// ── Resolve the full dependency graph ───────────────────────────────────
	fmt.Printf("Resolving dependency graph (Genero %s)...\n", gv)
	r, err := i.newResolver(gv)
	if err != nil {
		return fmt.Errorf("cannot initialise resolver: %w", err)
	}
	resolveOpts := resolver.DefaultResolveOptions()
	if opts.Production {
		resolveOpts.IncludeDev = false
	}
	plan, err := r.ResolveWithOptions(m, resolveOpts)
	if err != nil {
		return fmt.Errorf("dependency resolution failed:\n%w", err)
	}
	fmt.Printf("Resolved %d package(s), %d JAR(s)\n\n", len(plan.Packages), len(plan.JARs))

	// Write the lock file before installing so it's always present even if
	// installation is interrupted partway through.
	// When --production is in effect we do NOT overwrite the lock file,
	// because it would drop dev entries that should remain recorded.
	if !opts.Production {
		lf := lockfile.FromPlan(plan, m)
		if err := lf.Save(projectDir); err != nil {
			// Non-fatal: warn but continue with the install.
			fmt.Printf("warning: could not write lock file: %v\n", err)
		} else {
			fmt.Printf("Wrote %s\n\n", lockfile.Filename)
		}
	}

	return i.installFromPlan(plan, m, opts, projectDir)
}

// installFromLock installs every entry in the lock file using its pinned
// URLs and checksums, bypassing the resolver entirely. When opts.Production
// is true, dev-scoped entries are skipped.
func (i *Installer) installFromLock(lf *lockfile.LockFile, root *manifest.Manifest, opts Options, projectDir string) error {
	var pkgs []lockfile.LockedPackage
	var jars []lockfile.LockedJAR
	var wcs []lockfile.LockedWebcomponent
	if opts.Production {
		pkgs, jars, wcs = lf.FilterForProduction()
	} else {
		pkgs, jars, wcs = lf.ToInstallList()
	}

	// Filter packages that are already on disk so the parallel phase
	// only does real work. Already-installed lines are printed
	// synchronously up front for a stable "already there" prelude.
	var pkgsToInstall []lockfile.LockedPackage
	for _, pkg := range pkgs {
		if _, err := os.Stat(filepath.Join(i.packagesDir, pkg.Name)); err == nil {
			fmt.Printf("  ✓ %s@%s (already installed)\n", pkg.Name, pkg.Version)
			continue
		}
		pkgsToInstall = append(pkgsToInstall, pkg)
	}

	cap := installConcurrency()

	if err := runParallel(pkgsToInstall, cap, func(pkg lockfile.LockedPackage) error {
		info := &registry.PackageInfo{
			Name:        pkg.Name,
			Version:     pkg.Version,
			DownloadURL: pkg.DownloadURL,
			Checksum:    pkg.Checksum,
		}
		if err := i.Install(info); err != nil {
			return fmt.Errorf("failed to install %s: %w", pkg.Name, err)
		}
		printSync("  ✓ %s@%s\n", pkg.Name, pkg.Version)
		return nil
	}); err != nil {
		return err
	}

	// Webcomponent packages — install in parallel after the BDL pass.
	// A locked webcomponent entry is considered "already installed" when
	// any of its COMPONENTTYPE dirs are present; on a re-install we always
	// re-extract to refresh the contents anyway, so this gate just keeps
	// the no-op-fast-path from repeating itself.
	if err := runParallel(wcs, cap, func(wc lockfile.LockedWebcomponent) error {
		info := &registry.PackageInfo{
			Name:        wc.Name,
			Version:     wc.Version,
			DownloadURL: wc.DownloadURL,
			Checksum:    wc.Checksum,
			Variant:     "webcomponent",
		}
		if err := i.Install(info); err != nil {
			return fmt.Errorf("failed to install webcomponent %s: %w", wc.Name, err)
		}
		printSync("  ✓ %s@%s (webcomponent)\n", wc.Name, wc.Version)
		return nil
	}); err != nil {
		return err
	}

	// ── Dependency cross-check (post-extraction) ────────────────────────────
	// Diff each installed package's bundled manifest against the locked JAR
	// set. Scans ALL locked packages (including those already on disk) so a
	// stale lock is still cross-checked.
	installedPkgs := make(map[string]bool, len(pkgs)+len(wcs))
	bdlPkgNames := make([]string, 0, len(pkgs))
	for _, p := range pkgs {
		installedPkgs[p.Name] = true
		bdlPkgNames = append(bdlPkgNames, p.Name)
	}
	for _, w := range wcs {
		installedPkgs[w.Name] = true
	}
	install := make(map[string]manifest.JavaDependency, len(jars))
	for _, jar := range jars {
		install[jar.Key] = manifest.JavaDependency{
			GroupID: jar.GroupID, ArtifactID: jar.ArtifactID, Version: jar.Version,
		}
	}
	supplemental := i.crossCheckJava(root, bdlPkgNames, install, installedPkgs, opts)

	// Filter locked JARs that are already on disk.
	var jarsToInstall []lockfile.LockedJAR
	for _, jar := range jars {
		dep := manifest.JavaDependency{
			GroupID: jar.GroupID, ArtifactID: jar.ArtifactID, Version: jar.Version,
		}
		if _, err := os.Stat(filepath.Join(i.jarsDir, dep.JarFileName())); err == nil {
			fmt.Printf("  ✓ %s (already present)\n", jar.Key)
			continue
		}
		jarsToInstall = append(jarsToInstall, jar)
	}

	if err := runParallel(jarsToInstall, cap, func(jar lockfile.LockedJAR) error {
		dep := manifest.JavaDependency{
			GroupID:    jar.GroupID,
			ArtifactID: jar.ArtifactID,
			Version:    jar.Version,
			Checksum:   jar.Checksum,
			URL:        jar.DownloadURL,
		}
		if err := i.InstallJar(dep); err != nil {
			return fmt.Errorf("failed to install JAR %s: %w", jar.Key, err)
		}
		printSync("  ✓ %s\n", jar.Key)
		return nil
	}); err != nil {
		return err
	}

	// Fallback JARs recovered from bundled manifests — install as full
	// JavaDependency structs so url/jar overrides survive. InstallJar is
	// idempotent (skips JARs already on disk).
	if err := runParallel(supplemental, cap, func(dep manifest.JavaDependency) error {
		if err := i.InstallJar(dep); err != nil {
			return fmt.Errorf("failed to install fallback JAR %s: %w", dep.Key(), err)
		}
		printSync("  ✓ %s (manifest fallback)\n", dep.JarFileName())
		return nil
	}); err != nil {
		return err
	}

	if !opts.Production {
		i.recordManifestJARs(projectDir, supplemental)
	}
	return nil
}

// installFromPlan installs every entry in a freshly resolved Plan.
// Optional-scoped items whose download or extraction fails emit a warning
// and are skipped; hard-scope failures abort the install.
func (i *Installer) installFromPlan(plan *resolver.Plan, root *manifest.Manifest, opts Options, projectDir string) error {
	cap := installConcurrency()

	if err := runParallel(plan.Packages, cap, func(pkg resolver.ResolvedPackage) error {
		info := &registry.PackageInfo{
			Name:        pkg.Name,
			Version:     pkg.Version.String(),
			DownloadURL: pkg.DownloadURL,
			Checksum:    pkg.Checksum,
			Variant:     pkg.Variant,
		}
		if err := i.Install(info); err != nil {
			if pkg.Scope == manifest.ScopeOptional {
				printSync("  warning: skipping optional package %s: %v\n", pkg.Name, err)
				return nil
			}
			return fmt.Errorf("failed to install %s: %w", pkg.Name, err)
		}
		// Required-by hint joins the completion line so it doesn't
		// race onto a separate line from a sibling worker.
		kindHint := ""
		if pkg.IsWebcomponent() {
			kindHint = " (webcomponent)"
		}
		if len(pkg.RequiredBy) > 0 {
			printSync("  ✓ %s@%s%s  (required by: %s)\n",
				pkg.Name, pkg.Version.String(), kindHint, strings.Join(pkg.RequiredBy, ", "))
		} else {
			printSync("  ✓ %s@%s%s\n", pkg.Name, pkg.Version.String(), kindHint)
		}
		return nil
	}); err != nil {
		return err
	}

	// ── Dependency cross-check (post-extraction) ────────────────────────────
	var bdlPkgNames []string
	installedPkgs := make(map[string]bool, len(plan.Packages))
	for _, p := range plan.Packages {
		installedPkgs[p.Name] = true
		if !p.IsWebcomponent() {
			bdlPkgNames = append(bdlPkgNames, p.Name)
		}
	}
	install := make(map[string]manifest.JavaDependency, len(plan.JARs))
	for _, dep := range plan.JARs {
		install[dep.Key()] = dep
	}
	supplemental := i.crossCheckJava(root, bdlPkgNames, install, installedPkgs, opts)

	// Install the resolved JARs plus any manifest-fallback JARs. Fallback
	// JARs carry no plan scope, so they never hit the optional-skip path
	// (transitive Java is always production for the consumer).
	jarsToInstall := append(append([]manifest.JavaDependency(nil), plan.JARs...), supplemental...)
	if err := runParallel(jarsToInstall, cap, func(dep manifest.JavaDependency) error {
		if err := i.InstallJar(dep); err != nil {
			if plan.JARScopes[dep.Key()] == manifest.ScopeOptional {
				printSync("  warning: skipping optional JAR %s: %v\n", dep.Key(), err)
				return nil
			}
			return fmt.Errorf("failed to install JAR %s: %w", dep.Key(), err)
		}
		printSync("  ✓ %s\n", dep.JarFileName())
		return nil
	}); err != nil {
		return err
	}

	if !opts.Production {
		i.recordManifestJARs(projectDir, supplemental)
	}
	return nil
}

// Install downloads, verifies, and unpacks a single package — dispatching
// to the BDL or webcomponent install layout based on info.Variant.
func (i *Installer) Install(info *registry.PackageInfo) error {
	if info.Variant == "webcomponent" {
		return i.installWebcomponent(info)
	}
	return i.installBDL(info)
}

// installBDL is the BDL (or mixed) package install path: extract the zip
// into .fglpkg/packages/<name>/, splitting off any webcomponent bundles
// declared in the in-zip manifest into .fglpkg/webcomponents/<NAME>/, and
// make bin scripts executable.
func (i *Installer) installBDL(info *registry.PackageInfo) error {
	if err := i.ensureDirs(); err != nil {
		return err
	}

	tmp, err := os.CreateTemp("", "fglpkg-*.zip")
	if err != nil {
		return fmt.Errorf("cannot create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	// Normalize the download URL: the registry returns site-relative
	// download URLs (and older lock files persisted them in that form), so
	// resolve against the consumer base before the GET. No-op for URLs that
	// already carry a scheme (GitHub assets, R2/CDN redirects).
	downloadURL := registry.AbsoluteDownloadURL(info.DownloadURL)

	// Download and verify in one streaming pass.
	if err := downloadAndVerify(downloadURL, info.Checksum, info.Name, tmp, i.githubToken, i.registryToken, i.matchRepoAuth(downloadURL)); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()

	// Peek at the in-zip manifest before extracting so we know which
	// top-level directories are COMPONENTTYPE bundles (need to route to
	// .fglpkg/webcomponents/) vs. ordinary BDL paths (extract into
	// .fglpkg/packages/<name>/). Pure-BDL packages return an empty list.
	wcNames, err := readWebcomponentsFromZip(tmpName)
	if err != nil {
		return fmt.Errorf("cannot read manifest from zip: %w", err)
	}

	destDir := filepath.Join(i.packagesDir, info.Name)
	if err := os.RemoveAll(destDir); err != nil {
		return fmt.Errorf("cannot clean existing package dir: %w", err)
	}
	if err := extractZipRouted(tmpName, destDir, i.webcomponentsDir, wcNames); err != nil {
		return err
	}

	// Make bin scripts executable after extraction.
	pkgManifest, err := manifest.Load(destDir)
	if err == nil && len(pkgManifest.Bin) > 0 {
		if err := makeBinScriptsExecutable(destDir, pkgManifest); err != nil {
			return fmt.Errorf("cannot set bin script permissions: %w", err)
		}
	}
	return nil
}

// installWebcomponent downloads, verifies, and unpacks a webcomponent
// package. Unlike BDL packages — which extract to their own subdir under
// .fglpkg/packages/<name>/ — webcomponent bundles drop straight into
// .fglpkg/webcomponents/<COMPONENTTYPE>/ so Genero finds them via
// FGLIMAGEPATH/WEB_COMPONENT_DIRECTORY without an extra path segment. The
// in-zip layout already has the COMPONENTTYPE/ prefix (the pack step
// strips the leading "webcomponents/"), so a direct extraction is correct.
//
// The package's fglpkg.json is intentionally not extracted to disk —
// multiple webcomponent packages would collide on it. The component names
// are discoverable from the directory listing alone.
func (i *Installer) installWebcomponent(info *registry.PackageInfo) error {
	if err := i.ensureDirs(); err != nil {
		return err
	}

	tmp, err := os.CreateTemp("", "fglpkg-*.zip")
	if err != nil {
		return fmt.Errorf("cannot create temp file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	downloadURL := registry.AbsoluteDownloadURL(info.DownloadURL)
	if err := downloadAndVerify(downloadURL, info.Checksum, info.Name, tmp, i.githubToken, i.registryToken, i.matchRepoAuth(downloadURL)); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()

	return extractWebcomponentZip(tmpName, i.webcomponentsDir)
}

// InstallJar downloads and verifies a Java JAR into the jars directory.
// The JAR checksum field on JavaDependency is optional; if empty the
// integrity check is skipped (Maven Central is trusted by default).
func (i *Installer) InstallJar(dep manifest.JavaDependency) error {
	if err := i.ensureDirs(); err != nil {
		return err
	}

	dest := filepath.Join(i.jarsDir, dep.JarFileName())
	if _, err := os.Stat(dest); err == nil {
		// Already on disk. Callers report progress; this fast path is
		// silent to keep parallel install output clean.
		return nil
	}

	f, err := os.Create(dest)
	if err != nil {
		return fmt.Errorf("cannot create jar file: %w", err)
	}

	url := dep.MavenURL()

	// JavaDependency doesn't carry a checksum field today; pass "" to skip.
	// JARs come from Maven Central anonymously — no repo auth.
	if err := downloadAndVerify(url, dep.Checksum, dep.JarFileName(), f, "", "", nil); err != nil {
		f.Close()
		os.Remove(dest)
		return err
	}
	return f.Close()
}

// Remove deletes a BDL package directory.
func (i *Installer) Remove(name string) error {
	dir := filepath.Join(i.packagesDir, name)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		return fmt.Errorf("package %q is not installed", name)
	}
	return os.RemoveAll(dir)
}

// ReconcileAfterRemove brings the install state back in line with a manifest
// that has just had one or more dependencies removed. It re-resolves the
// remaining graph, rewrites the lock file (so the removed package and its now
// unreferenced JARs no longer reappear on the next install), and — when prune
// is true — deletes installed packages and JARs that the resolved graph no
// longer requires.
//
// prune MUST be false for a global (~/.fglpkg) home: those package and JAR
// directories are shared across every project, so pruning against a single
// project's graph would delete another project's dependencies. It is only
// safe for a project-local (.fglpkg/) install. The lock rewrite is always
// safe — the lock is project-local regardless of where artifacts live.
//
// A resolution failure (e.g. offline, registry unreachable) is returned so the
// caller can fall back to a manifest-only removal; nothing is pruned in that
// case.
func (i *Installer) ReconcileAfterRemove(m *manifest.Manifest, projectDir string, prune bool) ([]string, error) {
	if err := i.ensureDirs(); err != nil {
		return nil, err
	}
	gv, err := genero.Detect()
	if err != nil {
		return nil, fmt.Errorf("cannot detect Genero version: %w", err)
	}
	r, err := i.newResolver(gv)
	if err != nil {
		return nil, fmt.Errorf("cannot initialise resolver: %w", err)
	}
	plan, err := r.ResolveWithOptions(m, resolver.DefaultResolveOptions())
	if err != nil {
		return nil, fmt.Errorf("dependency resolution failed:\n%w", err)
	}

	// Rewrite the lock only if the project already had one; a `remove` should
	// not conjure a lock for a project that was never installed.
	if lockfile.Exists(projectDir) {
		if err := lockfile.FromPlan(plan, m).Save(projectDir); err != nil {
			return nil, fmt.Errorf("cannot write lock file: %w", err)
		}
	}

	if !prune {
		return nil, nil
	}
	return i.pruneToPlan(plan)
}

// pruneToPlan deletes installed BDL packages and JARs that are absent from
// plan, returning a human-readable list of what it removed. Webcomponent
// bundles are not pruned: their on-disk layout is keyed by COMPONENTTYPE, not
// package name, and that mapping is not persisted, so there is no reliable way
// to know which bundle belonged to a removed package.
func (i *Installer) pruneToPlan(plan *resolver.Plan) ([]string, error) {
	wantPkg := make(map[string]bool, len(plan.Packages))
	for _, p := range plan.Packages {
		if !p.IsWebcomponent() {
			wantPkg[p.Name] = true
		}
	}
	wantJar := make(map[string]bool, len(plan.JARs))
	for _, dep := range plan.JARs {
		wantJar[dep.JarFileName()] = true
	}

	var pruned []string

	pkgEntries, err := os.ReadDir(i.packagesDir)
	if err != nil && !os.IsNotExist(err) {
		return pruned, err
	}
	for _, e := range pkgEntries {
		if !e.IsDir() || wantPkg[e.Name()] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(i.packagesDir, e.Name())); err != nil {
			return pruned, fmt.Errorf("cannot prune package %s: %w", e.Name(), err)
		}
		pruned = append(pruned, "package "+e.Name())
	}

	jarEntries, err := os.ReadDir(i.jarsDir)
	if err != nil && !os.IsNotExist(err) {
		return pruned, err
	}
	for _, e := range jarEntries {
		if e.IsDir() || wantJar[e.Name()] {
			continue
		}
		if err := os.Remove(filepath.Join(i.jarsDir, e.Name())); err != nil {
			return pruned, fmt.Errorf("cannot prune jar %s: %w", e.Name(), err)
		}
		pruned = append(pruned, "jar "+e.Name())
	}

	return pruned, nil
}

// List returns all currently installed BDL packages by scanning the packages dir.
func (i *Installer) List() ([]InstalledPackage, error) {
	entries, err := os.ReadDir(i.packagesDir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var pkgs []InstalledPackage
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		version := "unknown"
		if m, err := manifest.Load(filepath.Join(i.packagesDir, e.Name())); err == nil {
			version = m.Version
		}
		pkgs = append(pkgs, InstalledPackage{Name: e.Name(), Version: version})
	}
	return pkgs, nil
}

// PackagesDir returns the path where BDL packages are installed.
func (i *Installer) PackagesDir() string { return i.packagesDir }

// WebcomponentsDir is the directory holding installed webcomponent bundles,
// one subdirectory per COMPONENTTYPE.
func (i *Installer) WebcomponentsDir() string { return i.webcomponentsDir }

// JarsDir returns the path where Java JARs are stored.
func (i *Installer) JarsDir() string { return i.jarsDir }

// ─── Download + verify ────────────────────────────────────────────────────────

// downloadAndVerify fetches url, streams the body through a DigestingReader
// into w, and verifies the SHA256 against expectedChecksum in a single pass.
// name is used only in error messages.
//
// Auth selection:
//   - GitHub URL + githubToken non-empty → send githubToken (legacy private
//     GitHub Releases path used by fglpkg-registry.fly.dev).
//   - Non-GitHub URL + registryToken non-empty → send registryToken (new
//     service.generointelligence.ai path where the registry serves zips).
//   - Otherwise → no auth (anonymous public download).
func downloadAndVerify(url, expectedChecksum, name string, w io.Writer, githubToken, registryToken string, repoHeaders map[string]string) error {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("download failed for %s: %w", name, err)
	}

	isGH := gh.IsGitHubURL(url)
	authToken := ""
	switch {
	case isGH && githubToken != "":
		authToken = githubToken
		req.Header.Set("Authorization", "Bearer "+authToken)
		req.Header.Set("Accept", "application/octet-stream")
	case !isGH && len(repoHeaders) > 0:
		// Secondary (Artifactory) repository: apply its configured auth scheme
		// headers (bearer / basic / apikey).
		for k, v := range repoHeaders {
			req.Header.Set(k, v)
		}
	case !isGH && registryToken != "":
		authToken = registryToken
		req.Header.Set("Authorization", "Bearer "+authToken)
	}

	// Use a custom client for GitHub API downloads. GitHub redirects asset
	// downloads to a different host (github-releases.githubusercontent.com),
	// and Go's default client strips the Authorization header on cross-host
	// redirects. We need to preserve the token through the redirect chain.
	client := http.DefaultClient
	if isGH && authToken != "" {
		client = &http.Client{
			CheckRedirect: func(r *http.Request, via []*http.Request) error {
				if len(via) > 10 {
					return fmt.Errorf("too many redirects")
				}
				r.Header.Set("Authorization", "Bearer "+authToken)
				return nil
			},
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("download failed for %s: %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("HTTP 401 downloading %s: Not authorised — run 'fglpkg login' or set FGLPKG_TOKEN", name)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d downloading %s from %s", resp.StatusCode, name, url)
	}

	dr := checksum.NewDigestingReader(resp.Body)
	if _, err := io.Copy(w, dr); err != nil {
		return fmt.Errorf("error writing %s: %w", name, err)
	}

	// Verify after the full body has been streamed — no second read.
	if err := dr.Verify(name, expectedChecksum); err != nil {
		return err // already a descriptive *checksum.ErrMismatch
	}
	return nil
}

// ─── Zip extraction ───────────────────────────────────────────────────────────

func (i *Installer) ensureDirs() error {
	for _, d := range []string{i.packagesDir, i.jarsDir, i.webcomponentsDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			return fmt.Errorf("cannot create directory %s: %w", d, err)
		}
	}
	return nil
}

// makeBinScriptsExecutable sets the executable bit on all bin scripts
// after extraction. On Windows this is a no-op.
func makeBinScriptsExecutable(pkgDir string, m *manifest.Manifest) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	for _, scriptPath := range m.BinFiles() {
		fullPath := filepath.Join(pkgDir, scriptPath)
		info, err := os.Stat(fullPath)
		if err != nil {
			return fmt.Errorf("bin script %q not found in installed package: %w", scriptPath, err)
		}
		if err := os.Chmod(fullPath, info.Mode()|0111); err != nil {
			return fmt.Errorf("cannot chmod %s: %w", fullPath, err)
		}
	}
	return nil
}

// readWebcomponentsFromZip opens the zip at zipPath, reads fglpkg.json
// from its root, and returns the manifest's Webcomponents list. A missing
// manifest or unrecognised JSON yields an empty list and no error — the
// caller treats the install as pure BDL.
func readWebcomponentsFromZip(zipPath string) ([]string, error) {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	for _, f := range r.File {
		if filepath.ToSlash(f.Name) != manifest.Filename {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, err
		}
		// Use a partial decode so unknown/new manifest fields don't
		// reject the read here (the resolver and pack flow do strict
		// validation; this is just a routing lookup).
		var partial struct {
			Webcomponents []string `json:"webcomponents"`
		}
		if err := json.Unmarshal(data, &partial); err != nil {
			return nil, fmt.Errorf("manifest in zip is not valid JSON: %w", err)
		}
		return partial.Webcomponents, nil
	}
	return nil, nil
}

// extractZipRouted unpacks a zip into destDir like extractZip, but if
// wcNames is non-empty it diverts any entry whose first path component
// matches one of those names to webcomponentsDir/<COMPONENTTYPE>/...
// instead. Used by mixed packages that ship BDL files alongside one or
// more webcomponent bundles in a single artifact.
//
// Each diverted COMPONENTTYPE directory is cleared at the destination
// before extraction so a re-install does not leave stale files behind.
func extractZipRouted(zipPath, destDir, webcomponentsDir string, wcNames []string) error {
	if len(wcNames) == 0 {
		return extractZip(zipPath, destDir)
	}
	wcSet := make(map[string]bool, len(wcNames))
	for _, n := range wcNames {
		wcSet[n] = true
	}

	// Clear any pre-existing install of these webcomponent dirs.
	for _, n := range wcNames {
		if err := os.RemoveAll(filepath.Join(webcomponentsDir, n)); err != nil {
			return fmt.Errorf("cannot clean existing webcomponent dir %s: %w", n, err)
		}
	}

	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("cannot open zip %s: %w", zipPath, err)
	}
	defer r.Close()

	for _, f := range r.File {
		clean := filepath.Clean(f.Name)
		if strings.HasPrefix(clean, "..") {
			return fmt.Errorf("unsafe path in zip: %s", f.Name)
		}
		slashed := filepath.ToSlash(clean)
		top := strings.SplitN(slashed, "/", 2)[0]

		var target string
		if wcSet[top] {
			// Webcomponent bundle — extract straight into the
			// webcomponents dir, preserving the COMPONENTTYPE prefix.
			target = filepath.Join(webcomponentsDir, clean)
		} else {
			// BDL content (or manifest, root docs) — stays inside
			// the package's own directory.
			target = filepath.Join(destDir, clean)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		if err := writeZipEntry(f, target); err != nil {
			return err
		}
	}
	return nil
}

// extractZip unpacks a zip archive into destDir, sanitising all paths.
func extractZip(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("cannot open zip %s: %w", zipPath, err)
	}
	defer r.Close()

	for _, f := range r.File {
		cleanName := filepath.Clean(f.Name)
		if strings.HasPrefix(cleanName, "..") {
			return fmt.Errorf("unsafe path in zip: %s", f.Name)
		}

		target := filepath.Join(destDir, cleanName)

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		if err := writeZipEntry(f, target); err != nil {
			return err
		}
	}
	return nil
}

// extractWebcomponentZip unpacks a webcomponent zip into destDir
// (typically .fglpkg/webcomponents/). Entries at the zip root that are
// not a COMPONENTTYPE/ directory are skipped — most importantly the
// publisher's fglpkg.json, which would otherwise collide between multiple
// installed webcomponent packages. Each top-level <COMPONENTTYPE>/ subtree
// is first removed at destDir/<COMPONENTTYPE>/ so a reinstall replaces
// stale files cleanly.
func extractWebcomponentZip(zipPath, destDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("cannot open zip %s: %w", zipPath, err)
	}
	defer r.Close()

	// First pass: identify each top-level COMPONENTTYPE/ dir we will touch
	// and clear any pre-existing install so a re-install does not leave
	// stale files behind.
	componentDirs := map[string]bool{}
	for _, f := range r.File {
		clean := filepath.Clean(f.Name)
		if strings.HasPrefix(clean, "..") {
			return fmt.Errorf("unsafe path in zip: %s", f.Name)
		}
		// Top-level entries that contain no slash are not part of a
		// component bundle — typically fglpkg.json or a stray doc file
		// (which lives at the project root). Skip them; the manifest
		// is intentionally not extracted.
		if !strings.ContainsRune(clean, filepath.Separator) && !strings.ContainsRune(clean, '/') {
			continue
		}
		top := strings.SplitN(filepath.ToSlash(clean), "/", 2)[0]
		componentDirs[top] = true
	}
	for top := range componentDirs {
		if err := os.RemoveAll(filepath.Join(destDir, top)); err != nil {
			return fmt.Errorf("cannot clean existing component dir %s: %w", top, err)
		}
	}

	for _, f := range r.File {
		clean := filepath.Clean(f.Name)
		if strings.HasPrefix(clean, "..") {
			return fmt.Errorf("unsafe path in zip: %s", f.Name)
		}
		// Skip zip-root files (manifest, root docs) — only COMPONENTTYPE
		// subtrees install to disk.
		slashed := filepath.ToSlash(clean)
		if !strings.Contains(slashed, "/") {
			continue
		}
		target := filepath.Join(destDir, clean)
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return err
		}
		if err := writeZipEntry(f, target); err != nil {
			return err
		}
	}
	return nil
}

func writeZipEntry(f *zip.File, target string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	out, err := os.Create(target)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, rc)
	return err
}
