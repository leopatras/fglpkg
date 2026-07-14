# Spec: `importRoot` — rebase the package archive to a build-output subdirectory

**Status:** Draft
**Date:** 2026-07-14
**Author:** Mike Folcher
**Motivation:** Field report while testing `fglpkg publish`. A project compiles its modules into a
build-output directory — e.g. `lib/com/fourjs/fglpkgtest/*.42m` — but the package needs to ship
those files as `com/fourjs/fglpkgtest/*.42m` (no `lib/` prefix), so that a consumer's
`IMPORT FGL com.fourjs.fglpkgtest.*` resolves. There is no way to do this today.
**Related:** [webcomponent-packages.md](webcomponent-packages.md) (the `webcomponents/` prefix-strip
precedent this generalizes); the `root` documentation in [user-guide.md](../docs/user-guide.md#L292).

---

## Summary

Add an optional manifest field, **`importRoot`**, naming a project subdirectory whose *contents*
become the root of the published archive. When set, every collected BDL/`bin` file's in-archive path
is computed **relative to `importRoot`** — so a source tree of `lib/com/fourjs/fglpkgtest/Module.42m`
with `"importRoot": "lib"` ships as `com/fourjs/fglpkgtest/Module.42m`.

The name reflects the field's purpose: it is the directory that becomes the **import-resolution
root** once the package is installed. Consumers put each installed package directory
(`~/.fglpkg/packages/<name>/`) on `FGLLDPATH` verbatim, so the package's namespace (`com/fourjs/…`)
must sit *directly* at that root — a stray `lib/` segment breaks every import.

To build the archive with a rebased (and optionally folded-in) layout **without ever mutating the
publisher's source tree**, packaging is refactored to a **temp-directory staging** model: the packer
materializes the exact archive layout in a throwaway temp directory, zips that directory, then
deletes it. Files that live *outside* `importRoot` can be pulled in via an explicit `include` list;
each is copied to the **top of `importRoot`** (the archive root).

Default (`importRoot` unset, no `include`) is behaviorally identical to today. The only observable
change for existing packages is a **one-time archive-byte reordering** (see [§ Determinism](#determinism--reproducibility)).

## Background — how it works today

### `root` filters the walk; it does not rebase paths

[`collectBDLFiles`](../internal/cli/cli.go#L1356) walks the tree at `m.Root` (default `"."`) and
matches each file against `m.Files` (default `*.42m`/`*.42f`/`*.sch`). But the path it stores in the
zip is computed **relative to the project directory**, never relative to `root`:

```go
relPath, relErr := filepath.Rel(".", path)   // cli.go:1388 — always rel to ".", not to root
```

So `"root": "lib"` still stores `lib/com/fourjs/fglpkgtest/*.42m`. The *documented* use of `root`
([user-guide.md:313](../docs/user-guide.md#L313), `"root": "com/fourjs/poiapi"`) actually **depends**
on this preservation — it exists so the `com/fourjs/poiapi/` path is kept intact and imports resolve.
`root` is therefore a **walk filter**, not a rebase. Redefining it would break every package that
follows that documented pattern.

### Why the prefix matters functionally

[`buildFGLLDPATH`](../internal/env/env.go#L166) adds each installed package directory to `FGLLDPATH`
verbatim — `filepath.Join(dir, e.Name())`, i.e. `~/.fglpkg/packages/<name>/` — and never appends
`root` ([env.go:178-207](../internal/env/env.go#L178)). So a zip storing
`lib/com/fourjs/fglpkgtest/X.42m` extracts to `…/packages/<name>/lib/com/fourjs/fglpkgtest/X.42m`,
and `IMPORT FGL com.fourjs.fglpkgtest.X` fails — the runtime looks for `com/…` directly under the
`FGLLDPATH` entry. Stripping `lib/` is a correctness requirement, not a cosmetic one.

### There is already one prefix-strip in the codebase

Webcomponent packing strips its own prefix:
[`collectWebcomponentFiles`](../internal/cli/cli.go#L1439) stores files relative to `webcomponents/`
via `filepath.Rel("webcomponents", relPath)` ([cli.go:1472](../internal/cli/cli.go#L1472)) so
`webcomponents/3DChart/3DChart.html` ships as `3DChart/3DChart.html`. `importRoot` is the same idea,
generalized to BDL content and made configurable.

### `fglpkg run` reads `root` from the *shipped* manifest

[`fglpkg run`](../internal/cli/cli.go#L1597) locates a program by joining the **installed** package
directory with the shipped manifest's `root`, then appending `<program>.42m`:

```go
workDir := pkgDir
if m.Root != "" { workDir = filepath.Join(pkgDir, m.Root) }   // cli.go:1599
modulePath := filepath.Join(workDir, moduleName+".42m")        // cli.go:1607
```

This is the key consistency constraint: if packing strips `lib/` from the on-disk layout, the shipped
`root` must be rewritten to describe the **post-strip** layout, or `fglpkg run` will look in a
directory that no longer exists.

### Archive entry metadata is already fixed

[`addFileToZip`](../internal/cli/cli.go#L1541) writes each entry with `zw.Create(name)`, which sets
**no per-file modification time from disk and no Unix mode bits**. Executable `bin` scripts are made
runnable by the *installer* after extraction ([installer.go:415](../internal/installer/installer.go#L415)),
not by a mode bit in the zip. Consequently the archive bytes depend only on **(entry name, entry
content, entry order)** — a fact the staging design leans on for reproducibility.

## Goals

- Let a publisher ship files from a build-output subdirectory with that subdirectory's prefix removed,
  so the package namespace sits at the archive root and imports resolve after install.
- Support pulling in files that live outside `importRoot`, **without mutating the source tree**.
- Keep `fglpkg run`, `FGLLDPATH`, `bin`, and the dependency cross-check consistent with the rebased
  layout — no separate manual step.
- Keep packaging **deterministic**: same source → same archive bytes, on any machine.
- Zero behavioral change when `importRoot`/`include` are unset (modulo the one-time byte reordering).

## Non-goals

- Redefining `root` (it stays a walk filter — see Background).
- Rebasing **docs**. Docs are added at their project-relative path at the archive root by
  [`addDocFilesToZip`](../internal/cli/cli.go#L1494) (like `README.md`/`USERGUIDE.md`) and stay there.
- Rebasing **webcomponents** (they keep their own `webcomponents/` strip).
- **Namespaced fold-in.** `include` places each file at the archive root under its basename. A file
  that must ship *under* a namespace (`com/fourjs/…`) belongs under `importRoot` in the source. (A
  `{from, to}` form could add explicit destinations later — see [Alternatives](#alternatives-considered).)
- Glob support in `include` (v1 takes explicit file paths).
- Any registry / GI-side change. This is entirely fglpkg-CLI (packer + manifest + schema + docs).

## Decisions (locked 2026-07-14)

1. **Field name:** `importRoot` (string, optional). Default `""` = no rebasing.
2. **Rebase semantics:** when set, each collected **BDL file and `bin` script** is placed in the
   archive at `filepath.Rel(importRoot, path)`. Docs and webcomponents are unaffected.
3. **Packaging is done via temp-directory staging** (chosen 2026-07-14): the packer materializes the
   full archive layout in a throwaway temp directory, zips it, and removes it. The publisher's source
   tree is **never** written to. (Rationale + rejected alternatives in
   [§ Alternatives considered](#alternatives-considered).)
4. **Out-of-tree files use an explicit `include` list** (`[]string`) — chosen 2026-07-14: each listed
   file is copied to the **top of `importRoot`** (the archive root) under its **basename**, matching
   the "copy into the `importRoot` directory" model. Rules:
   - Included paths are **skipped by the BDL walk** (so there is no double-handling), then copied to
     the root.
   - A file matched by `files`/`root` that lands **outside** `importRoot` and is **not** in `include`
     is a hard pack-time error (never emit a `../` entry) — fix `root`/`importRoot`, or list it.
   - Two entries (or an entry and a rebased file, or the manifest) resolving to the same archive path
     **collide → hard error** (basename flattening makes this the main sharp edge; caught at copy time
     because the staged target already exists).
5. **Shipped-manifest rewrite:** the published `fglpkg.json` (`PublishCopy`) has its `root` rewritten
   to `filepath.Rel(importRoot, root)` (becoming `"."` when `root == importRoot`) and `importRoot`
   cleared — the installed tree is already rebased, and installed-side consumers (`fglpkg run`) must
   see paths that match reality on disk. `include` is likewise dropped from the shipped copy.
6. **`programs`/`bin`/`Main` values are unchanged** — they are already expressed *relative to `root`*,
   and `root` is rewritten consistently, so they stay valid. (`Main` is a module reference relative to
   `root`, as today.)
7. **Determinism:** entries are added in a **stable lexical order** of archive path, and the packer
   keeps writing entries with `zw.Create` (constant metadata). It must **not** switch to
   `CreateHeader(FileInfoHeader(...))`, which would stamp the staged copies' mtimes into the archive
   and break reproducibility.
8. **Validation:** `importRoot` must be a clean relative path (not absolute, no `..`); when `root` is
   set, `root` must lie within `importRoot`. Each `include` entry must name an existing file; basename
   collisions (with another entry, a rebased file, or the manifest) are rejected at pack time.

## Determinism & reproducibility

Because entry metadata is fixed ([Background](#archive-entry-metadata-is-already-fixed)), archive bytes
are a function of the ordered list of `(name, content)` pairs. Two rules keep staging reproducible:

- **Fixed order:** after the stage is built, enumerate entries by `sort`-ed archive path and add them
  in that order (not raw `filepath.Walk` order, which is already lexical per-directory but interleaves
  differently across the merged tree). A single total order removes any dependence on walk internals.
- **Fixed metadata:** keep `zw.Create(name)`; never derive the entry header from the staged file's
  `os.FileInfo`.

**One-time consequence:** today's archive orders entries by category (BDL → webcomponents → docs →
manifest); the staged order is a single lexical sort. So the first `pack`/`publish` after this change
produces a **different SHA256 for every package**, even those not using `importRoot`. This is benign:
published versions are immutable and their checksums were recorded at publish time, so nothing already
in a registry or lockfile is invalidated. Our own golden-checksum tests (if any) are updated as part
of this change. Documented in the changelog.

## Proposed changes

### 1. Manifest fields (`internal/manifest/manifest.go`)

Add next to `Root` ([manifest.go:67](../internal/manifest/manifest.go#L67)):

```go
Root       string   `json:"root,omitempty"`       // walk filter: base directory to scan (default ".")
ImportRoot string   `json:"importRoot,omitempty"` // rebase base: its contents become the archive root
Include    []string `json:"include,omitempty"`    // extra files folded into the archive root, by basename
```

### 2. Schema (`schema/fglpkg.schema.json`)

Add after the existing `root` property ([schema:65](../schema/fglpkg.schema.json#L65)):

```json
"importRoot": {
  "type": "string",
  "description": "Directory whose contents become the archive root. Files under it are stored relative to it (e.g. lib/com/fourjs/x/M.42m → com/fourjs/x/M.42m). Defaults to \".\" (no rebasing)."
},
"include": {
  "type": "array",
  "description": "Extra project files folded into the archive root (top of importRoot), each stored under its basename.",
  "items": { "type": "string" }
}
```

### 3. Packer — staging model (`internal/cli/cli.go`)

Replace the direct-to-zip collectors with a two-phase `buildPackageZip`:

**Phase 1 — build the stage.** Create `stageDir, _ := os.MkdirTemp("", "fglpkg-pack-")` and
`defer os.RemoveAll(stageDir)`. Compute the archive path for each source using the existing rules,
then **copy** the bytes into `stageDir/<archivePath>` (creating parent dirs):

- BDL files (walk `root`, match `files`, honor `.fglpkgignore`, **skip anything listed in `include`**):
  archive path = `stagePathFor(importRoot, path)`.
- `bin` scripts: same rebase.
- Webcomponents: archive path via the existing `webcomponents/` strip.
- Docs: archive path = project-relative (archive root), unchanged.
- `include` entries: copy each listed file → `stageDir/<basename>` (archive root).
- Manifest: write `PublishCopy()` JSON to `stageDir/fglpkg.json`.

`stagePathFor` returns an error when a file resolves outside `importRoot` (a `..` prefix) — Decision 4.
Copying into a path that already exists in the stage is the collision guard — also a hard error.

**Phase 2 — zip the stage.** Walk `stageDir`, collect every file, `sort` by archive path, and for each
call `addFileToZip(zw, stagedDiskPath, archivePath)` (unchanged — still `zw.Create`, constant
metadata). Hash while writing, exactly as today.

This confines all rebasing/fold-in to *where a file is copied in the stage*; the zip step is a dumb,
sorted, deterministic walk.

### 4. Shipped manifest (`PublishCopy`, `internal/manifest/manifest.go`)

Extend [`PublishCopy`](../internal/manifest/manifest.go#L472):

```go
func (m *Manifest) PublishCopy() *Manifest {
    clone := *m
    clone.DevDependencies = Dependencies{}
    if clone.ImportRoot != "" {
        if rebased, err := filepath.Rel(clone.ImportRoot, cleanRoot(clone.Root)); err == nil {
            clone.Root = filepath.ToSlash(rebased) // lib/com/fourjs/x → com/fourjs/x
        }
        clone.ImportRoot = ""
    }
    clone.Include = nil // already materialized into the archive
    return &clone
}
```

(`cleanRoot` treats empty `root` as `"."`.)

### 5. Validation (`Validate`, `internal/manifest/manifest.go`)

In [`Validate`](../internal/manifest/manifest.go#L589), enforce Decision 8: `importRoot` clean-relative
and (if `root` set) `root` within `importRoot`. The `include`-existence and basename-collision checks
run at pack time (they need disk/stat access and the full computed layout), not during ordinary loads.

### 6. Docs (`docs/user-guide.md`)

Add a subsection after the `root` examples ([user-guide.md:313](../docs/user-guide.md#L313)) showing
the build-output layout (`lib/…`, `"root": "lib/com/fourjs/x"`, `"importRoot": "lib"`) and a short
`include` example, noting that `include` files land at the archive root by basename.

## Behavior — worked example (the reported case)

Project on disk:

```
fglpkg.json
lib/com/fourjs/fglpkgtest/ModuleA.42m
lib/com/fourjs/fglpkgtest/ModuleB.42m
dist/app.4st                                 (a loose file to ship at the root)
```

Manifest:

```json
{
  "name": "fglpkgtest",
  "version": "1.0.0",
  "root": "lib/com/fourjs/fglpkgtest",
  "importRoot": "lib",
  "files": ["*.42m"],
  "include": ["dist/app.4st"],
  "programs": ["ModuleA"]
}
```

Staged tree (temp dir), then zipped:

```
$TMPDIR/fglpkg-pack-XXXX/
  fglpkg.json                                (PublishCopy: root→"com/fourjs/fglpkgtest", importRoot/include dropped)
  app.4st                                    (include: copied from dist/app.4st to the top of importRoot)
  com/fourjs/fglpkgtest/ModuleA.42m          (copied from lib/com/fourjs/fglpkgtest/ModuleA.42m)
  com/fourjs/fglpkgtest/ModuleB.42m
```

| Stage | Path |
|---|---|
| Walk (`root`) finds | `lib/com/fourjs/fglpkgtest/ModuleA.42m` |
| Staged / stored (`Rel(importRoot, …)`) | `com/fourjs/fglpkgtest/ModuleA.42m` |
| `include` `dist/app.4st` staged at | `app.4st` (archive root, by basename) |
| Shipped `fglpkg.json` `root` (rewritten) | `com/fourjs/fglpkgtest` (`importRoot`/`include` dropped) |
| Extracted to | `~/.fglpkg/packages/fglpkgtest/com/fourjs/fglpkgtest/ModuleA.42m` |
| `FGLLDPATH` entry | `~/.fglpkg/packages/fglpkgtest/` |
| `IMPORT FGL com.fourjs.fglpkgtest.ModuleA` resolves | ✓ |
| `fglpkg run ModuleA` workDir (`pkgDir` + shipped `root`) | `…/fglpkgtest/com/fourjs/fglpkgtest/` → finds `ModuleA.42m` ✓ |

**Guidance:** set `root` to the directory that directly contains your program `.42m` files (unchanged
guidance — `fglpkg run` needs this), and `importRoot` to the build-output prefix to strip. A pure
library with no `programs` may set `root` shallower (or equal to `importRoot`); imports still resolve
because rebasing is driven by `importRoot`, not `root`. Use `include` only for loose files that belong
at the archive root — anything that must be namespaced belongs under `importRoot` in the source.

## Consistency matrix

| Concern | With `importRoot` set | OK? |
|---|---|---|
| `FGLLDPATH` import resolution | namespace sits at archive root; resolves | ✓ (the goal) |
| `fglpkg run` | shipped `root` rewritten to post-strip dir | ✓ (Decision 5) |
| `bin` scripts | rebased for storage; values stay relative to rewritten `root`; installer still chmods | ✓ |
| `programs` / `Main` | relative to `root`, which is rewritten consistently | ✓ |
| Dependency cross-check | reads the shipped `fglpkg.json` at archive root (unmoved) | ✓ |
| Docs (`README`/`USERGUIDE`/`docs/`) | not rebased; stay at archive root | ✓ (Non-goal) |
| Webcomponents | own `webcomponents/` strip, untouched | ✓ |
| Reproducibility | fixed metadata + sorted order; source tree untouched | ✓ ([§](#determinism--reproducibility)) |

## Test plan

- **Rebase** ([`pack_test.go`](../internal/cli/pack_test.go)): files under `lib/com/fourjs/x/` with
  `"importRoot": "lib"` → archive contains `com/fourjs/x/*.42m` (no `lib/`); `fglpkg.json` at root; a
  `bin` script rebased identically.
- **`include` fold-in:** a listed file lands at the archive root under its basename; it is skipped by
  the BDL walk (no duplicate); two entries with the same basename (or a basename clashing with a
  rebased file / the manifest) error; a missing entry errors.
- **Out-of-tree without mapping:** a matched `.42m` outside `importRoot`, not in `include` → pack-time
  error (no `../` entry).
- **`PublishCopy`:** `root: "lib/com/fourjs/x"`, `importRoot: "lib"` → shipped `root` = `com/fourjs/x`,
  `importRoot`/`include` empty; `root == importRoot` → shipped `root` = `"."`.
- **Validation:** absolute/`..` `importRoot`, and `root` outside `importRoot`, are each rejected.
- **Determinism:** packing the same tree twice yields byte-identical archives; and (regression) the
  no-`importRoot` archive still contains exactly today's file set (contents equal; order may differ —
  assert on the entry set + per-file bytes, not the whole-archive checksum).
- **Staging cleanup:** stage dir is removed on success and on error (inject a mid-pack failure; assert
  no leftover temp dir).
- **`fglpkg run` integration:** install a rebased package into a temp home; `fglpkg run <program>`
  resolves and runs.

## Acceptance criteria

1. With `"importRoot": "lib"`, `fglpkg pack --list` shows files at `com/fourjs/…` with no `lib/` prefix.
2. A package published with `importRoot` installs such that `IMPORT FGL com.fourjs.<pkg>.<Module>`
   compiles against it with no extra `FGLLDPATH` entries.
3. `fglpkg run <program>` works on that installed package.
4. An `include` entry places a loose file at the archive root (its basename) without any change to the
   source tree; the stage dir is gone afterward.
5. Packing is deterministic (same source → identical bytes) and the source tree is never modified.
6. A misconfiguration (unmapped file outside `importRoot`, `root` outside `importRoot`, `include`
   basename collision, or a missing `include` path) fails loudly at pack/validate time with an
   actionable message.

## Risks & backward compatibility

- **One-time checksum change** for all packages (entry reordering). Benign for published/immutable
  artifacts; see [§ Determinism](#determinism--reproducibility). Call it out in the changelog.
- **Basename flattening in `include`** is collision-prone by design (two `util.42m` from different dirs
  can't both be folded in). This is the accepted trade of the "top of `importRoot`" model; the error is
  clear, and the `{from, to}` form (Alternatives) is the escape hatch if it ever bites.
- **Extra I/O:** staging copies every packaged file once. Package trees are small (compiled `.42m` +
  docs), so the cost is negligible relative to compression; accepted in exchange for a source-tree-safe,
  auditable, uniform build path.
- **Temp-dir hygiene:** `os.MkdirTemp` under the OS temp dir + `defer os.RemoveAll`. A hard kill leaks
  a temp dir the OS reclaims; the source tree is never at risk.
- **Windows paths:** `filepath.Rel` yields OS separators; archive paths are normalized with
  `filepath.ToSlash` (matching the webcomponent path handling).

## Alternatives considered

- **Copy files into the real `importRoot`, zip, then delete (original proposal).** Rejected: mutates
  the publisher's source tree, so a crash/interrupt between copy and cleanup leaves stray files in the
  build dir (may be committed, may break the next `fglcomp`); a destination collision overwrites then
  deletes a real file (data loss); breaks on read-only CI checkouts; and IDE/file-watchers react to the
  transient files. Temp-dir staging keeps the same "assemble a tree then zip" model with none of these
  hazards, and folds the same files in via `include`.
- **Pure in-memory path mapping (no staging).** `addFileToZip` already decouples disk path from archive
  path, so rebasing/fold-in can be done without materializing anything — lighter on I/O. Not chosen:
  the materialized stage is auditable (it *is* the archive), keeps the zip step trivially deterministic,
  and gives future tree-level tooling (signing, SBOM, notarization) a real directory to operate on.
- **Explicit `include: [{from, to}]` destinations.** Considered; not chosen for v1 — the publisher
  wanted the simpler "copy to the top of `importRoot`" model (files land at the archive root by
  basename). The `{from, to}` form remains the natural extension if namespaced fold-in is later needed.
- **Redefining `root` to rebase.** Rejected: breaks the documented `root: "com/fourjs/poiapi"` pattern.

## Open questions

1. Should `include` accept globs (flattening all matches to the archive root by basename)? Deferred to
   keep v1 explicit; revisit if fold-in of many files becomes common.
2. Should `pack`/`publish` **warn** when files would land at the archive root under a `com/…`-style
   namespace but `importRoot` is unset (a likely-missing `importRoot`)? Deferred — a lint, not required
   for correctness.

## Cross-references

- Precedent: `webcomponents/` strip — [`collectWebcomponentFiles`](../internal/cli/cli.go#L1439).
- Entry-metadata handling — [`addFileToZip`](../internal/cli/cli.go#L1541).
- `FGLLDPATH` construction — [`buildFGLLDPATH`](../internal/env/env.go#L166).
- `fglpkg run` working-dir derivation — [cli.go:1597](../internal/cli/cli.go#L1597).
- Shipped-manifest production — [`PublishCopy`](../internal/manifest/manifest.go#L472).
- `bin` chmod after extraction — [installer.go:415](../internal/installer/installer.go#L415).
- `root` docs — [user-guide.md:292-340](../docs/user-guide.md#L292).
