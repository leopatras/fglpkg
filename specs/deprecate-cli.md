# Spec: `fglpkg deprecate` — CLI (publisher + consumer surfacing)

**Status:** 📋 Not started — GIS-247 (spec ready; GI-side endpoints already implemented)
**Date:** 2026-07-02
**Author:** Mike Folcher
**Tracking:** Workstream C in [docs/outstanding-work.md](../docs/outstanding-work.md) §4;
market-readiness gaps #11 (`deprecate`) and #31 (rename/relocate, folded in here).
**Registry counterpart:** the GI-side endpoints, schema, and read-model fields this
CLI depends on are specified in [specs/gi-registry-workstream-c.md](gi-registry-workstream-c.md) §1.
This document is the **fglpkg CLI** half.

---

## Summary

`fglpkg deprecate` marks a published package version (or a whole package) as
deprecated, attaching an advisory message and, optionally, a successor package via
`--moved-to`. This is the **npm model**: a deprecated version stays fully installable
and listed; consumers get a **non-fatal warning**. `--moved-to` is how a rename or
relocation is expressed (it replaced the dropped standalone `migrate` command). The
same command lifts a deprecation with `--undo`.

Two sides:
- **Publisher** (`fglpkg deprecate …`): owner-only write to the registry.
- **Consumer** (existing `install` / `info` / `outdated` / `update`): surface the
  deprecation as a warning + successor hint. Never blocks.

## Decisions (locked 2026-07-02)

| # | Decision | Choice |
|---|---|---|
| 1 | Version granularity | **Single version (`pkg@1.2.3`) or whole package (`pkg`) only.** No semver ranges in v1. |
| 2 | Message requirement | **Message required** (positional or `--message`). `--moved-to <new>` without a message **auto-fills** "<pkg> has moved to <new>". |
| 3 | Un-deprecate | **`--undo` flag** on the same command. No separate `undeprecate` verb; no empty-string trick. |
| 4 | Consumer strictness | **Warn-only, never blocks** install. `info`/`outdated` also surface it. No `--fail-on-deprecated` in v1. |

## Goals

- A publisher can deprecate a version or a whole package in one command, with a clear
  message and an optional successor.
- Rename/relocation is a first-class case: `deprecate old@ver --moved-to new`.
- Consumers are warned at the moments they'd act on it (install, info, outdated) and
  are pointed at the successor — without ever having an install fail because of it.
- Un-deprecating is a single obvious flag.

## Non-goals (v1)

- **Semver ranges** (`deprecate pkg@"<2.0"`). Single version or whole package only (decision #1).
- **A CI gate** (`--fail-on-deprecated`). Advisory-only in v1 (decision #4); revisit if asked.
- **Auto-installing the successor.** `--moved-to` warns and *suggests* the successor; it never silently swaps a dependency.
- **Deprecating draft/pending versions.** Only approved, live versions are meaningful to deprecate (a draft isn't installable yet).
- **Enforcing that the `--moved-to` target exists.** Forward references are allowed (see Open questions — a GI-side decision, [gi-registry-workstream-c.md](gi-registry-workstream-c.md) Appendix B).

---

## Command surface

### Synopsis

```
fglpkg deprecate <pkg>[@<version>] [<message>] [--moved-to <newpkg>[@<version>]]
fglpkg deprecate <pkg>[@<version>] --message <text> [--moved-to <newpkg>]
fglpkg deprecate <pkg>[@<version>] --undo
```

### Arguments & flags

| Token | Meaning |
|---|---|
| `<pkg>` | Package slug. Bare (no `@`) → **whole-package** relocation/deprecation. |
| `@<version>` | Optional. Present → **version-level** deprecation. Must be an exact published version (decision #1). |
| `<message>` | Positional deprecation message (npm-style). Alternative to `--message`. |
| `--message <text>` | The deprecation message. Mutually exclusive with a positional message. |
| `--moved-to <newpkg>[@<version>]` | Records the successor package (and optionally a version). Auto-fills the message if none was given (decision #2). |
| `--undo` | Lift the deprecation. Ignores/forbids `<message>` and `--moved-to`. |
| `--json` | Machine-readable result (`{slug, version?, deprecated, movedTo}`) instead of the human confirmation. |

### Argument rules (validation, before any network call)

1. Exactly one `<pkg>[@<version>]` positional is required.
2. **Not `--undo`:** a message is required — supply a positional `<message>`, or
   `--message`, or `--moved-to` (which auto-generates one). None of the three ⇒ error:
   `a deprecation message is required (pass a message, or --moved-to <pkg>)`.
3. **`--undo`:** `<message>`, `--message`, and `--moved-to` are not allowed ⇒ error.
4. Positional `<message>` and `--message` together ⇒ error.
5. `--moved-to` target is validated for slug shape (lowercase/digits/hyphens, matching
   the registry's `isValidSlug`); a malformed value is rejected locally.
6. Version selector: split on the first `@`. A leading `@` (would-be scoped name) is
   rejected — scoped names aren't supported yet (gap #14).

### Examples

```bash
# Deprecate one version with a message (npm-style positional)
fglpkg deprecate chart-3d@1.2.3 "security fix in 1.2.4; please upgrade"

# Rename / relocate a version — message auto-filled to "chart-3d has moved to chart-3d-ng"
fglpkg deprecate chart-3d@1.2.3 --moved-to chart-3d-ng

# Relocate a whole package (all versions), pinning a successor version
fglpkg deprecate chart-3d --moved-to chart-3d-ng@2.0.0

# Both a custom message and a successor
fglpkg deprecate chart-3d@1.2.3 "unmaintained" --moved-to chart-3d-ng

# Lift a deprecation
fglpkg deprecate chart-3d@1.2.3 --undo
```

---

## Publisher behaviour

`deprecate` is an **owner-only write**. It reuses the existing publisher auth path
(`publishJSON`, [registry.go:578](../internal/registry/registry.go#L578)) — the same
path `publish` uses — so it works with an OAuth session or a PAT.

### Mapping to registry endpoints

Per [gi-registry-workstream-c.md](gi-registry-workstream-c.md) §1.4:

| CLI form | Endpoint | Body |
|---|---|---|
| `deprecate pkg@ver …` | `PATCH /registry/packages/{pkg}/versions/{ver}` | `{ "deprecated": true, "deprecationMessage": "<msg>", "movedTo": "<new>" }` |
| `deprecate pkg …` (no version) | `PATCH /registry/packages/{pkg}` | `{ "deprecated": true, "deprecationMessage": "<msg>", "movedTo": "<new>" }` |
| `deprecate pkg@ver --undo` | `PATCH /registry/packages/{pkg}/versions/{ver}` | `{ "deprecated": false }` |
| `deprecate pkg --undo` | `PATCH /registry/packages/{pkg}` | `{ "deprecated": false }` |

`movedTo` is omitted from the body when not supplied. Re-running `deprecate` on an
already-deprecated target is idempotent and updates the message/successor (this is how
you *edit* a deprecation).

### New registry client methods

In [internal/registry/registry.go](../internal/registry/registry.go), alongside the
`Publish*` family:

```go
// PublishDeprecateVersion sets or clears deprecation on one version.
func PublishDeprecateVersion(slug, version, message, movedTo string, undo bool) error

// PublishDeprecatePackage sets or clears deprecation on the whole package.
func PublishDeprecatePackage(slug, message, movedTo string, undo bool) error
```

Both build the JSON body per the table above and call
`publishJSON(http.MethodPatch, registryBase()+…, body)`. They translate HTTP status
into typed errors (below).

### Publisher output

```
$ fglpkg deprecate chart-3d@1.2.3 --moved-to chart-3d-ng
✓ Deprecated chart-3d@1.2.3
  message:  chart-3d has moved to chart-3d-ng
  moved to: chart-3d-ng
  Consumers can still install it; they'll see a deprecation warning.

$ fglpkg deprecate chart-3d@1.2.3 --undo
✓ Cleared deprecation on chart-3d@1.2.3
```

`--json` emits `{"slug":"chart-3d","version":"1.2.3","deprecated":true,"movedTo":"chart-3d-ng"}`.

### Publisher error handling

Map registry responses to actionable messages (reuse the login-vs-not-found framing
already used by `privateHint`, [cli.go:65](../internal/cli/cli.go#L65)):

| Condition | HTTP | Message |
|---|---|---|
| Not authenticated | 401 | `you must be logged in to deprecate a package — run 'fglpkg login'` |
| Not the owning partner | 403 | `only the owning partner can deprecate chart-3d` |
| Package unknown | 404 | `no such package 'chart-3d'` (or the login hint if the caller is anonymous) |
| Version unknown | 404 | `chart-3d has no published version 1.2.3` |
| `--moved-to` malformed | (local) | `--moved-to: 'Chart_3D' is not a valid package name` |
| Message over cap | 400 | `deprecation message exceeds the 512-byte limit` (mirrors the registry scalar cap) |

---

## Consumer surfacing (warn-only)

Read fields added to the client (from [gi-registry-workstream-c.md](gi-registry-workstream-c.md) §1.5).
Add to `PackageInfo` ([registry.go:53](../internal/registry/registry.go#L53)) and its
mappers (`apiVersionSummary` [registry.go:428](../internal/registry/registry.go#L428),
`apiPackageDetail` [registry.go:468](../internal/registry/registry.go#L468)):

```go
Deprecated         bool   `json:"deprecated,omitempty"`
DeprecationMessage string `json:"deprecationMessage,omitempty"`
MovedTo            string `json:"movedTo,omitempty"`
```

The consumer treats a version as deprecated if **either** the version-level flag **or**
the package-level flag is set (whole-package relocation applies to every version).

### `install` / `update` / `add`

At the resolve point ([cli.go:277](../internal/cli/cli.go#L277) and the transitive
resolver [resolver.go:614](../internal/resolver/resolver.go#L614)), when a resolved
version is deprecated, print to **stderr** (so stdout stays clean for scripting) and
**continue** — the install always proceeds (decision #4):

```
warning: chart-3d@1.2.3 is deprecated: security fix in 1.2.4; please upgrade
```

If `MovedTo` is set, add a second line with a copy-paste suggestion:

```
warning: chart-3d has moved to chart-3d-ng
         → consider: fglpkg install chart-3d-ng
```

De-duplicate: warn once per (package, version) per invocation, even if the package
appears multiple times in the transitive graph.

### `info`

In `printInfo` ([info.go:114](../internal/cli/info.go#L114)), when deprecated, add a
block using the existing `printField` helper ([info.go:168](../internal/cli/info.go#L168)),
placed prominently right under the header:

```
chart-3d@1.2.3
──────────────

  Deprecated:  yes — security fix in 1.2.4; please upgrade
  Moved to:    chart-3d-ng

  Description: ...
```

`--json` output includes `deprecated`, `deprecationMessage`, `movedTo`.

### `outdated`

In `buildOutdatedRow` / `printOutdatedTable` ([outdated.go:112](../internal/cli/outdated.go#L112),
[outdated.go:198](../internal/cli/outdated.go#L198)), append a note to any row whose
installed version is deprecated:

```
Package    Current  Wanted  Latest  Notes
chart-3d   1.2.3    1.2.4   1.2.4   deprecated → chart-3d-ng
```

`--json` rows gain `"deprecated": true` and `"movedTo": "chart-3d-ng"`.

---

## Files touched (fglpkg)

- **New** [internal/cli/deprecate.go](../internal/cli/deprecate.go) — `cmdDeprecate(args []string) error`: arg parsing/validation, dispatch to the client methods, human/JSON output.
- [internal/cli/cli.go](../internal/cli/cli.go) — add `case "deprecate":` to the dispatch switch (near the other publisher verbs, ~line 110); add a usage line to `printUsage` (~line 2076); wire the consumer warning at the resolve site (~line 277).
- [internal/registry/registry.go](../internal/registry/registry.go) — `PublishDeprecateVersion` / `PublishDeprecatePackage`; new `PackageInfo` fields + mapper fields; a `PATCH` helper if `publishJSON` doesn't already take an arbitrary method (it does — it takes `method`).
- [internal/resolver/resolver.go](../internal/resolver/resolver.go) — carry the deprecation fields through resolution so the warning fires for transitive deps.
- [internal/cli/info.go](../internal/cli/info.go) — deprecation block in `printInfo` + JSON.
- [internal/cli/outdated.go](../internal/cli/outdated.go) — deprecated note in the table + JSON.
- [internal/cli/completion.go](../internal/cli/completion.go) — add `deprecate` to the command list.
- README — a "Deprecating & relocating packages" section (publisher usage + what consumers see).

---

## Testing strategy

**Unit — arg parsing** (`deprecate_test.go`): the validation matrix — missing message,
`--undo` with a message, positional + `--message` conflict, malformed `--moved-to`,
version vs whole-package split, leading-`@` rejection.

**Unit — client** (mock registry): each CLI form produces the correct method + path +
JSON body; status→error mapping (401/403/404/400).

**Unit — consumer surfacing:** a mock `PackageInfo` with `Deprecated=true` (a) prints
the install warning to stderr and still returns success; (b) renders the `info` block;
(c) adds the `outdated` note. Package-level vs version-level both trigger it.
De-duplication across a transitive graph.

**Integration** (mock registry, mirroring `info_test.go`): publish → deprecate a
version with `--moved-to` → `install` still succeeds and warns → `info` shows the block
→ `--undo` clears it → warning disappears. Whole-package variant.

**End-to-end (manual, pre-release):** against the live GI registry once §1 of the
registry spec lands — deprecate a real version, confirm the warning on a clean install
and the successor hint.

## Acceptance criteria

1. `fglpkg deprecate chart-3d@1.2.3 "msg"` marks the version deprecated; the registry read-model reflects it.
2. `fglpkg deprecate chart-3d@1.2.3 --moved-to chart-3d-ng` deprecates with an auto-generated message and records the successor.
3. `fglpkg install chart-3d@1.2.3` **succeeds** and prints the deprecation + moved-to warning to stderr.
4. `fglpkg info chart-3d` shows the `Deprecated:`/`Moved to:` block; `--json` includes the fields.
5. `fglpkg outdated` notes a deprecated installed dependency with its successor.
6. `fglpkg deprecate chart-3d@1.2.3 --undo` clears it; the warning stops.
7. `fglpkg deprecate chart-3d --moved-to chart-3d-ng` applies whole-package relocation, surfaced for every version.
8. Non-owner / logged-out attempts fail with the mapped, actionable errors.
9. Invalid arg combinations fail locally with a clear usage error and make **no** network call.

## Open questions (defer to the GI-side decisions)

- **Forward-reference `--moved-to`:** this spec assumes the successor need not exist yet
  (lets you publish the redirect first). If GI decides to validate existence
  ([gi-registry-workstream-c.md](gi-registry-workstream-c.md) Appendix B), the CLI adds
  a pre-flight existence check and a clearer error.
- **Response shape:** the exact body the `PATCH` returns isn't pinned in the registry
  spec; the client is written to tolerate either the updated summary object or a minimal
  `{slug, version, deprecated}` ack.
- **`outdated` column vs note:** shown here as a `Notes` column; final table layout to
  match whatever `outdated` adopts if other flags (e.g. signatures) also add notes.
