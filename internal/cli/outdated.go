package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/4js-mikefolcher/fglpkg/internal/config"
	"github.com/4js-mikefolcher/fglpkg/internal/lockfile"
	"github.com/4js-mikefolcher/fglpkg/internal/manifest"
	"github.com/4js-mikefolcher/fglpkg/internal/provider"
	"github.com/4js-mikefolcher/fglpkg/internal/registry"
	"github.com/4js-mikefolcher/fglpkg/internal/semver"
)

// outdatedRow describes one dependency's upgrade status.
type outdatedRow struct {
	Name       string `json:"name"`
	Constraint string `json:"constraint"`
	Current    string `json:"current"`
	Wanted     string `json:"wanted"`
	Latest     string `json:"latest"`
	Status     string `json:"status"`
	// Deprecated/MovedTo flag an installed version the registry marks
	// npm-style deprecated (advisory). Surfaced as a Notes column + JSON.
	Deprecated bool   `json:"deprecated,omitempty"`
	MovedTo    string `json:"movedTo,omitempty"`
}

// cmdOutdated compares each FGL dependency declared in fglpkg.json
// against the registry, reporting which ones have newer versions
// available. The command exits non-zero if any dependency is outdated,
// so it can be used as a CI gate.
//
//	fglpkg outdated                    → table output to stdout
//	fglpkg outdated --json             → JSON array to stdout
//
// Java dependencies are not included; they have exact version pins and
// would require a separate Maven Central query.
func cmdOutdated(args []string) error {
	var jsonOut bool
	for _, a := range args {
		switch a {
		case "--json":
			jsonOut = true
		default:
			return fmt.Errorf("unknown argument %q", a)
		}
	}

	m, err := manifest.Load(".")
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no %s in current directory", manifest.Filename)
		}
		return fmt.Errorf("failed to load %s: %w", manifest.Filename, err)
	}
	if len(m.Dependencies.FGL) == 0 {
		fmt.Println("No FGL dependencies declared.")
		return nil
	}

	// Current versions come from the lockfile — the deterministic record
	// of what was last installed. If the lockfile is missing we still
	// fetch registry data and mark everything as "not installed". The lock
	// also records each package's source repository, so an Artifactory-sourced
	// package is checked against its own repo rather than GI (spec §11).
	projectDir, _ := os.Getwd()
	current := map[string]string{}
	sources := map[string]string{}
	if lockfile.Exists(projectDir) {
		lf, err := lockfile.Load(projectDir)
		if err == nil {
			for _, p := range lf.Packages {
				current[p.Name] = p.Version
				sources[p.Name] = p.Registry
			}
		}
	}

	// Multi-provider set (nil in the single-registry case → GI-only client).
	home, _ := fglpkgHome()
	rs, _, _, rsErr := buildRepositorySet(home, m)
	if rsErr != nil {
		fmt.Fprintf(os.Stderr, "warning: ignoring registries config: %v\n", rsErr)
	}

	names := make([]string, 0, len(m.Dependencies.FGL))
	for n := range m.Dependencies.FGL {
		names = append(names, n)
	}
	sort.Strings(names)

	rows := make([]outdatedRow, 0, len(names))
	outdatedCount := 0

	for _, name := range names {
		row := buildOutdatedRow(rs, name, m.Dependencies.FGL[name], current[name], sources[name])
		rows = append(rows, row)
		if row.Status != "ok" {
			outdatedCount++
		}
	}

	if jsonOut {
		data, err := json.MarshalIndent(rows, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(data))
	} else {
		printOutdatedTable(rows)
	}

	if outdatedCount > 0 {
		plural := ""
		if outdatedCount > 1 {
			plural = "s"
		}
		return fmt.Errorf("%d dependenc%s out of date", outdatedCount, pluralY(outdatedCount)+plural)
	}
	return nil
}

// buildOutdatedRow fetches the version list for one package and computes
// its current/wanted/latest/status fields. When a multi-provider set is
// configured (rs != nil) the package is checked against its locked source
// repository (sourceReg; "" ⇒ the built-in GI registry) rather than always GI.
func buildOutdatedRow(rs *provider.RepositorySet, name, constraint, currentVer, sourceReg string) outdatedRow {
	row := outdatedRow{
		Name:       name,
		Constraint: constraint,
		Current:    currentVer,
	}
	if row.Current == "" {
		row.Current = "missing"
	}

	vl, err := outdatedVersionList(rs, name, sourceReg)
	if err != nil {
		row.Status = "registry error"
		return row
	}

	candidates := parseVersionStrings(vl.Versions)
	if len(candidates) == 0 {
		row.Status = "no published versions"
		return row
	}

	// Latest stable (release, not prerelease) — what a user would get
	// if they widened their constraint.
	if latest := newestStable(candidates); latest != nil {
		row.Latest = latest.String()
	} else {
		// No stable versions — fall back to the absolute newest.
		row.Latest = newest(candidates).String()
	}

	// Wanted = newest version satisfying the declared constraint.
	if c, err := semver.ParseConstraint(constraint); err == nil {
		if w, err := c.Latest(candidates); err == nil {
			row.Wanted = w.String()
		}
	}

	switch {
	case currentVer == "":
		row.Status = "not installed"
	case row.Wanted != "" && row.Wanted != currentVer:
		row.Status = "update available"
	case row.Latest != "" && row.Latest != currentVer:
		// Current satisfies the constraint, but a newer stable exists
		// outside the constraint (typically a major bump).
		row.Status = "major available"
	default:
		row.Status = "ok"
	}

	// Flag an installed version the registry marks deprecated. Best-effort and
	// GI-only: deprecation is a GI-registry concept (an Artifactory-sourced
	// package has none), and a lookup failure just leaves the row unflagged so
	// `outdated` never fails because of an advisory lookup.
	if currentVer != "" && isGISource(sourceReg) {
		if rs == nil {
			// Single-registry: the version list came from the GI package
			// detail, which already carries deprecation flags — no second
			// round-trip. A version is deprecated if its own flag OR the
			// whole-package flag is set (version successor wins).
			if e := vl.EntryFor(currentVer); e != nil && (e.Deprecated || vl.Deprecated) {
				row.Deprecated = true
				row.MovedTo = firstNonEmpty(e.MovedTo, vl.MovedTo)
			}
		} else if info, err := registry.FetchInfo(name, currentVer); err == nil && info.Deprecated {
			// Multi-provider: the version list came from the provider set (no
			// deprecation flags), so keep the best-effort GI lookup.
			row.Deprecated = true
			row.MovedTo = info.MovedTo
		}
	}
	return row
}

// isGISource reports whether a locked source repository name refers to the
// built-in GI registry ("" is the historical default; config.GIName is "gi").
func isGISource(sourceReg string) bool {
	return sourceReg == "" || sourceReg == config.GIName
}

// outdatedVersionList lists a package's versions from its locked source repo
// when a multi-provider set is configured, else via the GI-only client.
func outdatedVersionList(rs *provider.RepositorySet, name, sourceReg string) (*registry.VersionList, error) {
	if rs == nil {
		return registry.FetchVersionList(name)
	}
	cvs, err := rs.VersionsFrom(sourceReg, name)
	if err != nil {
		return nil, err
	}
	vs := make([]string, 0, len(cvs))
	for _, cv := range cvs {
		vs = append(vs, cv.Version.String())
	}
	return &registry.VersionList{Versions: vs}, nil
}

func parseVersionStrings(vs []string) []semver.Version {
	out := make([]semver.Version, 0, len(vs))
	for _, s := range vs {
		if v, err := semver.Parse(s); err == nil {
			out = append(out, v)
		}
	}
	return out
}

func newest(vs []semver.Version) *semver.Version {
	if len(vs) == 0 {
		return nil
	}
	best := vs[0]
	for i := 1; i < len(vs); i++ {
		if vs[i].GreaterThan(best) {
			best = vs[i]
		}
	}
	return &best
}

func newestStable(vs []semver.Version) *semver.Version {
	stable := make([]semver.Version, 0, len(vs))
	for _, v := range vs {
		if v.PreRelease == "" {
			stable = append(stable, v)
		}
	}
	return newest(stable)
}

func printOutdatedTable(rows []outdatedRow) {
	// The Notes column only appears when at least one row carries a note (a
	// deprecated installed version), so the common case is unchanged.
	showNotes := false
	for _, r := range rows {
		if r.Deprecated {
			showNotes = true
			break
		}
	}

	headers := []string{"Package", "Current", "Wanted", "Latest", "Status"}
	if showNotes {
		headers = append(headers, "Notes")
	}
	cells := make([][]string, len(rows))
	for i, r := range rows {
		cells[i] = []string{r.Name, r.Current, r.Wanted, r.Latest, r.Status}
		if showNotes {
			cells[i] = append(cells[i], deprecationNote(r))
		}
	}
	widths := make([]int, len(headers))
	for i, h := range headers {
		widths[i] = len(h)
	}
	for _, row := range cells {
		for i, c := range row {
			if len(c) > widths[i] {
				widths[i] = len(c)
			}
		}
	}

	printRow := func(cols []string) {
		parts := make([]string, len(cols))
		for i, c := range cols {
			parts[i] = fmt.Sprintf("%-*s", widths[i], c)
		}
		fmt.Println(strings.TrimRight(strings.Join(parts, "  "), " "))
	}
	printRow(headers)
	divider := make([]string, len(headers))
	for i, w := range widths {
		divider[i] = strings.Repeat("─", w)
	}
	printRow(divider)
	for _, row := range cells {
		printRow(row)
	}
}

// deprecationNote renders the Notes-column text for a row: "" when not
// deprecated, "deprecated" alone, or "deprecated → <successor>" with a
// --moved-to target.
func deprecationNote(r outdatedRow) string {
	if !r.Deprecated {
		return ""
	}
	if r.MovedTo != "" {
		return "deprecated → " + r.MovedTo
	}
	return "deprecated"
}

func pluralY(n int) string {
	if n == 1 {
		return "y"
	}
	return "ie"
}

// firstNonEmpty returns the first non-empty string among its arguments, or "".
func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
