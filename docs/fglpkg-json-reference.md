# The `fglpkg.json` Manifest — Reference

`fglpkg.json` is the **package manifest** for the fglpkg toolchain. It is the
single source of truth that describes a Genero BDL package (or a project that
consumes packages): its identity, its dependencies, what gets published, and
what runs at install/publish time.

This document explains what the file is *for*, how it is *structured* and
*parsed*, and gives a *detailed description of every key*. It is a companion to
the machine-readable [JSON Schema](../schema/fglpkg.schema.json) and the
task-oriented [user guide](user-guide.md). Where the two ever disagree with the
CLI's own strict parser, the parser wins — see [Parsing & validation
rules](#parsing--validation-rules).

---

## Table of contents

- [1. Purpose & function](#1-purpose--function)
- [2. Where the file lives & how it is found](#2-where-the-file-lives--how-it-is-found)
- [3. Project vs. package: two roles, one file](#3-project-vs-package-two-roles-one-file)
- [4. How fglpkg decides what kind of package this is](#4-how-fglpkg-decides-what-kind-of-package-this-is)
- [5. Parsing & validation rules](#5-parsing--validation-rules)
- [6. Property reference](#6-property-reference)
  - [6.1 Identity & registry metadata](#61-identity--registry-metadata)
  - [6.2 Runtime compatibility](#62-runtime-compatibility)
  - [6.3 Entry points & runnable content](#63-entry-points--runnable-content)
  - [6.4 Packaging layout](#64-packaging-layout)
  - [6.5 Dependencies](#65-dependencies)
  - [6.6 Java dependency object](#66-java-dependency-object)
  - [6.7 Lifecycle hooks](#67-lifecycle-hooks)
  - [6.8 Tooling & legacy keys](#68-tooling--legacy-keys)
- [7. Worked examples](#7-worked-examples)
- [8. Editor integration](#8-editor-integration)
- [9. Field summary table](#9-field-summary-table)

---

## 1. Purpose & function

`fglpkg.json` plays the same role for Genero BDL that `package.json` plays for
Node.js or `Cargo.toml` plays for Rust. A single JSON file, checked into the
project root, drives every fglpkg operation:

| Operation | What the manifest supplies |
|---|---|
| `fglpkg install` | The dependency graph (`dependencies`, `devDependencies`, `optionalDependencies`) to resolve, lock, and download. |
| `fglpkg add` / `fglpkg remove` | The manifest is rewritten in place to record the change. |
| `fglpkg pack` / `fglpkg publish` | Identity (`name`, `version`), the file-selection rules (`root`, `importRoot`, `files`, `include`, `docs`, `bin`), and registry metadata (`description`, `license`, `repository`, `author`, `visibility`, `keywords`). |
| `fglpkg env` | Combined with the lock file to build `FGLLDPATH` / `CLASSPATH`. |
| `fglpkg bdl` | The list of runnable `programs`. |
| Install/publish lifecycle | The declarative `hooks` to run around each event. |
| Registry search & `fglpkg info` | `description`, `keywords`, `author`, `license`, `repository`. |

Because the manifest is consumed by both the local CLI **and** the registry, it
must stay accurate: fields such as `dependencies` and `genero` propagate to
everyone who later installs the package.

The canonical definition of the file's shape lives in Go, in
[internal/manifest/manifest.go](../internal/manifest/manifest.go) — the
`Manifest` struct and its `Load`, `Validate`, and `ValidateForPublish`
functions. The constant filename is `fglpkg.json` (never configurable).

---

## 2. Where the file lives & how it is found

- The manifest is always named **`fglpkg.json`** and lives in the **root of the
  project or package** (the directory you run `fglpkg` from).
- Its presence is one of the two signals (`fglpkg.json` **or** a `.fglpkg/`
  directory) that fglpkg uses to decide it is operating *inside a project*, and
  therefore that dependencies should install **locally** to `.fglpkg/` rather
  than globally. See "Local vs Global" in the [user guide](user-guide.md).
- `fglpkg init` scaffolds one interactively; `fglpkg init --template
  library|app|webcomponent` pre-fills it for a common project kind (see
  [internal/cli/templates.go](../internal/cli/templates.go)).
- The file is loaded with `manifest.Load(dir)`. When absent, some commands fall
  back to a blank in-memory manifest (`name` = the directory basename,
  `version` = `0.1.0`) via `LoadOrNew`, but publishing always requires a real
  file on disk.

---

## 3. Project vs. package: two roles, one file

The same schema serves two audiences. Nothing in the file *type-tags* which one
you are — the fields you populate imply it.

**A consuming project** typically only needs identity plus its dependency list:

```json
{
  "name": "myproject",
  "version": "1.0.0",
  "description": "My Genero BDL project",
  "author": "Jane Developer",
  "license": "MIT",
  "dependencies": {
    "fgl": {
      "myutils": "^1.0.0",
      "dbtools": "2.1.0"
    },
    "java": [
      { "groupId": "com.google.code.gson", "artifactId": "gson", "version": "2.10.1" }
    ]
  }
}
```

**A publishable package** adds the fields that describe *what to ship* and the
registry metadata that publishing requires:

```json
{
  "name": "poiapi",
  "version": "1.0.0",
  "description": "POI API for Genero BDL",
  "author": "Jane Developer",
  "license": "MIT",
  "repository": "https://github.com/org/poiapi",
  "visibility": "public",
  "genero": "^4.0.0",
  "root": "com/fourjs/poiapi",
  "main": "PoiApi.42m",
  "programs": ["PoiConvert", "PoiMerge"],
  "dependencies": {
    "java": [
      { "groupId": "org.apache.poi", "artifactId": "poi", "version": "5.2.3" }
    ]
  }
}
```

---

## 4. How fglpkg decides what kind of package this is

There is **no explicit "kind" field** (the legacy `type` key is ignored — see
[§6.8](#68-tooling--legacy-keys)). The kind is *derived* from what the manifest
declares:

- **Has webcomponents** — true when `webcomponents` is non-empty
  (`Manifest.HasWebcomponents`). Triggers the webcomponent walker at pack time.
- **Has BDL content** — true when any of `main`, `root`, `files`, `programs`,
  `bin`, or a Java dependency (in any scope) is present
  (`Manifest.HasBDLContent`). Triggers the per-Genero-major variant fan-out at
  publish time, because compiled BDL is version-specific.

The three practical shapes:

| Shape | `webcomponents` | BDL fields | Published as |
|---|---|---|---|
| **BDL package / library** | absent | present | One `genero<N>` variant per Genero major you build. |
| **Pure webcomponent** | present | absent | A single version-independent web bundle. |
| **Mixed** (BDL wrapper + its companion webcomponent) | present | present | One `genero<N>` variant per major; the unchanged web bundle rides along. |

---

## 5. Parsing & validation rules

`fglpkg.json` is parsed **strictly**. Understanding the rules avoids a class of
confusing errors:

- **Unknown fields are rejected**, not silently ignored. The decoder uses
  `DisallowUnknownFields()`, so a typo anywhere in the tree (e.g. `licence`
  instead of `license`, or a package name placed directly under `dependencies`
  instead of `dependencies.fgl`) fails the load with a pointed error.
- **`dependencies` accepts only the keys `fgl` and `java`.** Anything else
  produces a hint: *`Did you mean "dependencies.fgl.<name>"?`* — because the
  most common mistake is nesting package names one level too shallow.
- **Hook events and operations are a closed vocabulary.** Unknown event names
  (`postintsall`), unknown ops, or unknown fields on an operation all fail at
  load time rather than being skipped.
- **The removed `scripts` field** produces a migration error pointing you at
  `hooks`.
- **Round-tripping preserves field order and drops empty buckets.** When
  fglpkg rewrites the file (`fglpkg add`, etc.), it writes 2-space-indented JSON
  in the struct's field order and omits `devDependencies` /
  `optionalDependencies` when they are empty.

Two tiers of validation run on top of parsing:

1. **`Validate()`** — structural sanity, run on install and pack. Requires
   `name` and `version`; checks the `genero` constraint parses; checks Java deps
   have `groupId`/`artifactId`/`version`; checks `bin` names have no path
   separators and paths stay inside the package; checks `root`/`importRoot`
   share a path; validates `include`, `docs` globs, `webcomponents` names, and
   every hook operation.
2. **`ValidateForPublish()`** — everything in `Validate()` **plus** four fields
   that must be non-empty to publish: `description`, `license`, `repository`,
   `author`. All missing fields are reported in one message.

> The JSON Schema in [schema/](../schema/fglpkg.schema.json) is purely an
> editor aid for autocomplete and inline hints. The CLI's parser is
> authoritative; if the schema and CLI ever disagree, that is a bug worth
> reporting.

---

## 6. Property reference

Every property below is optional unless marked **Required**. Fields are grouped
by role; within each group the heading gives the JSON key, its type, and a
concise definition, followed by behavior notes.

### 6.1 Identity & registry metadata

#### `name` — string · **Required**
The package's registry identifier. Must be unique within a registry.
Pattern `^[a-zA-Z0-9][a-zA-Z0-9_-]*$`, 1–128 characters — conventionally
lowercase, no spaces. This is the string consumers put under
`dependencies.fgl` and pass to `fglpkg install <name>`.

#### `version` — string · **Required**
The package version in [semver](https://semver.org) form:
`MAJOR.MINOR.PATCH`, optionally with a `-prerelease` and/or `+build` suffix
(e.g. `1.4.0`, `2.0.0-rc.1`). Each publish must use a version that does not
already exist on the registry.

#### `description` — string
One-line summary shown by `fglpkg search` and `fglpkg info`. **Required to
publish.**

#### `author` — string
The author, as a bare name, an email, or `Name <email@example.com>`.
**Required to publish.**

#### `license` — string
An [SPDX identifier](https://spdx.org/licenses/) (e.g. `MIT`, `Apache-2.0`) or
a custom string. `fglpkg init` defaults new manifests to `UNLICENSED`.
**Required to publish.**

#### `repository` — string
URL of the source repository (e.g. `https://github.com/org/repo`).
**Required to publish.**

#### `keywords` — array of string
Free-form tags that aid registry search and discovery, e.g.
`["database", "utilities"]`. Advisory metadata only — fglpkg does not interpret
them. Entries must be unique.

#### `visibility` — string · `"public"` | `"private"`
Who can read the package on the registry. `"public"` (the default when the key
is omitted) means anyone can browse and install; `"private"` restricts it to
members of the owning partner/tenant. **Applied only when the package is first
created** — it is ignored on subsequent publishes. The `fglpkg publish
--public` / `--private` flags override the manifest value for a single publish.

### 6.2 Runtime compatibility

#### `genero` — string (semver constraint)
Declares which Genero BDL **runtime** versions the package is compatible with,
using standard semver constraint syntax:

- `"^4.0.0"` — any 4.x.
- `">=3.20.0 <5.0.0"` — a bounded range.
- `"^3.20.0 || ^4.0.0"` — a union.
- `"*"` or omitted — compatible with any Genero version.

The constraint is parsed and validated at load (`semver.ParseConstraint`); an
unparseable constraint fails validation. Note the JSON key is `genero`, not
`geneoConstraint` (the Go field is `GeneroConstraint`).

### 6.3 Entry points & runnable content

#### `main` — string
The primary `.42m` entry point for the package. Advisory metadata identifying
the module a consumer would typically `IMPORT` first. Its presence also marks
the manifest as having BDL content (see [§4](#4-how-fglpkg-decides-what-kind-of-package-this-is)).

#### `programs` — array of string
Module names (**without** the `.42m` extension) that contain a `MAIN` block and
can therefore be launched with `fglpkg bdl <program>`. Example:
`["PoiConvert", "PoiMerge"]`. Entries must be unique. Listing a program here is
what makes it discoverable via `fglpkg bdl --list`.

#### `webcomponents` — array of string
The `COMPONENTTYPE` names this package provides to Genero forms. Each name must
match the Genero `COMPONENTTYPE` lexical rule `^[A-Za-z0-9][A-Za-z0-9_-]*$`
(digit-leading names like `3DChart` are valid) and must be unique in the list.
Each name corresponds to a source directory `webcomponents/<NAME>/` containing
at least `<NAME>.html`. When present, the array must have at least one entry.
May be combined freely with the BDL fields to ship a wrapper module alongside
its companion component (a "mixed" package).

### 6.4 Packaging layout

These fields control **which files land in the published archive and where**.
They are used by `fglpkg pack` / `fglpkg publish`; consumers never see most of
them (see the round-trip note under `importRoot`).

#### `root` — string
Base directory, relative to the manifest, under which fglpkg looks for package
files. Defaults to `"."`. The file walk that applies the `files` patterns is
rooted here. Presence marks the manifest as BDL content.

#### `files` — array of string (globs)
Glob patterns selecting the files to include in the published zip. Patterns are
matched against each file's **basename** while walking `root`. When omitted, the
default is `["*.42m", "*.42f", "*.sch"]` (compiled modules, compiled forms, and
schema files). Entries must be unique. Files can additionally be excluded with a
`.fglpkgignore` file.

#### `importRoot` — string
The directory whose *contents* become the **archive root**. Files packaged from
under it are stored relative to it, so the package namespace sits at the
`FGLLDPATH` root after install. For example, with `importRoot: "lib"`,
`lib/com/fourjs/x/M.42m` is archived as `com/fourjs/x/M.42m`.

Constraints and behavior:
- Must be a safe relative path (no absolute paths, no `..` escape).
- `root` and `importRoot` must lie **on the same path** — one must contain the
  other (e.g. `root: "."` with `importRoot: "lib"`, or `root: "lib/com/x"` with
  `importRoot: "lib"`). A disjoint pair is rejected because it could never
  rebase any file.
- Defaults to `"."` (no rebasing).
- **On publish, the shipped manifest is rewritten to describe the post-strip
  layout**: `root` is rebased to its path relative to `importRoot`, and both
  `importRoot` and `include` are dropped (they have already been applied to the
  staged archive). This is why consumers rarely see these keys.

#### `include` — array of string
Extra project files to fold into the **top of the archive root** (i.e. the top
of `importRoot`), each stored under its **basename**. Use this for files that
live outside `root` but must ship at the package root. Each entry must be a safe
relative path. Like `importRoot`, `include` is stripped from the published
manifest.

#### `docs` — array of string (globs)
Glob patterns selecting documentation files to include in the published zip,
e.g. `["README.md", "docs/**/*.md"]`. Supports `**` to match any number of
directory levels. **There is no default** — documentation is included only when
you declare it. Installed docs are browsable with `fglpkg docs`. Entries must be
unique.

#### `bin` — object (map: command name → script path)
Executable scripts shipped with the package so consumers can run them after
install. Keys are command names; values are paths to the scripts relative to
the package root, e.g. `{ "migrate": "scripts/migrate.sh" }`. Rules:
- A command name must be non-empty and must **not** contain path separators
  (`/` or `\`).
- A script path must be a safe relative path inside the package.
- Declared `bin` scripts are **always** included in the archive, even if a
  `.fglpkgignore` pattern would otherwise exclude them — dropping a declared
  script would silently break the package. Scripts are marked executable on
  install.

### 6.5 Dependencies

fglpkg has three dependency **scopes**, each with the same shape (a bucket
containing `fgl` and/or `java`). A given package name lives in **exactly one**
scope — the `add`/`remove` helpers move it rather than duplicating it.

#### `dependencies` — dependency bucket
**Production** dependencies: required at runtime by anyone who installs this
package. Pulled in transitively.

#### `devDependencies` — dependency bucket
**Development-only** dependencies for the *root project* — test harnesses,
linters, and similar tooling. They are installed for the root project but are
**never pulled in transitively** when another project depends on this package,
and they are **stripped from the published manifest** entirely (so consumers
never see your private tooling choices). Skipped by `fglpkg install
--production`.

#### `optionalDependencies` — dependency bucket
Installed like production dependencies, but a failure to resolve or download one
only emits a **warning** instead of aborting the install. Their transitive
dependencies inherit the same optional tolerance.

#### The dependency bucket shape

Each bucket is an object with two optional keys:

```json
{
  "fgl": {
    "myutils": "^1.0.0",
    "dbtools": ">=2.1.0 <3.0.0"
  },
  "java": [
    { "groupId": "org.apache.poi", "artifactId": "poi", "version": "5.2.3" }
  ]
}
```

- **`fgl`** — a map of BDL package name → semver **constraint** string. The
  constraint accepts the usual operators (`^`, `~`, ranges, `||`, exact pins).
- **`java`** — an array of [Java dependency objects](#66-java-dependency-object),
  resolved from Maven Central by default.

No other keys are allowed inside a bucket; putting a package name directly under
`dependencies` (rather than `dependencies.fgl`) is the classic mistake the
parser catches with a hint.

### 6.6 Java dependency object

Each entry in a bucket's `java` array declares a JAR by Maven coordinates.

#### `groupId` — string · **Required**
The Maven groupId, e.g. `com.google.code.gson`.

#### `artifactId` — string · **Required**
The Maven artifactId, e.g. `gson`.

#### `version` — string · **Required**
The **exact** Maven version, e.g. `2.10.1`. Version *ranges are not supported*
for Java dependencies — pin a single version.

#### `checksum` — string
Optional SHA-256 hex digest of the JAR. When present, the downloaded JAR is
verified against it before use. When absent, the download from Maven Central is
trusted without an integrity check.

#### `jar` — string
Optional override of the computed JAR filename. Defaults to
`<artifactId>-<version>.jar`.

#### `url` — string
Optional override of the download URL entirely. Useful for mirrors or
non-standard repositories. When omitted, fglpkg builds the standard Maven
Central URL (`https://repo1.maven.org/maven2/...`) from the coordinates.

### 6.7 Lifecycle hooks

#### `hooks` — object (map: event → array of operations)
Declares steps to run on well-known lifecycle events. The vocabulary is an
**intentionally closed set of declarative operations** — arbitrary shell
commands are *not* supported, because shell-based hooks are the dominant
supply-chain attack vector in mainstream package managers.

Hooks run on the **project (consumer) manifest**, not on dependency manifests,
and always execute with the **project root** as the working directory. A failure
in any operation aborts the surrounding command; later operations in the same
hook are skipped.

**Events** (each maps to an ordered list of operations):

| Event | When it fires |
|---|---|
| `preinstall` | Before `fglpkg install` starts resolving packages. |
| `postinstall` | After every dependency has been installed. |
| `prepublish` | Before `fglpkg publish` builds the zip. |
| `postpublish` | After `fglpkg publish` finishes the registry update. |
| `preuninstall` | Before `fglpkg remove` deletes a package. |

**Operations** (the closed vocabulary; each object needs an `op` key):

- **`copy-files`** — copy a file, a directory tree, or every match of a glob.
  Requires `from` (a relative path or glob using `*`, `?`, `[…]`) and `to` (a
  relative destination directory, created if missing, or a single-file
  destination). The `path` field is not valid here.
- **`mkdir`** — create a directory and its parents. Requires `path`. It is a
  no-op if the directory exists and fails if the path exists as a file. The
  `from`/`to` fields are not valid here.

All of `from`, `to`, and `path` must be **safe relative paths** — absolute
paths and `..` traversal are rejected at manifest load time.

```json
{
  "hooks": {
    "postinstall": [
      { "op": "mkdir", "path": "var/cache" },
      { "op": "copy-files", "from": "templates/*.tpl", "to": "share/templates" }
    ],
    "prepublish": [
      { "op": "copy-files", "from": "vendor", "to": "dist/vendor" }
    ]
  }
}
```

> More operations (`fetch-jar`, `compile-bdl`) are planned; the schema is
> designed so they can be added without breaking existing manifests.

### 6.8 Tooling & legacy keys

#### `$schema` — string
An optional reference to the JSON Schema so editors provide autocomplete and
inline validation (see [§8](#8-editor-integration)). fglpkg itself never
validates or uses this field — it exists only so the strict parser does not
reject manifests that opt into editor tooling. Preserved on round-trip.

#### `type` — string · deprecated / accepted-but-ignored
Older manifests set `"type": "webcomponent"` to flag a pure-webcomponent
package. The package kind is now *derived* (see
[§4](#4-how-fglpkg-decides-what-kind-of-package-this-is)), so this field plays
no role in validation, packing, or publish. It is preserved on round-trip for
backward compatibility but should be **omitted on new manifests**.

#### `scripts` — removed
The previous `scripts` field was defined but never executed and has been
removed. A manifest that still uses it fails to load with an error pointing at
`hooks`. Convert each entry to a declarative hook operation.

---

## 7. Worked examples

### A consuming application

```json
{
  "name": "salesapp",
  "version": "2.3.0",
  "description": "Internal sales order application",
  "author": "ACME Dev Team",
  "license": "UNLICENSED",
  "dependencies": {
    "fgl": {
      "dbtools": "^2.0.0",
      "reportkit": "~1.4.0"
    },
    "java": [
      { "groupId": "com.google.code.gson", "artifactId": "gson", "version": "2.10.1" }
    ]
  },
  "devDependencies": {
    "fgl": { "ggc-test": "^1.0.0" }
  }
}
```

### A publishable BDL library with a namespaced layout

```json
{
  "$schema": "https://fglpkg.io/schema/v1/fglpkg.schema.json",
  "name": "poiapi",
  "version": "1.2.0",
  "description": "Apache POI wrapper for Genero BDL",
  "author": "Jane Developer <jane@example.com>",
  "license": "MIT",
  "repository": "https://github.com/org/poiapi",
  "keywords": ["office", "xlsx", "poi"],
  "visibility": "public",
  "genero": "^4.0.0",
  "root": "lib/com/fourjs/poiapi",
  "importRoot": "lib",
  "main": "PoiApi.42m",
  "programs": ["PoiConvert"],
  "files": ["*.42m", "*.42f", "*.sch"],
  "docs": ["README.md", "docs/**/*.md"],
  "bin": { "poi-migrate": "scripts/migrate.sh" },
  "dependencies": {
    "java": [
      { "groupId": "org.apache.poi", "artifactId": "poi", "version": "5.2.3" }
    ]
  }
}
```

Here `importRoot: "lib"` strips the `lib/` prefix so the archive stores
`com/fourjs/poiapi/PoiApi.42m` at its root; after install the namespace resolves
directly on `FGLLDPATH`.

### A pure webcomponent package

```json
{
  "name": "chart-3d",
  "version": "1.0.0",
  "description": "3D chart widget for Genero forms",
  "author": "Jane Developer",
  "license": "MIT",
  "repository": "https://github.com/org/chart-3d",
  "webcomponents": ["3DChart"],
  "dependencies": {
    "fgl": { "wc-theme-base": "^1.0.0" }
  }
}
```

### A mixed package (BDL wrapper + companion webcomponent)

```json
{
  "name": "chart-3d",
  "version": "1.0.0",
  "description": "3D chart widget with a typed BDL wrapper",
  "author": "Jane Developer",
  "license": "MIT",
  "repository": "https://github.com/org/chart-3d",
  "genero": "^4.0.0",
  "main": "chart_3d.42m",
  "programs": ["Chart3dDemo"],
  "webcomponents": ["3DChart"]
}
```

---

## 8. Editor integration

Point your editor at [schema/fglpkg.schema.json](../schema/fglpkg.schema.json)
for autocomplete, hover docs, and inline validation. The simplest way is to add
a `$schema` reference to the manifest itself:

```json
{
  "$schema": "https://fglpkg.io/schema/v1/fglpkg.schema.json",
  "name": "myproject",
  "version": "0.1.0"
}
```

Until the canonical URL is hosted, point at the local file
(`"./schema/fglpkg.schema.json"`) or configure a workspace-wide mapping in
`.vscode/settings.json` / your JSON language server. See
[schema/README.md](../schema/README.md) for per-editor setup (VS Code,
JetBrains, Neovim/LSP).

Remember that the schema is only an editor aid — the CLI's strict parser is the
authority (see [§5](#5-parsing--validation-rules)).

---

## 9. Field summary table

| Key | Type | Required | Role |
|---|---|---|---|
| `name` | string | **Yes** | Registry identifier. |
| `version` | string | **Yes** | Semver version. |
| `description` | string | Publish | One-line summary. |
| `author` | string | Publish | Author. |
| `license` | string | Publish | SPDX or custom license. |
| `repository` | string | Publish | Source repo URL. |
| `keywords` | string[] | No | Search/discovery tags. |
| `visibility` | `"public"`\|`"private"` | No | Registry visibility (first publish only). |
| `genero` | string | No | Genero runtime semver constraint. |
| `main` | string | No | Primary `.42m` entry point. |
| `programs` | string[] | No | Modules with `MAIN`, runnable via `fglpkg bdl`. |
| `webcomponents` | string[] | No | `COMPONENTTYPE` names provided. |
| `root` | string | No | Base dir for package files (default `.`). |
| `importRoot` | string | No | Dir whose contents become the archive root. |
| `files` | string[] | No | Globs to package (default `*.42m`,`*.42f`,`*.sch`). |
| `include` | string[] | No | Extra files folded into the archive root by basename. |
| `docs` | string[] | No | Globs of docs to package (no default). |
| `bin` | map | No | Command name → script path. |
| `dependencies` | bucket | No | Production deps (`fgl` + `java`). |
| `devDependencies` | bucket | No | Dev-only deps; not transitive; stripped on publish. |
| `optionalDependencies` | bucket | No | Best-effort deps; failure warns. |
| `hooks` | map | No | Declarative lifecycle operations. |
| `$schema` | string | No | Editor schema reference (ignored by fglpkg). |
| `type` | string | No | **Deprecated**, accepted-but-ignored. |

*Buckets* (`dependencies`, `devDependencies`, `optionalDependencies`) each
contain `fgl` (name → semver constraint) and/or `java` (array of Maven-coordinate
objects with `groupId`, `artifactId`, `version`, and optional `checksum`, `jar`,
`url`).

---

*This document reflects the manifest as implemented in
[internal/manifest/manifest.go](../internal/manifest/manifest.go) and
[schema/fglpkg.schema.json](../schema/fglpkg.schema.json). If you change the
`Manifest` struct or the schema, update this reference too.*
