# Spec: Webcomponent GWA flag filtering & pure-WC install namespace

**Status:** 📋 Not started — GIS-248 (spec ready)
**Date:** 2026-07-12
**Author:** Mike Folcher
**Tracking:** Follow-up to [webcomponent-packages.md](webcomponent-packages.md) (Phase 4 — Env & GWA)
**Origin:** Field report — `fglpkg env --gwa` emits `--webcomponent` flags for non-component directories (`docs/`, `examples/`) installed from a pure-WC package's docs globs (observed with `fjs-dashboard-charts ≥ 1.0.3`).

---

## Summary

Two defects in the webcomponent install/discovery path, sharing one root cause: fglpkg treats **every top-level directory under `.fglpkg/webcomponents/` as a Genero webcomponent**, when in reality only directories that carry the required `<COMPONENTTYPE>/<COMPONENTTYPE>.html` entry point are components.

1. **(Primary)** `fglpkg env --gwa` emits a `--webcomponent <dir>` flag for *every* subdirectory of `.fglpkg/webcomponents/`, including non-component trees such as `docs/` and `examples/`. `gwabuildtool` then receives flags for directories that contain no `<name>.html` entry point.
2. **(Secondary, low priority)** The pure-webcomponent installer extracts *all* top-level subdirectory trees from a package's zip into the shared `.fglpkg/webcomponents/` namespace. Non-component trees shipped via docs globs (e.g. `docs/`, `examples/`) pollute that namespace, and two packages that ship a same-named top-level directory (e.g. both ship `examples/`) clobber each other on re-install.

The fix defines a single, platform-accurate notion of "what is a component" (a directory containing `<dir>/<dir>.html`) and applies it in two places: the GWA/FGLIMAGEPATH emitters (fixes #1), and the pure-WC install routing (fixes #2). No registry changes are required.

## Background — how it works today

### Genero's webcomponent contract

A Genero webcomponent is a directory named after its `COMPONENTTYPE`, containing an HTML entry point of the **same name**:

```
webcomponents/
└── 3DChart/
    ├── 3DChart.html      ← required entry point (name matches the directory)
    ├── 3DChart.css       ← optional
    ├── 3DChart.js        ← optional
    └── assets/           ← optional sub-assets
```

Genero's direct-mode loader searches for `<fglimagepath-dir>/webcomponents/<COMPONENTTYPE>/<COMPONENTTYPE>.html`; GAS uses `WEB_COMPONENT_DIRECTORY` (the directory *containing* the `<COMPONENTTYPE>/` subdirs); and `gwabuildtool` takes one `--webcomponent <dir>` flag per component directory. In all three, a directory with no `<name>.html` is not a loadable component. fglpkg already enforces this contract at **pack** time — [`collectWebcomponentFiles`](../internal/cli/cli.go) rejects a declared component whose `webcomponents/<NAME>/<NAME>.html` is missing ([cli.go:1084-1087](../internal/cli/cli.go#L1084-L1087)).

### How non-component trees end up under `webcomponents/`

A pure-WC package may also declare `docs` globs (kind-agnostic, [manifest.go:70](../internal/manifest/manifest.go#L70)). At pack time, [`addDocFilesToZip`](../internal/cli/cli.go#L1131-L1166) adds doc files at their **project-relative paths** at the zip root. So `fjs-dashboard-charts`'s zip contains, side by side:

```
3DChart/3DChart.html      (from the webcomponent walker, "webcomponents/" prefix stripped)
docs/guide.md             (from the docs glob, project-relative)
examples/demo.4gl         (from the docs glob, project-relative)
fglpkg.json               (manifest, at zip root)
```

On install, the **pure-WC path** [`installWebcomponent` → `extractWebcomponentZip`](../internal/installer/installer.go#L703-L761) extracts every zip entry that contains a slash into `.fglpkg/webcomponents/`, skipping only zip-*root* files (the manifest, stray root docs). It cannot distinguish a component directory from a docs tree, so it produces:

```
.fglpkg/webcomponents/3DChart/…     ✓ real component
.fglpkg/webcomponents/docs/…        ✗ pollution
.fglpkg/webcomponents/examples/…    ✗ pollution
```

> **Note — the mixed-package path does NOT have this bug.** [`installBDL` → `extractZipRouted`](../internal/installer/installer.go#L606-L661) routes only the manifest-declared `COMPONENTTYPE` dirs to `webcomponents/`, and sends everything else (including `docs/`, `examples/`) into `.fglpkg/packages/<name>/`. The defect is specific to the pure-WC installer, which ignores the manifest's `webcomponents` array entirely.

### How the junk reaches gwabuildtool

[`GenerateGWA`](../internal/env/env.go#L129-L156) lists every subdirectory of `.fglpkg/webcomponents/` (local and global) and emits a `--webcomponent` flag for each, with **no check** that the directory is actually a component:

```go
for _, e := range entries {
    if !e.IsDir() { continue }
    abs := filepath.Join(dir, e.Name())
    if !seen[e.Name()] {
        lines = append(lines, "--webcomponent "+abs)   // no <name>.html check
        seen[e.Name()] = true
    }
}
```

So `fglpkg env --gwa` emits `--webcomponent .../webcomponents/docs` and `.../examples` alongside the real `.../3DChart`, and `gwabuildtool -p . -o build/ $(fglpkg env --gwa)` receives flags for directories with no entry point.

The `FGLIMAGEPATH` emitters ([`buildFGLIMAGEPATH`](../internal/env/env.go#L72-L97) and [`GenerateLocal`](../internal/env/env.go#L277-L284)) have a milder version of the same blind spot: they decide whether to emit `FGLIMAGEPATH` at all by testing `len(entries) > 0` on the `webcomponents/` dir, so a directory containing *only* pollution still counts as "components installed."

### Why the lockfile can't help today

[webcomponent-packages.md](webcomponent-packages.md#L181-L198) designed a lockfile `components` array (one entry per WC package, listing the `COMPONENTTYPE`s it provides) precisely so `env --gwa` could emit flags without probing the filesystem. **It was never implemented** — `LockedWebcomponent` explicitly omits it ([lockfile.go:109-135](../internal/lockfile/lockfile.go#L109-L135)):

> COMPONENTTYPE names are not persisted here in v1 — they are inferred at runtime by listing subdirectories of the install location.

Populating it reliably would require component names to flow through the resolver, which builds the plan from **registry metadata that does not expose the manifest's `webcomponents` field** — so this path is blocked on a registry change and is out of scope here (see [Deferred](#deferred-the-lockfile-components-array)).

## Goals

- `fglpkg env --gwa` emits `--webcomponent` flags for genuine components only.
- `fglpkg env` / `env --gwa` share one platform-accurate definition of "component."
- The pure-WC installer stops polluting the shared `webcomponents/` namespace and stops clobbering across packages that ship same-named ancillary trees.
- No registry-side change; no lockfile schema change.

## Non-goals

- Persisting the lockfile `components` array (deferred — blocked on registry work).
- Changing the pack/publish side (packing already validates `<NAME>.html`; docs globs stay kind-agnostic).
- Editing GAS `.xcf` files (unchanged — still a copy/paste hint).

## Proposed changes

### Shared definition — `isComponentDir`

Add one helper (in `internal/env`, and mirror the notion in `internal/installer`) that encodes Genero's contract:

```go
// isComponentDir reports whether dir is a Genero webcomponent bundle: a
// directory containing the required <COMPONENTTYPE>.html entry point named
// after the directory itself. Non-component trees extracted into
// webcomponents/ by a package's docs globs (docs/, examples/) fail this test.
func isComponentDir(dir string) bool {
    name := filepath.Base(dir)
    info, err := os.Stat(filepath.Join(dir, name+".html"))
    return err == nil && !info.IsDir()
}
```

The entry-point name is matched exactly (`<name>.html`), which is what pack enforces and what Genero's loader resolves. (Case-insensitive filesystems will match case-insensitively; that is acceptable and matches how Genero itself resolves on those platforms.)

### Change A — filter the GWA and FGLIMAGEPATH emitters (fixes #1)

**Primary, high value, low risk.** In [`GenerateGWA`](../internal/env/env.go#L129-L156), skip directories that fail `isComponentDir`:

```go
for _, e := range entries {
    if !e.IsDir() { continue }
    abs := filepath.Join(dir, e.Name())
    if !isComponentDir(abs) { continue }        // NEW: skip docs/, examples/, …
    if !seen[e.Name()] {
        lines = append(lines, "--webcomponent "+abs)
        seen[e.Name()] = true
    }
}
```

Replace the `len(entries) > 0` presence tests in [`buildFGLIMAGEPATH`](../internal/env/env.go#L84-L95) and [`GenerateLocal`](../internal/env/env.go#L278-L283) with a shared `hasInstalledComponents(dir)` that counts only real component directories, so a `webcomponents/` dir holding only pollution no longer triggers an `FGLIMAGEPATH` export:

```go
func hasInstalledComponents(wcDir string) bool {
    entries, err := os.ReadDir(wcDir)
    if err != nil {
        return false
    }
    for _, e := range entries {
        if e.IsDir() && isComponentDir(filepath.Join(wcDir, e.Name())) {
            return true
        }
    }
    return false
}
```

Change A alone fully resolves the reported bug, independent of Change B: any non-component directory — however it got there — is filtered out.

### Change B — route pure-WC ancillary trees out of the shared namespace (fixes #2)

**Secondary, low priority.** This is the *root-cause* fix and also happens to keep `webcomponents/` clean at the source. The elegant move is to **delete `extractWebcomponentZip` and route the pure-WC install through the existing `extractZipRouted`**, so pure-WC and mixed packages install through one code path. The only difference between them is then simply "does it ship BDL modules or not," not "which extraction routine runs."

Rewrite [`installWebcomponent`](../internal/installer/installer.go#L363-L383) to mirror [`installBDL`](../internal/installer/installer.go#L300-L350):

```go
func (i *Installer) installWebcomponent(info *registry.PackageInfo) error {
    // … download + verify into tmp (unchanged) …

    // Which top-level dirs are real components? Prefer the in-zip manifest's
    // declared list; fall back to the <dir>/<dir>.html heuristic for a
    // malformed WC zip whose manifest omits the array.
    wcNames, err := readWebcomponentsFromZip(tmpName)
    if err != nil {
        return fmt.Errorf("cannot read manifest from zip: %w", err)
    }
    if len(wcNames) == 0 {
        wcNames = componentDirsInZip(tmpName)   // heuristic fallback
    }

    // Everything that is NOT a declared component (docs/, examples/, the
    // manifest) lands in the package's own dir — never the shared namespace.
    destDir := filepath.Join(i.packagesDir, info.Name)
    if err := os.RemoveAll(destDir); err != nil {
        return fmt.Errorf("cannot clean existing package dir: %w", err)
    }
    return extractZipRouted(tmpName, destDir, i.webcomponentsDir, wcNames)
}
```

`extractZipRouted` already: (a) clears each declared component dir under `webcomponents/` before extraction, (b) preserves the `COMPONENTTYPE/` prefix for component entries, and (c) sends all other entries (including the manifest and `docs/`, `examples/`) into `destDir`. So:

```
.fglpkg/webcomponents/3DChart/…             ← declared component (shared, correct)
.fglpkg/packages/fjs-dashboard-charts/
    ├── fglpkg.json
    ├── docs/guide.md                        ← per-package, no pollution, no clobber
    └── examples/demo.4gl
```

Because ancillary trees now live under the per-package `packages/<name>/`, two WC packages that both ship `examples/` can no longer clobber each other.

`componentDirsInZip` is a small, defensive helper (same "what is a component" notion as `isComponentDir`, applied to zip entries): return each top-level dir `D` for which the zip contains `D/D.html`. In practice a WC-variant zip always carries the manifest's `webcomponents` array, so this fallback rarely fires; it exists so a hand-built or legacy zip still routes correctly instead of dumping components into `packages/<name>/`.

**Bonus:** because the manifest and docs now land in `packages/<name>/`, pure-WC packages become visible to `fglpkg list` and `fglpkg docs <name>` (which read `packages/<name>/fglpkg.json` via [`findInstalledPackage`](../internal/cli/cli.go#L1907-L1923)) — today a pure-WC package appears in neither. See [Risks](#risks--backward-compatibility) for the one behavior shift this implies.

### Deferred — the lockfile `components` array

The [webcomponent-packages.md](webcomponent-packages.md#L189-L198) design would let `env --gwa` read component names from `fglpkg.lock` instead of the filesystem. We are **not** implementing it here because it is blocked on the resolver/registry exposing the manifest's `webcomponents` field, and because Changes A + B already resolve both reported problems using the filesystem as the source of truth. When the registry work lands, populating `components` (from the in-zip manifest at install time, written back to the lock) becomes a clean follow-up, and `GenerateGWA` can prefer the lock entry and fall back to the `isComponentDir` scan. The `isComponentDir` contract introduced here remains the correct fallback either way.

## Test plan

### New tests

- `internal/env` — `TestGenerateGWASkipsNonComponentDirs`: set up `webcomponents/{3DChart/3DChart.html, docs/README.md, examples/demo.4gl}`; assert exactly one flag, for `3DChart`.
- `internal/env` — `TestGenerateLocalSkipsFGLIMAGEPATHWhenOnlyNonComponents`: `webcomponents/docs/` only (no `<name>.html` anywhere); assert no `FGLIMAGEPATH` line.
- `internal/installer` — `TestInstallWebcomponentRoutesAncillaryTrees` (or extend `extractZipRouted` coverage): zip `{fglpkg.json: webcomponents:["3DChart"], 3DChart/3DChart.html, docs/guide.md, examples/demo.4gl}`; assert `webcomponents/3DChart/3DChart.html` exists, `webcomponents/docs` and `webcomponents/examples` do **not**, and `packages/<name>/docs/guide.md` + `packages/<name>/examples/demo.4gl` do.
- `internal/installer` — `TestInstallWebcomponentNoClobber`: two packages each shipping `examples/`; assert each package's `examples/` survives under its own `packages/<name>/` after both installs.
- `internal/installer` — `TestComponentDirsInZipFallback`: zip with **no** `webcomponents` in the manifest but a `Foo/Foo.html` entry; assert `Foo` is still routed to `webcomponents/`.

### Existing tests that MUST be updated (they rely on the old, unfiltered behavior)

- [`env_test.go` `TestGenerateGWAEmitsFlags`](../internal/env/env_test.go#L12-L40) creates `webcomponents/3DChart` and `webcomponents/Heatmap` as **empty** dirs. Under Change A these are filtered out and the test would see 0 flags. **Add `3DChart/3DChart.html` and `Heatmap/Heatmap.html`** so they qualify as components.
- [`env_test.go` `TestGenerateLocalIncludesFGLIMAGEPATH`](../internal/env/env_test.go#L45-L67) creates an empty `webcomponents/MyWidget`. **Add `MyWidget/MyWidget.html`** so the presence check passes.

These two migrations are the clearest signal that the old tests encoded the very assumption we are fixing (a bare directory = a component); updating them to add the entry point is part of the change, not a regression.

## Acceptance criteria

1. In a project where `fjs-dashboard-charts ≥ 1.0.3` is installed, `fglpkg env --gwa` emits `--webcomponent` flags only for directories that contain `<dir>/<dir>.html`; `docs/` and `examples/` are absent from the output.
2. `gwabuildtool -p . -o build/ $(fglpkg env --gwa)` runs without receiving flags for entry-point-less directories.
3. After `fglpkg install` of a pure-WC package that ships `docs`/`examples` globs, `.fglpkg/webcomponents/` contains only real component directories; the ancillary trees live under `.fglpkg/packages/<name>/`.
4. Installing two pure-WC packages that both ship a top-level `examples/` leaves both packages' `examples/` intact (no clobber).
5. `fglpkg env` (direct-mode `FGLIMAGEPATH`) is unchanged for a project that has at least one real component installed; it no longer emits `FGLIMAGEPATH` for a `webcomponents/` dir that holds only non-component trees.
6. `go test ./...` passes, including the updated existing env tests.

## Risks & backward compatibility

- **`env --gwa` output shrinks.** Any workflow that (incorrectly) depended on `docs`/`examples` flags loses them — this is the intended fix; those flags were never valid component inputs.
- **Change B shifts install layout for pure-WC packages.** Their manifest + ancillary trees now land in `.fglpkg/packages/<name>/`, so such packages start appearing in `fglpkg list` and become addressable by `fglpkg docs <name>`. This is arguably an improvement (today they are invisible to both), but it is a visible behavior change and re-install after upgrade is needed to clean stale `.fglpkg/webcomponents/{docs,examples}/` left by the old installer. Call this out in the changelog. If the `list`/`docs` visibility is judged undesirable, a fallback is to route ancillary trees under a reserved `.fglpkg/webcomponents/.pkg/<name>/` that the emitters ignore — but routing to `packages/<name>/` (mirroring the mixed path) is the recommended, more consistent choice.
- **Lockfile validation** for webcomponents ([lockfile.go:374-383](../internal/lockfile/lockfile.go#L374-L383)) only checks that `webcomponents/` is non-empty; it is unaffected by either change (a real component still lands there).
- **Global vs local scope:** both changes apply uniformly to `.fglpkg/webcomponents/` (local) and `~/.fglpkg/webcomponents/` (global), matching the existing scan order.

## Recommendation & phasing

- **Ship Change A now** — it is a ~15-line, high-value, low-risk fix that fully resolves the reported `env --gwa` bug on its own, with no install-layout change.
- **Ship Change B as a companion** (same PR if appetite allows, otherwise a fast follow) — it removes the pollution at the source, closes the clobbering gap, unifies the two install paths, and deletes `extractWebcomponentZip`. Its only non-trivial consideration is the `list`/`docs` visibility shift noted above.
- **Defer** the lockfile `components` array until the registry exposes the manifest's `webcomponents` field.

## Open questions

1. **`list`/`docs` visibility (Change B).** Is surfacing pure-WC packages in `fglpkg list` / `fglpkg docs` desired (recommended) or should ancillary trees be hidden under a reserved subdir? Decide before implementing Change B.
2. **Upgrade cleanup.** Should `fglpkg install`/`update` proactively remove stale `.fglpkg/webcomponents/{docs,examples}/` left by the pre-fix installer, or is a re-install sufficient? Leaning: a re-install of the WC package already clears its declared component dirs; a one-line note in the changelog covers the rest.

## Cross-references

- [webcomponent-packages.md](webcomponent-packages.md) — the parent design; this spec closes gaps in its Phase 3 (install routing) and Phase 4 (env/GWA). Its Open Question #2 (installer collision policy) is partially addressed here for the pure-WC case.
- Field report: `fjs-dashboard-charts ≥ 1.0.3` (`docs/`, `examples/` shipped via docs globs).
