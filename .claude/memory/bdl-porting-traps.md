---
name: bdl-porting-traps
description: Genero BDL language/runtime traps that actually bit during the Go→4GL port — scan before touching g/fglpkg modules
type: project
last-updated: 2026-07-11
---

# BDL traps hit during the port

The canonical, explained catalogue lives in `g/PORTING.md` §"BDL traps
encountered" — read that section before writing or reviewing 4GL code in
this repo. Compressed checklist:

- `DEFINE` only at function top (-6609 otherwise); method calls on
  function results (`NVL(s,"").trim()`) don't parse — assign first.
- Reserved-ish identifiers: `next`, `label`, `current`, `notFound`.
- `""` IS NULL; `||` propagates NULL (use StringBuffer/SFMT helpers);
  `NULL == NULL` is FALSE and OR-chains over NULL yield NULL (use rank/
  lookup functions for validity checks); `JSONObject.put(k, "")` emits
  `null` (post-patch the JSON string where Go emits `""`).
- Record assignment shares DYNAMIC ARRAY/DICTIONARY members by
  reference (deep-copy via util.JSON round-trip); repeated
  `util.JSON.parse` into a record keeps stale array entries when the key
  is absent — clear arrays before each parse.
- STRING indexing is byte-based — never trim through multibyte `─`.
- `RUN ... RETURNING` gives the raw Unix wait status — use
  `fglpkgutils.childExitCode()`.
- `doFormEncodedRequest` URL-encodes values itself (pass raw, double
  literal `&`/`=`); server sockets need `util.Channels.select()` before
  `accept()`; `openFile(NULL,"r")` = stdin; `fgl_setenv(x, NULL)` stores
  `" "`; `CreateRandomString(n)` = base64 of n bytes;
  `CreateUUIDString()` is uppercase; no `util.Datetime.add` — use epoch
  seconds; preprocessor macros are single-line; `IIF` can't return
  records; multi-value returns need CALL...RETURNING.

- STRING positional ops are NOT O(1) in a UTF-8 locale (measured
  2026-07-11, Genero 6.00.02): `subString(start, end)` costs O(start)
  even under byte length semantics, and with `FGL_LENGTH_SEMANTICS=CHAR`
  `getCharAt(i)` costs O(i) too. Any walk-and-slice loop (splitOnChar
  pattern, char-by-char scanners) is therefore quadratic: 80 kB input →
  3.5 s (byte) / 18-25 s (char semantics). Native `STRING.split(regexp)`
  does the same job in ~2 ms and its edge-case semantics match Go's
  `strings.Split` exactly ("a,,b"→3, "a,"→2, ""→1 empty) — but two
  gotchas: the separator is a REGEX (escape metacharacters — `"."`
  unescaped splits on every char and returns only empty strings,
  silently; use `fglpkgutils.quoteRegexp`), and empty fields come back
  empty-but-not-NULL while `""` literals are NULL — normalize before
  `IS NULL`/`TEQ` comparisons. FIXED in the port 2026-07-11: the
  fglpkgutils splitters wrap native split(), prettyJSON explodes to a
  char array once via `split("")`. Details: `g/BENCHMARKS.md`.

- Native `MATCHES` is ~140× faster than an interpreted glob matcher but
  is NOT filepath.Match: its `*` crosses `/`, malformed patterns match
  leniently (Go: never), and `NULL MATCHES x` is NULL (Go: "" matches
  "*"). Equivalent ONLY for slashless operands with patterns free of
  `[` and `\` — glob.pathMatch fast-paths exactly that shape (proven by
  a 238k-pair differential fuzz; see g/BENCHMARKS.md).
- Hand-rolled insertion sorts are O(n²) interpreted comparisons — 2min+
  for 10k path strings. Use `DYNAMIC ARRAY.sortByComparisonFunction`
  (since 4.01.03, FGL-5904; first arg = record member or NULL for flat
  arrays) with `fglpkgutils.cmpBytes` for Go-parity byte order. Quirk:
  function-reference compatibility includes the parameter NAMES — the
  comparator must be declared `(s1 STRING, s2 STRING) RETURNS INTEGER`.
  Arrays nested in records are shared by reference, so sort a `copyTo`
  copy when the input must stay unmodified.

**How to apply:** consult the genero-intelligence MCP skills for any API
you're not 100% sure about; when a compile fails with -6609 look for a
mid-function DEFINE or a method call on a function result first. Prefer
native `split()`/`util.Regexp`/`sortByComparisonFunction` over
hand-rolled getCharAt/subString loops and insertion sorts on anything
that can grow beyond a few hundred elements.
