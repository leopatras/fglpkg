package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
)

// cmdLint validates fglpkg.json and the package it would produce, printing an
// errors + warnings report. It exits non-zero when any error is found, so it
// can gate CI. The same checks run automatically inside `pack` and `publish`
// (see enforceLint) so the footguns cannot be skipped.
//
//	fglpkg lint      (alias: fglpkg check)
func cmdLint(args []string) error {
	for _, a := range args {
		return fmt.Errorf("unexpected argument %q (fglpkg lint takes no arguments)", a)
	}

	m, err := manifest.Load(".")
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no %s in current directory — run 'fglpkg init' first", manifest.Filename)
		}
		// A load failure is the friendly, field-named error from manifest.Load
		// (GIS-269). Report it as the single blocking error — semantic checks
		// cannot run on a manifest that does not decode.
		var r manifest.Report
		r.Errorf("", "%s", err.Error())
		printLintReport(os.Stdout, &r)
		return &ExitError{Code: 1, Err: lintSummaryErr(&r)}
	}

	report := lintManifest(m, ".")
	printLintReport(os.Stdout, report)
	if report.HasErrors() {
		return &ExitError{Code: 1, Err: lintSummaryErr(report)}
	}
	return nil
}

// builtPackage is the result of staging + zipping the package once. lint builds
// it to inspect the authoritative staged layout; pack/publish then reuse the
// same bytes rather than repeating the (identical, deterministic) staging walk.
type builtPackage struct {
	zip      []byte
	checksum string
	entries  []zipEntry
}

// lintManifest runs the full lint pass and returns the combined report. It is
// the report-only view used by `fglpkg lint` and the tests; pack/publish call
// lintProject directly so they can reuse the built package.
func lintManifest(m *manifest.Manifest, srcDir string) *manifest.Report {
	report, _, buildErr := lintProject(m, srcDir)
	if buildErr != nil {
		report.Errorf("", "package cannot be built: %s", buildErr.Error())
	}
	return report
}

// lintProject runs the full lint pass for a project rooted at srcDir. It merges
// the manifest-level diagnostics (m.LintInto) with the filesystem-derived ones,
// which need the staging/walk primitives that live in this package, and builds
// the package once.
//
// It returns three things kept deliberately separate:
//   - the diagnostics report (manifest + semantic findings),
//   - the built package (nil when the build failed), for callers to reuse,
//   - the raw build error (nil on success). Staging/build failures are returned
//     here, NOT folded into the report as a validation error, so callers can
//     report them as build errors instead of mislabeling them "manifest failed
//     validation".
func lintProject(m *manifest.Manifest, srcDir string) (*manifest.Report, *builtPackage, error) {
	report := &manifest.Report{}
	m.LintInto(report)

	root := m.Root
	if root == "" {
		root = "."
	}

	// root / importRoot existence are friendly hints. A missing root that
	// actually breaks staging is surfaced authoritatively by the build below
	// (as a build error that blocks pack/publish); we no longer bail out here,
	// which used to let `fglpkg lint` pass a project that pack/publish cannot
	// build. For a pure-webcomponent package the BDL walk is skipped, so a
	// missing root is only ever the advisory warning.
	if fi, err := os.Stat(filepath.Join(srcDir, root)); err != nil || !fi.IsDir() {
		report.Warnf("root", "root directory %q does not exist", root)
	}
	if m.ImportRoot != "" {
		if fi, err := os.Stat(filepath.Join(srcDir, m.ImportRoot)); err != nil || !fi.IsDir() {
			report.Warnf("importRoot", "importRoot directory %q does not exist", m.ImportRoot)
		}
	}

	// Zero-match globs. Only explicit patterns are checked — the default
	// *.42m/*.42f/*.sch set is not user-authored, so its not-matching is
	// covered by the no-modules error instead.
	lintZeroMatchFiles(m, srcDir, root, report)
	lintZeroMatchDocs(m, srcDir, report)

	// Build the package once to get the authoritative staged layout. A staging
	// error (importRoot escape, missing bin script, webcomponent entry point,
	// path collision, nonexistent root, …) is a real, blocking build failure —
	// returned as buildErr so the caller frames it honestly.
	zipData, checksum, err := buildPackageZip(m)
	if err != nil {
		return report, nil, err
	}
	entries, err := listZipEntries(zipData)
	if err != nil {
		return report, nil, err
	}
	built := &builtPackage{zip: zipData, checksum: checksum, entries: entries}

	// Collect the basenames of staged .42m modules for the program and
	// no-modules checks.
	modules := make(map[string]bool)
	hasBDL := false
	for _, e := range entries {
		base := filepath.Base(e.name)
		if strings.HasSuffix(base, ".42m") {
			modules[base] = true
		}
		if isBDLSourceFile(base) {
			hasBDL = true
		}
	}

	// No BDL modules / empty package. Scoped with the same gate stagePackage
	// uses to decide whether to run the BDL walk, so pure-webcomponent packages
	// (which legitimately ship no BDL) are exempt.
	if (m.HasBDLContent() || !m.HasWebcomponents()) && !hasBDL {
		report.Errorf("", "package would contain no BDL modules or source files — "+
			"check `root` and `files`, or add modules under %q", root)
	}

	// Program resolution: each declared program must have a staged <name>.42m.
	for _, p := range m.Programs {
		if !modules[p+".42m"] {
			report.Warnf("programs", "declared program %q does not resolve to a staged %s.42m under %q",
				p, p, root)
		}
	}

	return report, built, nil
}

// lintZeroMatchFiles warns for each explicit `files` pattern that matches no
// file under root, reusing the exact matcher and skip rules the pack staging
// walk uses (filesPatternMatch + .fglpkgignore + the .fglpkg/ artifact-dir
// skip) so lint agrees with what `pack` produces.
func lintZeroMatchFiles(m *manifest.Manifest, srcDir, root string, report *manifest.Report) {
	if len(m.Files) == 0 {
		return
	}
	ignore, err := loadIgnore(srcDir)
	if err != nil {
		return // a broken .fglpkgignore surfaces elsewhere; skip zero-match here
	}
	hits := make(map[string]int, len(m.Files))
	for _, p := range m.Files {
		hits[p] = 0
	}
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if isPackArtifactDir(path) || dirShouldBeSkipped(ignore, path) {
				return filepath.SkipDir
			}
			return nil
		}
		base := filepath.Base(path)
		relToRoot, relToRootErr := filepath.Rel(root, path)
		relPath, relErr := filepath.Rel(".", path)
		if relErr != nil {
			relPath = path
		}
		if ignore.shouldExclude(relPath, false) {
			return nil
		}
		for _, p := range m.Files {
			if filesPatternMatch(p, base, relToRoot, relToRootErr) {
				hits[p]++
			}
		}
		return nil
	})
	for _, p := range m.Files {
		if hits[p] == 0 {
			report.Warnf("files", "pattern %q matched no files under root %q", p, root)
		}
	}
}

// lintZeroMatchDocs warns for each `docs` pattern that matches no file,
// mirroring stageDocFiles (matchGlob against the project-relative path).
func lintZeroMatchDocs(m *manifest.Manifest, srcDir string, report *manifest.Report) {
	if len(m.Docs) == 0 {
		return
	}
	ignore, err := loadIgnore(srcDir)
	if err != nil {
		return
	}
	hits := make(map[string]int, len(m.Docs))
	for _, p := range m.Docs {
		hits[p] = 0
	}
	_ = filepath.Walk(".", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if isPackArtifactDir(path) || dirShouldBeSkipped(ignore, path) {
				return filepath.SkipDir
			}
			return nil
		}
		relPath, relErr := filepath.Rel(".", path)
		if relErr != nil {
			relPath = path
		}
		if ignore.shouldExclude(relPath, false) {
			return nil
		}
		for _, p := range m.Docs {
			if matchGlob(p, relPath) {
				hits[p]++
			}
		}
		return nil
	})
	for _, p := range m.Docs {
		if hits[p] == 0 {
			report.Warnf("docs", "pattern %q matched no files", p)
		}
	}
}

// bdlSourceExtensions are the file kinds that count as "BDL content" for the
// no-modules check: compiled modules/forms/schemas and 4gl/per source.
var bdlSourceExtensions = []string{".42m", ".42f", ".42s", ".sch", ".4gl", ".per"}

func isBDLSourceFile(base string) bool {
	for _, ext := range bdlSourceExtensions {
		if strings.HasSuffix(base, ext) {
			return true
		}
	}
	return false
}

// printLintReport writes the human-readable errors + warnings report, using the
// repo's inline glyph convention (✓ clean, ⚠ warning, ✗ error).
func printLintReport(w io.Writer, r *manifest.Report) {
	for _, d := range r.Errors() {
		fmt.Fprintf(w, "✗ %s\n", formatDiagnostic(d))
	}
	for _, d := range r.Warnings() {
		fmt.Fprintf(w, "⚠ %s\n", formatDiagnostic(d))
	}
	nErr, nWarn := len(r.Errors()), len(r.Warnings())
	if nErr == 0 && nWarn == 0 {
		fmt.Fprintln(w, "✓ fglpkg.json looks good")
		return
	}
	fmt.Fprintf(w, "\n%d error%s, %d warning%s\n",
		nErr, plural(nErr), nWarn, plural(nWarn))
}

// formatDiagnostic renders a single diagnostic as "field: message" (or just
// the message for whole-manifest findings).
func formatDiagnostic(d manifest.Diagnostic) string {
	if d.Field == "" {
		return d.Message
	}
	// Avoid a doubled "field: field: …" when the message already leads with it.
	if strings.HasPrefix(d.Message, d.Field+":") || strings.HasPrefix(d.Message, d.Field+" ") {
		return d.Message
	}
	return d.Field + ": " + d.Message
}

// lintSummaryErr builds the one-line error that drives the non-zero exit and is
// printed to stderr by main.
func lintSummaryErr(r *manifest.Report) error {
	n := len(r.Errors())
	return fmt.Errorf("fglpkg lint found %d error%s", n, plural(n))
}

// enforceLint runs the shared lint pass for pack/publish: it prints any
// warnings loudly to stderr and returns a blocking error when the manifest
// fails validation, so the package is never silently built/uploaded with a
// footgun. On success it returns the package built during the lint pass, so the
// caller reuses it instead of staging + zipping a second time.
//
// A staging/build failure is returned as a build error (not framed as a
// manifest-validation problem); only genuine manifest/semantic errors get the
// "manifest failed validation" framing.
func enforceLint(m *manifest.Manifest, srcDir string) (*builtPackage, error) {
	report, built, buildErr := lintProject(m, srcDir)
	for _, d := range report.Warnings() {
		fmt.Fprintf(os.Stderr, "warning: %s\n", formatDiagnostic(d))
	}
	if buildErr != nil {
		return nil, fmt.Errorf("cannot build package zip: %w", buildErr)
	}
	if report.HasErrors() {
		var b strings.Builder
		b.WriteString("manifest failed validation:")
		for _, d := range report.Errors() {
			b.WriteString("\n  - " + formatDiagnostic(d))
		}
		return nil, fmt.Errorf("%s", b.String())
	}
	return built, nil
}

// plural returns "s" when n != 1, for simple message pluralization.
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
