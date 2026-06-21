# fglpkg User Guide

This guide covers the day-to-day usage of fglpkg, the package manager for Genero BDL projects.

## Table of Contents

- [Getting Started](#getting-started)
- [Installing fglpkg](#installing-fglpkg)
- [Setting Up Your Environment](#setting-up-your-environment)
- [Creating a New Project](#creating-a-new-project)
- [Managing Dependencies](#managing-dependencies)
- [Publishing a Package](#publishing-a-package)
- [Working with Java JARs](#working-with-java-jars)
- [Webcomponent Packages](#webcomponent-packages)
- [Distributable Scripts](#distributable-scripts)
- [Lifecycle Hooks](#lifecycle-hooks)
- [Package Documentation](#package-documentation)
- [Registry Authentication](#registry-authentication)
- [Workspaces (Monorepos)](#workspaces-monorepos)
- [Lock Files](#lock-files)
- [Package Ownership](#package-ownership)
- [Troubleshooting](#troubleshooting)

---

## Getting Started

fglpkg manages three kinds of assets for your Genero BDL projects:

- **BDL packages** — compiled Genero modules (`.42m`, `.42f`, `.sch` files) published to a registry
- **Java JARs** — Java libraries downloaded from Maven Central (or custom URLs), needed when your BDL code calls into Java
- **Webcomponent packages** — html/css/js bundles published under a `COMPONENTTYPE` name and consumed by `WEBCOMPONENT` form fields. See [Webcomponent Packages](#webcomponent-packages).

## Installing fglpkg

### Download a Pre-built Binary

Visit the [Releases page](https://github.com/4js-mikefolcher/fglpkg/releases) and download the binary for your platform:

| Platform | Binary |
|---|---|
| Linux (Intel) | `fglpkg-linux-amd64` |
| Linux (ARM) | `fglpkg-linux-arm64` |
| macOS (Apple Silicon) | `fglpkg-darwin-arm64` |
| macOS (Intel) | `fglpkg-darwin-amd64` |
| Windows (Intel) | `fglpkg-windows-amd64.exe` |
| Windows (ARM) | `fglpkg-windows-arm64.exe` |

Place the binary in a directory on your `PATH`:

```bash
# macOS / Linux
sudo cp fglpkg-darwin-arm64 /usr/local/bin/fglpkg
sudo chmod +x /usr/local/bin/fglpkg
```

```powershell
# Windows — copy to a directory in your PATH
copy fglpkg-windows-amd64.exe C:\tools\fglpkg.exe
```

Verify the installation:

```bash
fglpkg version
```

### Build from Source

If you have Go installed:

```bash
git clone https://github.com/4js-mikefolcher/fglpkg.git
cd fglpkg
go build -o fglpkg ./cmd/fglpkg
sudo cp fglpkg /usr/local/bin/
```

## Setting Up Your Environment

fglpkg manages the `FGLLDPATH` and `CLASSPATH` environment variables so Genero can find installed packages and JARs.

### macOS / Linux

Add to your shell profile (`~/.bashrc`, `~/.zshrc`, or equivalent):

```bash
eval "$(fglpkg env --global)"
```

Then reload your shell:

```bash
source ~/.bashrc
```

Use `--global` in your shell profile so it always includes all globally installed packages, regardless of your current directory.

### Windows (cmd.exe)

Run before building, or add to a `setup-env.bat` script:

```cmd
@echo off
FOR /F "tokens=*" %%i IN ('fglpkg env --global') DO %%i
```

To run directly at the command prompt (single `%`):

```cmd
FOR /F "tokens=*" %i IN ('fglpkg env --global') DO %i
```

On Windows, `fglpkg env` outputs `SET` commands:

```
SET FGLLDPATH=C:\Users\you\.fglpkg\packages\poiapi;%FGLLDPATH%
SET CLASSPATH=C:\Users\you\.fglpkg\jars\poi-5.2.3.jar;%CLASSPATH%
```

### Genero Studio

Run `fglpkg env --gst` and paste the output into your project's environment variable settings:

```
FGLLDPATH=$(ProjectDir)/.fglpkg/packages/poiapi;$(FGLLDPATH)
CLASSPATH=$(ProjectDir)/.fglpkg/jars/poi-5.2.3.jar;$(CLASSPATH)
```

Genero Studio translates `$(ProjectDir)` to the actual project path and `;` to the platform-specific separator automatically.

### Environment Output Modes

`fglpkg env` varies its output depending on context and flags:

| Command | Scope | Format | Use case |
|---|---|---|---|
| `fglpkg env` | Auto (local if in project) | Shell (Unix) or SET (Windows) | Project-specific builds |
| `fglpkg env --global` | All global packages | Shell (Unix) or SET (Windows) | Shell profile setup |
| `fglpkg env --local` | Local `.fglpkg/` only | Shell (Unix) or SET (Windows) | Force local scope |
| `fglpkg env --gst` | Local `.fglpkg/` only | Genero Studio format | Genero Studio projects |

Key points:
- Existing `FGLLDPATH` and `CLASSPATH` values are preserved (fglpkg prepends its paths)
- All installed package directories are added to `FGLLDPATH`
- All downloaded JARs are added to `CLASSPATH`

### Home Directory

Everything fglpkg manages lives under `~/.fglpkg` by default. Override this by setting the `FGLPKG_HOME` environment variable:

```bash
# macOS / Linux
export FGLPKG_HOME=/opt/fglpkg
```

```cmd
REM Windows
SET FGLPKG_HOME=C:\fglpkg
```

## Creating a New Project

To start a new Genero BDL project with fglpkg:

```bash
mkdir myproject
cd myproject
fglpkg init
```

This interactively prompts for the package name, version, description, and author, then creates a `fglpkg.json` file:

```json
{
  "name": "myproject",
  "version": "0.1.0",
  "description": "",
  "author": "",
  "license": "UNLICENSED",
  "dependencies": {
    "fgl": {},
    "java": []
  }
}
```

## Managing Dependencies

### Local vs Global (Context-Aware)

fglpkg automatically detects whether to install packages locally or globally:

- **Inside a project** (directory has `.fglpkg/` or `fglpkg.json`): packages install to `.fglpkg/` in the project directory
- **Outside a project**: packages install to `~/.fglpkg/` (global)

You can override this with flags:

```bash
fglpkg install --local     # force local .fglpkg/
fglpkg install --global    # force global ~/.fglpkg/
```

These flags work on `install`, `remove`, `update`, `list`, and `env`.

When using local installs, add `.fglpkg/` to your `.gitignore`.

### Installing All Dependencies

If your project already has a `fglpkg.json` with dependencies listed, install them all:

```bash
fglpkg install
```

This resolves the dependency graph, writes a lock file (`fglpkg.lock`), downloads BDL packages from the registry, and downloads Java JARs from Maven Central. Because `fglpkg.json` exists, packages are installed locally to `.fglpkg/` by default.

### Adding a Package

To add a BDL package dependency:

```bash
# Add the latest version
fglpkg install myutils

# Add a specific version
fglpkg install myutils@1.2.0
```

This resolves the version, adds it to your `fglpkg.json`, and installs it.

### Dependency Scopes (prod / dev / optional)

Packages can be recorded under three scopes depending on when they should be installed:

| Scope | When installed | Added to | Transitively pulled? |
|---|---|---|---|
| `dependencies` (prod) | Always | default, or `-P` / `--save-prod` | Yes — consumers get these too |
| `devDependencies` | Developer workflows only, skipped with `--production` | `-D` / `--save-dev` | No — a library's dev deps are private to it |
| `optionalDependencies` | Attempted like prod, but failures warn and continue | `-O` / `--save-optional` | Yes — and the optional tolerance inherits to transitive deps |

```bash
fglpkg install mytester -D         # add to devDependencies
fglpkg install telemetry -O        # add to optionalDependencies
fglpkg install core-lib -P         # explicit prod (same as no flag)
fglpkg install --production        # install everything EXCEPT devDependencies
```

`--production` is intended for CI / deployment builds: it skips the dev scope entirely and still attempts optional packages, warning on failure. It does NOT overwrite `fglpkg.lock`, so a production install cannot accidentally strip dev entries from the lock recorded by the developer.

Peer dependencies are intentionally not supported — they solve a JS/TS singleton problem (React, TypeScript) that has no clean analog in BDL's module layout. Use a version constraint on a prod dep if you need callers to align on a version.

### Removing a Package

```bash
fglpkg remove myutils
```

This deletes the package from `~/.fglpkg/packages/` and removes it from whichever scope (`dependencies`, `devDependencies`, or `optionalDependencies`) it lives in. The command tells you which scope was touched.

### Updating Dependencies

To re-resolve all dependencies to their latest compatible versions (ignoring the lock file):

```bash
fglpkg update
```

### Listing Installed Packages

```bash
$ fglpkg list
Installed packages:
  myutils                        1.0.0
  poiapi                         1.0.0
```

### Searching the Registry

```bash
$ fglpkg search json
Results for "json":
  NAME                           VERSION      DESCRIPTION
  ----                           -------      -----------
  jsonutils                      2.0.1        JSON utility functions for BDL
```

## Publishing a Package

### Package Structure

A publishable package needs a `fglpkg.json` with at least `name` and `version`. The `root` field tells fglpkg where to find the compiled files.

**Example: Simple package (flat directory)**

```
myutils/
├── fglpkg.json
├── strings.42m
└── dates.42m
```

```json
{
  "name": "myutils",
  "version": "1.0.0",
  "description": "String and date utilities for BDL"
}
```

**Example: Fully-qualified package (Java-style directory structure)**

If your package uses a fully-qualified name like `com.fourjs.poiapi`, Genero expects the `.42m` files to live in a matching directory structure. Set `root` to tell fglpkg where to find them:

```
poiapi/
├── fglpkg.json
└── com/
    └── fourjs/
        └── poiapi/
            ├── PoiApi.42m
            └── PoiHelper.42m
```

```json
{
  "name": "poiapi",
  "version": "1.0.0",
  "description": "POI API for Genero BDL",
  "root": "com/fourjs/poiapi",
  "genero": "^4.0.0",
  "dependencies": {
    "java": [
      {
        "groupId": "org.apache.poi",
        "artifactId": "poi",
        "version": "5.2.3"
      }
    ]
  }
}
```

When published, the zip preserves the full directory structure (`com/fourjs/poiapi/PoiApi.42m`). When installed, it extracts to `~/.fglpkg/packages/poiapi/com/fourjs/poiapi/PoiApi.42m`. Since `~/.fglpkg/packages/poiapi` is on the `FGLLDPATH`, Genero resolves `com.fourjs.poiapi` correctly.

### File Selection

By default, fglpkg collects files matching `*.42m`, `*.42f`, and `*.sch`. To customize this, use the `files` field:

```json
{
  "files": ["*.42m", "*.42f", "*.sch", "*.str"]
}
```

### Excluding Files with `.fglpkgignore`

Place a `.fglpkgignore` file in the project root to subtract files from the inclusion set computed by `files`/`docs`. The syntax is a small subset of `.gitignore`:

```
# comments start with #
*.bak           # exclude any .bak file at any depth
build/          # trailing slash → directory-only
/scratch        # leading slash → anchored to project root
docs/internal.md
!docs/internal-public.md   # ! re-includes a previously excluded path
```

Notes:
- Patterns are evaluated in file order; the last matching rule wins.
- Files declared in the manifest's `bin` field are always included, even if they match an ignore pattern — dropping a declared script would silently break the package.
- `fglpkg.json` is always included.

### GitHub Setup (Required for Publishing and Installing)

Package zips are stored as GitHub Release assets on a private repository. The fglpkg registry server stores only metadata.

**Admin one-time setup:**

1. Create a private GitHub repository for package storage (e.g., `4js-mikefolcher/fglpkg-packages`)
2. Register the repo on the registry:
   ```bash
   fglpkg config github-repos add 4js-mikefolcher/fglpkg-packages
   ```
   This stores the repo in the registry config so all clients discover it automatically.

**Per-developer setup:**

3. Create a GitHub Personal Access Token (see [GitHub Token Setup](github-token-setup.md)):
   - **Publishers**: fine-grained token with **Contents: Read and write** on the packages repo
   - **Consumers**: fine-grained token with **Contents: Read** on the packages repo
4. Log in to save both tokens:
   ```bash
   fglpkg login
   ```

The `FGLPKG_GITHUB_REPO` environment variable can still be used to override the registry-configured repo (useful for CI or testing against a different repo).

### Publishing

Authenticate with both the registry and GitHub:

```bash
fglpkg login
```

This prompts for your registry token and GitHub token. Both are stored in `~/.fglpkg/credentials.json`.

Then publish:

```bash
fglpkg publish
```

The CLI fetches the GitHub repo from the registry config automatically.

The publish flow:
1. Builds a zip of your package files and computes the SHA256 checksum
2. Creates a GitHub Release tagged `{name}-v{version}` and uploads the zip as an asset
3. Registers the metadata (including the GitHub download URL) with the registry server

### Unpublishing a Version

To remove a published version from both the registry and GitHub:

```bash
fglpkg unpublish poiapi@1.0.0
```

This deletes the GitHub Release (and its zip asset) and removes the version metadata from the registry. You must be an owner of the package.

### Genero Version Variants

Genero BDL compiled modules (`.42m` files) are not compatible across major versions — a module compiled with Genero 4.x cannot be loaded by the Genero 6.x runtime. fglpkg handles this with **platform variants**: each package version can have multiple builds, one per Genero major version.

#### Publishing variants

When you run `fglpkg publish`, it automatically detects your local Genero version and uploads the zip as a variant. For example, on a Genero 4.x machine:

```
$ fglpkg publish
Publishing poiapi@1.0.0 (Genero 4 variant) to https://fglpkg-registry.fly.dev...
  Package zip: 4096 bytes (SHA256: abc123...)
  Uploading to GitHub (4js-mikefolcher/fglpkg-packages)...
  Uploaded: poiapi-1.0.0-genero4.zip
✓ Published poiapi@1.0.0
```

To publish for another Genero version, run the same command on a machine with that version installed:

```bash
# On a Genero 6.x machine
FGLPKG_GENERO_VERSION=6.0.0 fglpkg publish
```

Both variants are stored as separate assets under the same GitHub Release (`poiapi-v1.0.0`).

#### Installing the correct variant

When you run `fglpkg install`, the resolver automatically detects your local Genero version and selects the matching variant. If no variant exists for your Genero version, the install fails with an error listing the available variants.

```
$ fglpkg install
Resolving dependency graph (Genero 4.01.12)...
  → poiapi@1.0.0 (genero4 variant)
✓ poiapi@1.0.0
```

#### Lock file and Genero changes

The lock file records which Genero major version was used during resolution. If you switch to a different Genero major version, `fglpkg install` will automatically re-resolve to select the correct variants.

### Genero Version Constraints

Use the `genero` field to declare which Genero BDL versions your package supports:

```json
{
  "genero": "^4.0.0"
}
```

Supported constraint syntax:
- `^1.0.0` — compatible with 1.x.x (>=1.0.0, <2.0.0)
- `~1.2.0` — patch-level changes (>=1.2.0, <1.3.0)
- `>=3.20.0 <5.0.0` — explicit range
- `^3.20.0 || ^4.0.0` — multiple ranges
- `*` or omit — compatible with any version

## Running BDL Programs

Packages can declare runnable BDL programs — modules that contain a `MAIN` block. These are listed in the `programs` field of `fglpkg.json`:

```json
{
  "name": "poiapi",
  "version": "1.0.0",
  "root": "com/fourjs/poiapi",
  "programs": ["PoiConvert", "PoiMerge"]
}
```

### Running a program

```bash
fglpkg bdl poiapi PoiConvert input.xlsx output.pdf
```

This:
1. Finds the `poiapi` package in installed packages (local first, then global)
2. Verifies `PoiConvert` is declared in the package's `programs` list
3. Derives the working directory from the package's `root` field (`com/fourjs/poiapi/`)
4. Sets up `FGLLDPATH` and `CLASSPATH` with all installed packages
5. Runs `fglrun PoiConvert input.xlsx output.pdf` from that directory

All arguments after the module name are passed through to `fglrun`.

### Listing available programs

```bash
$ fglpkg bdl --list
Available BDL programs:
  PROGRAM                   PACKAGE                   SOURCE
  -------                   -------                   ------
  PoiConvert                poiapi                    local
  PoiMerge                  poiapi                    local
```

### Requirements

- Genero BDL must be installed (`fglrun` on `PATH` or `$FGLDIR` set)
- The package must be installed (`fglpkg install`)
- The module must be declared in the package's `programs` list
- The `.42m` file must exist in the package's `root` directory

## Working with Java JARs

Genero BDL can call Java code, so fglpkg also manages JAR dependencies. Declare them using Maven coordinates:

```json
{
  "dependencies": {
    "java": [
      {
        "groupId": "com.google.code.gson",
        "artifactId": "gson",
        "version": "2.10.1"
      },
      {
        "groupId": "org.apache.poi",
        "artifactId": "poi",
        "version": "5.2.3",
        "checksum": "abc123..."
      }
    ]
  }
}
```

### Optional JAR Fields

| Field | Description |
|---|---|
| `checksum` | SHA256 hex digest for integrity verification (optional, Maven Central is trusted by default) |
| `jar` | Override the JAR filename (default: `artifactId-version.jar`) |
| `url` | Override the download URL entirely (default: Maven Central) |

JARs are downloaded to `~/.fglpkg/jars/` and added to `CLASSPATH` by `fglpkg env`.

## Webcomponent Packages

A webcomponent package ships a Genero `COMPONENTTYPE` — an html/css/js bundle that a form references with `WEBCOMPONENT … COMPONENTTYPE = "<name>"`. Webcomponent packages publish under their own variant (`webcomponent`), are not Genero-version-specific, and install to a parallel directory (`.fglpkg/webcomponents/`) so they coexist with BDL packages and Java JARs without colliding.

A package is either BDL **or** webcomponent — never both. A BDL library that needs a webcomponent declares it as a regular dependency under `dependencies.fgl`, and fglpkg pulls in both.

### Creating a webcomponent package

```bash
mkdir mywidget
cd mywidget
fglpkg init --template webcomponent
```

The template scaffolds:

```
mywidget/
├── fglpkg.json                 # type: "webcomponent", webcomponents: ["MyWidget"]
├── README.md
├── .gitignore
└── webcomponents/
    └── MyWidget/
        ├── MyWidget.html       # required entry point
        ├── MyWidget.css
        └── MyWidget.js         # demo gICAPI handshake
```

Rename `MyWidget` to your `COMPONENTTYPE`, update the `webcomponents` array in `fglpkg.json` to match, and fill in the HTML/CSS/JS. One package can ship multiple components — add more `webcomponents/<NAME>/` directories and list each name.

### Manifest shape

```json
{
  "name": "chart-3d",
  "version": "1.0.0",
  "type": "webcomponent",
  "description": "3D chart widget for Genero forms",
  "license": "MIT",
  "repository": "https://github.com/4js-mikefolcher/chart-3d",
  "webcomponents": ["3DChart"],
  "dependencies": {
    "fgl": {
      "wc-theme-base": "^1.0.0"
    }
  }
}
```

Notes:
- `type: "webcomponent"` is the discriminator. Omit it (or use `"bdl"`) for a classic BDL package.
- `webcomponents` is required and must list at least one `COMPONENTTYPE` name. Each name must match `^[A-Za-z0-9][A-Za-z0-9_-]*$` (digit-leading names like `3DChart` are valid).
- `main`, `programs`, `bin`, `root`, and any `dependencies.java` / `devDependencies.java` / `optionalDependencies.java` are **forbidden** — they are BDL-only concepts. `dependencies.fgl` is allowed (depend on other packages, BDL or webcomponent).

### Publishing

```bash
fglpkg login           # once, OAuth in the browser (or use FGLPKG_TOKEN)
fglpkg pack --list     # preview the zip contents before pushing
fglpkg publish --dry-run
fglpkg publish
```

The publish flow uploads a single artifact per version under the `webcomponent` variant (no Genero-major fan-out). The in-zip layout has the `webcomponents/` prefix stripped — so a source file at `webcomponents/3DChart/3DChart.html` is stored as `3DChart/3DChart.html` in the artifact, ready to drop into the consumer's install directory.

### Consuming a webcomponent package

```bash
# In the consuming project
fglpkg install chart-3d
eval "$(fglpkg env)"
```

`fglpkg install` extracts each `<COMPONENTTYPE>/` directory directly into `.fglpkg/webcomponents/`, so:

```
yourproject/
└── .fglpkg/
    └── webcomponents/
        └── 3DChart/
            ├── 3DChart.html
            ├── 3DChart.css
            └── 3DChart.js
```

Then reference the component from a form just like a built-in one:

```
WEBCOMPONENT wc = FORMONLY.mychart,
    COMPONENTTYPE = "3DChart";
```

### Environment wiring

`fglpkg env` adds `.fglpkg/` to `FGLIMAGEPATH` when webcomponents are installed, so Genero's direct-mode loader resolves `<COMPONENTTYPE>` against `.fglpkg/webcomponents/<COMPONENTTYPE>/<COMPONENTTYPE>.html` automatically:

```bash
$ eval "$(fglpkg env)"
$ env | grep FGLIMAGEPATH
FGLIMAGEPATH=/path/to/project/.fglpkg:...
```

Alongside the export, `fglpkg env` prints a hint comment showing the value to add to your GAS application's `.xcf` (fglpkg cannot edit your `.xcf` for you — that's a deployment concern):

```bash
$ fglpkg env --local
export FGLLDPATH=...
export FGLIMAGEPATH=/path/to/project/.fglpkg"${FGLIMAGEPATH:+:$FGLIMAGEPATH}"
# For GAS: add to your .xcf's <WEB_COMPONENT_DIRECTORY>: /path/to/project/.fglpkg/webcomponents
```

### Packaging for GWA with `gwabuildtool`

For Genero Web Applications, webcomponents must be bundled into the GWA artifact at build time. `fglpkg env --gwa` emits one `--webcomponent` flag per installed `COMPONENTTYPE`, suitable for splicing into a `gwabuildtool` invocation:

```bash
$ fglpkg env --gwa
--webcomponent /path/to/project/.fglpkg/webcomponents/3DChart
--webcomponent /path/to/project/.fglpkg/webcomponents/Heatmap

$ gwabuildtool -p . -o build/ $(fglpkg env --gwa)
```

### Install layout summary

| Asset | Install path | Discovered via |
|---|---|---|
| BDL package | `.fglpkg/packages/<name>/` | `FGLLDPATH` |
| Java JAR | `.fglpkg/jars/<artifact>-<version>.jar` | `CLASSPATH` |
| Webcomponent | `.fglpkg/webcomponents/<COMPONENTTYPE>/` | `FGLIMAGEPATH` (direct mode), `WEB_COMPONENT_DIRECTORY` (GAS), `--webcomponent` flag (GWA) |

The lockfile records webcomponent packages under a separate `webcomponents` array, so a fresh `fglpkg install --frozen` from a committed `fglpkg.lock` reproduces the install byte-for-byte.

## Distributable Scripts

Packages can ship executable scripts (bash, python, etc.) that consumers can run after installation. Declare them using the `bin` field in `fglpkg.json`:

```json
{
  "name": "dbtools",
  "version": "1.0.0",
  "bin": {
    "db-migrate": "scripts/migrate.sh",
    "db-seed": "scripts/seed.py"
  }
}
```

Each key is the command name and each value is the path to the script file (relative to the package `root`). Script files are automatically included in the package zip when publishing, even if they don't match the `files` patterns.

After installation, scripts are automatically made executable (on Unix). Run them with `fglpkg run`:

```bash
# List all available commands from installed packages
$ fglpkg run --list
Available commands:
  COMMAND              PACKAGE              SOURCE     SCRIPT
  -------              -------              ------     ------
  db-migrate           dbtools              global     scripts/migrate.sh
  db-seed              dbtools              global     scripts/seed.py

# Run a command
$ fglpkg run db-migrate

# Pass arguments to the script (use -- to separate)
$ fglpkg run db-migrate -- --up --env production
```

### How It Works

- `fglpkg run` scans installed packages (local first, then global) to find the named command
- If the same command name exists in multiple packages, an error is reported listing the conflicts
- On Unix, scripts are executed directly (relying on the shebang line, e.g., `#!/bin/bash`)
- On Windows, scripts are executed via `cmd.exe` or the appropriate interpreter based on file extension

### Writing Portable Scripts

For maximum compatibility, include a shebang line at the top of your scripts:

```bash
#!/usr/bin/env bash
set -euo pipefail
echo "Running migration..."
```

```python
#!/usr/bin/env python3
import sys
print("Seeding database...")
```

## Lifecycle Hooks

The optional `hooks` field declares steps to run on well-known events. The vocabulary is intentionally a closed set of declarative operations — arbitrary shell commands are not supported, since shell-based hooks are the dominant supply-chain attack vector in mainstream package managers.

```json
{
  "name": "poiapi",
  "version": "1.0.0",
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

### Events

Hooks run on the project (consumer) manifest, not on dependency manifests:

| Event          | When it fires                                            | Working directory |
|----------------|----------------------------------------------------------|-------------------|
| `preinstall`   | Before `fglpkg install` starts resolving packages        | project root      |
| `postinstall`  | After every dependency has been installed                | project root      |
| `prepublish`   | Before `fglpkg publish` builds the zip                   | project root      |
| `postpublish`  | After `fglpkg publish` finishes the registry update      | project root      |
| `preuninstall` | Before `fglpkg remove` deletes a package                 | project root      |

A failure in any operation aborts the surrounding command; later operations in the same hook are skipped.

### Operations

Two operations are supported in this release. More can be added without breaking the schema (`fetch-jar` and `compile-bdl` are planned for a later phase).

**`copy-files`** — copy a file, a directory tree, or every match of a glob.

```json
{ "op": "copy-files", "from": "templates/*.tpl", "to": "share/templates" }
```

- `from` is a relative path or a glob (`*`, `?`, `[…]`).
- `to` is a relative directory (created if missing) or a single-file destination.
- Absolute paths and `..` traversal are rejected at manifest load time.

**`mkdir`** — create a directory and its parents. No-op if the directory already exists; fails if the path exists as a file.

```json
{ "op": "mkdir", "path": "var/cache" }
```

### Migrating from `scripts`

The previous `scripts` field was defined but never executed. It has been removed. A manifest that still uses `scripts` fails to load with a hint pointing at `hooks` — convert each entry to one of the operations above.

## Package Documentation

Packages can include documentation files that consumers can browse after installation. Declare them using the `docs` field with glob patterns:

```json
{
  "name": "poiapi",
  "version": "1.0.0",
  "docs": ["README.md", "docs/**/*.md"]
}
```

Documentation is only included when the author explicitly declares the `docs` field — there are no default patterns. The glob patterns support `**` for matching any number of directory levels.

### Browsing Documentation

Use `fglpkg docs` to discover and read documentation for installed packages:

```bash
# List available documentation files
$ fglpkg docs poiapi
Documentation for poiapi@1.0.0:
  README.md
  docs/getting-started.md
  docs/api-reference.md

View a file: fglpkg docs poiapi <file>

# Display a specific file
$ fglpkg docs poiapi README.md

# You can also use just the filename (if unique)
$ fglpkg docs poiapi api-reference.md
```

### Docs Glob Patterns

The `docs` field supports standard glob syntax with `**` for recursive matching:

| Pattern | Matches |
|---|---|
| `README.md` | Only `README.md` in the package root |
| `*.md` | Any `.md` file in the package root |
| `docs/**/*.md` | Any `.md` file anywhere under `docs/` |
| `CHANGELOG.md` | Only `CHANGELOG.md` in the package root |

## Registry Authentication

### Logging In

```bash
$ fglpkg login
Registry URL (https://fglpkg-registry.fly.dev):
Token: my-secret-token
✓ Logged in to https://fglpkg-registry.fly.dev as jdeveloper
GitHub token (optional, for package downloads): ghp_xxxxxxxxxxxx
✓ GitHub token saved for package downloads
```

Credentials (both registry and GitHub tokens) are stored in `~/.fglpkg/credentials.json`.

### Checking Your Identity

```bash
$ fglpkg whoami
Logged in to https://fglpkg-registry.fly.dev as jdeveloper
```

### Logging Out

```bash
fglpkg logout
```

### Using Tokens Directly (CI/CD)

For CI/CD environments, set tokens as environment variables instead of using `fglpkg login`:

```bash
# macOS / Linux
export FGLPKG_PUBLISH_TOKEN=my-secret-token
export FGLPKG_GITHUB_TOKEN=ghp_xxxxxxxxxxxx
fglpkg publish
```

```cmd
REM Windows
SET FGLPKG_PUBLISH_TOKEN=my-secret-token
SET FGLPKG_GITHUB_TOKEN=ghp_xxxxxxxxxxxx
fglpkg publish
```

The GitHub repo is automatically fetched from the registry config. Override it with `FGLPKG_GITHUB_REPO` if needed.

For install-only CI jobs, only the GitHub token is needed:

```bash
# macOS / Linux
export FGLPKG_GITHUB_TOKEN=ghp_xxxxxxxxxxxx
fglpkg install
```

```cmd
REM Windows
SET FGLPKG_GITHUB_TOKEN=ghp_xxxxxxxxxxxx
fglpkg install
```

### Token Management (Admin)

Administrators can create, revoke, and rotate tokens:

```bash
# Create a token for a new user
fglpkg token create jdeveloper

# Revoke a user's token
fglpkg token revoke jdeveloper

# Rotate your own token
fglpkg token rotate
```

## Workspaces (Monorepos)

Workspaces let you develop multiple related packages in a single repository. Local packages are automatically linked via `FGLLDPATH` without needing to publish and install them.

### Setting Up a Workspace

```bash
# In your monorepo root
fglpkg workspace init packages/myutils packages/dbtools
```

This creates a `fglpkg-workspace.json` file. Each listed path should contain its own `fglpkg.json`.

### Adding Members

```bash
fglpkg workspace add packages/newlib
```

### Listing Members

```bash
$ fglpkg workspace list
Workspace: /path/to/monorepo
  myutils                        v1.0.0
  dbtools                        v2.1.0
  newlib                         v0.1.0
```

### Workspace Info

```bash
fglpkg workspace info
```

### How It Works

When `fglpkg env` detects that you are inside a workspace, it adds each member's source directory to `FGLLDPATH` with higher priority than installed packages. This means you can edit a local package and immediately use it in another member without re-publishing.

## Lock Files

When you run `fglpkg install`, a `fglpkg.lock` file is created alongside your `fglpkg.json`. The lock file pins:

- Exact resolved versions of every BDL package
- Download URLs and SHA256 checksums
- The Genero version used at resolution time

This ensures reproducible installs across machines and CI environments. Commit `fglpkg.lock` to version control.

To bypass the lock and re-resolve everything:

```bash
fglpkg update
```

## Package Ownership

Packages can have multiple owners who are allowed to publish new versions.

### List Owners

```bash
fglpkg owner list myutils
```

### Add an Owner

```bash
fglpkg owner add myutils jdeveloper
```

### Remove an Owner

```bash
fglpkg owner remove myutils jdeveloper
```

## Troubleshooting

### "not logged in" when publishing

Make sure you have authenticated:

```bash
fglpkg login
```

Or set the `FGLPKG_PUBLISH_TOKEN` environment variable.

### Packages not found by Genero after install

Make sure your environment is set up:

**macOS / Linux** — your shell profile should include:

```bash
eval "$(fglpkg env --global)"
```

Restart your shell or run `source ~/.bashrc` after adding it.

**Windows (cmd.exe)** — run before building:

```cmd
FOR /F "tokens=*" %i IN ('fglpkg env --global') DO %i
```

**Genero Studio** — paste the output of `fglpkg env --gst` into your project's environment settings.

### Stale lock file

If dependencies in `fglpkg.json` have changed and `fglpkg install` says the lock file is stale, it will automatically re-resolve. You can also force it:

```bash
fglpkg update
```

### Wrong Genero version detected

Override the detected version:

```bash
# macOS / Linux
export FGLPKG_GENERO_VERSION=4.1.0
fglpkg install
```

```cmd
REM Windows
SET FGLPKG_GENERO_VERSION=4.1.0
fglpkg install
```

### Using a private registry

Point fglpkg at your registry:

```bash
# macOS / Linux
export FGLPKG_REGISTRY=https://registry.example.com
```

```cmd
REM Windows
SET FGLPKG_REGISTRY=https://registry.example.com
```

Add this to your shell profile or batch script for persistence.

### Checking the installed version

```bash
fglpkg version
```

This shows the version and build number embedded at compile time.
