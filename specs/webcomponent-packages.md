# Spec: Webcomponent packages (v1)

**Status:** Draft
**Date:** 2026-06-19
**Author:** Mike Folcher
**Tracking:** new — Genero webcomponent packaging for fglpkg

---

## Summary

Extend fglpkg with a new **package kind** — `type: "webcomponent"` — that ships Genero webcomponents (`COMPONENTTYPE` html/css/js bundles) instead of BDL modules. The publisher lays out their source the same way Genero expects in a `programdir/webcomponents/` tree; fglpkg packs, uploads, installs into `.fglpkg/webcomponents/`, and wires the discovery path so direct-mode, GAS, and GWA all find the components without manual configuration.

## Motivation

Today fglpkg packages can only ship BDL modules (`.42m`/`.42f`/`.sch`) and Java JARs. Webcomponents — a sibling asset type that Genero forms reference via the `WEBCOMPONENT … COMPONENTTYPE = "<name>"` syntax — have to be hand-copied into each project's `webcomponents/` directory. There is no shared distribution channel for them, no version control, no reproducibility.

Adding webcomponent packages closes the gap: a charting widget can be published once and reused across projects with `fglpkg install chart-3d`, with the same lockfile, audit, and ownership guarantees BDL packages already get.

## Goals

- A package's manifest declares it as `type: "webcomponent"`; it ships one or more `COMPONENTTYPE` bundles.
- Single artifact per version on the registry (variant `webcomponent`) — no Genero-major duplication.
- `fglpkg install` extracts the bundle to `.fglpkg/webcomponents/<COMPONENTTYPE>/` and updates the lockfile.
- `fglpkg env` makes installed webcomponents discoverable to **direct mode** (via `FGLIMAGEPATH`).
- `fglpkg env --gwa` emits the `--webcomponent` flags `gwabuildtool` expects.
- `fglpkg env` prints a documented WEB_COMPONENT_DIRECTORY hint for **GAS** users to copy into their `.xcf`.
- `fglpkg init --template webcomponent` scaffolds the layout.
- Schema (`fglpkg.schema.json`) enforces that `type` and `webcomponents` are used consistently.

## Non-goals (v1)

- **Mixed-kind packages.** A package is either BDL or webcomponent, never both. A BDL library that needs a UI widget declares the webcomponent package in `dependencies.fgl` and the resolver pulls it in.
- **Automatic .xcf editing for GAS.** fglpkg prints what to add but does not mutate the user's `.xcf` file. GAS deployment is the user's concern.
- **Cross-publication of the same webcomponent under multiple variants.** Variant is always `webcomponent`. If Genero ever ships a major release that breaks webcomponent compatibility, that's a v2 problem.
- **Source-mode webcomponents (raw .ts/.scss).** fglpkg ships whatever the publisher zips — there is no build step. Publishers compile/bundle their own assets before `fglpkg publish`.

## Manifest shape

```json
{
  "name": "chart-3d",
  "version": "1.0.0",
  "type": "webcomponent",
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

### Field semantics

- **`type`** (new) — package kind. One of `"bdl"` (default when omitted; the existing behavior) or `"webcomponent"`. Other kinds may be added later (e.g. `"theme"`, `"schema"`); the field is forward-extensible.
- **`webcomponents`** (new) — non-empty array of `COMPONENTTYPE` names. Required when `type: "webcomponent"`; forbidden otherwise. Each name must match `^[A-Za-z][A-Za-z0-9_-]*$` (Genero's `COMPONENTTYPE` lexical rule) and must correspond to a directory `webcomponents/<NAME>/` in the publisher's source tree containing `<NAME>.html` at minimum.
- **`dependencies.fgl`** — webcomponent packages may depend on other packages (BDL or webcomponent). The resolver walks them like any other fgl dep; the installer routes the resulting artifacts by the resolved package's own `type`.
- **`dependencies.java`** — forbidden for `type: "webcomponent"`. Webcomponents are frontend-only; a Java dep makes no sense and indicates the package should have been BDL.
- **`main`, `programs`, `bin`, `root`** — forbidden for `type: "webcomponent"`. These are BDL-only concepts.
- **`files`, `docs`, `hooks`, `genero`** — all keep their existing meanings. `files` defaults to the webcomponent glob set (see below) when omitted on a `type: "webcomponent"` manifest. `genero` is meaningful but has no enforcement effect since the registry stores variant `any`.

### Default `files` patterns

When `type: "webcomponent"` and `files` is omitted, the pack step uses:

```
webcomponents/<NAME>/**
```

…iterated over every entry in `webcomponents`. This gives publishers the no-config path: lay the source out the Genero way, and `fglpkg publish` does the right thing. Authors who need to exclude assets (build outputs, sourcemaps, dotfiles) use `.fglpkgignore` exactly as for BDL packages.

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

`fglpkg pack` honours the type:
- `type: "bdl"` (or omitted) — unchanged.
- `type: "webcomponent"` — uses the webcomponent file selector (above), strips the `webcomponents/` prefix in the zip, names the archive `<name>-<version>-webcomponent.zip`.

### Publish

`fglpkg publish` for a webcomponent package:
1. `POST /registry/packages` — same as BDL (slug/name/description/visibility).
2. `POST /registry/packages/<slug>/versions` — same payload as today, with `dependencies` populated from the manifest's fgl deps (java deps are forbidden, so the `java` array is always empty).
3. `PUT /registry/packages/<slug>/versions/<version>/artifacts/webcomponent` — variant literal `webcomponent` instead of `genero<N>`.
4. `POST /registry/packages/<slug>/versions/<version>/submit` — same as BDL.

The registry already accepts arbitrary variant strings (verified in `4js-genero-intelligence`: variants are stored as strings without an enum check). **No registry-side schema change is required for v1.**

### Detecting kind on consume

The variant string *is* the discriminator: a version whose artifact set contains a `webcomponent` variant is a webcomponent package; otherwise it's BDL (variants `genero4`/`genero5`/`genero6`/…). No sniffing-by-elimination is needed. Future asset kinds (e.g. `theme`, `schema`) get their own variant tag and the same rule extends naturally.

A package version is **mono-kind** — its artifact set is either all `webcomponent` (exactly one entry, since the variant is genero-agnostic) or all `genero<N>` (one entry per published major). Mixed sets are rejected at publish time; the spec deliberately does not allow shipping a BDL and a webcomponent under the same version, since the consume-side semantics (install location, env path) would have to fork on a per-version basis.

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

### Forbidden combinations

- A webcomponent package **cannot** declare `dependencies.java`. Pack-time validation rejects it with an error pointing at the `type` field.
- A BDL package **cannot** declare `webcomponents`. Same treatment.

## Schema changes (`schema/fglpkg.schema.json`)

Add:

- `"type"` property: `enum: ["bdl", "webcomponent"]`, default `"bdl"`. Free-form string for forward-compat is tempting but explicit enum gives editors useful autocomplete and catches typos.
- `"webcomponents"` property: array of strings matching `^[A-Za-z][A-Za-z0-9_-]*$`, `minItems: 1`, `uniqueItems: true`.
- Conditional `allOf`:
  - `if type=webcomponent` → `then` `webcomponents` required; `main`/`programs`/`bin`/`root`/`dependencies.java` forbidden.
  - `if type=bdl or absent` → `then` `webcomponents` forbidden (or unset).

## `fglpkg init --template webcomponent`

Scaffolds:

```
mywidget/
├── fglpkg.json                 # type: webcomponent, webcomponents: ["MyWidget"]
├── README.md                   # one-line: "MyWidget webcomponent for Genero forms"
├── .fglpkgignore               # excludes common build dirs
└── webcomponents/
    └── MyWidget/
        ├── MyWidget.html       # hello-world stub demonstrating gICAPI
        ├── MyWidget.css        # empty
        └── MyWidget.js         # registers a basic message handler
```

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
6. **Forbidden combinations rejected:** authoring a manifest with `type: "webcomponent"` plus `dependencies.java` fails at pack time with a clear error pointing at the conflict; same for `type: "bdl"` plus `webcomponents`.

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
