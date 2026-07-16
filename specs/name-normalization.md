# Spec: PyPI-style package-name normalization (canonical slugs)

**Status:** 📋 Not started — GIS-271 (spec ready)
**Date:** 2026-07-16
**Author:** Mike Folcher
**Motivation:** Package names that differ only in separators or case are treated as *different*
packages today, and underscores are rejected outright by the slug rule — yet the manifest schema
*accepts* them, so a name like `fgl_ai_sdk` validates locally but is rejected by the registry at
publish (a confusing, late, server-side error). Cargo and PyPI solve this by normalizing: `-`, `_`,
`.` and case don't fragment a package's identity. Adopt the PyPI/PEP 503 rule so `fgl_ai_sdk`,
`fgl-ai-sdk`, and `Fgl.AI.SDK` are one package.
**Related:** [genero-aware-search.md](genero-aware-search.md) + the search work (GIS-268, query
normalization), [fglpkg-prepublish-validation.md](fglpkg-prepublish-validation.md) and the manifest
lint (GIS-270, which can drop its underscore warning once this lands),
[artifactory-secondary-repository.md](artifactory-secondary-repository.md) §6 (the collision guard
compares names across repos — it must compare *canonical* names).

---

## Summary

Introduce a single **canonicalization** rule for package names and make the canonical form the
package's identity everywhere it matters (URLs, registry storage, lookup, the lockfile, search, and
collision detection). The manifest `name` keeps the author's chosen spelling as a **display name**;
the **slug** is the canonical form derived from it. Any two names that canonicalize to the same slug
are the same package.

## Decision (GIS-271, 2026-07-16)

Full PyPI / PEP 503 normalization:

```
canonical(name) = lowercase(name) with every maximal run of [-_.] replaced by a single "-"
                = re.sub(r"[-_.]+", "-", name).lower()   # PEP 503
```

| Input | Canonical slug |
|---|---|
| `fgl_ai_sdk` | `fgl-ai-sdk` |
| `Fgl.AI.SDK` | `fgl-ai-sdk` |
| `fgl__ai--sdk` | `fgl-ai-sdk` (runs collapse to one `-`) |
| `fgl-ai-sdk` | `fgl-ai-sdk` (already canonical) |

Chosen over a narrower "`_` → `-`" rule so behaviour matches PyPI exactly (case- and dot-insensitive
too), since the slug already had to be lowercase.

## Background — how it works today

- **Slug is lowercase + hyphen only, and used verbatim.** Client
  [`validSlugRe`](../internal/cli/cli.go#L83) = `^[a-z0-9][a-z0-9-]{0,62}[a-z0-9]$`; publish sends the
  name unchanged as the slug — [`slug := m.Name`](../internal/cli/cli.go#L1340) — with no
  normalization. GI enforces the same shape in `isValidSlug` (`registry-routes.ts:53`).
- **The schema is looser than the slug rule.** The manifest `name`
  [pattern](../schema/fglpkg.schema.json) is `^[a-zA-Z0-9][a-zA-Z0-9_-]*$` — underscores and
  uppercase pass local validation but are then rejected by the registry. This mismatch is the bug.
- **Slug validity is only checked interactively.** `isValidPackageSlug` runs in
  [`promptPackageSlug`](../internal/cli/cli.go#L3216) (`fglpkg init`) — never on the publish path.
- **Lookup is exact.** GI `getPackageBySlug` (`registry-routes.ts:115`) matches the slug exactly;
  browse/search `LIKE`s `p.name` / `p.slug` / `p.description` (`registry-routes.ts:312`).

## Design

### 1. One canonicalization function per code-base

- **Client:** a small helper (e.g. `internal/slug.Canonical(name string) string`, or on the
  `manifest` package). Lowercase, collapse `[-_.]+` → `-`.
- **GI:** the equivalent helper, used by *every* registry route that takes a slug.

**Validation.** After canonicalization the slug must satisfy the existing shape
`^[a-z0-9][a-z0-9-]{0,62}[a-z0-9]$` (2–64 chars, start/end alphanumeric). If it doesn't (e.g. a name
that is all separators, or too short/long), reject with a message that shows the canonical form —
e.g. `name "x." normalizes to slug "x-", which is not a valid package slug`.

### 2. Manifest: display name vs. slug

- `name` — the author's spelling, kept verbatim and shown in listings/`info`. Widen the schema
  pattern to allow `.` and uppercase: `^[A-Za-z0-9]([A-Za-z0-9._-]*[A-Za-z0-9])?$`.
- **slug** — `canonical(name)`, the identity. Derived, never stored in the manifest.

### 3. Client behaviour

- **Publish.** Compute `slug = canonical(m.Name)`; send `slug` as the identity and `m.Name` as the
  display name in `POST /registry/packages`. (Today [`PublishCreatePackage`](../internal/cli/cli.go#L1422)
  passes the raw name as both.) Print the resolved slug, and note it when `name != slug`.
- **Install / resolve.** A dependency key in `dependencies.fgl` may be written in any spelling.
  Canonicalize it before constructing `/registry/packages/<slug>/…` URLs and before matching lockfile
  entries. The user's manifest keeps their spelling; resolution uses the canonical slug.
- **Lockfile.** Record the **canonical slug** as the package key (`LockedPackage.Name`), so a lock is
  unambiguous and reproducible regardless of how a dependency was spelled. Back-compat holds:
  existing locks only contain already-canonical names (the old rule forbade `_`/`.`/uppercase), so
  no lock changes for existing projects. Manifest↔lock reconciliation matches by canonical slug.
- **Search / info / registry list.** Show the server's display `name`; identity is the slug.

### 4. GI registry — the canonical source of truth

Normalization lives server-side so the web portal, MCP search, and every client stay consistent; the
client mirrors it only for URL construction.

- **`POST /packages`.** `slug = canonical(body.slug ?? body.name)`; store `slug` + display `name`.
  Reject when the canonical slug is invalid. A `409` when the slug already exists (owner check) is the
  collision case below.
- **`getPackageBySlug` and all `:slug` routes** (versions, artifacts, submit, download).
  Canonicalize the path parameter before lookup, so `/registry/packages/fgl_ai_sdk/…` and
  `/registry/packages/fgl-ai-sdk/…` resolve to the same record.
- **Browse / search.** Match `canonical(q)` against `p.slug` (already canonical) in addition to the
  text `LIKE` on `name`/`description`, so either spelling of a query finds the package (this is the
  slug half of the GIS-268 search work).

### 5. Collision / dependency-confusion semantics

Two names that canonicalize to the same slug are **one** package. The first publish claims
`(slug, owner)`; a later publish of a *different* spelling by a *different* owner is a `409` conflict,
not a new package — surfaced with a clear message. This also tightens the multi-repository collision
guard ([artifactory-secondary-repository.md](artifactory-secondary-repository.md) §6): the guard must
compare **canonical** names, so `foo_bar` in one repo and `foo-bar` in another are recognised as the
same name and trigger the guard rather than installing as two packages.

### 6. Back-compat / migration

- Existing slugs are already lowercase+hyphen, so `canonical(slug) == slug` — **no data migration**,
  no URL changes for published packages.
- Existing lockfiles contain already-canonical names — unaffected, byte-identical.
- The only new behaviour: names that were previously rejected (containing `_`, `.`, or uppercase) are
  now accepted and normalized.

## CLI / UX

- `fglpkg init` accepts `_`/`.`/uppercase and echoes the slug it will publish under
  (`will publish as "fgl-ai-sdk"`).
- `fglpkg publish` prints the canonical slug and warns when `name != slug`, so the author sees the
  identity they are claiming.
- No new flags.

## Testing

- **Unit — `canonical()` (client + GI, table-driven):** `fgl_ai_sdk`, `Fgl.AI.SDK`, `fgl__ai--sdk`,
  an already-canonical name, and invalid results (all-separator, too short/long) → error.
- **Publish:** a name with `_` posts `slug` canonical and `name` as display; republish under the same
  canonical slug is additive.
- **Install:** a dependency written `fgl_ai_sdk` resolves to slug `fgl-ai-sdk`, hits the canonical
  URL, and the lock records `fgl-ai-sdk`.
- **GI:** `getPackageBySlug` canonicalizes the path; search matches either spelling; a cross-spelling
  publish by a different owner returns `409`.
- **Back-compat:** an existing hyphen-only slug is unchanged; a lock for already-canonical names is
  byte-identical to today.

## Non-goals

- **Renaming or redirecting already-published packages.** Existing slugs are canonical; nothing moves.
- **Unicode / IDN normalization.** Names remain ASCII (the existing charset); this spec only handles
  case and `-`/`_`/`.`.
- **Changing the version-string format** or any non-name identifier.

## Rollout

1. **GI first (source of truth):** canonicalize on store, lookup, and search. Back-compat, no migration.
2. **Client:** canonicalize name→slug on publish; canonicalize dependency names for URL + lock keys;
   widen the schema `name` pattern; `init`/`publish` UX.
3. **Follow-through:** GIS-270 lint drops the "underscore in name" warning; GIS-268 search consumes
   the query normalization.
