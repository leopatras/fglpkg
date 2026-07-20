# fglpkg — Genero BDL Package Manager

A package manager for Genero BDL projects, supporting both BDL packages and Java JAR dependencies.

## Project Structure

```
fglpkg/
├── cmd/
│   ├── fglpkg/main.go              # Package manager CLI entry point
│   └── build.sh                    # Cross-platform build script
├── internal/
│   ├── cli/cli.go                  # Command dispatch & user interaction
│   ├── manifest/manifest.go        # fglpkg.json parsing & manipulation
│   ├── semver/                     # Semver parsing & constraint matching
│   ├── genero/genero.go            # Genero BDL version detection
│   ├── resolver/resolver.go        # Transitive dependency resolution
│   ├── installer/installer.go      # Zip download, extraction, JAR management
│   ├── lockfile/lockfile.go        # fglpkg.lock read/write/validate
│   ├── checksum/checksum.go        # SHA256 streaming verification
│   ├── credentials/                # Registry credential storage
│   ├── workspace/workspace.go      # Monorepo workspace support
│   └── registry/registry.go        # Registry HTTP client
├── docs/
│   └── user-guide.md               # User instruction guide
├── .github/workflows/release.yml   # Automated release on tag push
├── go.mod
└── README.md
```

## Installation

Download the latest binary for your platform from [GitHub Releases](https://github.com/4js-mikefolcher/fglpkg/releases) and place it in your `PATH`:

```bash
# macOS / Linux
sudo cp fglpkg-darwin-arm64 /usr/local/bin/fglpkg
sudo chmod +x /usr/local/bin/fglpkg
```

```powershell
# Windows — copy to a directory in your PATH
copy fglpkg-windows-amd64.exe C:\tools\fglpkg.exe
```

### macOS Gatekeeper warning

If you download the macOS binary through a **browser**, macOS tags it with a quarantine
attribute and Gatekeeper blocks it on first run — *"fglpkg cannot be opened because the developer
cannot be verified."* Clear the quarantine flag after copying it into place:

```bash
sudo xattr -d com.apple.quarantine /usr/local/bin/fglpkg
```

Alternatively, right-click the file in Finder → **Open** once to add a one-time exception, or
download the asset with `curl -L -O <asset-url>` (curl does not set the quarantine attribute).

Add environment setup:

**macOS / Linux** — add to `~/.bashrc` or `~/.zshrc`:

```bash
echo 'eval "$(fglpkg env --global)"' >> ~/.bashrc
source ~/.bashrc
```

**Windows (cmd.exe)** — create a `setup-env.bat` script or run before building:

```cmd
FOR /F "tokens=*" %%i IN ('fglpkg env --global') DO %%i
```

**Genero Studio** — paste the output of `fglpkg env --gst` into your project's environment settings.

Use `--global` in shell profiles so all installed packages are available regardless of your current directory.

### Keeping fglpkg up to date

Once installed, fglpkg can update itself — no need to re-download by hand:

```bash
fglpkg self-update            # download, verify, and install the latest release
fglpkg self-update --check    # just report whether a newer version exists
```

`self-update` fetches the latest stable build for your OS/architecture and verifies its
**Ed25519 release signature** (chained to a key pinned in the binary) **and** its SHA-256
checksum before atomically replacing the running executable. It never installs an unverified
binary; on any verification failure it prints a manual-download link instead. Scope is
latest-stable only — no version pinning, pre-releases, or downgrades. `--yes` skips the
confirmation prompt (for scripts); `--force` reinstalls even when you are already current.

fglpkg also **passively notices** new releases: at most once every 24h, after a command
finishes, it prints a one-line "a new version is available" hint to stderr. It never blocks a
command, changes an exit code, or reports network errors. Turn it off with
`FGLPKG_NO_UPDATE_CHECK=1`, or in `~/.fglpkg/config.json`:

```json
{
  "updateCheck": false,
  "updateCheckInterval": "24h"
}
```

Self-update is unavailable for `dev` builds (built from source) and for installs managed by a
package manager such as Homebrew — update those with the tool that installed them.

## Building from Source

```bash
go build -o fglpkg ./cmd/fglpkg
```

Use the build script to cross-compile for all platforms with embedded version info:

```bash
./cmd/build.sh                    # uses default version from script
FGLPKG_VERSION=2.0.0 ./cmd/build.sh   # override version
```

This produces ARM and Intel binaries for Linux, macOS, and Windows in the `./bin/` directory.

## Home Directory Layout

fglpkg stores everything under `~/.fglpkg` (override with `FGLPKG_HOME`):

```
~/.fglpkg/
├── packages/          # Installed BDL packages (each in its own subdir)
│   ├── myutils/
│   │   ├── fglpkg.json
│   │   ├── strings.42m
│   │   └── dates.42m
│   └── poiapi/
│       └── com/fourjs/poiapi/
│           ├── fglpkg.json
│           └── PoiApi.42m
├── jars/              # Java JARs
│   ├── gson-2.10.1.jar
│   └── commons-lang3-3.12.0.jar
└── credentials.json   # Registry + GitHub auth tokens
```

When working inside a project, fglpkg can also install to a local `.fglpkg/` directory:

```
myproject/
├── fglpkg.json
├── .fglpkg/           # Local package install (add to .gitignore)
│   ├── packages/
│   └── jars/
└── ...
```

## Local vs Global (Context-Aware)

fglpkg automatically detects whether to use local or global package storage:

| Current directory has... | Default behavior |
|---|---|
| `.fglpkg/` directory | Local (`.fglpkg/`) |
| `fglpkg.json` file | Local (`.fglpkg/`) |
| Neither | Global (`~/.fglpkg/`) |

Override with `--local` / `-l` or `--global` / `-g` on `install`, `remove`, `update`, `list`, and `env`.

For shell profiles, always use `--global` so all installed packages are available regardless of directory:

```bash
eval "$(fglpkg env --global)"
```

## fglpkg.json Format

### For a project (consuming packages)

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
      {
        "groupId": "com.google.code.gson",
        "artifactId": "gson",
        "version": "2.10.1"
      }
    ]
  }
}
```

### For a package (publishing to registry)

```json
{
  "name": "poiapi",
  "version": "1.0.0",
  "description": "POI API for Genero BDL",
  "author": "Jane Developer",
  "license": "MIT",
  "visibility": "public",
  "root": "com/fourjs/poiapi",
  "genero": "^4.0.0",
  "main": "PoiApi.42m",
  "programs": ["PoiConvert", "PoiMerge"],
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

### Manifest Fields

| Field | Required | Description |
|---|---|---|
| `name` | Yes | Package name (used as the registry identifier) |
| `version` | Yes | Semver version string |
| `description` | No | Short description |
| `author` | No | Author name |
| `license` | No | License identifier (e.g., `MIT`, `Apache-2.0`) |
| `repository` | No | Source repository URL |
| `keywords` | No | Free-form tags for discovery (e.g. `["database", "utilities"]`). Advisory metadata — not currently matched by `fglpkg search`. |
| `main` | No | Primary `.42m` entry point |
| `genero` | No | Genero BDL version constraint (e.g., `^4.0.0`) |
| `root` | No | Base directory for package files when publishing (default `.`) |
| `files` | No | Glob patterns for files to include in the zip (default `["*.42m", "*.42f", "*.sch"]`) |
| `bin` | No | Command name to script path mappings (e.g., `{"migrate": "scripts/migrate.sh"}`) |
| `docs` | No | Glob patterns for documentation files to include (e.g., `["README.md", "docs/**/*.md"]`) |
| `dependencies.fgl` | No | BDL production package dependencies. Each value is either a version-constraint string (`"^1.0.0"`) or an object pinning the source repository: `{ "version": "^1.0.0", "registry": "acme" }` (see [Secondary Package Repositories](#secondary-package-repositories-jfrog-artifactory)) |
| `dependencies.java` | No | Java JAR production dependencies (Maven coordinates) |
| `registries` | No | Additional package repositories (e.g. a JFrog Artifactory instance) consulted alongside the built-in GI registry. See [Secondary Package Repositories](#secondary-package-repositories-jfrog-artifactory) |
| `defaultRegistry` | No | Name of the repository `fglpkg publish` targets when no `--registry` is given (publish-only; does not affect where packages are consumed from) |
| `devDependencies` | No | Test / tooling deps (fgl + java), skipped with `--production` |
| `optionalDependencies` | No | Attempted like prod, failures emit a warning instead of aborting |
| `programs` | No | List of module names with MAIN blocks (e.g., `["PoiConvert"]`) |
| `visibility` | No | Who can see this package on the registry: `"public"` (default) or `"private"`. Defaults to `"public"` if omitted — set `"private"` explicitly to restrict access. Applied on first publish only; ignored on subsequent publishes. |
| `scripts` | No | Custom script definitions |

## Environment Variables

| Variable | Purpose |
|---|---|
| `FGLPKG_HOME` | Override default `~/.fglpkg` home |
| `FGLPKG_REGISTRY` | GI registry URL — used by `install`, `search`, `audit`, `info`, `outdated`, `whoami`, `login`, `publish`. Default: `https://service.generointelligence.ai` |
| `FGLPKG_TOKEN` | Bearer token for the **GI** registry. Takes precedence over stored OAuth/PAT credentials, and cannot be cleared by `fglpkg logout` (unset it to fully log out). Does not authenticate secondary repositories |
| `FGLPKG_PUBLISH_REGISTRY` | Name of the repository `fglpkg publish` targets when no `--registry` is given. Overrides the manifest's `defaultRegistry`. See [Secondary Package Repositories](#secondary-package-repositories-jfrog-artifactory) |
| `FGLPKG_GENERO_VERSION` | Override Genero version detection |
| `FGLPKG_INSTALL_CONCURRENCY` | Cap parallel downloads during install (default 4) |
| `FGLPKG_SIGNING` | Layer 1 signature enforcement: `require`, `warn`, or `off`. Overrides `signing.enforce` in `config.json` |
| `FGLPKG_NO_UPDATE_CHECK` | Set to disable the passive "new version available" notice (also configurable via `updateCheck` in `~/.fglpkg/config.json`). Always off for `dev` builds, in CI, and for non-interactive output |
| `FGLLDPATH` | Auto-managed by `fglpkg env` (prepends, preserves existing value) |
| `CLASSPATH` | Auto-managed by `fglpkg env` (prepends, preserves existing value) |

### Authentication

`fglpkg login` (no args) opens a browser and runs OAuth (auth code + PKCE) against the consumer registry. Tokens are persisted to `~/.fglpkg/credentials.json` and refreshed silently when they expire.

For non-interactive use (CI, SSH boxes, scripts), pass a Personal Access Token:

```bash
fglpkg login --token gpr_…       # or: export FGLPKG_TOKEN=gpr_…
```

All commands authenticate using the same OAuth/PAT credentials stored by `fglpkg login`.

## Signature verification (Layer 1)

Every artifact the registry serves is signed with **Ed25519** over a canonical
(RFC 8785 / JCS) payload of its identity and `sha256`. On install, `fglpkg`
reconstructs that payload and verifies the signature — proving the bytes you
received are exactly what the registry stored (defence against transport,
mirror, and cache tampering), a layer above the plain SHA256 integrity check.

**How trust is anchored.** The registry's working public keys are published in
a signed manifest at `GET /registry/.well-known/keys.json`. That manifest is
itself signed by a **root key whose public half is pinned in the fglpkg binary**
(`internal/signing/root.go`) — it is never fetched, so a rogue registry cannot
substitute its own keys. The verified manifest is cached at `~/.fglpkg/keys.json`
so reinstalls and `--production` deploys work offline.

**What it does not do.** It does not prove *who built* the package (that is
Layer 2, Sigstore provenance — opt-in, not in this release), and Java JARs pulled
from Maven Central keep their existing checksum-only trust.

### Enforcement modes

Set `signing.enforce` in `~/.fglpkg/config.json` (or the `FGLPKG_SIGNING` env
var, which wins):

```json
{ "signing": { "enforce": "warn" } }
```

| Mode | Behaviour |
|---|---|
| `warn` *(default)* | A bad or missing signature prints a warning but the install continues. |
| `require` | A bad or missing signature aborts the install. |
| `off` | Signature verification is skipped entirely. |

`fglpkg install --no-verify-signature` skips verification for a single run
(discouraged; for emergencies).

### Auditing

```bash
fglpkg audit signatures        # re-verify every locked package against the keys manifest
```

Prints one line per package and exits non-zero if any package is unsigned or
fails to verify — suitable as a CI gate.

## Usage

```bash
# Package management
fglpkg init                              # Initialise fglpkg.json interactively
fglpkg init --template library           # Scaffold a publishable package
fglpkg init --template app               # Scaffold a consuming application
fglpkg install                           # Install deps (auto-detects local vs global)
fglpkg install myutils                   # Add + install latest version
fglpkg install myutils@1.2.0             # Add + install specific version
fglpkg install tester -D                 # Add under devDependencies
fglpkg install telemetry -O              # Add under optionalDependencies
fglpkg install --production              # Skip devDependencies (CI / deploy)
fglpkg install --global                  # Force install to ~/.fglpkg/
fglpkg install --local                   # Force install to .fglpkg/
fglpkg remove myutils                    # Remove a package (any scope)
fglpkg update                            # Re-resolve and update all dependencies
fglpkg list                              # List installed packages
fglpkg env                               # Print export statements (auto-detects scope)
fglpkg env --global                      # Print exports for all global packages
fglpkg env --gst                         # Print in Genero Studio format
fglpkg search json                       # Search the registry (matches name/description)
fglpkg search --all                      # List every package in the registry
                                         #   a STATUS column appears only when a match is
                                         #   deprecated, e.g. "chart-3d  1.2.3  deprecated -> chart-3d-ng  3D charts"
fglpkg audit signatures                  # Verify registry signatures of locked packages
fglpkg bdl <pkg> <module> [args...]      # Run a BDL program from a package
fglpkg bdl --list                        # List available BDL programs

# Discovery & inspection
fglpkg info <pkg>[@ver]                  # Show registry metadata for a package
fglpkg outdated                          # List FGL deps with newer versions (CI gate)
fglpkg audit                             # Scan installed Java JARs for CVEs (OSV.dev)
fglpkg sbom                              # Emit a CycloneDX SBOM from fglpkg.lock
fglpkg pack                              # Build the publishable zip without uploading
fglpkg completion bash                   # Print shell completion script

# Publishing
fglpkg publish                           # Publish current package to registry
fglpkg publish --dry-run                 # Preview the publish calls, no network
fglpkg publish --ci                      # Non-interactive publish (CI): needs FGLPKG_TOKEN
fglpkg publish --private                 # Publish as private (overrides fglpkg.json visibility)
fglpkg publish --public                  # Publish as public (overrides fglpkg.json visibility)
fglpkg publish --changelog "notes..."    # Set this version's changelog inline (overrides CHANGELOG.md)

# Deprecating & relocating (npm-style; stays installable, warns consumers)
fglpkg deprecate chart-3d@1.2.3 "reason"       # Deprecate one version with a message
fglpkg deprecate chart-3d@1.2.3 --moved-to chart-3d-ng  # Deprecate + point at a successor
fglpkg deprecate chart-3d --moved-to chart-3d-ng        # Relocate the whole package (rename)
fglpkg deprecate chart-3d@1.2.3 --undo         # Lift the deprecation

# Secondary repositories (JFrog Artifactory) — see section below
fglpkg registry list                     # Show configured repositories + auth status
fglpkg registry add acme https://a.example --repo-key GeneroBDL   # Add a repo (global)
fglpkg registry add acme https://a.example --repo-key K --project # Add to fglpkg.json instead
fglpkg registry remove acme              # Remove a configured repo
fglpkg login --registry acme --token …   # Sign in to a secondary repo
fglpkg install pkg --registry acme        # Add a package, pinning its source repo
fglpkg publish --registry acme            # Publish to a secondary repo
fglpkg publish --registry acme --force    # Overwrite an existing variant

# Authentication
fglpkg login                             # Save registry + GitHub credentials
fglpkg logout                            # Remove saved credentials
fglpkg whoami                            # Show current authenticated user

# Workspaces
fglpkg workspace init [paths...]         # Initialise a monorepo workspace
fglpkg workspace add <path>              # Add a member to the workspace
fglpkg workspace list                    # List workspace members
fglpkg workspace info                    # Show workspace details

# Scripts (bin)
fglpkg run --list                        # List all available commands
fglpkg run <command> [-- args...]        # Run a script from an installed package

# Documentation
fglpkg docs <package>                    # List documentation files
fglpkg docs <package> <file>             # Display a documentation file

# Misc
fglpkg self-update                       # Update fglpkg to the latest release
fglpkg self-update --check               # Report whether an update is available
fglpkg version                           # Print version and build info
fglpkg help                              # Show help
```

### Publishing a Package

`publish` talks the Genero Intelligence registry protocol (paths under
`/registry/...`) at `FGLPKG_REGISTRY` (default `https://service.generointelligence.ai`).
The registry stores artifact zips itself (in R2) — there is no GitHub-Releases
indirection and no per-repo setup. Any authenticated user can publish:

```bash
# Log in once (OAuth in the browser, or a PAT for CI — see Authentication above)
fglpkg login

# From the package directory
fglpkg publish
fglpkg publish --dry-run    # preview the calls without touching the network
```

Publishing is **additive and reviewed**: a freshly published version is marked
*pending* and only becomes installable once a registry admin approves it.

The publish flow:
1. Builds a zip from the directory specified by `root` (or `.`), collecting files matching `files` patterns (default: `*.42m`, `*.42f`, `*.sch`) plus any declared `bin` scripts and `docs`, and SHA256s it.
2. `POST /registry/packages` — creates the package slug on first publish (a `409` means it already exists, which is fine). New packages carry the manifest's `visibility` field. If `visibility` is omitted from `fglpkg.json`, fglpkg defaults to `"public"` — this is intentional (npm-style: public unless you opt out). To publish a private package, set `"visibility": "private"` explicitly. Visibility is set once on first publish and ignored on subsequent publishes.
3. `POST /registry/packages/:slug/versions` — creates the version (a `409` means the version already exists; publish proceeds to add a new variant to it). This call also carries the version's **changelog**: by default the section for the version being published is extracted from a `CHANGELOG.md` in the project root ([Keep a Changelog](https://keepachangelog.com) format, e.g. `## [1.2.0]`), or you can supply it inline with `--changelog "<text>"`. If `CHANGELOG.md` exists but has no entry for the version, publish warns and sends an empty changelog.
4. `PUT /registry/packages/:slug/versions/:version/artifacts/:variant` — streams the zip body; the registry computes size + checksum and stores it in R2.
5. `POST /registry/packages/:slug/versions/:version/submit` — marks the version pending for admin review.

Authentication uses the same OAuth/PAT bearer as the other consumer commands
(`FGLPKG_TOKEN` overrides stored credentials). No GitHub token is involved in
publishing.

### Deprecating & relocating packages

`fglpkg deprecate` marks a published version (or a whole package) as
deprecated, following the **npm model**: the version stays **fully installable
and listed** — consumers just get a non-fatal warning pointing at the
successor. This is how a **rename or relocation** is expressed; there is no
separate `rename`/`migrate` command.

```bash
# Deprecate one version with a message (owner-only; requires login)
fglpkg deprecate chart-3d@1.2.3 "security fix in 1.2.4; please upgrade"

# Rename / relocate — message auto-fills to "chart-3d has moved to chart-3d-ng"
fglpkg deprecate chart-3d@1.2.3 --moved-to chart-3d-ng

# Relocate the whole package (every version), pinning a successor version
fglpkg deprecate chart-3d --moved-to chart-3d-ng@2.0.0

# Lift a deprecation
fglpkg deprecate chart-3d@1.2.3 --undo
```

A bare `<pkg>` (no `@version`) deprecates/relocates the whole package; with
`@<version>` it targets that one version. A message is required unless
`--moved-to` is given (which auto-fills one). `--json` prints a machine-readable
result. Re-running `deprecate` edits the existing message/successor
(idempotent).

Deprecation is **not** withdrawal: it never hides or un-lists the package, and
it never renames the slug in place — it records an advisory pointer to a
separately-published successor. What consumers see:

- **`install` / `update`** — a `warning:` line on stderr for each deprecated
  resolved dependency (including transitive ones), with a `→ consider: fglpkg
  install <successor>` hint when a successor is set. The install still
  succeeds; deprecation never blocks it. (Warnings fire on a fresh resolve, not
  on a lock-file-only reinstall.)
- **`info`** — a `Deprecated:` / `Moved to:` block under the header.
- **`outdated`** — a `deprecated → <successor>` note in a `Notes` column for any
  installed dependency that is deprecated.

### Private Packages

Packages are **public by default**. To restrict a package to members of your tenant, set `"visibility": "private"` in `fglpkg.json` before the first publish:

```json
{
  "name": "internal-utils",
  "version": "1.0.0",
  "visibility": "private"
}
```

Alternatively, override the manifest at publish time with the `--private` / `--public` flags (mutually exclusive). The flag takes priority over `fglpkg.json`, which takes priority over the `public` default:

```bash
fglpkg publish --private    # publish as private regardless of fglpkg.json
fglpkg publish --public     # publish as public regardless of fglpkg.json
```

Visibility is recorded once when the package is first created on the registry and ignored on subsequent publishes — you cannot change it after the fact via `fglpkg publish`.

Consumers trying to install a private package must be logged in as a member of the owning tenant:

```bash
fglpkg login          # authenticate first
fglpkg install internal-utils
```

An unauthenticated or unauthorised `install` will receive a 404 (the registry does not reveal that the package exists).

### Genero Version Variants

Each package version can have multiple builds, one per Genero major version. When you publish, fglpkg detects your local Genero version and uploads it as a named variant:

```bash
# On a Genero 4.x machine
fglpkg publish    # uploads the genero4 variant (poiapi-1.0.0-genero4.zip)

# On a Genero 6.x machine
fglpkg publish    # uploads the genero6 variant (poiapi-1.0.0-genero6.zip)
```

Both variants live under the same version (`1.0.0`) as separate artifacts on the
registry. Publishing a second variant for an existing version is allowed and does
not require bumping the version. When a consumer runs `fglpkg install`, the
resolver automatically selects the variant matching their local Genero major
version.

## Secondary Package Repositories (JFrog Artifactory)

fglpkg can consume and publish **FGL/BDL packages** from one or more **JFrog
Artifactory** repositories alongside the built-in Genero Intelligence (GI)
registry. This lets a team keep pulling public packages from GI while hosting
their **internal** packages in their own Artifactory. Everything is client-side —
no GI backend involvement. (Java JARs are out of scope here; they continue to
resolve from Maven Central.)

### Configuring repositories

Repositories are declared in a `registries` array, with **no secrets** — those
stay in `~/.fglpkg/credentials.json`. The effective set is a cascade, in
increasing precedence: the built-in GI registry → the machine-wide
`~/.fglpkg/config.json` → the project's `fglpkg.json`. Entries merge by `name`.

Put it in the project `fglpkg.json` (committed, so teammates inherit the URL on
clone):

```json
{
  "registries": [
    {
      "name": "acme",
      "type": "artifactory",
      "url": "https://artifactory.acme.example/artifactory",
      "repoKey": "fgl-internal-generic",
      "priority": 2,
      "auth": "bearer",
      "packages": ["acme-*"]
    }
  ]
}
```

…or provision it once machine-wide in `~/.fglpkg/config.json` (same shape), so an
ops team can set it for every project:

```json
{ "registries": [ { "name": "acme", "type": "artifactory", "url": "…", "repoKey": "…", "priority": 2, "auth": "bearer" } ] }
```

| Descriptor field | Required | Description |
|---|---|---|
| `name` | Yes | Logical id used in `--registry`, credentials, and dependency pins |
| `type` | Yes | `"genero"` or `"artifactory"` |
| `url` | Yes | Base URL (including any context path, e.g. `…/artifactory`) |
| `repoKey` | For `artifactory` | The Artifactory **generic** repository key |
| `priority` | Yes | Lower is tried first; must be unique. Ordering/diagnostics only — it is **not** a precedence tiebreak (see collision guard) |
| `auth` | No | `bearer` (default) \| `basic` \| `apikey` \| `anonymous` |
| `packages` | No | Glob allow-list (e.g. `["acme-*"]`) — names outside it are never queried against this repo |

Rather than editing JSON by hand, `fglpkg registry add`/`remove` manage these
entries for you (validated before write, with the priority auto-assigned after
`gi` when omitted):

```bash
fglpkg registry add acme https://artifactory.acme.example/artifactory \
    --repo-key fgl-internal-generic --packages "acme-*"   # → ~/.fglpkg/config.json
fglpkg registry add acme https://… --repo-key K --project # → project fglpkg.json
fglpkg registry remove acme
```

`add` defaults `--type` to `artifactory`; pass `--type genero`, `--auth`,
`--priority`, and repeatable/comma-separated `--packages` as needed. It refuses
to redefine the built-in `gi` or collide on name/priority.

The built-in GI registry is always present as if declared `{ "name": "gi", "type": "genero", "priority": 1 }`. `FGLPKG_REGISTRY`, if set, retargets the GI URL.

Inspect the effective set and login status:

```bash
fglpkg registry list
# NAME   TYPE         PRIO  AUTH    LOGIN  URL
# gi     genero       1     bearer  env    https://service.generointelligence.ai
# acme   artifactory  2     bearer  yes    https://artifactory.acme.example/artifactory
```

`LOGIN` values: `yes` (credentials stored), `env` (GI authenticated by `FGLPKG_TOKEN`), `no` (none), `anon` (no auth needed).

### Authentication

Credentials are keyed by repository URL, so you can be logged into GI **and** any
number of secondary repos at once — logging into one never affects another. The
flag matches the repo's `auth` scheme:

```bash
fglpkg login --registry acme --token <access-token>          # bearer (recommended)
fglpkg login --registry acme --user <u> --password <p|token> # basic
fglpkg login --registry acme --api-key <key>                 # apikey
# anonymous repos need no login
fglpkg logout --registry acme
```

A JFrog access token works either as `bearer` (`--token`) or as the `basic`
password. Note `FGLPKG_TOKEN` authenticates **GI only** — it does not apply to
secondary repos.

### Consuming — routing and the collision guard

When you resolve dependencies, each package name is routed to the repository that
owns it:

- Found in exactly **one** repository → resolved from there; the lockfile records
  the source (`"registry": "acme"`), so installs are reproducible.
- Found in **more than one** repository → a **hard error**. fglpkg refuses to
  guess (this closes the dependency-confusion hole). Disambiguate by pinning the
  source, or by giving the repo a `packages` allow-list so the name is only ever
  queried against one repo:

```json
"dependencies": { "fgl": { "utils": { "version": "^1.0.0", "registry": "acme" } } }
```

`fglpkg install <pkg> --registry acme` does the same in one step: it resolves the
package from `acme` and writes that pin into `fglpkg.json`.

**Transitive pins travel.** A package published to Artifactory carries its own
dependency pins in its sidecar manifest. When you consume such a package, fglpkg
honours the pins its author declared — so a transitive dependency resolves from
the repository the author intended even when its name also exists elsewhere. (An
explicit pin in *your* `fglpkg.json` always wins over a package's declared pin.)

### Publishing

Publish to a secondary repo with `--registry`; the build is identical to a GI
publish, but the zip and a sidecar `fglpkg.json` are deployed directly (no
submit/approval step):

```bash
fglpkg publish --registry acme            # deploy to acme
fglpkg publish --registry acme --dry-run  # print the exact PUT URLs, no network
fglpkg publish --registry acme --force    # overwrite an existing variant (guarded by default)
```

To avoid typing `--registry` every time, set a **default publish target**. It is
resolved in decreasing precedence: `FGLPKG_PUBLISH_REGISTRY` → the project's
`defaultRegistry` → the global `defaultRegistry` → GI:

```json
{ "defaultRegistry": "acme", "registries": [ … ] }
```

With that, a bare `fglpkg publish` deploys to `acme`; `--registry gi` still
reaches GI on demand.

### Integrity

Artifactory computes and verifies SHA-256 on deploy and returns it in file
metadata; `fglpkg install` verifies the checksum on download, exactly as with GI.

For the full design and rationale, see
[specs/artifactory-secondary-repository.md](specs/artifactory-secondary-repository.md).

## Releases

Releases are automated via GitHub Actions. Push a tag to create a release with binaries for all platforms:

```bash
git tag v1.0.0
git push origin v1.0.0
```

Pre-built binaries are available at [GitHub Releases](https://github.com/4js-mikefolcher/fglpkg/releases).
