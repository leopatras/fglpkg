# Spec: Prepublish validation (v1)

**Status:** ✅ Implemented — shipped ([internal/cli/publish_validation.go](../internal/cli/publish_validation.go))
**Date:** 2026-05-14
**Author:** Mike Folcher
**Tracking:** P1 #16 in [docs/market-readiness-gaps.md](../docs/market-readiness-gaps.md)

---

## Summary

Make `fglpkg publish` (and its `--dry-run` form) reject packages whose manifest is missing key metadata or whose version has already been published. Today the command happily uploads packages with no `license`, no `description`, no `repository`, and no `author`, and silently lets you re-publish the same version — three foot-guns that consumers and the registry will both punish you for.

## Motivation

- **Procurement / legal blockers.** Customer security review flags packages with no license. Easier to catch at publish than after the artifact is in production.
- **Discovery / triage.** A registry browse with rows of `(no description)` is unusable. Same for "where do I file bugs?" with no repository.
- **Accidental re-publish.** Today nothing stops `fglpkg publish` running twice on the same version. Server-side errors are deferred until after the GitHub upload completes, so a partial failure can leave a release asset orphaned. Catching the version collision pre-flight saves a round trip.

These match the **prepublish validation** entry in the gap analysis (P1, "expected by professional users").

## Goals

- Block `fglpkg publish` and `fglpkg publish --dry-run` if the manifest is missing fields a published package needs.
- Block `fglpkg publish` if the named version is already on the registry.
- Give clear, actionable error messages that say *what to fix* and *how*.
- Zero new flags. No `--force` or `--skip-validation` — if a rule is wrong, change the rule.

## Non-goals (v1)

- Auto-fix (`fglpkg publish --fix-manifest`). Out of scope.
- Validating the *content* of fields beyond presence (e.g., SPDX license recognition, repository URL reachability). Deferred — easy to add later without a CLI change.
- A standalone `fglpkg validate` command. The integrated publish check covers the immediate need; a separate command is straightforward to expose later if developers want it during editing.

## Behavior

### Required-for-publish manifest fields

Block publish if any of these are missing or empty:

| Field | Rationale |
|---|---|
| `name` | Already enforced by `manifest.Validate()`. |
| `version` | Already enforced by `manifest.Validate()`. |
| `description` | npm/PyPI/RubyGems all require this. Consumers can't evaluate a package without one. |
| `license` | Procurement teams flag missing licenses; SPDX identifier expected (no value-format check in v1). |
| `repository` | Tells consumers where to file bugs / read source. |
| `author` | Attribution. (User opted to include in v1.) |

Implementation lives as a new function `manifest.ValidateForPublish(m *Manifest) error` in [internal/manifest/manifest.go](../internal/manifest/manifest.go). It calls the existing `m.Validate()` first, then layers the extra required-field checks.

The function returns a single multi-line error listing **all** missing fields, not just the first — fewer round-trips for developers fixing things up:

```
manifest is not ready to publish:
  - description is required
  - license is required (e.g. "MIT", "Apache-2.0")
  - repository is required (e.g. "https://github.com/owner/repo")
```

### Version-already-published check

After `ValidateForPublish` passes, `cmdPublish` calls `registry.FetchVersionList(m.Name)`:

- If the package is unknown to the registry (404 → `registry.ErrNotFound`), this is a **first publish**; no error.
- If any other registry error is returned (network failure, 5xx), abort the publish with a clear "could not verify whether this version is already published" message. Better than letting it through and failing late.
- If `m.Version` appears in the returned versions list, abort with:

  ```
  version 1.2.3 of poiapi is already published on https://fglpkg-registry.fly.dev
  bump the version before publishing again:
      fglpkg version patch     # 1.2.3 → 1.2.4
      fglpkg version minor     # 1.2.3 → 1.3.0
      fglpkg version major     # 1.2.3 → 2.0.0
  ```

The check runs for both `publish` and `publish --dry-run` — a dry run should surface the same blockers that a real publish would.

### Integration with `--dry-run`

`--dry-run` already short-circuits the actual upload. Prepublish validation runs *before* the dry-run branch, so:

```
fglpkg publish --dry-run
  → ValidateForPublish ✓
  → version not already published ✓
  → print "DRY RUN" header and the would-upload preview
```

If validation fails, the dry-run exits with the validation error and no preview is printed. Same exit code as a real publish failure.

## API additions

### `internal/registry/registry.go`

```go
// ErrNotFound is returned when the registry responds 404 to a GET
// (typically a package or version that does not exist).
var ErrNotFound = errors.New("package not found in registry")
```

Update `httpGet` to return `ErrNotFound` directly (instead of a freshly-constructed `fmt.Errorf`) so callers can use `errors.Is(err, registry.ErrNotFound)`. Existing wrappers (`FetchVersionList`, `FetchInfo`, …) already use `%w`, so the chain is preserved.

### `internal/manifest/manifest.go`

```go
// ValidateForPublish performs the structural sanity checks plus the
// extra required-field checks for publishing: description, license,
// repository, author. Returns a single error whose message lists every
// missing field, so developers can fix them all in one pass.
func (m *Manifest) ValidateForPublish() error
```

### `internal/cli/publish.go` *(new file)* or alongside `cli.go`

```go
// checkVersionNotPublished asks the registry whether m.Version of
// m.Name is already published. Returns nil when the version is free
// (including the first-publish case where the package is unknown).
func checkVersionNotPublished(m *manifest.Manifest) error
```

The check is split out so it can be unit-tested against a stub registry without needing a full `cmdPublish` integration.

## Exit codes & error messages

No exit-code changes — these are regular errors flowing through `main.Execute`, producing exit 1. The new failures are:

| Scenario | Error message (abbreviated) |
|---|---|
| Missing required fields | `manifest is not ready to publish:\n  - <field> is required\n  ...` |
| Version already published | `version X is already published; bump the version before publishing again:\n    fglpkg version patch ...` |
| Registry unreachable during pre-check | `cannot check whether version X is already published: <underlying>` |

## Testing

Unit tests:

- `internal/manifest/manifest_test.go`
  - `TestValidateForPublishOK` — fully-populated manifest passes.
  - `TestValidateForPublishMissingFields` — table-driven: omit each required field individually; assert the error mentions that field.
  - `TestValidateForPublishCollectsAllMissing` — manifest missing several fields → error message mentions every missing one.
  - `TestValidateForPublishDelegatesToValidate` — passing `Validate`'s checks first is part of the contract; remove `version` and assert the structural error surfaces.
- `internal/registry/registry_test.go` *(new file if absent)*
  - `TestFetchVersionListReturnsErrNotFound` — stub server replies 404; `errors.Is(err, ErrNotFound)` is true.
- `internal/cli/publish_validation_test.go` *(new file)*
  - `TestCheckVersionNotPublishedFirstPublish` — registry says 404; returns nil.
  - `TestCheckVersionNotPublishedExisting` — registry returns a versions list containing the current version; error mentions `fglpkg version`.
  - `TestCheckVersionNotPublishedDifferentVersion` — registry returns other versions only; returns nil.
  - `TestCheckVersionNotPublishedRegistryDown` — stub returns 500; error wraps the underlying failure.

The existing `TestPublishPackageDryRunNoNetwork` test still exercises the inner `publishPackage` and is unaffected — the new validation lives in `cmdPublish`, which the dry-run test does not call.

## Acceptance criteria

1. `fglpkg publish` rejects a manifest missing `description`, `license`, `repository`, or `author` with an error message naming the missing field(s). ✅
2. `fglpkg publish --dry-run` performs the same validation and exits before printing the dry-run preview if validation fails. ✅
3. `fglpkg publish` rejects a re-publish of an already-published version with a message pointing at `fglpkg version`. ✅
4. First publish of a new package (registry returns 404) is not blocked by the version check. ✅
5. Registry network errors during the version check abort the publish (not silently allow it). ✅
6. `registry.ErrNotFound` is reachable via `errors.Is(err, registry.ErrNotFound)`. ✅
7. No new top-level CLI flags. No `--force` / `--skip-validation`. ✅
8. `go build ./...` clean; `go test ./...` passes.

## Open questions

- **License format.** We don't validate that the value is a known SPDX identifier in v1. If users want autocomplete and validation, that comes with the JSON schema work already shipped — the schema can grow an `enum` of SPDX ids without code changes here.
- **Repository format.** Same — accept any non-empty string. Could later require a URL shape, but `git@github.com:org/repo` and `https://...` both need to be accepted, plus shorthand `org/repo`. Defer.
- **Description length.** No min/max in v1. npm warns on >200-char descriptions; we don't.

## Future work (explicitly deferred)

- `fglpkg validate` standalone command.
- `fglpkg publish --fix-manifest` interactive fix-up.
- SPDX license identifier validation.
- Repository URL well-formedness check.
- Description min/max length warnings.
- Pre-flight check that the configured GitHub repo exists and the token has write access.
