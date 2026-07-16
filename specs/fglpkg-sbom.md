# Spec: `fglpkg sbom` (v1)

**Status:** ✅ Implemented — shipped ([internal/sbom/](../internal/sbom/))
**Date:** 2026-05-15
**Author:** Mike Folcher
**Tracking:** P2 #24 in [docs/market-readiness-gaps.md](../docs/market-readiness-gaps.md)

---

## Summary

Add a new CLI command, `fglpkg sbom`, that emits a Software Bill of Materials (SBOM) for the current project in **CycloneDX 1.5 JSON** format, generated entirely from the project's existing `fglpkg.lock`. No network calls. Pairs with `fglpkg audit`: audit answers *"are there known CVEs?"*, SBOM answers *"what do you ship?"*

## Motivation

SBOMs have moved from nice-to-have to procurement-blocker:

- US Executive Order 14028 (May 2021) — federal procurement requires an SBOM.
- EU Cyber Resilience Act (in force 2027) — required for any software placed on the EU market.
- Enterprise vendor security questionnaires ask for one as a checklist item.

A Genero project that cannot produce an SBOM in a recognized industry format is, for an increasing share of customers, simply not deployable. Every consumer of `fglpkg.lock` data — Dependency-Track, Anchore Syft, Trivy, the GitHub Dependency Submission API — already speaks CycloneDX.

## Goals

- One command (`fglpkg sbom`) emits a valid CycloneDX 1.5 JSON document.
- Generated entirely from the lockfile — **no network calls, no registry hits**.
- Honest output: omit fields we don't have rather than fabricate them.
- Useful in CI: clean stdout pipeline (`fglpkg sbom > sbom.json`), `--production` filter, predictable exit codes.

## Non-goals (v1)

- SPDX output. Reserve `--format=spdx` but error out on it in v1.
- Network-enriched output (license, supplier, description from registry). Defer to a `--enrich` flag once someone asks.
- Signature / attestation / in-toto wrapping. Future work, separate from SBOM emission.
- Vulnerability data inline in the SBOM. `fglpkg audit` handles that; the two outputs are intended to be consumed alongside each other.

## CLI surface

```
fglpkg sbom                          Emit CycloneDX 1.5 JSON to stdout
fglpkg sbom -o sbom.json             Write to file
fglpkg sbom --output sbom.json       Long form
fglpkg sbom --pretty                 Indented JSON (default: compact)
fglpkg sbom --production             Skip JARs with lockfile scope == "dev"
                                     (optional-scope entries always included).
                                     Matches `fglpkg audit --production`.
fglpkg sbom --format=cyclonedx       Default. The only supported value in v1.
fglpkg sbom --format=spdx            Reserved; errors out in v1.
fglpkg sbom --help                   Usage
```

Add `sbom` to the dispatcher in [internal/cli/cli.go](../internal/cli/cli.go), to the `help` usage, and to the [completion.go](../internal/cli/completion.go) command/flag lists.

## Output format

**CycloneDX 1.5 JSON.** Per the spec at https://cyclonedx.org/specification/overview/.

### Document skeleton

```json
{
  "bomFormat": "CycloneDX",
  "specVersion": "1.5",
  "serialNumber": "urn:uuid:<v4-uuid>",
  "version": 1,
  "metadata": {
    "timestamp": "<RFC3339 UTC>",
    "tools": [{"vendor": "Four Js", "name": "fglpkg", "version": "<cli.Version>"}],
    "component": {
      "bom-ref": "root",
      "type": "application",
      "name": "<root.name from lock>",
      "version": "<root.version from lock>"
    }
  },
  "components": [ /* one per BDL package and JAR in the lockfile */ ],
  "dependencies": [ /* edges from requiredBy */ ]
}
```

### BDL package component

```json
{
  "bom-ref": "pkg:fglpkg/<name>@<version>",
  "type": "library",
  "name": "<name>",
  "version": "<version>",
  "purl": "pkg:fglpkg/<name>@<version>",
  "hashes": [{"alg": "SHA-256", "content": "<lower-hex checksum>"}],
  "externalReferences": [{"type": "distribution", "url": "<downloadUrl>"}],
  "properties": [
    {"name": "fglpkg:generoMajor", "value": "<4|6|...>"}
  ]
}
```

Omit `hashes` when checksum is empty (registry didn't provide one).
Omit `externalReferences` when `downloadUrl` is empty.
Omit `properties` when `generoMajor` is empty.

### JAR component

```json
{
  "bom-ref": "pkg:maven/<groupId>/<artifactId>@<version>",
  "type": "library",
  "name": "<artifactId>",
  "group": "<groupId>",
  "version": "<version>",
  "purl": "pkg:maven/<groupId>/<artifactId>@<version>",
  "hashes": [{"alg": "SHA-256", "content": "<lower-hex checksum>"}],
  "externalReferences": [{"type": "distribution", "url": "<downloadUrl>"}]
}
```

### Dependency graph

Built from `requiredBy` on each `LockedPackage`. For each component C with `requiredBy = [P1, P2, ...]`, append `{ref: Pi, dependsOn: [C]}` for each Pi.

JARs do not currently carry a `requiredBy` field. Until that gap is closed, the SBOM emits **a single `{ref: "root", dependsOn: [<all JAR purls>]}`** entry covering JARs — accurate at the depth we can produce today, with no false claims about which BDL package pulled which JAR. Documented in the spec's open questions.

The root entry collapses all `<root>`-required BDL packages into a single `{ref: "root", dependsOn: [...]}` edge.

### PURL types

| Lockfile entry | PURL type | Form |
|---|---|---|
| BDL package | `fglpkg` (custom) | `pkg:fglpkg/<name>@<version>` |
| Java JAR | `maven` (registered) | `pkg:maven/<groupId>/<artifactId>@<version>` |

The `pkg:fglpkg` type is custom — PURL spec explicitly allows this. Whether to upstream it as a registered type is an ecosystem decision and out of scope here.

### Pretty vs compact

Default is compact (single-line JSON, easier to diff in CI). `--pretty` switches to 2-space indented JSON for human inspection.

## Algorithm

```
1. Load fglpkg.lock from cwd.
   If absent → error: "no fglpkg.lock; run `fglpkg install` first" → exit 1.

2. Apply --production filter: drop LockedJAR entries with scope == "dev".
   (BDL packages don't carry scope in the lockfile today; pass them all
    through. Future work to add scope to LockedPackage.)

3. Build the CycloneDX Document:
     - Stable serial number: random uuid (or deterministic for tests).
     - Metadata timestamp: time.Now().UTC().Format(RFC3339).
     - Tool info: pull cli.Version at runtime.
     - Root component from lock.RootManifest.
     - One Component per BDL package (sorted by name) and per JAR (sorted by key).
     - Dependencies edges: see "Dependency graph" above.

4. Marshal to JSON (pretty or compact).

5. Write to stdout or to the file named by -o/--output.
   On file write failure → error → exit 1.

6. Exit 0.
```

## Public API

`internal/sbom/cyclonedx.go`:

```go
package sbom

// Options configure SBOM generation.
type Options struct {
    Production  bool
    ToolName    string             // default "fglpkg"
    ToolVendor  string             // default "Four Js"
    ToolVersion string             // default "" (caller passes cli.Version)
    Now         func() time.Time   // injectable for deterministic tests
    NewUUID     func() string      // injectable for deterministic tests
}

// Document is a CycloneDX 1.5 root.
type Document struct { /* ... */ }

// Build constructs a CycloneDX Document from a lockfile.
func Build(lf *lockfile.LockFile, opts Options) *Document
```

## Testing

Unit tests:

- `internal/sbom/cyclonedx_test.go`
  - `TestBuildShape` — a small lockfile with 1 BDL package + 2 JARs produces a document with the right top-level fields, correct serialNumber/timestamp from injected stubs, and correct component count.
  - `TestBuildPURLsForBDL` — BDL component has `pkg:fglpkg/<name>@<version>` purl.
  - `TestBuildPURLsForJARs` — JAR components have `pkg:maven/<group>/<artifact>@<version>` purl.
  - `TestBuildHashesOmittedWhenEmpty` — checksum=="" → no hashes array.
  - `TestBuildGeneroMajorProperty` — BDL with generoMajor set produces the property; empty omits it.
  - `TestBuildDependencyGraph` — `requiredBy = ["<root>", "myutils"]` produces `dependsOn` edges in both directions.
  - `TestBuildProductionFilterDropsDevJARs` — JAR with scope=="dev" is excluded when `Production: true`.
  - `TestBuildEmptyLockfile` — empty packages + jars yields a valid Document with no components.

- `internal/cli/sbom_test.go`
  - `TestSbomFlagParsing` — defaults, `-o`, `--output`, `--production`, `--pretty`, unknown args.
  - `TestSbomMissingLockfile` — error + exit 1.
  - `TestSbomToFile` — writes the JSON to the supplied path, content roundtrips.
  - `TestSbomFormatSpdxRejected` — `--format=spdx` errors out in v1.
  - `TestSbomCompactByDefault` — output has no leading whitespace on lines other than the first.

No live network; no fixtures over a few KB. `go test ./...` stays fast.

## Acceptance criteria

1. `fglpkg sbom` reads `fglpkg.lock` and emits CycloneDX 1.5 JSON to stdout. ✅
2. `fglpkg sbom -o sbom.json` writes the same content to the named file. ✅
3. `fglpkg sbom --pretty` emits 2-space indented JSON; default is compact. ✅
4. `fglpkg sbom --production` skips dev-scoped JARs. ✅
5. `fglpkg sbom --format=spdx` errors with "spdx format not supported in v1". ✅
6. Missing `fglpkg.lock` produces a clear error and non-zero exit. ✅
7. The emitted document validates against CycloneDX 1.5 (manual check via [cyclonedx-cli validate] is part of the merge checklist; CI does shape checks via Go unit tests). ✅
8. BDL components carry `pkg:fglpkg/...` purls; JARs carry `pkg:maven/...` purls. ✅
9. Dependency edges from `requiredBy` are emitted, including the root edge. ✅
10. Help text (`fglpkg help`, `fglpkg sbom --help`) lists the command. ✅
11. Shell completion includes `sbom` and its flags. ✅
12. No new top-level `go.mod` dependencies — stdlib only (`crypto/rand` for the UUID v4 + `encoding/json`). ✅
13. `go build ./...` clean; `go test ./...` passes.

## Open questions

- **JAR dependency parentage.** The lockfile records `requiredBy` for BDL packages but not for JARs. Today's SBOM emits a flat `root → [all JARs]` edge for JARs, which is honest but coarse. Closing this gap is a `lockfile` change (add `requiredBy` to `LockedJAR`) tracked separately — out of scope here.
- **License field.** Not in the lockfile; only on the registry. v1 omits the `licenses` field on every component. Procurement tools we surveyed (Dependency-Track, Trivy) handle missing license gracefully. Adding an optional `--enrich` flag that fetches license + supplier from the registry is the natural follow-up.
- **PURL custom type adoption.** Using `pkg:fglpkg/...` is valid per the PURL spec but unregistered. We may want to PR the spec at github.com/package-url/purl-spec at some point — not blocking.

## Future work (explicitly deferred)

- `--format=spdx` (with SPDX 2.3 JSON).
- `--enrich` flag for license/supplier from the registry.
- `LockedJAR.RequiredBy` upstream to produce a complete dependency graph for JARs.
- SBOM signing / attestation (in-toto wrapping; aligns with the Sigstore signing track).
- `fglpkg sbom diff old.json new.json` for change reports between releases.
- Per-component `licenses` field once enrich lands.
