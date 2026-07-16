# Spec: Push rich package metadata on publish

**Status:** ✅ Implemented — shipped (publish sends repo/author/license/genero/deps + README/USERGUIDE)
**Date:** 2026-06-06
**Author:** Mike Folcher
**Companion:** `4js-genero-intelligence/specs/registry-metadata-fglpkg-alignment.md`
(registry side) and the already-implemented `registry-package-metadata.md`.

---

## Summary

Make `fglpkg publish` send the metadata the Genero Intelligence registry already
knows how to store: `repository`, `author`, `license`, `genero` (constraint),
`dependencies`, and the `README` / `USERGUIDE` markdown bodies. Today the new
`/registry/*` publish path sends only `{version, changelog}`, so the registry
portals show blank metadata even though every field already lives in the
publisher's `fglpkg.json` and root-level doc files.

This is a backward-compatible, additive payload on the existing
`POST /registry/packages/:slug/versions` call. No new commands, no new endpoints,
no change to the artifact-upload or submit steps.

## Motivation

- The registry + portals can render repository/author/license/dependency info and
  the README/USERGUIDE without ever unzipping an artifact — but only if the
  publisher pushes it. The receiving side already exists (registry migration
  `0024`, `serializeVersion`, version-create accepts the fields).
- `fglpkg` already collects the README ([internal/cli/readme.go](../internal/cli/readme.go)) —
  that code was built for the legacy `fglpkg-registry.fly.dev` server and is **not
  wired into the new `/registry/*` publish path at all**. This spec connects it
  and extends it to the new flow.

## Current state

| Piece | Today |
|---|---|
| `publishPackage` → `PublishCreateVersion(slug, m.Version, "", nil)` | sends `{version, changelog:"", tags:nil}` only ([cli.go:717](../internal/cli/cli.go)) |
| `PublishCreateVersion(slug, version, changelog, tags)` | payload is `{version, changelog, tags?}` ([registry.go:275](../internal/registry/registry.go)) |
| `collectReadme(dir)` | exists, candidate-list root scan, 256 KB truncate + marker — **unused by the new path** ([readme.go:34](../internal/cli/readme.go)) |
| USERGUIDE collection | does not exist |
| Manifest fields | `Repository`, `Author`, `License`, `GeneroConstraint` (`json:"genero"`), `Dependencies` (`{fgl, java}`), `Visibility` all already present ([manifest.go:27-51](../internal/manifest/manifest.go)) |

## Goals

- A new version published with `fglpkg publish` carries repository, author,
  license, genero constraint, production dependencies, README, and USERGUIDE
  through to the registry in one call.
- Reuse the existing, tested `collectReadme`; add a symmetric `collectUserguide`.
- `--dry-run` lists the metadata that would be sent, including doc byte sizes.
- Fully backward compatible: omitted/empty fields are simply not populated; the
  registry defaults them. Old behaviour (publish with no docs) still works.

## Non-goals

- Sending `devDependencies` / `optionalDependencies` — production `dependencies`
  only (matches what consumers resolve from the registry).
- Rendering or validating markdown content. The registry stores it verbatim; the
  portal renders it.
- Backfilling metadata onto already-published versions. Going forward only.
- Mutating metadata after version-create (see "Variant interaction" below).

## Behavior

### 1. Collect the docs

- **README:** keep `collectReadme(m.Root)` exactly as-is — ordered candidate list
  (`README.md`, `.markdown`, `.rst`, `.txt`, `README`), case-insensitive on the
  basename, root-level only, 256 KB cap with the existing
  `*(README truncated at 256 KB)*` marker. Returns `("", nil)` when absent.
- **USERGUIDE:** add `collectUserguide(m.Root)`, a sibling in
  [readme.go](../internal/cli/readme.go) with candidates
  `USERGUIDE.md`, `USERGUIDE.markdown`, `USERGUIDE.rst`, `USERGUIDE.txt`,
  `USERGUIDE`. Same case-insensitive root-only scan, same 256 KB cap, parallel
  marker `*(USERGUIDE truncated at 256 KB)*`. Factor the shared cap/scan logic so
  the two collectors don't drift.
- Both are **root-level only** — never pull a dependency's `README`/`USERGUIDE`.

> **256 KB cap.** The client cap stays 256 KB; the registry's hard cap must be at
> least 256 KB + the truncation marker (~40 bytes), or a truncated doc would be
> rejected. The companion alignment doc raises the registry cap to 512 KB (2×),
> matching the assumption already documented in `readme.go` and the legacy
> server's `MaxReadmeBytes`. **This spec must not ship before that registry
> change is live**, otherwise large READMEs publish-then-400.

### 2. Extend the version-create payload

Change `PublishCreateVersion` to accept the metadata. Recommended shape — pass a
struct so the call site stays readable and the field set can grow:

```go
type VersionMeta struct {
    Repository   string                 // m.Repository
    Author       string                 // m.Author
    License      string                 // m.License
    Genero       string                 // m.GeneroConstraint  → JSON "genero"
    Dependencies manifest.Dependencies  // m.Dependencies      → JSON "dependencies"
    Readme       string                 // collectReadme(...)
    Userguide    string                 // collectUserguide(...)
}

func PublishCreateVersion(slug, version, changelog string, tags map[string][]string, meta VersionMeta) error
```

The JSON body gains `repository`, `author`, `license`, `genero`, `dependencies`
(the `{fgl, java}` object marshalled straight from `manifest.Dependencies`),
`readme`, `userguide`. Empty fields are sent as empty / `{}` (or omitted — the
registry defaults either way). The existing `version`, `changelog`, `tags` are
unchanged.

`dependencies` marshals to exactly the shape the registry's `parseDependencies`
expects: `{ "fgl": { "<name>": "<constraint>" }, "java": [ {groupId, artifactId,
version} ] }` — which is `manifest.Dependencies`'s own JSON encoding, so no
remapping is needed.

### 3. Wire it into the publish flow

In `publishPackage` ([cli.go:678](../internal/cli/cli.go)), before the
create-version call, build `VersionMeta` from the already-loaded manifest plus
`collectReadme(root)` / `collectUserguide(root)`, and pass it to
`PublishCreateVersion`.

### 4. Variant interaction (no change needed, document the consequence)

`publishPackage` already skips create-version on a `409` (adding a second Genero
variant to an existing version — [cli.go:717-722](../internal/cli/cli.go)). Since
the registry sets metadata at version-create time only, the metadata that sticks
is whatever the **machine that first published the version** sent. A later
variant-add does not (and must not) resend it. This is consistent with version
immutability; correcting metadata means publishing a new version. Call this out
in the publish output is not required, but document it in the user guide.

### 5. Dry-run output

`fglpkg publish --dry-run` already prints the planned calls. Extend the
create-version preview to list the metadata:

```
[dry-run] would POST   …/registry/packages/<slug>/versions
          body: {version:"1.2.0", changelog:""}
          metadata:
            repository: https://github.com/acme/rest-client
            author:     Acme <dev@acme.com>
            license:    MIT
            genero:     ^6.0.0
            dependencies: 2 fgl, 1 java
            readme:     12.4 KB
            userguide:  3.1 KB   (truncated)   ← only when the marker was appended
```

Show `(none)` for an absent README/USERGUIDE and `(truncated)` when the cap was hit.

## Affected files

- `internal/registry/registry.go` — `VersionMeta` type; extend
  `PublishCreateVersion` signature + payload.
- `internal/cli/readme.go` — add `collectUserguide`; factor shared cap/scan.
- `internal/cli/cli.go` — build + pass `VersionMeta` in `publishPackage`; extend
  dry-run preview.
- `internal/cli/readme_test.go` — add USERGUIDE collector tests.
- `internal/registry/registry_test.go` — assert the create-version payload
  carries the new fields.
- `internal/cli/publish_dryrun_test.go` — assert the dry-run prints metadata +
  sizes + truncation flag.
- Docs: README publish section + user guide (variant/metadata note).

## Acceptance criteria

1. `fglpkg publish` on a package with `repository`/`author`/`license`/`genero`/
   `dependencies` and a root `README.md` + `USERGUIDE.md` sends all of them on the
   create-version call; the registry round-trips every field.
2. Production `dependencies` are sent in `{fgl, java}` form; `devDependencies` and
   `optionalDependencies` are **not** sent.
3. Publishing with no README/USERGUIDE (or none of the optional manifest fields)
   succeeds and sends empty/`{}` — no regression to the current flow.
4. A README/USERGUIDE larger than 256 KB is truncated client-side with the marker
   and is accepted by the registry (requires the companion 512 KB cap change).
5. Adding a second Genero variant to an existing version does **not** resend
   metadata (create-version is skipped on 409) and does not error.
6. `--dry-run` lists each metadata field and the README/USERGUIDE byte sizes, and
   flags truncation, without making network calls.

## Test plan

- **Unit:** `collectUserguide` — absent, present, candidate precedence,
  over-cap-truncates-with-marker (mirror the existing README tests).
- **Unit:** `PublishCreateVersion` against an `httptest` server asserts the JSON
  body contains the metadata fields with correct shapes (esp. `dependencies`).
- **Unit:** dry-run output snapshot includes metadata block + sizes + `(truncated)`.
- **Manual / smoke:** publish a real package to the test deployment
  (`genero-intelligence-test.michael-folcher.workers.dev`); confirm the registry
  portal detail page renders repository/author/license/deps/README/USERGUIDE.

## Rollout

1. ~~Land the registry 512 KB cap change first (companion alignment doc).~~
   **Done** — the registry now caps README/USERGUIDE at 512 KB, so the
   client's 256 KB-plus-truncation-marker payload is accepted.
2. ~~Land this fglpkg change; from then on new publishes populate the metadata.~~
   **Done** — shipped in v2.x; `publish` sends repository/author/license/genero/
   dependencies + README/USERGUIDE on version-create.

Status: **complete on both sides.**
