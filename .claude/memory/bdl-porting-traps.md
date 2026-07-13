---
name: bdl-porting-traps
description: Genero BDL language/runtime traps that actually bit during the Go→4GL port — scan before touching g/fglpkg modules
type: project
last-updated: 2026-07-13
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
- The **plain** `DYNAMIC ARRAY.sort(NULL, FALSE)` (no comparator) uses
  **locale collation**, not byte order — verified: it floats
  punctuation to the front and interleaves case
  (`_underscore, 10, 9, alpha, Alpha, ...`), diverging from Go's
  `sort.Strings` for anything not lowercase-only. Package names happen
  to be lowercase (registry-enforced at publish time, but NOT for
  dependency-map keys or Maven `groupId:artifactId`), so this is easy
  to miss until a mixed-case input shows up. Use
  `sortByComparisonFunction(NULL, FALSE, FUNCTION cmpBytes)` for any
  string sort whose output needs to be deterministic across locales
  (lockfiles, sboms, anything diffed or committed) — see
  `g/BENCHMARKS.md`.

- Under **BYTE** semantics, `getCharAt(i)` at a byte offset landing
  **inside** a multi-byte UTF-8 character does NOT return that raw
  byte — it silently substitutes a space (`ORD` confirmed, not a
  display artifact). Any `getCharAt`-loop over non-ASCII content is
  therefore silently wrong, not just slow. Fix: `explodeChars(s)` in
  `fglpkgutils.4gl` decodes via `s.split("")` (semantics-independent,
  UTF-8-safe) instead — `split("")` always yields exactly `length+2`
  elements (empty first/last bracketing one per real character, for
  any input including `""`/NULL); drop exactly the first and last, not
  a generic "filter empties". Then run the port under
  `FGL_LENGTH_SEMANTICS=CHAR` (the `fglpkg`/`fglpkg.bat` launcher
  scripts set this, mirroring gwa's `gwabuildtool` wrapper) so `ORD()`
  on each decoded character resolves the full Unicode code point
  (confirmed by the documented `ORD()` contract) instead of just the
  first byte — code-point order and UTF-8 byte order are equivalent
  for valid UTF-8, so this reproduces Go's byte-wise comparison
  exactly. `test/Makefile` also exports `FGL_LENGTH_SEMANTICS=CHAR` so
  tests exercise the same mode as production. See `g/BENCHMARKS.md`.

- Neither core BDL nor GWS has a sub-second `SLEEP` — the syntax
  requires an INTEGER seconds expression, and `SLEEP 0.01` silently
  truncates to 0 (measured: 0.000 s elapsed vs `SLEEP 1`'s 1.005 s).
  `util.Channels.selectWithTimeout` is the closest GWS analog but is
  ALSO integer-seconds-only and selects on `base.Channel` server
  sockets, not `com.HttpRequest`. There is no fine-grained wait
  primitive anywhere in BDL/GWS — a tight busy-poll is the only option
  if you need one (acceptable for a short-lived CLI, not for a
  long-running server).
- `fglpkgutils.makeTempName()`'s uniqueness check re-probed disk
  existence on every call instead of tracking what it had already
  issued — calling it several times back-to-back (e.g. building N
  download destinations up front) before any of the returned paths
  were created on disk made every call pass the same "doesn't exist
  yet" probe and return the **identical path**. Invisible until
  something called it more than once without creating the file in
  between. Fixed with an in-process monotonic counter for the suffix
  (see `g/BENCHMARKS.md` "parallel downloads"). Any function whose
  contract is "return N distinct not-yet-existing names/paths" needs
  to guarantee uniqueness by construction, not by re-checking disk
  state each time — the disk isn't a reliable "already issued" ledger
  until the caller actually writes there.
- `curl --parallel` does NOT fire every transfer immediately by
  default: it first waits to find out whether the initial connection
  can be HTTP/2-multiplexed before committing the rest of the batch —
  against a plain HTTP/1.1 server that's a wasted round-trip's stall
  (measured: 3 concurrent 0.5 s-delayed requests via `--parallel
  --parallel-max 3` took 1.03 s, ~2× the expected ~0.5 s). Add
  `--parallel-immediate` to skip that probe and get true N-way overlap
  (same test: 0.52 s). Relevant any time BDL shells out to curl for
  concurrent transfers instead of trying to parallelize inside the
  single-threaded interpreter.

**How to apply:** consult the genero-intelligence MCP skills for any API
you're not 100% sure about; when a compile fails with -6609 look for a
mid-function DEFINE or a method call on a function result first. Prefer
native `split()`/`util.Regexp`/`sortByComparisonFunction` over
hand-rolled getCharAt/subString loops and insertion sorts on anything
that can grow beyond a few hundred elements. Any new `getCharAt`-loop
over user-supplied strings needs the same `explodeChars`/CHAR-semantics
treatment — don't reach for raw `getCharAt` without checking this file
first.
