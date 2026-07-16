# Spec: Registry `readme` field on package metadata

**Status:** ✅ Implemented — shipped (`readme`/`userguide` surfaced on package metadata)
**Date:** 2026-05-14
**Author:** Mike Folcher
**Motivation:** Enable an MCP service (and future web UI) to surface a package's README without downloading the published zip.

---

## Summary

Add an optional `readme` field to the registry's per-version metadata. The CLI extracts the project's top-level README at publish time and POSTs it as part of the publish payload; the server stores it on the `versionRecord`; `GET /packages/:name/:version` returns it.

No new endpoints. No client UX change. The field is content for downstream consumers (MCP server, web UI, future `fglpkg info --readme`).

## Goals

- One existing call (`GET /packages/:name/:version`) carries enough information for an MCP `getPackageReadme(name, version)` tool.
- Matches the npm/PyPI/crates.io convention of bundling a top-level README field with version metadata.
- Backwards compatible: old packages with no readme return `""` (omitted from JSON via `omitempty`).
- No server-side zip download. Publish remains JSON-only; the server is dumb storage for whatever the client sends.

## Non-goals

- Full doc tree (multi-file). That's the larger change; layer it on later.
- README rendering (markdown → HTML). The MCP / web layer can handle that.
- README content validation. Whatever the client sends, the server stores.

## Behavior

### Client side (publish)

In `publishPackage` ([internal/cli/cli.go](../internal/cli/cli.go)), before constructing the publish JSON, look for a README in the package root directory and read its content.

- **Search root:** the `m.Root` directory (same as the zip root). Defaults to `.` if not set.
- **Filename match (in order):** `README.md`, `README.MD`, `README.markdown`, `README.rst`, `README.txt`, `README`. First match wins. Case-insensitive on the basename.
- **Size cap:** 256 KB. If the file is larger, truncate to the cap and append a single trailing line `\n\n*(README truncated at 256 KB)*\n`. (Truncation in v1 because the registry payload sits inside a JSON body; unbounded blobs are a foot-gun.)
- **No README found:** publish proceeds without the field. Not an error.

The collected README is added to the publish meta map as `"readme"`. Dry-run prints it like any other field (truncated in the preview if long).

### Server side (publish)

`publishRequest` grows:

```go
Readme string `json:"readme,omitempty"`
```

`versionRecord` (in store.go) grows:

```go
Readme string `json:"readme,omitempty"`
```

All three save paths (`savePackage`, `savePackageMetadata`, `savePackageVariant`) copy `meta.Readme` to `vr.Readme`. For variant publishes that *add* to an existing version, the variant publish carries the readme alongside the variant data, and the server uses it only when the underlying versionRecord is brand new — re-publishing variants does not overwrite an existing README.

**Server-side size cap:** 512 KB. Anything over that is rejected with HTTP 400. The client truncates at 256 KB; the server's cap is 2× to give headroom and to keep the check independent of the client.

### Read side

No new endpoints. `GET /packages/:name/:version` returns the existing `versionRecord` JSON; the new `readme` field is included when populated.

The client's `PackageInfo` struct ([internal/registry/registry.go](../internal/registry/registry.go)) gets a matching `Readme string` field so existing Go consumers (and future tooling) can read it without parsing untyped JSON.

## API change summary

| Endpoint | Change |
|---|---|
| `POST /packages/:name/:version/publish` | Accepts a new optional `readme` field in the JSON body. Server enforces ≤ 512 KB. |
| `GET /packages/:name/:version` | Response now includes an optional `readme` field. |
| All others | No change. |

## Testing

- `internal/cli/publish_readme_test.go` *(new)*
  - `TestCollectReadmePrefersMarkdown` — both `README.md` and `README.txt` exist; the .md wins.
  - `TestCollectReadmeCaseInsensitive` — file is named `readme.md`; still found.
  - `TestCollectReadmeMissing` — no README in dir; returns "" with no error.
  - `TestCollectReadmeTruncates` — file > 256 KB; returned content is capped + has the truncation marker.
- `internal/registry/server/server_test.go` *(append)*
  - `TestPublishWithReadmeRoundTrip` — publish a package with `readme` set; `GET /packages/.../1.0.0` returns it verbatim.
  - `TestPublishReadmeTooLarge` — publish with `readme` > 512 KB; server returns 400.
  - `TestPublishWithoutReadmeOmitsField` — clean publishes don't include `readme` in the response JSON.

## Acceptance criteria

1. `fglpkg publish` discovers a top-level README (md/rst/txt) and sends its content as `readme` in the publish payload. ✅
2. Server stores the `readme` and returns it from `GET /packages/:name/:version`. ✅
3. Server rejects `readme` payloads exceeding 512 KB with HTTP 400. ✅
4. Packages without a README publish successfully; the `readme` field is omitted from responses. ✅
5. `PackageInfo.Readme` on the client populates correctly when the server returns one. ✅
6. No new endpoints; no changes to existing endpoint shapes beyond the optional field. ✅
7. `go build ./...` clean; `go test ./...` passes.

## Future work (explicitly deferred)

- Multi-file documentation endpoints (`/docs`, `/docs/<path>`).
- README rendering / sanitization.
- README content negotiation (markdown vs. plain text).
- `fglpkg info --readme` CLI display.
