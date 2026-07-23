# Porting notes — fglpkg Go → Genero 4GL

How the Genero BDL implementation under `g/fglpkg/` was produced from the
Go sources, and everything a maintainer needs to keep the two in sync.
See `g/fglpkg/README.md` for the module map, build instructions and the
condensed deviations list, and `g/BENCHMARKS.md` for resolver runtime
measurements against the Go binary (huge dependency graphs).

## Goal and parity philosophy

The 4GL port is a **full-parity reimplementation**: every command of the
Go binary, with byte-identical output wherever output can be compared.
Parity is the acceptance criterion, verified by diffing stdout/stderr and
exit codes against a reference build (`bin/fglpkg-go`, gitignored — build
with `go build -o bin/fglpkg-go ./cmd/fglpkg`).

Consequences of "parity first":

- Exact error strings, table paddings, pluralization, JSON key order and
  omission rules are ported verbatim from the Go sources.
- Even upstream bugs are replicated deliberately when they are visible in
  output. Known case: **Go's `pluralY()` (internal/cli/outdated.go)
  returns `"ie"` — `cmdOutdated` appends the missing `"s"` itself, but
  `cmdAudit` uses it bare, so the binary prints "2 vulnerabilitie
  found"**. `audit.4gl` reproduces this (marked with a comment). Fix both
  sides together.
- Deviations are allowed only where the platform forces them, and each
  one is documented (README "Deviations" section). Highlights: shell-out
  zip handling, shell-out `curl --parallel` for concurrent downloads
  (falls back to sequential `com.HttpRequest` if curl is absent),
  loopback OAuth port scanning (9101–9300), `CreateUUIDString()` for
  the sbom serialNumber, byte-level zip differences (Info-ZIP vs
  archive/zip).

## Layout and style decisions (confirmed by Leo)

- Flat, gwa-style layout: one `.4gl` module per Go package, all in
  `PACKAGE fglpkg`, unprefixed module names, `FGLLDPATH=<repo>/g`,
  imported as `IMPORT FGL fglpkg.<module>`.
- Style copied from `~/w/gwa/src` (esp. `gwautils.4gl`): `OPTIONS SHORT
  CIRCUIT`, T-prefixed PUBLIC TYPEs, 2-space indent, `myassert.inc`
  MYASSERT macro, `&include` + preprocessor test macros.
- Target compatibility: Genero **4.01** (local toolchain 6.00.02).
- Windows browser launch follows **gwabrowser** (`start` command +
  `winQuoteUrl` escaping `%`→`^%`, `&`→`^&`), NOT Go's rundll32 — an
  explicit review decision.
- Zip/unzip shells out to Info-ZIP `zip`/`unzip` on Unix, `tar` on
  Windows; entries are pre-scanned for zip-slip.
- Auth: PAT (`--token`, `FGLPKG_TOKEN`) plus full browser OAuth
  (PKCE S256, RFC 8252 loopback callback, RFC 7591 dynamic client
  registration, silent refresh with a 401-retry hook in registry calls).
  `FGLPKG_BROWSER` overrides the browser command (4GL-port extension,
  used for headless testing with `curl -sL`).

## Phases

1. **Consumer core** — init/install/remove/update/list/env/search/info/
   pack/version/help (+ per-command help via the `commands.4gl` registry).
2. **Publisher + auth** — publish (dry-run/ci/visibility), login/logout/
   whoami (OAuth + PAT), outdated, token refresh.
3. **Full parity** — workspace (monorepo members, topo sort, resolver/env
   integration), audit (OSV.dev, exit codes 0/1/2), sbom (CycloneDX 1.5),
   completion (bash/zsh/fish/powershell), bdl/run/docs (with exact child
   exit-code propagation for `bdl`).

## BDL traps encountered (beyond the standard pitfall lists)

Language/runtime behavior that actually bit during this port. Worth
scanning before touching any module.

**Language / compiler**

- `DEFINE` must sit at the top of a FUNCTION/MAIN — a `DEFINE` after the
  first statement is error -6609. (Hit repeatedly; move declarations up.)
- Method calls on function results are a grammar error:
  `NVL(s,"").trim()` does not parse. Assign to a variable first.
- Reserved/ambiguous identifiers to avoid as variable names: `next`,
  `label`, `current`, `notFound`.
- Preprocessor macro invocations must stay on a single source line.
- `IIF()` cannot return records; use IF/ELSE.
- Multi-value returns cannot be forwarded (`RETURN f()` fails when `f`
  returns several values) — use `CALL f() RETURNING ...` then `RETURN`.
- `VAR x = expr` needs an explicit type when the initializer type is not
  inferable (e.g. `ORD()`, `util.Integer.toHexString()`).
- A single-segment `PACKAGE X` whose own module file is *also* named `X`
  breaks cross-package resolution: `fglcomp` compiles the module fine on
  its own, but another package's `IMPORT FGL X.X` fails with `-8447
  function 'main' not found in package 'X'` even though the function is
  declared `PUBLIC` (confirmed empirically, 2026-07-21 — see
  `samples/A/a/Core.4gl`). Always give the module a distinct (PascalCase)
  name from its package path, per the documented naming convention — it
  isn't just style, it sidesteps a real compiler bug.

**NULL semantics**

- `""` IS NULL. `||` propagates NULL — concatenation helpers must go
  through `base.StringBuffer` or `SFMT` (see `fglpkgutils.concat`,
  `padRight`).
- `NULL == NULL` is FALSE; comparison chains like
  `x == "a" OR x == "b"` return NULL for NULL x (and `NOT NULL` is
  NULL). Route validity checks through a rank/lookup function
  (see `audit.validSeverity`).
- `util.JSONObject.put(k, "")` emits JSON `null` — where the Go output
  has `""`, the serialized string is post-patched
  (`replace(':null', ':""')`, see outdated/publish).
- `fgl_setenv(name, NULL)` stores `" "` — `fglpkgutils.getEnvDefault`
  treats whitespace-only as unset.
- A `readLine` result can be empty-but-not-NULL; test `getLength() == 0`.

**Data structures**

- Record assignment shares `DYNAMIC ARRAY` / `DICTIONARY` members **by
  reference** — reusing a scratch record corrupts previously stored
  values. Deep-copy via a util.JSON round-trip when needed
  (see `publish.publishCopy`).
- Repeated `util.JSON.parse` into the same record keeps stale DYNAMIC
  ARRAY entries when the key is absent from the new document — clear the
  arrays before each parse (see `audit.auditJARs`).
- STRING length/indexing is **byte**-based: never right-trim through
  multibyte characters (e.g. `─`) with `getCharAt` loops. Pad
  fixed-width columns instead and leave the last column unpadded.

**Runtime / classes**

- `RUN cmd RETURNING st` yields the raw Unix wait status (`exit << 8`);
  convert with `fglpkgutils.childExitCode()` (handles signals). Windows
  returns the code directly.
- `com.HttpRequest.doFormEncodedRequest(form, TRUE)` URL-encodes the
  values itself — pass raw values and double literal `&`/`=`
  (see `oauth.formValue`).
- Server sockets: `base.Channel.dataAvailable()` does NOT report pending
  connections; use `util.Channels.select()` then `accept()`.
- `base.Channel.openFile(NULL, "r")` is stdin ("<stdin>" is not).
- `security.RandomGenerator.CreateRandomString(n)` returns base64 of n
  random bytes (convert to base64url for PKCE);
  `CreateUUIDString()` returns an uppercase UUID (lowercase it for
  urn:uuid). `util.Datetime.add` does not exist — do epoch-seconds
  arithmetic via `toSecondsSinceEpoch`/`fromSecondsSinceEpoch`.

## Testing strategy

Three layers, all runnable offline:

1. **Unit tests** — `g/fglpkg/test/test*.4gl`, assert macros from
   `testassert.inc` (TEQ/TOK/TSUMMARY). `make test` builds and runs all
   of them (18 programs, ~1100 checks).
2. **Mock-server E2E** — `test/mock_registry.py <port> <statedir>`
   implements the registry API, the OAuth endpoints (/register,
   /authorize, /token) and `POST /v1/query` as an OSV.dev stand-in
   (canned responses in `<statedir>/osv.json`, keyed by purl). Drive the
   CLI with `FGLPKG_REGISTRY`, `FGLPKG_AUDIT_URL`, `FGLPKG_HOME`,
   `FGLPKG_BROWSER="curl -sL"`.
3. **Byte-parity diffs** — run the same scenario against `bin/fglpkg-go`
   and `fglrun main.42m`, diff stdout/stderr and compare exit codes.
   Normalize the genuinely random parts first (sbom serialNumber +
   timestamp, audit auditedAt, OAuth state).

Headless invocation of the 4GL CLI:

```bash
FGLLDPATH=<repo>/g FGLGUI=0 TERM=xterm fglrun <repo>/g/fglpkg/main.42m <cmd> [args...]
```

## Keeping the port in sync

When the Go implementation changes:

1. Identify the Go package(s) touched; the module map in
   `g/fglpkg/README.md` gives the owning 4GL module.
2. Port the change with the exact strings/format, mind the trap list
   above, and consult the Genero MCP skills for any API you're not
   certain about (LLM training data on Genero is unreliable).
3. Update/extend the module's unit test, re-run `make test`.
4. Re-run the relevant byte-parity diff against a fresh `bin/fglpkg-go`.
5. Help text lives in `commands.4gl` — Go's `commands.go` `Long` strings
   must be copied fully (all 24 `fglpkg <cmd> --help` outputs are
   currently byte-identical; keep it that way).

**Drift found this way (2026-07-21):** the `files` manifest field's
path-scoped pattern matching (GIS-275 — a `/`-containing pattern like
`"a/*.4gl"` matches relative to `root`, not just the basename) shipped
in the Go binary but was never ported to `pack.4gl`'s
`collectBDLFiles` — every `/`-containing pattern silently matched
nothing, so a package whose files live under a subdirectory (needed for
any `PACKAGE`-declared module — see the import-root spec) packed as
empty. Ported as `pack.filesPatternMatch`, covered by
`test/testpack.4gl`'s `testFilesPatternMatch`/`testBuildZipPathScopedFiles`.
`specs/*.md` documents Go-side features like this one *before* they're
necessarily ported — treat a spec file as a prompt to check the 4GL
side has caught up, not proof that it has.
