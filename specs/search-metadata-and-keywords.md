# Spec: Complete `fglpkg search` metadata — descriptions in every result + keyword search

**Status:** ✅ Done — GIS-268 (closed 2026-07-17). All three fixes shipped: **(E)** Artifactory
`Search` sidecar enrichment (PR #12/#13); **(F)/(G)** the GI service side — updatable package
description + keyword storage and `q` matching — on `package-management`, plus the fglpkg client
push of the manifest's current `description` + `keywords` on every publish
(`registry.PublishUpdateMetadata`, PR #22, merged to `main`).
**Date:** 2026-07-15
**Author:** Mike Folcher
**Motivation:** Two defects surfaced while testing `fglpkg search` after the Artifactory merge
(PR #12): **(1)** result descriptions are frequently blank, and **(2)** searching by a package
`keyword` never matches. Both undermine discovery — the whole point of `search`. They are distinct
from the Genero-version annotation work in [genero-aware-search.md](genero-aware-search.md) (GIS-254)
but share the same command surface and should be designed together.
**Related:** [genero-aware-search.md](genero-aware-search.md) (Genero-version-aware search — the
sibling search-quality effort; the search list response is enriched by both),
[publish-rich-metadata.md](publish-rich-metadata.md) (per-version publish metadata),
[artifactory-secondary-repository.md](artifactory-secondary-repository.md) §18 (where ISSUE-E/F/G
were first logged; this spec is their new home).

---

## Summary

Make `fglpkg search` return **complete, correct metadata**:

1. **Descriptions appear for every result**, regardless of source repository, and stay current after
   the package's first publish. Two independent gaps produce today's blank descriptions:
   - **(E)** the Artifactory provider's `Search` never reads the package's `fglpkg.json` sidecar, so
     description/author are always empty for Artifactory-sourced rows (**fglpkg client**).
   - **(F)** the GI package `description` is *write-once* — set only when the slug is first created
     and never updated on republish — so a description added or edited later never reaches the
     registry (**GI service + a small client call**).
2. **Keywords are searchable** — `fglpkg search <keyword>` matches a package's declared `keywords`,
   which today are collected in the manifest but never transmitted, stored, or queried (**GI service
   + client**).

Each fix is tagged **[client]** / **[GI]** so the two code-bases can be scheduled independently.

## Background — how it works today

### The `search` display path is correct

Both the single-registry path ([`cmdSearch`](../internal/cli/cli.go#L723)) and the multi-provider
fan-out ([`searchAcrossProviders`](../internal/cli/cli.go#L773)) already print a `DESCRIPTION`
column, and [`registry.Search`](../internal/registry/registry.go#L254) maps the GI browse response's
`description` into [`SearchResult.Description`](../internal/registry/registry.go#L112). So blank
descriptions are a **data** problem, not a rendering one.

### (E) Artifactory `Search` omits description/author — [client]

[`ArtifactoryProvider.Search`](../internal/provider/artifactory.go#L203) builds
`SearchResult{Name, LatestVersion}` from the storage-API folder listing and stops there — it never
fetches the per-version sidecar `fglpkg.json`, so `Description` and `Author` are always empty for
Artifactory results. (`registry.SearchResult` even carries a `Source` field, but the description/
author fields are simply never populated on this path.)

### (F) GI package description is write-once — [GI] + [client]

On publish, [`PublishCreatePackage`](../internal/registry/registry.go#L281) sends the manifest
`description` **only when it creates the slug**. On every later publish the slug already exists →
`409` → the call is a no-op, and there is no other package-metadata write. The GI
`POST /registry/packages` handler likewise inserts `description` once at creation. Net: a description
added or changed in `fglpkg.json` after the first publish never propagates, so `search` shows the
original (often empty) value forever.

### (G) Keywords are never sent, stored, or matched — [GI] + [client]

- **Client:** the manifest has [`Keywords []string`](../internal/manifest/manifest.go#L45)
  (documented in README + `fglpkg.json` reference), but no publish call includes them — they are
  dropped on the client side.
- **GI storage:** `registry_packages` has **no keywords column** (migration `0020_registry.sql`);
  keywords are not persisted anywhere for registry packages.
- **GI query:** the browse handler's `q` filter matches only `p.name`, `p.slug`, and
  `p.description` — keywords are not (and cannot be) part of the match.

So `fglpkg search <keyword>` structurally cannot hit a keyword today.

## Design

### 1. Descriptions in every result

**(E) Enrich Artifactory `Search` — [client].** After listing candidate package folders, best-effort
fetch each hit's latest-version sidecar `fglpkg.json` (the provider already reads sidecars in
`FetchInfo`) and populate `Description` (and `Author`) on the `SearchResult`. Notes:
- One extra metadata read per matched package. Acceptable for the storage-API path and pruned by the
  `packages` allow-list; make it best-effort (a sidecar 404 / parse error leaves the fields blank
  rather than failing the search).
- Alternatively, defer enrichment to `info <pkg>` if per-result latency proves too high for large
  repos — but showing a description in `search` is the point, so enrich in `Search` by default.

**(F) Keep the GI package description current — [GI] + [client].** Preferred: add an owner-only
`PATCH /registry/packages/:slug` on GI that updates `description` (and, per §G, `keywords`), and call
it from `fglpkg publish` after the create/version step so the manifest's current values are pushed
every publish. Acceptable alternative (no new endpoint): at version-approval time, refresh the
package `description` from the just-approved version's manifest. Either way the client always sends
its current manifest metadata; the registry stops treating description as immutable-after-create.

### 2. Keyword search (G) — [GI] + [client]

- **[client]** Send `keywords` on publish (alongside the description update from §F — same
  `PATCH`/create payload). No new manifest field; `Keywords` already exists.
- **[GI] storage:** persist package keywords — either a `keywords` column on `registry_packages`
  (JSON array, mirroring the skills-metadata pattern) or a `registry_package_keywords` join table.
  Decide against the existing `tag` facet system (`registry_version_tags`, which is `key=value` and
  version-scoped): free-form keywords are package-scoped and don't carry a key, so a dedicated field
  is cleaner than overloading tags.
- **[GI] query:** extend the browse `q` filter to also match keywords (e.g. `… OR EXISTS(keyword
  LIKE ?)`), so `search <term>` matches name / slug / description / **keyword**. Keep it behind the
  same `q` param — no new query surface.
- **[client] display (optional):** none required; keywords need not be shown in the results table,
  only matched. A future `--json` already carries whatever fields the response includes.

### Client/GI split (scheduling)

| Item | fglpkg client | GI service |
|---|---|---|
| E — Artifactory search description/author | ✅ enrich `Search` from sidecar | — |
| F — description stays current | small: send current metadata + call update | ✅ `PATCH …/packages/:slug` (or approval-time refresh) |
| G — keyword search | small: send `keywords` on publish | ✅ store keywords + add to `q` match |

The **[client]-only** part of E ships independently and immediately (it needs no GI change). F and G
need the GI change first, then the client change that feeds it.

## Non-goals

- **Genero-version-aware annotation** of results — that is [genero-aware-search.md](genero-aware-search.md)
  (GIS-254); this spec does not duplicate it, though both enrich the same list response.
- **Ranking / relevance scoring** — keyword *matching* only (substring `LIKE`), not weighted ranking.
- **Retro-fixing existing published packages** — descriptions/keywords become correct on the next
  publish once F/G ship; no bulk backfill is specified here.
- **Tag/facet search** — the existing `?tag=key=value` facet system is unchanged.

## Testing

- **[client] Artifactory search (unit, recorded fixtures):** a repo with a sidecar carrying a
  description/author → `Search` returns those fields populated; a missing/invalid sidecar → fields
  blank, no error.
- **[client] publish sends metadata (unit):** publish issues the description+keywords update with the
  manifest's current values (assert the request body), including on a republish (existing slug).
- **[GI] keyword query (unit/integration):** a package whose only match is a keyword is returned by
  `q=<keyword>`; name/slug/description matches still work; non-matches excluded.
- **[GI] description update (integration):** create → change description → republish → browse shows
  the new description.
- **Back-compat:** a registry without keyword support (older GI) degrades gracefully — the client
  send is ignored and search still matches name/slug/description.

## Rollout

1. **[client] E** — Artifactory search enrichment (independent; ships in the next fglpkg patch).
2. **[GI] F+G** — `PATCH …/packages/:slug` (description + keywords) and the `q` keyword match, plus
   the keywords storage migration.
3. **[client] F+G** — publish sends current description + keywords once the GI endpoint exists.

No CLI flag changes; `search`/`publish` surfaces are unchanged.
