# Spec: Webcomponent packages (v1.1 — mixed packages)

**Status:** ✅ Implemented — shipped v2.5.0 (`8f4eb13`, 2026-06-20); mixed BDL+WC follow-up `af35901` (2026-06-30)
**Date:** 2026-06-19 (v1) · 2026-06-25 (v1.1 — mixed packages)
**Author:** Mike Folcher
**Tracking:** Genero webcomponent packaging for fglpkg

> **v1.1 update — mixed packages allowed.** The earlier mono-kind rule (a package was either BDL *or* webcomponent, never both) is reversed. A single package may now ship a BDL wrapper alongside its companion webcomponent in one artifact. The `type` field is now accepted-but-ignored; kind is derived from which assets the manifest actually declares. Pure-WC and pure-BDL packages behave exactly as before.

---

## Summary

Extend fglpkg so any package's manifest may declare Genero webcomponents (`COMPONENTTYPE` html/css/js bundles) alongside the existing BDL and Java JAR assets. The publisher lays out their source the same way Genero expects in a `programdir/webcomponents/` tree; fglpkg packs the right files into the right places, picks a variant tag automatically, installs into `.fglpkg/webcomponents/`, and wires the discovery path so direct-mode, GAS, and GWA all find the components without manual configuration.

Three package shapes are supported, all driven by which fields the manifest declares:
- **Pure BDL** — `.42m`/`.42f`/`.sch` + optional Java JARs (no `webcomponents`). Variant: `genero<N>`.
- **Pure webcomponent** — only `webcomponents` declared; no BDL/JAR fields. Variant: `webcomponent`.
- **Mixed** — `webcomponents` plus BDL/JAR fields. Variant: `genero<N>` (BDL fan-out wins; the WC rides along in each per-major artifact).

## Motivation

Today fglpkg packages can only ship BDL modules (`.42m`/`.42f`/`.sch`) and Java JARs. Webcomponents — a sibling asset type that Genero forms reference via the `WEBCOMPONENT … COMPONENTTYPE = "<name>"` syntax — have to be hand-copied into each project's `webcomponents/` directory. There is no shared distribution channel for them, no version control, no reproducibility.

Adding webcomponent packages closes the gap: a charting widget can be published once and reused across projects with `fglpkg install chart-3d`, with the same lockfile, audit, and ownership guarantees BDL packages already get.

## Goals

- A manifest may declare one or more `COMPONENTTYPE` bundles via a `webcomponents` field; the field is allowed alongside BDL fields so a BDL wrapper can pair with its companion webcomponent in a single package.
- Pure-WC packages (no BDL content) publish under variant `webcomponent` — no Genero-major duplication.
- Mixed packages and pure-BDL packages publish under `genero<N>` variants.
- `fglpkg install` extracts BDL files to `.fglpkg/packages/<name>/`, webcomponent bundles to `.fglpkg/webcomponents/<COMPONENTTYPE>/`, splitting from one zip when the package is mixed.
- `fglpkg env` makes installed webcomponents discoverable to **direct mode** (via `FGLIMAGEPATH`).
- `fglpkg env --gwa` emits the `--webcomponent` flags `gwabuildtool` expects.
- `fglpkg env` prints a documented WEB_COMPONENT_DIRECTORY hint for **GAS** users to copy into their `.xcf`.
- `fglpkg init --template webcomponent` scaffolds the layout.
- Schema (`fglpkg.schema.json`) validates the `webcomponents` array shape.

## Non-goals (v1)

- **Automatic .xcf editing for GAS.** fglpkg prints what to add but does not mutate the user's `.xcf` file. GAS deployment is the user's concern.
- **Cross-publication of the same webcomponent under multiple variants for pure-WC packages.** Pure-WC publishes once under `webcomponent`. (Mixed packages necessarily duplicate the WC bytes across each `genero<N>` artifact — this is a known tradeoff of mixed packaging.)
- **Source-mode webcomponents (raw .ts/.scss).** fglpkg ships whatever the publisher zips — there is no build step. Publishers compile/bundle their own assets before `fglpkg publish`.

## Manifest shape

### Pure-WC example

```json
{
  "name": "chart-3d",
  "version": "1.0.0",
  "description": "3D chart widget for Genero forms",
  "author": "michael.folcher@4js.com",
  "license": "MIT",
  "repository": "https://github.com/4js-mikefolcher/chart-3d",
  "keywords": ["webcomponent", "chart", "visualization"],
  "webcomponents": ["3DChart"],
  "dependencies": {
    "fgl": {
      "wc-theme-base": "^1.0.0"
    }
  }
}
```

### Mixed (BDL wrapper + WC) example

```json
{
  "name": "chart-3d",
  "version": "1.0.0",
  "description": "BDL wrapper + 3D chart webcomponent",
  "license": "MIT",
  "repository": "https://github.com/4js-mikefolcher/chart-3d",
  "programs": ["ChartHelper"],
  "webcomponents": ["3DChart"],
  "dependencies": { "fgl": {} }
}
```

### Field semantics

- **`type`** — **accepted but ignored**. Older manifests scaffolded by `fglpkg init --template webcomponent` set this to `"webcomponent"`; the value is preserved on round-trip but plays no role in validation, packing, or publish. New manifests should omit it. (Kept for back-compat with manifests already on disk and on the registry; do not rely on it.)
- **`webcomponents`** — non-empty array of `COMPONENTTYPE` names. Each name must match `^[A-Za-z0-9][A-Za-z0-9_-]*$` (digit-leading names like `3DChart` are valid — matches Genero's example COMPONENTTYPE strings) and must correspond to a directory `webcomponents/<NAME>/` in the publisher's source tree containing `<NAME>.html` at minimum.
- **`dependencies.fgl`** — any package can depend on any other (BDL or webcomponent). The resolver walks them uniformly; the installer routes the resulting artifacts based on the resolved package's own variant tag.
- **`dependencies.java`** — fully allowed on any package, including mixed ones. (A pure-WC package with Java deps doesn't make practical sense, but no rule prevents it.)
- **`main`, `programs`, `bin`, `root`, `files`** — all BDL-side fields keep their existing meanings. Their presence is what triggers the per-Genero-major variant fan-out at publish time.
- **`docs`, `hooks`, `genero`, `keywords`** — kind-agnostic; same meaning as today.

### Pack-time defaults

- **BDL walker** — runs whenever the manifest has any BDL content (`Files`, `Main`, `Programs`, `Bin`, `Root`, or Java deps). Uses the existing default file patterns (`*.42m`, `*.42f`, `*.sch`) when `Files` is omitted. Walks `m.Root` (default `.`).
- **Webcomponent walker** — runs whenever `webcomponents` is non-empty. For each declared name, walks `webcomponents/<NAME>/` and zips every file (filtered by `.fglpkgignore`) with the leading `webcomponents/` prefix stripped.
- **Both walkers** run for mixed packages; their outputs land side-by-side in a single zip.

## Source layout (publisher side)

```
chart-3d/
├── fglpkg.json
├── README.md
├── LICENSE
└── webcomponents/
    └── 3DChart/
        ├── 3DChart.html        # required
        ├── 3DChart.css         # optional
        ├── 3DChart.js          # optional
        └── assets/             # optional sub-assets (images, sub-bundles)
            └── icon.svg
```

This mirrors the Genero `programdir/webcomponents/` convention exactly. A publisher can drop their existing webcomponent source into a fresh `fglpkg init --template webcomponent` scaffold with zero rearrangement.

### Validation at pack time

- For each name in `webcomponents`, the directory `webcomponents/<NAME>/` must exist and contain `<NAME>.html`.
- A `webcomponents/<NAME>/` directory not listed in the manifest is **packed anyway** (treated as a sub-asset of a sibling component) but emits a warning suggesting the author add it to the manifest.
- The pack step **strips the leading `webcomponents/` prefix** from zipped paths so the in-zip layout is:

  ```
  3DChart/3DChart.html
  3DChart/3DChart.css
  ...
  ```

  This keeps the install step a clean rsync into `.fglpkg/webcomponents/` without a redundant nested `webcomponents/` directory.

## Pack & publish

### Pack

`fglpkg pack` picks a variant tag from the manifest's declared content:

| Manifest contents | Variant | Walkers run | Artifact filename |
|---|---|---|---|
| BDL fields only (no `webcomponents`) | `genero<N>` | BDL | `<name>-<version>-genero<N>.zip` |
| `webcomponents` only (no BDL fields) | `webcomponent` | WC | `<name>-<version>-webcomponent.zip` |
| BDL fields **and** `webcomponents` | `genero<N>` | BDL + WC | `<name>-<version>-genero<N>.zip` |

The webcomponent walker always strips the leading `webcomponents/` prefix from in-zip paths. The BDL walker preserves project-relative paths. A mixed zip therefore contains entries like `ChartHelper.42m` *and* `3DChart/3DChart.html` side-by-side; the installer uses the in-zip manifest's `webcomponents` array to tell them apart.

### Publish

`fglpkg publish` for any package shape:
1. `POST /registry/packages` — same as BDL (slug/name/description/visibility).
2. `POST /registry/packages/<slug>/versions` — same payload as today, with `dependencies` populated from the manifest's deps.
3. `PUT /registry/packages/<slug>/versions/<version>/artifacts/<variant>` — variant from the table above.
4. `POST /registry/packages/<slug>/versions/<version>/submit` — same as BDL.

The registry accepts arbitrary variant strings (verified in `4js-genero-intelligence`: variants are stored as strings without an enum check). **No registry-side schema change is required.**

### Detecting kind on consume

The variant tag drives the installer's behavior:
- `webcomponent` variant → pure-WC: extract straight into `.fglpkg/webcomponents/`, skip the BDL bin/manifest handling.
- `genero<N>` variant → BDL or mixed: peek at the in-zip manifest's `webcomponents` array; route any top-level zip entry whose first path component matches a declared `COMPONENTTYPE` to `.fglpkg/webcomponents/<COMPONENTTYPE>/`, everything else to `.fglpkg/packages/<name>/`.

A mixed package's WC bytes are duplicated across each `genero<N>` artifact (the WC itself doesn't change between Genero majors, but it lives inside each per-major zip). This is the known tradeoff of paired publishing — the WC is usually small (few KB to few hundred KB), and the developer experience of "one package, one install" usually wins.

## Install & discovery

### Install layout

```
project/
├── fglpkg.json
├── fglpkg.lock
└── .fglpkg/
    ├── packages/                # BDL packages (existing)
    │   └── ...
    ├── jars/                    # Java JARs (existing)
    │   └── ...
    └── webcomponents/           # NEW: webcomponent bundles
        └── 3DChart/
            ├── 3DChart.html
            ├── 3DChart.css
            └── 3DChart.js
```

The lockfile gains a `webcomponents` array parallel to `packages` and `jars`:

```json
{
  "webcomponents": [
    {
      "name": "chart-3d",
      "version": "1.0.0",
      "components": ["3DChart"],
      "downloadUrl": "https://.../packages/chart-3d/versions/1.0.0/artifacts/webcomponent",
      "checksum": "sha256-...",
      "requiredBy": ["<root>"]
    }
  ]
}
```

Note the `components` array — one lock entry per package, listing every `COMPONENTTYPE` it provides. This lets `fglpkg env --gwa` (below) emit the right flags without re-reading the manifest.

### Discovery — direct mode (FGLIMAGEPATH)

Genero's direct-mode webcomponent search includes:

```
<fglimagepath-dir>/webcomponents/<COMPONENTTYPE>/<COMPONENTTYPE>.html
```

So `fglpkg env` adds the **parent** of `webcomponents/` (i.e. `.fglpkg/`) to `FGLIMAGEPATH`:

```bash
export FGLIMAGEPATH="/Users/me/proj/.fglpkg:${FGLIMAGEPATH}"
```

Existing `FGLIMAGEPATH` values are preserved (prepended). Auto-detects local-vs-global the same way as `FGLLDPATH`/`CLASSPATH` today.

### Discovery — GAS

GAS uses a different convention — `WEB_COMPONENT_DIRECTORY` in the application's `.xcf`, listing the directory **containing** the `<COMPONENTTYPE>/` subdirs (so `.fglpkg/webcomponents/`, not `.fglpkg/`). fglpkg cannot edit the `.xcf` (deployment concern), so `fglpkg env` emits a comment line the user copies in:

```
# For GAS: add to your .xcf's <WEB_COMPONENT_DIRECTORY>:
#   $(application.path)/.fglpkg/webcomponents
```

This comment appears regardless of mode whenever any webcomponents are installed.

### Discovery — GWA (`fglpkg env --gwa`)

New CLI mode that emits one `--webcomponent <abs-path>` flag per installed component, suitable for `gwabuildtool` interpolation:

```bash
$ fglpkg env --gwa
--webcomponent /Users/me/proj/.fglpkg/webcomponents/3DChart
--webcomponent /Users/me/proj/.fglpkg/webcomponents/Heatmap
```

Typical use:

```bash
gwabuildtool -p . -o build/ $(fglpkg env --gwa)
```

Each `COMPONENTTYPE` directory is emitted as a separate `--webcomponent` flag (matching `gwabuildtool`'s "one switch per component" expectation).

## Cross-type dependencies

### BDL → webcomponent

A BDL package that requires a UI widget lists the webcomponent package in `dependencies.fgl`:

```json
{
  "name": "report-runner",
  "type": "bdl",
  "dependencies": {
    "fgl": { "chart-3d": "^1.0.0" }
  }
}
```

The resolver walks the dep as usual; the installer routes the resolved `chart-3d` artifact to `.fglpkg/webcomponents/` (based on the variant tag of the version it picks), and adds a lockfile entry under `webcomponents`. BDL consumers get the webcomponents on their `FGLIMAGEPATH` automatically via `fglpkg env`.

### Webcomponent → webcomponent

Same mechanism — a webcomponent package can list another in `dependencies.fgl` (e.g. a shared theme). The resolver pulls it in; both end up under `.fglpkg/webcomponents/`.

### Allowed combinations

Any combination of BDL fields, Java JAR deps, and `webcomponents` is permitted on a single manifest. Practical guidance:
- A **pure webcomponent** (UI bundle only) declares `webcomponents` and nothing BDL-side. Picks the `webcomponent` variant.
- A **BDL wrapper paired with a webcomponent** declares `webcomponents` plus `programs` (and/or `files`, `main`, `bin`, `root`, `dependencies.java`). Picks `genero<N>` and ships a single artifact per Genero major containing both halves.
- A **pure BDL package** declares no `webcomponents`. Picks `genero<N>`.

The only validation enforcement is the COMPONENTTYPE name format and uniqueness inside the `webcomponents` array.

## Schema changes (`schema/fglpkg.schema.json`)

- `"type"` property: free-form string, accepted-but-ignored. Preserves backward compatibility with manifests scaffolded under v1.0 that set `"type": "webcomponent"`.
- `"webcomponents"` property: array of strings matching `^[A-Za-z0-9][A-Za-z0-9_-]*$`, `minItems: 1`, `uniqueItems: true`.
- No conditional `allOf` rules — mixed combinations are permitted.

## `fglpkg init --template webcomponent`

Scaffolds a pure-WC starter:

```
mywidget/
├── fglpkg.json                 # webcomponents: ["MyWidget"]
├── README.md                   # one-line: "MyWidget webcomponent for Genero forms"
├── .gitignore                  # excludes .fglpkg/ and compiled artifacts
└── webcomponents/
    └── MyWidget/
        ├── MyWidget.html       # hello-world stub demonstrating gICAPI
        ├── MyWidget.css        # empty
        └── MyWidget.js         # registers a basic message handler
```

To create a mixed package (BDL wrapper + WC), scaffold with `--template library` instead, then add a `webcomponents` array and a `webcomponents/<NAME>/` source directory by hand. A `--template mixed` shorthand is deferred to a future iteration.

The HTML stub demonstrates the gICAPI shape so a publisher knows where to plug in interaction with the BDL backend.

## Registry interaction

**No server-side changes required for v1.** Two registry facts the design relies on:

1. The variant string is stored as-is — `webcomponent` is accepted alongside `genero4`/`genero6`.
2. The version `dependencies` field round-trips. (Once v2.4.1 ships, the CLI also reads it back correctly — see [docs/outstanding-work.md] / the v2.4.1 fix.)

**Optional registry follow-up** (nice-to-have, not blocking): explicitly index variant tags so the GI Public Portal can offer "Browse Webcomponents" pages and `fglpkg search --kind webcomponent` filters without scanning artifact sets per package. A `type` column on the package row would also work but is now redundant with the variant tag. Tracked as a coordination point in `docs/outstanding-work.md`.

## Acceptance criteria

End-to-end smoke test mirroring the existing fgl-log4j flow:

1. **Publish:** `fglpkg init --template webcomponent` → drop in a working `MyWidget.html` → `fglpkg publish` (with `--dry-run` first) lands a `mywidget-0.1.0-webcomponent.zip` on the GI test deployment.
2. **Direct mode install:** in a fresh consumer project, `fglpkg install` of a BDL package that depends on `mywidget` pulls both. `.fglpkg/webcomponents/MyWidget/MyWidget.html` exists. `fglpkg env` includes the right FGLIMAGEPATH entry. A form with `COMPONENTTYPE = "MyWidget"` resolves and renders.
3. **GAS dry-run:** with the WEB_COMPONENT_DIRECTORY entry added to the `.xcf`, the same form resolves under GAS.
4. **GWA build:** `gwabuildtool -p . -o build/ $(fglpkg env --gwa)` produces a GWA bundle whose preloaded assets include `MyWidget`.
5. **Lockfile reproducibility:** committing the lockfile and running `fglpkg install --frozen` (existing flag, if applicable) in a fresh checkout produces byte-identical `.fglpkg/webcomponents/` contents.
6. **Mixed package install splits correctly:** a manifest with both `webcomponents` and `programs` packs into a single `genero<N>` artifact; consumer install puts BDL files under `.fglpkg/packages/<name>/` AND webcomponent files under `.fglpkg/webcomponents/<COMPONENTTYPE>/`, driven by the in-zip manifest's `webcomponents` array.

## Phased implementation

1. **Phase 1 — Manifest, schema, init template.** Adds `type` and `webcomponents` fields, schema rules, `fglpkg init --template webcomponent`. No publish/install changes yet. Small, lands first.
2. **Phase 2 — Pack & publish.** File selector for webcomponent type, prefix stripping in the zip, `webcomponent` variant in the publish flow. End-to-end against the GI test deployment.
3. **Phase 3 — Resolve & install.** Resolver recognizes the variant tag, installer routes to `.fglpkg/webcomponents/`, lockfile gains the `webcomponents` array.
4. **Phase 4 — Env & GWA.** `fglpkg env` adds `.fglpkg/` to FGLIMAGEPATH when components are installed, prints the GAS hint comment, and supports `--gwa` for `gwabuildtool` flags.
5. **Phase 5 — Smoke test, docs.** End-to-end with a real component, README section, user-guide section, archive a tutorial example.

## Open questions

1. **What does `fglpkg env --gst` (Genero Studio output) look like for webcomponents?** GST has its own variable conventions — does the `FGLIMAGEPATH` line translate, or does GST want a dedicated `WEB_COMPONENT_DIRECTORY` variable? Needs verification with a real GST project.
2. **Multiple components per package — installer collision policy.** If `pkgA` ships component `Chart` and `pkgB` also ships `Chart`, the second install would overwrite the first. v1 should **error out** on collision and tell the user which two packages collide. Forcing a rename is the user's problem.
3. **What is in the `init --template webcomponent` HTML stub?** A minimal gICAPI handshake (receives a "hello" property, fires a "ready" event) is more useful than a blank `<div>`. Spec defers the exact contents to implementation; should be reviewable in the Phase 1 PR.

---

## Cross-references

- Outstanding-work / R&D handoff plan — add a new workstream entry pointing here.
- Market-readiness-gaps — webcomponent packaging is currently unlisted; add as a new P1 item (it's a Genero-specific differentiator, parallel in spirit to `fglpkg bdl`).
- `4js-genero-intelligence` repo — flag for the registry team whether to ship the `type` field server-side alongside Phase 1.
