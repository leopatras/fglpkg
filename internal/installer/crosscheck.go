package installer

// Dependency cross-check & fallback.
//
// The installer resolves a package's Java (JAR) dependencies from the
// registry's version metadata, before anything is downloaded. The
// fglpkg.json bundled *inside* the package is only visible after extraction.
// When the two disagree — as they did for poiapi@1.4.0, whose registry record
// had an empty java list while its bundled manifest declared 11 coordinates —
// the install silently proceeds with the (empty) registry list.
//
// This file diffs each installed package's bundled manifest against the set
// the installer is actually going to install, warns on every divergence, and
// (by default) installs any Java coordinate the manifest declares but the
// install set omits. See specs/dependency-crosscheck-fallback.md.

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/4js-mikefolcher/fglpkg/internal/lockfile"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
	"github.com/4js-mikefolcher/fglpkg/internal/semver"
)

// rootPkgLabel is the pseudo-package name used for the root project's own
// declarations in a divergence report.
const rootPkgLabel = "<root>"

// divergenceClass classifies how a declared dependency differs from the set
// the installer is actually going to install.
type divergenceClass int

const (
	// classMissing: declared by a manifest, absent from the install set —
	// the poiapi case. The only class that triggers fallback.
	classMissing divergenceClass = iota
	// classVersionMismatch: present in both, versions differ. Warn only; the
	// install-set version stays authoritative.
	classVersionMismatch
	// classExtra: present in the install set, declared by no manifest. Warn
	// only (informational); installed as-is.
	classExtra
)

// declaredDep is one Java coordinate a manifest declares, tagged with the
// package whose bundled manifest declared it. The root project's own
// declarations use pkg == rootPkgLabel.
type declaredDep struct {
	pkg        string
	pkgVersion string // version of the declaring package (for warning fidelity); "" if unknown
	dep        manifest.JavaDependency
}

// declaredFGL is one FGL dependency name a package's bundled manifest
// declares, tagged with the declaring package.
type declaredFGL struct {
	pkg  string
	name string
}

// declaredSet is everything the cross-check reads off disk after extraction.
type declaredSet struct {
	java []declaredDep
	fgl  []declaredFGL
}

// divergence is one manifest↔install-set discrepancy for a single Java key.
type divergence struct {
	class divergenceClass
	// dep is the DECLARED coordinate for missing/version-mismatch, and the
	// INSTALL coordinate for extra. The full struct is retained so url/jar
	// overrides survive into the fallback set.
	dep manifest.JavaDependency
	// pkg is the package that declared dep (missing/version-mismatch); "" for extra.
	pkg string
	// pkgVersion is the declaring package's version, for the warning header
	// (e.g. "poiapi@1.4.0"); "" when unknown or for extra.
	pkgVersion string
	// installVersion is the version the install set pins (version-mismatch only).
	installVersion string
}

// classify diffs DECLARED against the INSTALL set (keyed by
// JavaDependency.Key() == groupId:artifactId) and returns every divergence.
//
// When several packages declare the same coordinate at different versions the
// higher version is chosen as the representative, matching the resolver's own
// higher-version-wins dedup (resolver.addJARScoped). This function is pure —
// no I/O — so the classification is exhaustively unit-testable.
func classify(declared []declaredDep, install map[string]manifest.JavaDependency) []divergence {
	// Group declarations by key, preserving first-seen order for stable output.
	byKey := map[string][]declaredDep{}
	var order []string
	for _, d := range declared {
		k := d.dep.Key()
		if _, ok := byKey[k]; !ok {
			order = append(order, k)
		}
		byKey[k] = append(byKey[k], d)
	}

	var out []divergence

	// missing + version-mismatch: walk the declared keys.
	for _, k := range order {
		rep := byKey[k][0]
		for _, d := range byKey[k][1:] {
			if higherVersion(d.dep.Version, rep.dep.Version) {
				rep = d
			}
		}
		inst, inInstall := install[k]
		if !inInstall {
			out = append(out, divergence{class: classMissing, dep: rep.dep, pkg: rep.pkg, pkgVersion: rep.pkgVersion})
			continue
		}
		if inst.Version != rep.dep.Version {
			out = append(out, divergence{
				class:          classVersionMismatch,
				dep:            rep.dep,
				pkg:            rep.pkg,
				pkgVersion:     rep.pkgVersion,
				installVersion: inst.Version,
			})
		}
	}

	// extra: install-set keys declared by no manifest, sorted for stable output.
	var extraKeys []string
	for k := range install {
		if _, ok := byKey[k]; !ok {
			extraKeys = append(extraKeys, k)
		}
	}
	sort.Strings(extraKeys)
	for _, k := range extraKeys {
		out = append(out, divergence{class: classExtra, dep: install[k]})
	}

	return out
}

// supplementalJARs is the fallback set: the DECLARED coordinates that the
// install set is missing. classify already reduced each key to a single
// highest-version representative, so this just extracts them — copying the
// full JavaDependency struct so any url/jar override is carried through.
func supplementalJARs(divs []divergence) []manifest.JavaDependency {
	var out []manifest.JavaDependency
	for _, d := range divs {
		if d.class == classMissing {
			out = append(out, d.dep)
		}
	}
	return out
}

// crossCheckJava runs the manifest↔install-set cross-check after the package
// pass and before the JAR pass. It reads every relevant bundled manifest off
// disk, reports each divergence to stderr, and — unless fallback is disabled —
// returns the supplemental JARs (missing coordinates) for the caller to
// install. It also runs the warn-only FGL divergence check.
//
// bdlPkgNames are the BDL package directory names under packagesDir whose
// manifests should be scanned (including already-installed ones, so a stale
// lock is still checked). install is the JAR set the installer will fetch,
// keyed by groupId:artifactId. installedPkgs is the set of every resolved
// package name (BDL + webcomponent) for the FGL check.
func (i *Installer) crossCheckJava(
	root *manifest.Manifest,
	bdlPkgNames []string,
	install map[string]manifest.JavaDependency,
	installedPkgs map[string]bool,
	opts Options,
) []manifest.JavaDependency {
	set := i.collectDeclared(root, bdlPkgNames)
	divs := classify(set.java, install)
	fallbackEnabled := !opts.NoManifestFallback
	if len(divs) > 0 {
		reportJavaDivergences(divs, fallbackEnabled)
	}
	reportFGLDivergences(set.fgl, installedPkgs)
	if !fallbackEnabled {
		return nil
	}
	return supplementalJARs(divs)
}

// recordManifestJARs loads the lock file and appends the given fallback JARs
// (marked Source "manifest"), so subsequent and --production installs stay
// deterministic. Failures are non-fatal — the install itself already
// succeeded; a stale lock only costs a re-check next time.
func (i *Installer) recordManifestJARs(projectDir string, jars []manifest.JavaDependency) {
	if len(jars) == 0 {
		return
	}
	lf, err := lockfile.Load(projectDir)
	if err != nil {
		printSync("warning: could not update lock file with fallback JARs: %v\n", err)
		return
	}
	if !lf.AddManifestJARs(jars) {
		return // nothing new to record
	}
	if err := lf.Save(projectDir); err != nil {
		printSync("warning: could not write lock file with fallback JARs: %v\n", err)
	}
}

// higherVersion reports whether semver a is greater than b, falling back to a
// lexical comparison if either string does not parse.
func higherVersion(a, b string) bool {
	av, aerr := semver.Parse(a)
	bv, berr := semver.Parse(b)
	if aerr != nil || berr != nil {
		return a > b
	}
	return av.GreaterThan(bv)
}

// collectDeclared reads the root manifest and every installed package's
// bundled manifest off disk and returns the union of their Java and FGL
// declarations. A package whose manifest is not on disk — a webcomponent-only
// package does not extract its fglpkg.json — is silently skipped.
//
// The root's own declarations are folded into the Java set (across all
// scopes) so a consumer's directly declared JARs are not mis-flagged as
// "extra". FGL declarations are collected only from downloaded packages: a
// missing root FGL dep fails resolution loudly upstream and needs no warning
// here.
func (i *Installer) collectDeclared(root *manifest.Manifest, bdlPkgNames []string) declaredSet {
	var set declaredSet

	for _, dep := range manifestJavaDeps(root) {
		set.java = append(set.java, declaredDep{pkg: rootPkgLabel, pkgVersion: root.Version, dep: dep})
	}

	for _, name := range bdlPkgNames {
		pm, err := manifest.Load(filepath.Join(i.packagesDir, name))
		if err != nil {
			continue // not on disk (webcomponent-only) or unreadable — skip
		}
		for _, dep := range manifestJavaDeps(pm) {
			set.java = append(set.java, declaredDep{pkg: name, pkgVersion: pm.Version, dep: dep})
		}
		for _, fglName := range manifestFGLNames(pm) {
			set.fgl = append(set.fgl, declaredFGL{pkg: name, name: fglName})
		}
	}
	return set
}

// manifestJavaDeps returns a manifest's Java dependencies across the prod,
// optional, and dev scopes. Published package manifests carry no dev deps
// (they are stripped at publish), so including dev is harmless and makes the
// root-fold robust against a consumer's dev-declared JARs.
func manifestJavaDeps(m *manifest.Manifest) []manifest.JavaDependency {
	var out []manifest.JavaDependency
	out = append(out, m.Dependencies.Java...)
	out = append(out, m.OptionalDependencies.Java...)
	out = append(out, m.DevDependencies.Java...)
	return out
}

// manifestFGLNames returns a manifest's FGL dependency names across the prod
// and optional scopes (the scopes a consumer installs transitively).
func manifestFGLNames(m *manifest.Manifest) []string {
	var out []string
	for name := range m.Dependencies.FGL {
		out = append(out, name)
	}
	for name := range m.OptionalDependencies.FGL {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// reportJavaDivergences writes one stderr warning per divergence. Missing
// coordinates are grouped into a single actionable warning per declaring
// package (the poiapi shape); version-mismatch and extra get one line each.
// The output is intentionally structured so it can later be emitted as a
// machine-readable record for the security pipeline.
func reportJavaDivergences(divs []divergence, fallbackEnabled bool) {
	missingByPkg := map[string][]manifest.JavaDependency{}
	pkgLabels := map[string]string{}
	var missingOrder []string
	for _, d := range divs {
		if d.class != classMissing {
			continue
		}
		if _, ok := missingByPkg[d.pkg]; !ok {
			missingOrder = append(missingOrder, d.pkg)
			pkgLabels[d.pkg] = pkgLabel(d.pkg, d.pkgVersion)
		}
		missingByPkg[d.pkg] = append(missingByPkg[d.pkg], d.dep)
	}
	for _, pkg := range missingOrder {
		deps := missingByPkg[pkg]
		noun := "dependency"
		if len(deps) != 1 {
			noun = "dependencies"
		}
		fmt.Fprintf(os.Stderr,
			"warning: %s declares %d Java %s its install set omits:\n  %s\n",
			pkgLabels[pkg], len(deps), noun, formatCoords(deps))
		if fallbackEnabled {
			fmt.Fprintln(os.Stderr,
				"  Installed from the package manifest as a fallback (--no-manifest-fallback to disable).")
		} else {
			fmt.Fprintln(os.Stderr,
				"  NOT installed (--no-manifest-fallback is set); the package may be broken.")
		}
		fmt.Fprintln(os.Stderr,
			"  The registry metadata is stale — ask the publisher to re-publish so the registry record is authoritative.")
	}

	for _, d := range divs {
		switch d.class {
		case classVersionMismatch:
			fmt.Fprintf(os.Stderr,
				"warning: %s declares %s@%s but the install set pins @%s; keeping the resolved version.\n",
				pkgLabel(d.pkg, d.pkgVersion), d.dep.Key(), d.dep.Version, d.installVersion)
		case classExtra:
			fmt.Fprintf(os.Stderr,
				"warning: install set includes %s@%s which no package manifest declares; installed as-is.\n",
				d.dep.Key(), d.dep.Version)
		}
	}
}

// reportFGLDivergences warns when a package's bundled manifest declares an FGL
// dependency that was not resolved/installed. Unlike Java, a missing
// transitive FGL dependency cannot be recovered additively (it would require
// re-entering the resolver mid-install), so this is warn-only — no fallback.
func reportFGLDivergences(fgl []declaredFGL, installedPkgs map[string]bool) {
	seen := map[string]bool{}
	for _, d := range fgl {
		if installedPkgs[d.name] {
			continue
		}
		key := d.pkg + "|" + d.name
		if seen[key] {
			continue
		}
		seen[key] = true
		fmt.Fprintf(os.Stderr,
			"warning: %s declares an FGL dependency %q that was not resolved/installed.\n"+
				"  fglpkg cannot recover an FGL dependency automatically — ask the publisher to re-publish.\n",
			d.pkg, d.name)
	}
}

// maxCoordsShown caps how many coordinates a single missing-dependency warning
// lists inline before summarising the remainder as a count.
const maxCoordsShown = 8

// formatCoords renders JAR coordinates as "groupId:artifactId@version, …",
// truncating a long list with a "(N total)" summary so a package that omits
// many JARs does not flood the terminal.
func formatCoords(deps []manifest.JavaDependency) string {
	coords := make([]string, len(deps))
	for i, d := range deps {
		coords[i] = d.Key() + "@" + d.Version
	}
	if len(coords) <= maxCoordsShown {
		return strings.Join(coords, ", ")
	}
	return strings.Join(coords[:maxCoordsShown], ", ") +
		fmt.Sprintf(", … (%d total)", len(coords))
}

// pkgLabel renders a declaring package as "name@version", or just "name" when
// the version is unknown.
func pkgLabel(pkg, version string) string {
	if version == "" {
		return pkg
	}
	return pkg + "@" + version
}
