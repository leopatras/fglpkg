# fglpkg — Genero BDL Package Manager

A package manager for Genero BDL projects, supporting both BDL packages and Java JAR dependencies.

## Project Structure

```
fglpkg/
├── cmd/
│   ├── fglpkg/main.go              # Package manager CLI entry point
│   ├── registry/main.go            # Registry server entry point
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
│   ├── credentials/                # Registry + GitHub credential storage
│   ├── github/github.go            # GitHub Releases API client
│   ├── workspace/workspace.go      # Monorepo workspace support
│   ├── registry/registry.go        # Registry HTTP client
│   └── registry/server/            # Registry HTTP server
│       ├── server.go               # Route handlers
│       ├── store.go                # Flat-file storage backend
│       └── testing.go              # Test helper (NewTestServer)
├── docs/
│   ├── user-guide.md               # User instruction guide
│   └── github-token-setup.md       # GitHub PAT setup instructions
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
| `keywords` | No | Free-form tags that aid registry search/discovery (e.g. `["database", "utilities"]`) |
| `main` | No | Primary `.42m` entry point |
| `genero` | No | Genero BDL version constraint (e.g., `^4.0.0`) |
| `root` | No | Base directory for package files when publishing (default `.`) |
| `files` | No | Glob patterns for files to include in the zip (default `["*.42m", "*.42f", "*.sch"]`) |
| `bin` | No | Command name to script path mappings (e.g., `{"migrate": "scripts/migrate.sh"}`) |
| `docs` | No | Glob patterns for documentation files to include (e.g., `["README.md", "docs/**/*.md"]`) |
| `dependencies.fgl` | No | BDL production package dependencies (`name` -> `version constraint`) |
| `dependencies.java` | No | Java JAR production dependencies (Maven coordinates) |
| `devDependencies` | No | Test / tooling deps (fgl + java), skipped with `--production` |
| `optionalDependencies` | No | Attempted like prod, failures emit a warning instead of aborting |
| `programs` | No | List of module names with MAIN blocks (e.g., `["PoiConvert"]`) |
| `visibility` | No | Who can see this package on the registry: `"public"` (default) or `"private"`. Defaults to `"public"` if omitted — set `"private"` explicitly to restrict access. Applied on first publish only; ignored on subsequent publishes. |
| `scripts` | No | Custom script definitions |

## Environment Variables

| Variable | Purpose |
|---|---|
| `FGLPKG_HOME` | Override default `~/.fglpkg` home |
| `FGLPKG_REGISTRY` | Registry URL — used by `install`, `search`, `audit`, `info`, `outdated`, `whoami`, `login`, `publish`. Default: `https://service.generointelligence.ai` |
| `FGLPKG_PUBLISH_REGISTRY` | Overrides `FGLPKG_REGISTRY` for the `publish` command only |
| `FGLPKG_TOKEN` | Bearer token for the registry. Overrides stored OAuth/PAT credentials |
| `FGLPKG_PUBLISH_TOKEN` | Bearer for the **legacy** `fglpkg-registry.fly.dev` commands only (`unpublish`, `owner`, `token`, `config`) |
| `FGLPKG_GITHUB_TOKEN` | GitHub PAT — only used by legacy `unpublish` and downloads from private GitHub Releases |
| `FGLPKG_GITHUB_REPO` | GitHub `owner/repo` — only used by legacy commands |
| `FGLPKG_GENERO_VERSION` | Override Genero version detection |
| `FGLPKG_INSTALL_CONCURRENCY` | Cap parallel downloads during install (default 4) |
| `FGLLDPATH` | Auto-managed by `fglpkg env` (prepends, preserves existing value) |
| `CLASSPATH` | Auto-managed by `fglpkg env` (prepends, preserves existing value) |

### Authentication

`fglpkg login` (no args) opens a browser and runs OAuth (auth code + PKCE) against the consumer registry. Tokens are persisted to `~/.fglpkg/credentials.json` and refreshed silently when they expire.

For non-interactive use (CI, SSH boxes, scripts), pass a Personal Access Token:

```bash
fglpkg login --token gpr_…       # or: export FGLPKG_TOKEN=gpr_…
```

The `publish` command uses the same OAuth/PAT credentials as the other consumer commands. The legacy `unpublish`/`owner`/`token`/`config` commands talk only to `https://fglpkg-registry.fly.dev` and require `FGLPKG_PUBLISH_TOKEN` to authenticate.

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
fglpkg search json                       # Search registry by keyword
fglpkg search --all                      # List every package in the registry
fglpkg bdl <pkg> <module> [args...]      # Run a BDL program from a package
fglpkg bdl --list                        # List available BDL programs

# Publishing
fglpkg publish                           # Publish current package to registry
fglpkg publish --dry-run                 # Preview the publish calls, no network
fglpkg publish --ci                      # Non-interactive publish (CI): needs FGLPKG_TOKEN
fglpkg publish --private                 # Publish as private (overrides fglpkg.json visibility)
fglpkg publish --public                  # Publish as public (overrides fglpkg.json visibility)
fglpkg unpublish pkg@1.0.0               # Remove a published version

# Authentication
fglpkg login                             # Save registry + GitHub credentials
fglpkg logout                            # Remove saved credentials
fglpkg whoami                            # Show current authenticated user

# Registry configuration (admin)
fglpkg config github-repos list          # List configured GitHub repos
fglpkg config github-repos add o/r       # Add a GitHub package repo
fglpkg config github-repos remove o/r    # Remove a GitHub package repo

# Package ownership
fglpkg owner list <pkg>                  # List package owners
fglpkg owner add <pkg> <user>            # Add a package owner
fglpkg owner remove <pkg> <user>         # Remove a package owner

# Token management (admin)
fglpkg token create <user>               # Create a user + token
fglpkg token revoke [<user>]             # Revoke a token
fglpkg token rotate                      # Rotate your own token

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
fglpkg version                           # Print version and build info
fglpkg help                              # Show help
```

## Legacy registry server

> The consumer commands (`install`, `search`, `info`, `outdated`, `whoami`,
> `login`, `publish`) talk to the Genero Intelligence registry over the
> `/registry/*` protocol — see [Publishing a Package](#publishing-a-package).
> The standalone server below is the **legacy** flat-file registry
> (`cmd/registry`, deployed at `fglpkg-registry.fly.dev`). The admin commands
> `unpublish`, `owner`, `token` and `config` still operate against it (overridable
> with `FGLPKG_PUBLISH_REGISTRY`) until the Genero Intelligence registry exposes
> equivalent endpoints. The API and storage layout documented here apply to this
> legacy server only.

```bash
# Build the registry binary
go build -o fglpkg-registry ./cmd/registry

# Start the server
export FGLPKG_PUBLISH_TOKEN=my-secret-token
./fglpkg-registry \
  --addr :8080 \
  --data /var/lib/fglpkg-registry \
  --base-url https://registry.example.com

# Point fglpkg clients at your registry
export FGLPKG_REGISTRY=https://registry.example.com
```

### Registry API

| Method | Path | Description |
|---|---|---|
| `GET` | `/packages/:name/versions` | List all versions, Genero constraints, and available variants |
| `GET` | `/packages/:name/:version` | Full package metadata (append `?genero=4` to select a variant) |
| `GET` | `/packages/:name/:version/download` | Download the zip (or redirect to external storage) |
| `POST` | `/packages/:name/:version/publish` | Publish a new version or variant (auth required) |
| `DELETE` | `/packages/:name/:version/unpublish` | Remove a published version (auth required) |
| `GET` | `/packages/:name/owners` | List package owners |
| `POST` | `/packages/:name/owners` | Add a package owner (auth required) |
| `DELETE` | `/packages/:name/owners/:user` | Remove a package owner (auth required) |
| `GET` | `/config` | Registry configuration (GitHub repos) |
| `POST` | `/config/github-repos` | Add a GitHub repo (admin only) |
| `DELETE` | `/config/github-repos/:owner/:repo` | Remove a GitHub repo (admin only) |
| `POST` | `/auth/token` | Create a user + token (admin only) |
| `DELETE` | `/auth/token` | Revoke a token |
| `POST` | `/auth/token/rotate` | Rotate own token |
| `GET` | `/auth/whoami` | Identify current token |
| `GET` | `/auth/users` | List all users (admin only) |
| `GET` | `/search?q=<term>` | Search by name or description |
| `GET` | `/health` | Liveness probe |

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
3. `POST /registry/packages/:slug/versions` — creates the version (a `409` means the version already exists; publish proceeds to add a new variant to it).
4. `PUT /registry/packages/:slug/versions/:version/artifacts/:variant` — streams the zip body; the registry computes size + checksum and stores it in R2.
5. `POST /registry/packages/:slug/versions/:version/submit` — marks the version pending for admin review.

Authentication uses the same OAuth/PAT bearer as the other consumer commands
(`FGLPKG_TOKEN` overrides stored credentials). No GitHub token is involved in
publishing.

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

### Registry Storage

The Genero Intelligence registry persists package and version metadata in its own
database and stores artifact zips in object storage (R2); it never unzips an
artifact. fglpkg interacts with it purely over the `/registry/*` HTTP API above —
there is no client-visible on-disk layout.

The legacy flat-file storage layout (`index.json`, `config.json`, `auth.json`,
per-package `meta.json`) belongs to the bundled standalone server described under
[Legacy registry server](#legacy-registry-server) below, not the Genero
Intelligence registry.

## Releases

Releases are automated via GitHub Actions. Push a tag to create a release with binaries for all platforms:

```bash
git tag v1.0.0
git push origin v1.0.0
```

Pre-built binaries are available at [GitHub Releases](https://github.com/4js-mikefolcher/fglpkg/releases).
