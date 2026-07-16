# Spec: `fglpkg audit` (v1)

**Status:** ✅ Implemented — shipped ([internal/audit/](../internal/audit/))
**Date:** 2026-05-13
**Author:** Mike Folcher
**Tracking:** P0 #5 in [docs/market-readiness-gaps.md](../docs/market-readiness-gaps.md)

---

## Summary

Add a new CLI command, `fglpkg audit`, that cross-checks the project's installed Java JAR dependencies against a public vulnerability database and reports any known CVEs. Report-only in v1 — no lockfile mutation, no auto-fix. Exits non-zero when vulnerabilities are found, so the command can be wired into CI as a gate.

## Motivation

Enterprise security review will block adoption of any package manager that cannot answer "are there known vulnerabilities in my dependency tree?" The lockfile already records Maven coordinates (`groupId:artifactId:version`) for every resolved JAR — everything we need to query an advisory database. This is the last remaining P0 item that can ship without server-side work and is the credibility item enterprise sec teams ask for first.

## Goals

- One command (`fglpkg audit`) that surfaces CVEs in Java JARs of the current project.
- Useful in CI: predictable exit codes, `--json` output, severity threshold.
- Zero new credentials or registration to use it.
- Honest about what it can and cannot see (BDL packages are not covered in v1).

## Non-goals (v1)

- Auto-fix / lockfile rewriting — deferred. (Reconsider after v1 ships.)
- BDL package vulnerability data — no advisory store exists yet. Stub the path so it can be added later without a breaking CLI change.
- Multiple advisory sources — OSV.dev only in v1. NVD as a separate source is deferred (most CVEs of interest are already in OSV.dev via the GHSA feed).
- Offline / cached advisory data — every run hits the network.
- 2FA, signing verification, SBOM emission — separate workstreams.

## Out of scope: BDL packages

The lockfile records BDL packages (`LockedPackage`), but no public CVE feed indexes them. v1 audits only `LockFile.JARs`. The command prints one informational line stating BDL packages were not scanned so users don't assume otherwise. When a BDL advisory store exists (future work), the same command grows to cover it without a flag change.

## CLI surface

```
fglpkg audit                                 Audit installed Java JARs against OSV.dev.
fglpkg audit --json                          Emit a JSON report on stdout instead of a table.
fglpkg audit --severity=<low|medium|high|critical>
                                              Minimum severity that causes a non-zero exit. Default: "medium".
                                              Vulnerabilities below this floor are still reported but do not fail.
fglpkg audit --production                    Skip JARs whose lockfile scope is "dev". optional-scope JARs are
                                              always included.
fglpkg audit --offline                       Reserved for a future cached-advisory mode. v1: errors out with
                                              "offline mode not yet supported".
fglpkg audit --help                          Print usage.
```

Add `audit` to the dispatcher in [internal/cli/cli.go](../internal/cli/cli.go) and to the `help` text. Add a completion entry in [internal/cli/completion.go](../internal/cli/completion.go).

## Data source

**OSV.dev** (https://osv.dev/), v1 single-package query API.

- Endpoint: `POST https://api.osv.dev/v1/query`
- Auth: **none** (anonymous, free, no token).
- Request body (one package per request — OSV.dev returns full vuln details inline, so no follow-up lookups are needed):
  ```json
  { "package": { "purl": "pkg:maven/com.google.code.gson/gson@2.10.1" } }
  ```
- Response: `{"vulns": [...]}` where each vuln has `id` (GHSA-...), `summary`, `details`, `aliases` (includes CVE), `severity[]` (CVSS vectors), `references[]`, and `database_specific.severity` (`LOW|MODERATE|HIGH|CRITICAL` for GHSA-sourced entries).
- Per-JAR query: OSV.dev does not have a batch endpoint that returns full vuln details — `/v1/querybatch` returns vuln IDs only, requiring N+K follow-up calls. v1 issues one `/v1/query` request per deduplicated PURL. For typical projects (≤ ~30 JARs) this completes in under a second.
- Timeout: 30s per request. On HTTP error (non-2xx, timeout, network), abort with a clear error — never silently report "no vulnerabilities" on a failed query.

**Severity is taken from `database_specific.severity`** (GHSA-sourced advisories — the majority — set this field directly). When absent the finding defaults to `medium`, erring conservative so unclassified CVEs surface at the default floor rather than being silently demoted to `low` and skipped.

The endpoint URL is a package-level constant and may be overridden via env var to ease testing:

| Env var | Purpose |
|---|---|
| `FGLPKG_AUDIT_URL` | Override the OSV.dev endpoint (test fixtures, mirror). |

Intentionally undocumented for end users.

**Historical note:** an earlier draft of this spec targeted Sonatype OSS Index v3. That API was confirmed in May 2026 to no longer accept anonymous requests (HTTP 401 with empty body). OSV.dev was chosen as the replacement because it preserves the "zero new credentials" goal, is run by an open-source consortium with broader coverage, and emits richer per-vuln metadata.

## Algorithm

```
1. Load fglpkg.lock from cwd. If absent, error:
     "no fglpkg.lock; run `fglpkg install` first"
   Lock is the source of truth — manifest constraints are not enough.

2. Collect JAR coordinates from lock.JARs. Filter by --production if set
   (skip scope == "dev"). Skip duplicates.

3. If no JARs remain, print "No Java JARs to audit." and exit 0.

4. Convert each JAR to its purl: pkg:maven/<groupId>/<artifactId>@<version>
   (URL-encode any path-illegal characters in groupId/artifactId — Maven
    coords don't normally need it, but be defensive).

5. For each PURL, POST <url> with body { "package": { "purl": "..." } }.
   Aggregate the responses into a flat list of (coord, vulnerability) pairs.

6. For each vulnerability, derive a severity bucket from
   `database_specific.severity` (GHSA-sourced entries set this directly):
     CRITICAL → critical
     HIGH     → high
     MODERATE → medium    (GHSA uses "MODERATE", not "MEDIUM")
     LOW      → low
     absent   → medium    (fail-safe default; surfaces unclassified CVEs at the default floor)

7. Render output (see below).

8. Exit:
     0  if no vulnerabilities found OR none meet --severity threshold
     1  if any vulnerability has severity >= --severity threshold
     2  if the command itself failed (network, missing lockfile, etc.)
```

The auditor lives in a new internal package: `internal/audit/audit.go`. Public API:

```go
package audit

// Finding is one vulnerability against one component.
type Finding struct {
    Coordinate    string   // pkg:maven/...@...
    GroupID       string
    ArtifactID    string
    Version       string
    ID            string   // OSV/GHSA advisory id
    CVE           string   // first CVE alias, if any
    Title         string
    Description   string
    CVSSScore     float64  // unset in v1 (no CVSS parser)
    CVSSVector    string   // raw CVSS_V3 score string from OSV.dev
    Severity      string   // critical|high|medium|low
    Reference     string   // preferred ADVISORY URL
}

// Audit queries the advisory service and returns findings.
type Options struct {
    URL        string         // empty → default OSV.dev endpoint
    HTTPClient *http.Client   // nil → http.DefaultClient with 30s timeout
}

func Audit(jars []lockfile.LockedJAR, opts Options) ([]Finding, error)
```

The `internal/cli/audit.go` command translates flags into `Options`, calls `audit.Audit`, formats the output, and chooses an exit code.

## Output

### Table (default)

```
3 vulnerabilities found in 2 packages:

  com.fasterxml.jackson.core:jackson-databind  2.9.10
    CVE-2020-36518  high    Out-of-bounds write in jackson-databind
        https://github.com/advisories/GHSA-57j2-w4cx-62h2
    CVE-2022-42003  medium  Deeply nested arrays cause StackOverflowError
        https://github.com/advisories/GHSA-jjjh-jjxp-wpff

  org.apache.commons:commons-text  1.9
    CVE-2022-42889  critical  Apache Commons Text RCE (Text4Shell)
        https://github.com/advisories/GHSA-599f-7c49-w659

Summary: 1 critical, 1 high, 1 medium, 0 low
BDL packages were not scanned (no advisory database available yet).
```

When there are no findings:

```
Audited 5 Java JARs against OSV.dev.
No known vulnerabilities found.
BDL packages were not scanned (no advisory database available yet).
```

### JSON (`--json`)

```json
{
  "schemaVersion": 1,
  "auditedAt": "2026-05-14T12:34:56Z",
  "source": "osv.dev",
  "jarsAudited": 5,
  "findings": [
    {
      "coordinate": "pkg:maven/com.fasterxml.jackson.core/jackson-databind@2.9.10",
      "groupId": "com.fasterxml.jackson.core",
      "artifactId": "jackson-databind",
      "version": "2.9.10",
      "id": "GHSA-57j2-w4cx-62h2",
      "cve": "CVE-2020-36518",
      "title": "Out-of-bounds write in jackson-databind",
      "description": "...",
      "cvssVector": "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:N/I:N/A:H",
      "severity": "high",
      "reference": "https://github.com/advisories/GHSA-57j2-w4cx-62h2"
    }
  ],
  "summary": { "critical": 1, "high": 1, "medium": 1, "low": 0 },
  "notes": ["BDL packages were not scanned"]
}
```

The JSON shape is versioned (`schemaVersion: 1`) so future fields can be added without breaking CI parsers.

## Exit codes

| Code | Meaning |
|---|---|
| 0 | No findings meet the severity floor. |
| 1 | One or more findings at or above the severity floor. |
| 2 | Command failure (missing lockfile, network error, HTTP non-2xx, invalid JSON). |

This matches `npm audit` semantics and the existing `fglpkg outdated` pattern (which also exits non-zero on dependencies needing attention).

## Error handling

- **No lockfile:** clear error, exit 2.
- **Lockfile present but no JARs:** print informational line, exit 0.
- **OSV.dev returns 4xx:** show the response body in the error and exit 2 — likely a malformed PURL.
- **OSV.dev returns 5xx or network failure:** exit 2 with the underlying error. Do not retry in v1; CI users can rerun the command. (Retry-with-backoff is a future enhancement.)
- **Any per-JAR failure** is treated as full failure — never produce a "maybe-clean" report from a partially failed run.

## Configuration

No changes to `fglpkg.json` schema in v1. (A future `audit` field — exclusions, custom severity floor — is plausible but out of scope.)

## Testing

Unit tests:

- `internal/audit/audit_test.go`
  - `TestAuditEmpty` — no JARs → no findings, no HTTP calls.
  - `TestAuditPerJARQuery` — three JARs produce three queries in order.
  - `TestAuditSeverityFromGHSA` — covers the GHSA severity label mapping (incl. case-insensitive, `MODERATE`→medium, and unknown → empty).
  - `TestAuditUnknownSeverityDefaultsToMedium` — vulns with no `database_specific.severity` surface at the default floor.
  - `TestAuditHTTPError` — 500 response yields an error, not findings.
  - `TestAuditMalformedResponse` — bad JSON yields an error.
  - `TestAuditDedupsCoordinates` — duplicate JARs are queried once.
  - Uses `httptest.NewServer` to stub OSV.dev. No real network calls.
- `internal/cli/audit_test.go`
  - `TestAuditCommandJSON` — full end-to-end via stub server, asserts JSON shape.
  - `TestAuditCommandExitCode` — severity floor logic.
  - `TestAuditCommandNoLockfile` — error message + exit 2.
  - `TestAuditCommandProductionFilter` — `--production` excludes scope==dev JARs.

Integration: no live OSV.dev calls in CI. A manual smoke test against the public endpoint is part of the merge checklist (recorded in the PR description, not automated).

## Acceptance criteria

1. `fglpkg audit` runs in a project with a populated `fglpkg.lock`, queries OSV.dev, and prints findings (or a clean-tree message). ✅
2. `fglpkg audit --json` emits a stable, documented JSON shape with `schemaVersion: 1`. ✅
3. Exit code is 0 on a clean tree (no findings ≥ `--severity` floor), 1 on findings ≥ floor, 2 on command failure. ✅
4. `fglpkg audit --severity=high` does not fail the build on medium/low findings. ✅
5. `fglpkg audit --production` excludes JARs marked `"scope": "dev"` in the lockfile. ✅
6. Running with no `fglpkg.lock` produces a clear error and exit 2 — never silently passes. ✅
7. Network or HTTP errors produce exit 2 with the underlying error — never reported as "no findings." ✅
8. Help text (`fglpkg help`, `fglpkg audit --help`) lists the command and its flags. ✅
9. Shell completion (bash/zsh/fish/pwsh) includes `audit` and its flags. ✅
10. Unit tests cover per-JAR querying, severity mapping, HTTP error paths, severity threshold, the unknown-severity default, and the no-lockfile case.
11. No new top-level dependencies in `go.mod` — stdlib `net/http` + `encoding/json` only.
12. `go test ./...` passes; `go build ./...` is clean.

## Open questions

- **OSV.dev rate limits.** OSV.dev's anonymous limits are loose (no documented per-IP cap as of May 2026) but not zero. For very large dependency trees or aggressive CI parallelism we may eventually need to slow down or batch via `/v1/querybatch` + per-vuln detail GETs. Not blocking v1.
- **Output stability.** If we add NVD/GHSA later, a single CVE may appear multiple times (once per source). The current data model carries `id` per finding, not `cve`-keyed dedup. We'll need to revisit when adding sources.

## Future work (explicitly deferred)

- `--fix` to bump vulnerable JARs to the lowest safe version within the manifest's constraint.
- BDL package coverage (needs an advisory data store — probably a registry endpoint).
- Multiple advisory sources with dedup (GHSA, NVD).
- Cached advisory data + `--offline` mode.
- Retry-with-backoff on transient HTTP failures.
- SARIF output for GitHub Code Scanning integration.
- Suppress / acknowledgement file (`.fglpkg-audit-ignore`) for known-accepted findings.
