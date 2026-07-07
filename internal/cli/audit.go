package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/4js-mikefolcher/fglpkg/internal/audit"
	"github.com/4js-mikefolcher/fglpkg/internal/lockfile"
)

// auditFlags holds the parsed flags specific to `fglpkg audit`.
type auditFlags struct {
	jsonOut    bool
	severity   string
	production bool
	offline    bool
}

// auditReport is the JSON shape emitted by `fglpkg audit --json`.
// schemaVersion lets future fields be added without breaking CI parsers.
type auditReport struct {
	SchemaVersion int             `json:"schemaVersion"`
	AuditedAt     string          `json:"auditedAt"`
	Source        string          `json:"source"`
	JARsAudited   int             `json:"jarsAudited"`
	Findings      []audit.Finding `json:"findings"`
	Summary       auditCounts     `json:"summary"`
	Notes         []string        `json:"notes,omitempty"`
}

type auditCounts struct {
	Critical int `json:"critical"`
	High     int `json:"high"`
	Medium   int `json:"medium"`
	Low      int `json:"low"`
}

const bdlNotScanned = "BDL packages were not scanned (no advisory database available yet)."

// cmdAudit cross-checks installed Java JAR dependencies against the
// Sonatype OSS Index v3 advisory database and reports any known
// vulnerabilities.
//
//	fglpkg audit                                Default: severity floor = medium
//	fglpkg audit --json                         Machine-readable JSON output
//	fglpkg audit --severity=<low|medium|high|critical>
//	fglpkg audit --production                   Skip dev-scoped JARs
//
// Exit codes:
//
//	0 = no findings at or above the severity floor
//	1 = at least one finding at or above the severity floor
//	2 = command failed (missing lockfile, network error, etc.)
func cmdAudit(args []string) error {
	flags, err := parseAuditFlags(args)
	if err != nil {
		return &ExitError{Code: 2, Err: err}
	}
	if flags.offline {
		return &ExitError{Code: 2, Err: fmt.Errorf("--offline mode not yet supported")}
	}

	projectDir, err := os.Getwd()
	if err != nil {
		return &ExitError{Code: 2, Err: fmt.Errorf("cannot determine working directory: %w", err)}
	}
	if !lockfile.Exists(projectDir) {
		return &ExitError{Code: 2, Err: fmt.Errorf("no %s in current directory; run `fglpkg install` first", lockfile.Filename)}
	}
	lf, err := lockfile.Load(projectDir)
	if err != nil {
		return &ExitError{Code: 2, Err: fmt.Errorf("failed to load %s: %w", lockfile.Filename, err)}
	}

	jars := filterAuditJARs(lf.JARs, flags.production)

	if !flags.jsonOut && len(jars) == 0 {
		fmt.Println("No Java JARs to audit.")
		fmt.Println(bdlNotScanned)
		return nil
	}

	findings, err := audit.Audit(jars, audit.Options{
		URL: os.Getenv("FGLPKG_AUDIT_URL"),
	})
	if err != nil {
		return &ExitError{Code: 2, Err: fmt.Errorf("audit failed: %w", err)}
	}
	sortFindings(findings)

	if flags.jsonOut {
		if err := writeAuditJSON(os.Stdout, findings, len(jars)); err != nil {
			return &ExitError{Code: 2, Err: err}
		}
	} else {
		writeAuditTable(os.Stdout, findings, len(jars))
	}

	threshold := audit.SeverityRank(flags.severity)
	for _, f := range findings {
		if audit.SeverityRank(f.Severity) >= threshold {
			return &ExitError{Code: 1, Err: fmt.Errorf(
				"%d vulnerabilit%s found at severity >= %s",
				countAtOrAbove(findings, threshold),
				pluralY(countAtOrAbove(findings, threshold)),
				flags.severity,
			)}
		}
	}
	return nil
}

// parseAuditFlags parses arguments for `fglpkg audit`. Unknown
// arguments return an error so typos don't silently produce a
// permissive run.
func parseAuditFlags(args []string) (auditFlags, error) {
	f := auditFlags{severity: audit.SeverityMedium}
	for _, a := range args {
		switch {
		case a == "--json":
			f.jsonOut = true
		case a == "--production", a == "--prod":
			f.production = true
		case a == "--offline":
			f.offline = true
		case strings.HasPrefix(a, "--severity="):
			sev := strings.TrimPrefix(a, "--severity=")
			if !audit.ValidSeverity(sev) {
				return f, fmt.Errorf("invalid --severity %q (want: low, medium, high, critical)", sev)
			}
			f.severity = sev
		default:
			return f, fmt.Errorf("unknown argument %q", a)
		}
	}
	return f, nil
}

// filterAuditJARs drops dev-scoped JARs when production is set.
// optional-scoped JARs are always included.
func filterAuditJARs(in []lockfile.LockedJAR, production bool) []lockfile.LockedJAR {
	if !production {
		return in
	}
	out := make([]lockfile.LockedJAR, 0, len(in))
	for _, j := range in {
		if j.Scope == "dev" {
			continue
		}
		out = append(out, j)
	}
	return out
}

// sortFindings yields a stable order: most severe first, then by
// coordinate, then by id. Useful both for readable output and for
// deterministic test assertions.
func sortFindings(fs []audit.Finding) {
	sort.SliceStable(fs, func(i, j int) bool {
		ri, rj := audit.SeverityRank(fs[i].Severity), audit.SeverityRank(fs[j].Severity)
		if ri != rj {
			return ri > rj
		}
		if fs[i].Coordinate != fs[j].Coordinate {
			return fs[i].Coordinate < fs[j].Coordinate
		}
		return fs[i].ID < fs[j].ID
	})
}

func writeAuditTable(w io.Writer, findings []audit.Finding, jarsAudited int) {
	if len(findings) == 0 {
		fmt.Fprintf(w, "Audited %d Java JAR%s against OSV.dev.\n",
			jarsAudited, pluralS(jarsAudited))
		fmt.Fprintln(w, "No known vulnerabilities found.")
		fmt.Fprintln(w, bdlNotScanned)
		return
	}

	// Group by coordinate for compact display.
	byCoord := map[string][]audit.Finding{}
	order := make([]string, 0)
	for _, f := range findings {
		if _, ok := byCoord[f.Coordinate]; !ok {
			order = append(order, f.Coordinate)
		}
		byCoord[f.Coordinate] = append(byCoord[f.Coordinate], f)
	}

	fmt.Fprintf(w, "%d vulnerabilit%s found in %d package%s:\n\n",
		len(findings), pluralY(len(findings)),
		len(order), pluralS(len(order)))
	for _, coord := range order {
		group := byCoord[coord]
		fmt.Fprintf(w, "  %s:%s  %s\n",
			group[0].GroupID, group[0].ArtifactID, group[0].Version)
		for _, f := range group {
			id := f.CVE
			if id == "" {
				id = f.ID
			}
			fmt.Fprintf(w, "    %s  %-8s  %s\n", id, f.Severity, f.Title)
			if f.Reference != "" {
				fmt.Fprintf(w, "        %s\n", f.Reference)
			}
		}
	}
	c := tallyCounts(findings)
	fmt.Fprintf(w, "\nSummary: %d critical, %d high, %d medium, %d low\n",
		c.Critical, c.High, c.Medium, c.Low)
	fmt.Fprintln(w, bdlNotScanned)
}

func writeAuditJSON(w io.Writer, findings []audit.Finding, jarsAudited int) error {
	rep := auditReport{
		SchemaVersion: 1,
		AuditedAt:     time.Now().UTC().Format(time.RFC3339),
		Source:        audit.SourceLabel,
		JARsAudited:   jarsAudited,
		Findings:      findings,
		Summary:       tallyCounts(findings),
		Notes:         []string{bdlNotScanned},
	}
	if rep.Findings == nil {
		rep.Findings = []audit.Finding{}
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(rep)
}

func tallyCounts(findings []audit.Finding) auditCounts {
	var c auditCounts
	for _, f := range findings {
		switch f.Severity {
		case audit.SeverityCritical:
			c.Critical++
		case audit.SeverityHigh:
			c.High++
		case audit.SeverityMedium:
			c.Medium++
		case audit.SeverityLow:
			c.Low++
		}
	}
	return c
}

func countAtOrAbove(findings []audit.Finding, threshold int) int {
	n := 0
	for _, f := range findings {
		if audit.SeverityRank(f.Severity) >= threshold {
			n++
		}
	}
	return n
}

// pluralS returns "s" when n != 1.
func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
