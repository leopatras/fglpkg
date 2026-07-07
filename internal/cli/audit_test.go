package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/4js-mikefolcher/fglpkg/internal/audit"
	"github.com/4js-mikefolcher/fglpkg/internal/lockfile"
)

// writeLockfileForAudit writes a minimal fglpkg.lock at dir containing
// the provided JAR entries.
func writeLockfileForAudit(t *testing.T, dir string, jars []lockfile.LockedJAR) {
	t.Helper()
	lf := &lockfile.LockFile{
		Version:       1,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		GeneroVersion: "4.0.0",
		RootManifest:  lockfile.RootEntry{Name: "demo", Version: "0.1.0"},
		Packages:      []lockfile.LockedPackage{},
		JARs:          jars,
	}
	if err := lf.Save(dir); err != nil {
		t.Fatalf("write lockfile: %v", err)
	}
}

// osvStubResponse is the minimal OSV.dev /v1/query shape consumed by
// the audit package. Tests build instances of this and feed them via
// osvStubServer.
type osvStubResponse struct {
	Vulns []osvStubVuln `json:"vulns"`
}

type osvStubVuln struct {
	ID               string                   `json:"id"`
	Summary          string                   `json:"summary"`
	Details          string                   `json:"details,omitempty"`
	Aliases          []string                 `json:"aliases,omitempty"`
	References       []map[string]string      `json:"references,omitempty"`
	DatabaseSpecific osvStubDatabaseSpecific  `json:"database_specific,omitempty"`
}

type osvStubDatabaseSpecific struct {
	Severity string `json:"severity"`
}

// osvStubServer returns a test server that responds to every OSV
// query with vulnsForPURL[purl] (empty slice → clean response). It
// also records every PURL it sees in *purlsSeen for assertions.
func osvStubServer(t *testing.T, vulnsForPURL map[string][]osvStubVuln, purlsSeen *[]string) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Package struct {
				PURL string `json:"purl"`
			} `json:"package"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if purlsSeen != nil {
			mu.Lock()
			*purlsSeen = append(*purlsSeen, req.Package.PURL)
			mu.Unlock()
		}
		_ = json.NewEncoder(w).Encode(osvStubResponse{Vulns: vulnsForPURL[req.Package.PURL]})
	}))
}

// exitCode returns the ExitError code from err, or 0 if err is nil,
// or -1 if err is a plain error without an exit code attached.
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		return ee.Code
	}
	return -1
}

func TestAuditFlagParsing(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		f, err := parseAuditFlags(nil)
		if err != nil {
			t.Fatalf("parseAuditFlags(nil) error: %v", err)
		}
		if f.severity != audit.SeverityMedium {
			t.Errorf("default severity = %q, want medium", f.severity)
		}
		if f.jsonOut || f.production || f.offline {
			t.Errorf("default flags should all be false, got %+v", f)
		}
	})
	t.Run("severity_valid", func(t *testing.T) {
		f, err := parseAuditFlags([]string{"--severity=high"})
		if err != nil {
			t.Fatalf("parseAuditFlags error: %v", err)
		}
		if f.severity != audit.SeverityHigh {
			t.Errorf("severity = %q, want high", f.severity)
		}
	})
	t.Run("severity_invalid", func(t *testing.T) {
		_, err := parseAuditFlags([]string{"--severity=urgent"})
		if err == nil {
			t.Fatal("expected error for invalid severity, got nil")
		}
	})
	t.Run("unknown_arg", func(t *testing.T) {
		_, err := parseAuditFlags([]string{"--what"})
		if err == nil {
			t.Fatal("expected error for unknown arg, got nil")
		}
	})
}

func TestCmdAuditMissingLockfile(t *testing.T) {
	dir := t.TempDir()
	chdirTest(t, dir)
	err := cmdAudit(nil)
	if got := exitCode(err); got != 2 {
		t.Fatalf("exit code = %d, want 2 (missing lockfile)", got)
	}
	if !strings.Contains(err.Error(), "fglpkg.lock") {
		t.Errorf("err = %v, want one mentioning fglpkg.lock", err)
	}
}

func TestCmdAuditOffline(t *testing.T) {
	dir := t.TempDir()
	chdirTest(t, dir)
	writeLockfileForAudit(t, dir, nil)
	err := cmdAudit([]string{"--offline"})
	if got := exitCode(err); got != 2 {
		t.Fatalf("exit code = %d, want 2 (--offline unsupported)", got)
	}
}

func TestCmdAuditCleanTreeExit0(t *testing.T) {
	dir := t.TempDir()
	chdirTest(t, dir)
	writeLockfileForAudit(t, dir, []lockfile.LockedJAR{
		{Key: "com.example:foo", GroupID: "com.example", ArtifactID: "foo", Version: "1.0.0"},
	})

	ts := osvStubServer(t, nil, nil) // empty map → all queries return clean
	defer ts.Close()
	t.Setenv("FGLPKG_AUDIT_URL", ts.URL)

	if err := cmdAudit(nil); err != nil {
		t.Fatalf("cmdAudit error: %v (exit %d)", err, exitCode(err))
	}
}

func TestCmdAuditFindingsExit1(t *testing.T) {
	dir := t.TempDir()
	chdirTest(t, dir)
	writeLockfileForAudit(t, dir, []lockfile.LockedJAR{
		{Key: "com.example:foo", GroupID: "com.example", ArtifactID: "foo", Version: "1.0.0"},
	})

	ts := osvStubServer(t, map[string][]osvStubVuln{
		"pkg:maven/com.example/foo@1.0.0": {{
			ID:               "GHSA-aaaa",
			Summary:          "Bad bug",
			Aliases:          []string{"CVE-2024-1"},
			DatabaseSpecific: osvStubDatabaseSpecific{Severity: "HIGH"},
		}},
	}, nil)
	defer ts.Close()
	t.Setenv("FGLPKG_AUDIT_URL", ts.URL)

	err := cmdAudit(nil)
	if got := exitCode(err); got != 1 {
		t.Fatalf("exit code = %d, want 1 (high finding at default medium floor)", got)
	}
}

// TestCmdAuditSeverityFloor verifies that a medium finding does NOT
// fail when --severity=high.
func TestCmdAuditSeverityFloor(t *testing.T) {
	dir := t.TempDir()
	chdirTest(t, dir)
	writeLockfileForAudit(t, dir, []lockfile.LockedJAR{
		{Key: "com.example:foo", GroupID: "com.example", ArtifactID: "foo", Version: "1.0.0"},
	})

	ts := osvStubServer(t, map[string][]osvStubVuln{
		"pkg:maven/com.example/foo@1.0.0": {{
			ID:               "GHSA-bbbb",
			Summary:          "Mid bug",
			Aliases:          []string{"CVE-2024-2"},
			DatabaseSpecific: osvStubDatabaseSpecific{Severity: "MODERATE"},
		}},
	}, nil)
	defer ts.Close()
	t.Setenv("FGLPKG_AUDIT_URL", ts.URL)

	err := cmdAudit([]string{"--severity=high"})
	if got := exitCode(err); got != 0 {
		t.Fatalf("exit code = %d (err=%v), want 0 (medium below high floor)", got, err)
	}
}

func TestCmdAuditUpstreamError(t *testing.T) {
	dir := t.TempDir()
	chdirTest(t, dir)
	writeLockfileForAudit(t, dir, []lockfile.LockedJAR{
		{Key: "com.example:foo", GroupID: "com.example", ArtifactID: "foo", Version: "1.0.0"},
	})

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer ts.Close()
	t.Setenv("FGLPKG_AUDIT_URL", ts.URL)

	err := cmdAudit(nil)
	if got := exitCode(err); got != 2 {
		t.Fatalf("exit code = %d, want 2 (network failure)", got)
	}
}

func TestCmdAuditProductionFilter(t *testing.T) {
	dir := t.TempDir()
	chdirTest(t, dir)
	writeLockfileForAudit(t, dir, []lockfile.LockedJAR{
		{Key: "com.example:prod", GroupID: "com.example", ArtifactID: "prod", Version: "1.0.0"},
		{Key: "com.example:dev", GroupID: "com.example", ArtifactID: "dev", Version: "1.0.0", Scope: "dev"},
	})

	var seen []string
	ts := osvStubServer(t, nil, &seen)
	defer ts.Close()
	t.Setenv("FGLPKG_AUDIT_URL", ts.URL)

	if err := cmdAudit([]string{"--production"}); err != nil {
		t.Fatalf("cmdAudit error: %v (exit %d)", err, exitCode(err))
	}
	if len(seen) != 1 || !strings.Contains(seen[0], "/prod@1.0.0") {
		t.Errorf("audited coords = %v, want only the prod JAR", seen)
	}
}

func TestWriteAuditJSONShape(t *testing.T) {
	var buf bytes.Buffer
	findings := []audit.Finding{
		{
			Coordinate: "pkg:maven/com.example/foo@1.0.0",
			GroupID:    "com.example",
			ArtifactID: "foo",
			Version:    "1.0.0",
			ID:         "GHSA-aaaa",
			CVE:        "CVE-2024-1",
			Title:      "Bug",
			Severity:   audit.SeverityCritical,
		},
	}
	if err := writeAuditJSON(&buf, findings, 1); err != nil {
		t.Fatalf("writeAuditJSON error: %v", err)
	}
	var out auditReport
	if err := json.Unmarshal(buf.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON output: %v", err)
	}
	if out.SchemaVersion != 1 {
		t.Errorf("schemaVersion = %d, want 1", out.SchemaVersion)
	}
	if out.Source != audit.SourceLabel {
		t.Errorf("source = %q, want %q", out.Source, audit.SourceLabel)
	}
	if out.JARsAudited != 1 {
		t.Errorf("jarsAudited = %d, want 1", out.JARsAudited)
	}
	if out.Summary.Critical != 1 {
		t.Errorf("summary.critical = %d, want 1", out.Summary.Critical)
	}
	if len(out.Findings) != 1 || out.Findings[0].ID != "GHSA-aaaa" {
		t.Errorf("findings = %+v", out.Findings)
	}
	if len(out.Notes) == 0 {
		t.Error("notes should mention BDL coverage gap")
	}
}

func TestSortFindingsBySeverity(t *testing.T) {
	fs := []audit.Finding{
		{Severity: audit.SeverityLow, Coordinate: "a", ID: "1"},
		{Severity: audit.SeverityCritical, Coordinate: "b", ID: "2"},
		{Severity: audit.SeverityMedium, Coordinate: "c", ID: "3"},
		{Severity: audit.SeverityHigh, Coordinate: "d", ID: "4"},
	}
	sortFindings(fs)
	got := []string{fs[0].Severity, fs[1].Severity, fs[2].Severity, fs[3].Severity}
	want := []string{audit.SeverityCritical, audit.SeverityHigh, audit.SeverityMedium, audit.SeverityLow}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("sorted[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
