# Spec: Genero-aware `fglpkg search` — annotate results by compatibility with the running Genero version

**Status:** Draft
**Date:** 2026-07-14
**Author:** Mike Folcher
**Motivation:** `fglpkg search` is the one discovery command that is completely blind to the Genero
version the user is running. `install` and `info` already resolve a Genero-major-specific artifact
variant ([`FetchInfoForGenero`](../internal/registry/registry.go#L145)), but `search` sends only a
keyword and prints whatever the registry returns — so a user on Genero 3.x sees 4.x-only packages
with no hint that they can't use them, only discovering the mismatch at `install` time.
**Related:** [publish-rich-metadata.md](publish-rich-metadata.md) (the per-version `genero` constraint
this consumes); [import-root.md](import-root.md) (recent spec, house style).

---

## Summary

Make `fglpkg search` **Genero-version aware**. The command detects the running Genero version
(reusing [`genero.Detect()`](../internal/genero/genero.go#L42), overridable with a new `--genero`
flag or the existing `FGLPKG_GENERO_VERSION`), then **annotates** each result as compatible or
incompatible with that version. Nothing is hidden — every match is still listed — so discovery is
never silently narrowed; the annotation is advisory.

Compatibility is evaluated **client-side** by matching the running version against each package's
declared `genero` constraint via the existing [`Version.Satisfies`](../internal/genero/genero.go#L96).
This requires one small piece of new data: the search list response must carry the latest version's
`genero` constraint per package (it does not today — see [§ Background](#background--how-it-works-today)).
The client **degrades gracefully** when that field is absent (older registries), rendering an
"unknown" marker rather than a false verdict.

Default behavior for a registry that does not yet return the constraint is **visually identical to
today** except for one extra column showing `?`.

## Background — how it works today

### `search` sends only a keyword; it never detects the Genero version

[`cmdSearch`](../internal/cli/cli.go#L664) parses a term and calls `registry.Search(term)`. It never
calls `genero.Detect()`:

```go
func cmdSearch(args []string) error {
    term, all, err := parseSearchArgs(args)   // cli.go:665
    ...
    results, err := registry.Search(term)     // cli.go:670 — no genero context
```

[`registry.Search`](../internal/registry/registry.go#L238) issues `GET /registry/packages?q=<term>`
— the query string is the *only* input:

```go
u := fmt.Sprintf("%s/registry/packages?q=%s", registryBase(), url.QueryEscape(term))  // registry.go:239
```

### The result type cannot express compatibility

[`SearchResult`](../internal/registry/registry.go#L95) carries `Name`, `LatestVersion`,
`Description`, `Author` — there is no `genero` field to filter or annotate on. The registry's
list-shaped payload [`apiListedPackage`](../internal/registry/registry.go#L456) likewise has no
`genero` field:

```go
type apiListedPackage struct {
    Slug, Name, Description, Visibility string
    Owner         apiOwner
    Status        string
    LatestVersion string
    Downloads     int64
    Tags          map[string][]string
    // no genero constraint
}
```

The per-version `genero` constraint **does** exist on the registry, but only on the *detail*-shaped
[`apiVersionSummary.Genero`](../internal/registry/registry.go#L440) returned by
`GET /registry/packages/<slug>` ([`fetchPackageDetail`](../internal/registry/registry.go#L480)) —
not on the browse/search list.

### The compatibility machinery already exists and is proven

- **Detection + override:** [`genero.Detect()`](../internal/genero/genero.go#L42) already honors
  `FGLPKG_GENERO_VERSION`, then `fglcomp --version`, then `$FGLDIR`. `install`/`publish` call it and
  take `gv.MajorString()` ([cli.go:299](../internal/cli/cli.go#L299),
  [cli.go:1045](../internal/cli/cli.go#L1045)).
- **Constraint matching:** [`Version.Satisfies(constraint)`](../internal/genero/genero.go#L96) parses
  a semver constraint and reports a match; an empty/`*` constraint is treated as "any". This is the
  exact primitive needed to grade a package.

The only gap is plumbing the per-package `genero` constraint into the search list and rendering a
verdict.

## Design

### 1. Detect the target Genero version (with a `--genero` override)

`cmdSearch` resolves a target version once, before displaying results:

- Add a `--genero <version>` flag to `search` (parsed in `parseSearchArgs`). When set, parse it with
  [`genero.Parse`](../internal/genero/genero.go#L82).
- Otherwise call [`genero.Detect()`](../internal/genero/genero.go#L42) (which already honors
  `FGLPKG_GENERO_VERSION`).
- If detection **fails** (no `fglcomp`, no `$FGLDIR`, no override), search must still work — it is a
  discovery command that should run anywhere. Fall back to "no target version": every result is
  annotated `?` (unknown) and a one-line note explains how to set the version. Do **not** abort.

### 2. Carry the `genero` constraint into the search result

Add an optional field to both the wire type and the client type:

```go
// apiListedPackage
Genero string `json:"genero,omitempty"`   // latest version's genero constraint

// SearchResult
GeneroConstraint string `json:"genero,omitempty"`
```

`registry.Search` copies `p.Genero` into `SearchResult.GeneroConstraint`. Because the field is
`omitempty` and the client treats empty as "unknown", a registry that does not populate it is
handled transparently — **no client crash, no false verdict** (see [§ Compatibility](#compatibility--fallback)).

> **Registry-side note (out of scope for the client change, tracked here):** the registry's
> `GET /registry/packages` handler must include `genero` (the latest published version's constraint)
> on each listed package for annotation to be meaningful. Until it does, all results render `?`.
> This is a purely additive response field; see [gi-registry-workstream-c.md](gi-registry-workstream-c.md).

### 3. Grade each result client-side

For each result, compute a verdict from the target version and the result's constraint:

| Condition | Verdict | Marker |
|---|---|---|
| No target version resolved | Unknown | `?` |
| Result has empty/absent constraint | Unknown | `?` |
| `target.Satisfies(constraint)` is true | Compatible | `✓` |
| `target.Satisfies(constraint)` is false | Incompatible | `✗` |
| `constraint` fails to parse | Unknown | `?` (and the raw constraint is still shown) |

Grading uses [`Version.Satisfies`](../internal/genero/genero.go#L96) verbatim. A malformed
constraint from the registry must never abort the whole search — it degrades that one row to `?`.

### 4. Render an annotated table

Extend the existing table ([cli.go:694-698](../internal/cli/cli.go#L694)) with a compatibility
column. Show the target version in the header so the verdict is self-explanatory:

```
Results for "json" (Genero 4.01):
  NAME                           VERSION      GENERO     ?  DESCRIPTION
  ----                           -------      ------     -  -----------
  jsonutils                      2.1.0        ^4.0.0     ✓  JSON helpers
  legacyjson                     1.4.0        ^3.0.0     ✗  JSON for Genero 3
  mystery                        0.9.0        -          ?  (registry reports no genero constraint)
```

- The `GENERO` column shows the raw constraint (`-` when absent).
- The `?`/`✓`/`✗` column is the verdict. Prefer ASCII-safe glyphs; if terminal encoding is a concern,
  a `OK`/`NO`/`--` variant is acceptable — pick one and keep it consistent with any `info` output.
- When **no** target version was resolved, the header reads `Results for "json" (Genero version
  unknown — set FGLPKG_GENERO_VERSION or pass --genero):` and every verdict is `?`.

`--all` mode gets the same column treatment.

### Compatibility & fallback

- **Old registry (no `genero` in list response):** field decodes as empty → every verdict `?`.
  Output is today's table plus a `?` column and no false claims.
- **Detection unavailable:** search still runs; verdicts are `?`; a hint is printed once.
- **Malformed constraint:** that row is `?`; other rows are unaffected.
- **`omitempty` on the wire** keeps the request/response byte-compatible for older clients and
  servers.

## Non-goals

- **No hiding/filtering.** This spec annotates only. A future `--compatible-only` flag could hide
  `✗` rows, but is explicitly out of scope (the chosen behavior is "show all, annotate").
- **No registry-side filtering** (no new query parameter). Grading is entirely client-side; the only
  registry dependency is the additive `genero` list field.
- **Latest-version only.** The verdict reflects the package's *latest* version's constraint, matching
  what the search row already shows (`LatestVersion`). Grading every historical version is out of
  scope — a `✗` on latest does not prove no compatible older version exists, so the marker is
  advisory, and `fglpkg info <pkg>` remains the way to inspect per-version compatibility.

## Testing

- **`parseSearchArgs`**: `--genero <ver>` parsed; `--genero` + conflicting positional handled;
  existing `--all` / term rules unchanged. (Extends [search_test.go](../internal/cli/search_test.go).)
- **`registry.Search` mapping**: `genero` on `apiListedPackage` maps to
  `SearchResult.GeneroConstraint`; absent field → empty string. (Extends
  [TestSearchMapsBrowseResponse](../internal/registry/registry_test.go#L133).)
- **Grading**: table-driven over (targetVersion, constraint) → verdict, covering compatible,
  incompatible, empty constraint, unparseable constraint, and no-target-version.
- **Rendering**: golden output for a mixed result set with a resolved version, and for the
  unknown-version fallback header.

## Rollout

Additive and backward-compatible. Client ships immediately; results show `?` until the registry
starts returning `genero` on listed packages, at which point verdicts light up with no client change.
Document the `--genero` flag and the compatibility column in
[docs/user-guide.md](../docs/user-guide.md) and the `search` entries in [README.md](../README.md).
