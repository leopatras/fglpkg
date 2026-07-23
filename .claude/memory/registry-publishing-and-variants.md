# Registry publishing workflows and variant resolution

Knowledge from publishing real packages (samples A–D, the webcomponent
sample, and the `fglwebrun` example package) to the live test registry,
plus reading the actual resolver/publish source. Complements
`port-4gl-status.md`/`bdl-porting-traps.md` (which cover the 4GL port
specifically) — this file is about the registry/CLI behavior itself,
relevant to either implementation.

## Test registry

`FGLPKG_REGISTRY=https://genero-intelligence-test.michael-folcher.workers.dev`
is a live Cloudflare-Workers-hosted test deployment of the real GI
registry backend (R2-backed artifact storage, OAuth2 auth-code+PKCE).
Auth via `fglpkg login` (opens a browser) or `FGLPKG_TOKEN`/`--token`
for CI; check with `fglpkg whoami`. Every publish lands as **pending
admin review** — visible via `fglpkg info`/`search` immediately, but a
real person needs to approve it. Modern publish flow stores the zip
itself (no GitHub Release involved) — the older "creates a GitHub
Release tagged `{name}-v{version}`" behavior described in stale docs is
gone; verified empirically (checked `4js-mikefolcher/fglpkg`'s and a
fork's Releases via the GitHub API — nothing created) and confirmed by
upstream's own doc rewrite (`docs/user-guide.md`, "Publishing" section).

**`unpublish` is documented (`docs/user-guide.md` before upstream's
rewrite) but not implemented** — grepped the entire `internal/cli`
tree, no `unpublish` command exists. Confirmed independently: the
4js internal Confluence "FGLPKG: Product Card" page
(https://4js.atlassian.net/wiki/spaces/RD/pages/2430435332) asks the
identical question in its own "Questions" section ("What happened to
the 'unpublish' sub command? How do we remove a package from the
registry?") — this is a known, real gap, not a fluke of an old
checkout. Practical consequence: a botched publish (e.g. missing
precompiled `.42m`) can't be removed, only superseded by bumping the
version and republishing correctly; the broken version sits inert in
admin-review limbo.

## Variant resolution (`pickArtifact`)

`internal/registry/registry.go:522` (mirrored in
`internal/provider/artifactory.go:240`-ish for the Artifactory
provider) — the actual algorithm fglpkg uses to pick an artifact for
the installing machine's Genero major:

1. `webcomponent` variant — matches unconditionally, checked first
2. exact `genero<N>` match (N = installer's detected/overridden major)
3. `default` variant tag
4. **`arts[0]`** — literally the first artifact in whatever order the
   registry returns them — arbitrary, not "closest compatible below"

There is **no version-aware fallback** (no "highest available <= my
major" logic). Grepped the whole `internal/cli` publish path: **no
code ever writes a variant literally `"default"`** — `fglpkg publish`
only ever produces `genero<N>` or `webcomponent`. So `default` is a
resolver-side convention with no CLI-level way to actually use it
today (would need a direct API call bypassing the CLI).

**Practical implication**: if a package only has ONE published variant
total, every installer falls through to `arts[0]` and gets it
regardless of requested major — this can look like "it just works
across versions" but is an accident of there being nothing else to
fall back to, not a real compatibility guarantee. The moment a second
variant is published, `arts[0]` becomes order-dependent (registry
listing order, not version proximity) and installers on a
non-exact-match major get an unpredictable pick. For a package whose
compiled bytecode genuinely differs per major, this is a real footgun
(silently installs a possibly-incompatible `.42m`). Always publish
explicit exact-match variants for every major you actually support —
don't rely on the fallback.

**Known gap / suggested improvement** (not yet implemented, discussed
2026-07-16, not filed anywhere yet): there's no manifest field to mark
a package as Genero-version-agnostic (e.g. pure shell/script tools, or
BDL source that self-compiles at run time rather than shipping
`.42m`). Proposed shape: a `"sourceOnly": true` manifest field (or a
`"universal"` variant name) that skips the `genero<N>` fan-out at
publish time and gets the same unconditional-match priority as
`webcomponent` in `pickArtifact`, instead of relying on the `arts[0]`
accident. Worth an upstream issue/spec if this pattern recurs.

## `bin` field / `fglpkg run` Windows limitation

`internal/cli/cli.go` `buildScriptCommand`: one script path per command
name; on Windows, dispatch is purely by file extension
(`.bat`/`.cmd`/`.ps1`/`.py`/`.sh`/`.exe`) via `cmd.exe`/`powershell.exe`
etc.; on Unix, executed directly via its shebang. **No cross-OS variant
selection** — you cannot declare "use this file on Unix, that file on
Windows" for the same command name. A tool shipping both a shebang
script and a `.bat` counterpart (e.g. `fglwebrun`) can only wire one of
them into `bin`; the other file can still be included in the package
(so a Windows user can invoke it directly / add the install dir to
PATH) but `fglpkg run <name>` itself won't dispatch to it.

## Example: the `fglwebrun` package

Published `fglwebrun` (from `github.com/FourjsGenero/tool_fglwebrun`,
MIT) to the test registry as a worked example of a **source-only,
self-compiling** package: its own `fglwebrun` launcher script always
runs `make clean_prog all` (recompiling from `.4gl` source) before
`exec fglrun fglwebrun.42m "$@"`, so there's never a precompiled `.42m`
to ship — the identical zip is genuinely compatible with any Genero
major, which is exactly the scenario the "known gap" above is about.
Published explicitly under `genero4`/`genero5`/`genero6` anyway (byte-
identical artifacts, verified via matching SHA256) rather than leaning
on the `arts[0]` fallback, per the reasoning above.

Manifest shape worth remembering: `"bin": {"fglwebrun": "fglwebrun"}`
(exposes it via `fglpkg run fglwebrun -- <args>`) plus
`"webcomponents": ["input"]` for its bundled demo webcomponent — a
real example of a **mixed BDL+webcomponent** package per
`specs/webcomponent-packages.md`. Consumer test project at
`~/tmp/mytest_fglwebrun` demonstrates the whole loop (install →
`fglpkg env` → delegate to the installed package's own `Makefile`'s
`demo` target) end-to-end, including a real `httpdispatch`+browser
session. Packaging source lives in `~/tmp/tool_fglwebrun` (the user's
real upstream checkout, with its own `fglpkg.json` + `make
fglpkg-publish`/`fglpkg-pack-list` targets added) — NOT in this repo.

## Registry-side name validation is stricter than the client's own schema

The fglpkg CLI's own manifest schema and `internal/slug` package (PEP 503 /
GIS-271) are explicitly designed to accept dotted, Maven-`groupId`-style
names (e.g. `com.fourjs.utils.cli`) and derive a canonical hyphenated slug
client-side (`Canonical`: lowercase, collapse runs of `.`/`_`/`-` to one
`-`). But the live test registry's `POST /registry/packages` rejects a
dotted raw name outright: `HTTP 400 {"error":"slug must be 2-64 chars:
lowercase letters, digits, hyphens"}` — it does not canonicalize
server-side the way the client expects. Confirmed publishing
`samples/cli-utils` (PACKAGE `com.fourjs.utils.cli`): had to rename the
manifest's `name` to `com-fourjs-utils-cli` to get past the registry,
even though the client-side schema/slug logic already fully supports the
dotted form. This is a registry-side gap, not a client-side one — the
registry backend's own source isn't in this repo (it's Mike's separate
Cloudflare Worker deployment). Leo's take (2026-07-23): dots should be
allowed — no problem with permitting them; worth raising with the
registry maintainer.

The BDL/Java `PACKAGE`/import path is unaffected either way — it's driven
entirely by the archive's internal directory layout (`com/fourjs/utils/cli/`),
never by the registry's own package name/slug (same as Maven `groupId` vs.
the Java package a `.class` file actually declares — see the conversation
that prompted this, no ticket filed).

## Reference

4js internal Confluence "FGLPKG: Product Card":
https://4js.atlassian.net/wiki/spaces/RD/pages/2430435332 — product
context (people: PM/PO Michael Folcher, R&D supervisor Mathieu Hofert,
contributors Andrew Pishchulin/Leo Schubert/Sébastien Flaesch),
architecture diagram, and Mike's stated plan to eventually move
`4js-mikefolcher/fglpkg` into the public FourJs GitHub org with a small
group reviewing PRs from a wider group (internal + external) — relevant
context for how PRs like #14 get reviewed.
