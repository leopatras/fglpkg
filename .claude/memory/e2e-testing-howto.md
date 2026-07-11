---
name: e2e-testing-howto
description: How to run the 4GL port headlessly and byte-diff it against the Go binary (mock registry, mock OSV, headless OAuth)
type: project
last-updated: 2026-07-11
---

# E2E testing the 4GL port

**Run the 4GL CLI headlessly:**

```bash
FGLLDPATH=<repo>/g FGLGUI=0 TERM=xterm fglrun <repo>/g/fglpkg/main.42m <cmd> [args...]
```

**Reference binary:** `go build -o bin/fglpkg-go ./cmd/fglpkg`
(bin/ is gitignored). Diff stdout, stderr AND exit codes; normalize the
random parts first (sbom serialNumber/timestamp, audit `auditedAt`).

**Mock server:** `g/fglpkg/test/mock_registry.py <port> <statedir>`
serves:

- the registry API (packages, versions, artifact upload, whoami) —
  point the CLI at it with `FGLPKG_REGISTRY=http://127.0.0.1:<port>`;
  valid bearer tokens: `gpr_e2e_pat`, `at_oauth_1`, `at_oauth_2`
- the OAuth endpoints `/register`, `/authorize`, `/token` — for a fully
  headless browser-OAuth login set `FGLPKG_BROWSER="curl -sL"` (the 4GL
  port launches that instead of a browser); the mock's authorize
  endpoint 302s straight back to the loopback callback
- `POST /v1/query` as an OSV.dev stand-in for `fglpkg audit` — set
  `FGLPKG_AUDIT_URL=http://127.0.0.1:<port>/v1/query`; canned responses
  are read per-request from `<statedir>/osv.json`, format
  `{ "<purl>": { "vulns": [ ...OSV objects... ] }, ... }`, unknown purls
  get `{}` like the real service

Use `FGLPKG_HOME=<tmpdir>` to sandbox credentials/global packages.
Request bodies the mock received are dumped into `<statedir>`
(token.json, register.json, version-meta.json, ...) — useful to assert
what the client actually sent (this is how the doFormEncodedRequest
double-encoding bug was found).

**bdl exit-code E2E:** compile a fixture module that exits with a known
code, install it as a package, run `fglpkg bdl <pkg> <module>` and check
`$?` — the port must propagate the exact code (Go parity; `run` on the
other hand deliberately collapses failures to exit 1).
