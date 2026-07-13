# Resolver benchmarks — 4GL port vs Go implementation

Runtime comparison of dependency-graph traversal (`fglpkg install`'s
resolution phase) between the Go reference and the Genero 4GL port,
measured 2026-07-11 on an Apple-silicon Mac (Go 1.26.5, Genero 6.00.02).

## Method

Twin benchmark programs drive both resolvers through their injectable
fetcher seams with an **identical synthetic graph generated on the fly**
inside the fetchers — no network, no disk, nothing but the traversal is
measured:

- N packages named `pkg000001..pkgN`, package *i* depends on
  *i+1 .. i+K* (fan-out K, capped at N), root depends on `pkg000001`
- every package has versions 1.0.0 / 1.1.0 / 1.2.0 and every edge
  carries the constraint `^1.0.0`, so semver parse + match runs on
  every edge (E ≈ N·K)

Programs:

- 4GL: `g/fglpkg/test/benchresolver.4gl` — run with
  `FGLLDPATH=<repo>/g FGLGUI=0 fglrun benchresolver <N> <K>`
- Go: a throwaway external test in `internal/resolver`
  (`hugegraph_bench_test.go`) — run with
  `BENCH_N=<N> BENCH_K=<K> go test ./internal/resolver -run TestBenchHugeGraph -count=1 -v`

## Results (median of 2 runs)

| N packages | K | 4GL (fglrun) | Go as shipped | Go with sort fixed |
|-----------:|--:|-------------:|--------------:|-------------------:|
|      1 000 | 5 |        85 ms |        8.6 ms |              3.9 ms |
|      5 000 | 5 |       425 ms |        171 ms |               19 ms |
|     20 000 | 5 |        1.7 s |     **2.7 s** |               79 ms |
|     50 000 | 5 |        4.3 s |    **18.5 s** |              211 ms |
|      5 000 | 20|        1.3 s |        212 ms |               58 ms |

## Findings

**1. The Go implementation is accidentally O(N²); the 4GL port is
linear — above ~10 000 packages the interpreted 4GL beats native Go.**

A CPU profile at N=20000 attributes 91% of Go's runtime to
`state.buildPlan` (63% in `runtime.mapaccess1_faststr`). The cause is
`internal/resolver/resolver.go` (`buildPlan`): an insertion sort that
restores discovery order over a slice populated from Go's **randomized**
map iteration, with two map lookups per comparison. Random input is the
worst case for insertion sort → quadratic.

The 4GL port contains the byte-faithful *same* insertion sort
(`resolver.4gl`, `buildPlan`) — but Genero's `DICTIONARY.getKeys()`
returns keys in **insertion order** (verified empirically), and
`markResolved` inserts in exactly the discovery order being sorted. The
4GL sort's inner loop therefore never fires: O(N) always, by accident of
dictionary semantics.

**2. With the Go sort fixed, the honest language comparison is a ~20×
constant factor.** Replacing the insertion sort with `sort.Slice`
(patch verified against the full resolver test suite, then reverted)
makes Go cleanly linear at ~4 µs/package; the 4GL runs at a very stable
~85 µs/package. The p-code VM penalty is dominated by record copies,
STRING operations, and per-edge semver constraint parsing. Both sides
scale linearly in edge count (K=5→20 quadruples E; 4GL ×3.2, fixed Go
×3).

**3. For realistic graphs this is all noise.** At tens to a few hundred
packages both resolvers finish in single-digit milliseconds (4GL) or
sub-millisecond (Go); a real `install` spends its time on the 2 HTTP
round-trips per package, which outweigh the traversal by orders of
magnitude.

## Recommendation

Replace the insertion sort in Go's `buildPlan` with
`sort.Slice(pkgs, ...)` over the `order` field (88× faster at N=50k,
no behavior change — the sort key is unchanged). Per the parity rule
(`g/PORTING.md`), give the 4GL `buildPlan` the matching change in the
same commit — it is already effectively linear thanks to
insertion-ordered `getKeys()`, but should not rely on that accident.

## String operations: positional loops vs native split (fixed 2026-07-11)

Investigation prompted by Leo: STRING walk-and-slice loops are quadratic
in UTF-8 environments. Measured on 80 kB inputs (Genero 6.00.02):

| operation | byte semantics (default) | `FGL_LENGTH_SEMANTICS=CHAR` |
|---|---:|---:|
| plain `getCharAt(i)` scan | 9 ms (linear, O(1)/call) | **17.7 s** (O(i)/call) |
| old `splitOnChar` (getCharAt + subString) | **3.6 s** | **8.6 s** |
| native `STRING.split(regexp)` | 2 ms | 2 ms |

Two distinct causes: `getCharAt(i)` is O(i) only under char length
semantics, but **`subString(start, end)` costs O(start) under both
semantics** — so any loop that slices pieces at a growing position is
quadratic everywhere (fixed-position `subString`: 26k calls = 2 ms;
growing-position: 3.4 s).

Fix applied in the port: `splitOnChar`/`splitOnString`/`splitFields`
are now thin wrappers over native `STRING.split()` (BDL 4.00 feature)
with a new `fglpkgutils.quoteRegexp()` escaping the literal separator,
and `manifest.prettyJSON` explodes the document into a char array once
via `split("")` instead of calling `getCharAt` on a growing index.
Results: `splitOnChar` 3.6 s → 2 ms at 80 kB; `prettyJSON` handles a
275 kB document in 87 ms in both semantics modes. Two `split()` gotchas
encoded in the wrappers: the separator is a regex (metacharacters must
be escaped — unescaped `"."` returns only empty strings), and empty
fields come back empty-but-not-NULL where the historical helpers
yielded `""` (= NULL) — normalized to keep `IS NULL` checks working.
All 1093 checks and the help/sbom byte-parity diffs vs the Go binary
still pass.

## Sorting: insertion sorts vs sortByComparisonFunction (fixed 2026-07-11)

Follow-up prompted by Leo: `cmpBytes` (the Go `strings.Compare`
substitute) drove five hand-rolled insertion sorts — `glob.sortBytewise`
(13 call sites, largest input: `pack`'s project-wide file list) plus
inline sorts in lockfile (packages/webcomponents/jars), sbom (jars) and
audit (findings). O(n²) interpreted comparisons, made worse by shared
path prefixes (deep char loops per compare):

| n path-like strings | insertion sort | `sortByComparisonFunction` |
|---:|---:|---:|
|  1 000 |       1.3 s |  51 ms |
|  5 000 |      29.7 s | 330 ms |
| 10 000 | **2 min 8 s** | 795 ms |

Fix: all bytewise sorts now use the native
`DYNAMIC ARRAY.sortByComparisonFunction` driver (available since
4.01.03, FGL-5904) with `fglpkgutils.cmpBytes` as comparator — verified
element-by-element to produce the identical order. Genero quirk: the
comparator's **parameter names** must be `s1`/`s2` — function-reference
compatibility includes parameter names, not just types. Left as-is:
`audit.sortFindings` (multi-key stable sort, tiny inputs) and
single-comparison `cmpBytes` uses.

Bonus: the mixed-case parity fixture for this change exposed a latent
port bug — `sbom.4gl` emitted BDL package components in lockfile order
while Go sorts them by name (`internal/sbom/cyclonedx.go`); invisible
with fglpkg-written locks (sorted on save), wrong for hand-edited ones.
Fixed (sorted copies for components + dependency edges) with a
regression test in testsbom.

## fglpkgutils.sortStringArray: locale collation vs byte order (fixed 2026-07-13)

Audit prompted by Leo, once the Go binary stopped being a required
parity target: does `sortStringArray` (used by `fglpkg info`'s
dependency listing, `fglpkg outdated`, and `resolver.buildPlan`'s JAR
key ordering) need `cmpBytes`, or would the plain built-in
`arr.sort(NULL, FALSE)` do? It was still using the plain sort — missed
by the `sortBytewise`/lockfile/sbom pass above because it's a
separately named function.

Measured: `arr.sort(NULL, FALSE)` applies **locale collation**, not
byte order — verified with `["zebra","Alpha","alpha","apple","Apple",
"élan","_underscore","10","9","Zebra"]`: plain sort produced
`_underscore, 10, 9, alpha, Alpha, apple, Apple, élan, zebra, Zebra`
(punctuation floated to the front, case interleaved), while
`sortByComparisonFunction(NULL, FALSE, FUNCTION cmpBytes)` produced
`10, 9, Alpha, Apple, Zebra, _underscore, alpha, apple, zebra, élan` —
byte-identical to Go's `sort.Strings` on the same input.

For fglpkg's own package names (registry-constrained to lowercase/
digit/hyphen) the two happen to agree, so this was invisible in
practice — but that constraint isn't structural: it's enforced only by
the *external* registry at publish time, on the package's own name;
nothing in either client validates `dependencies.fgl` **keys**, so a
hand-authored `fglpkg.json` or workspace member can carry any-case
names. Maven JAR coordinates (`groupId:artifactId`, sorted by
`resolver.buildPlan` for JAR install order) are not case-constrained at
all, and diverge for realistic input:
`com.Google.code.gson:gson` / `com.google.code.gson:gson` /
`org.Apache.poi:poi` / `Zend:foo` sort case-insensitively-grouped
under collation but byte-ordered (`Zend` first) under `cmpBytes` —
confirmed against the real `resolver.buildPlan` code path with a fake
Java-dependency fixture, not just a synthetic array.

Why it matters even without a Go target to match: locale-dependent
order means the same lockfile-generating input can produce different
JAR install/display order depending on the machine's `LANG`, which
undermines the reproducibility a lockfile/sbom-generating tool is
supposed to provide. Decision: keep byte order as the one sort
behavior everywhere rather than branch human-display code from
artifact-writing code — simpler to reason about, and package names in
practice are lowercase-hyphenated so collation wouldn't even look
different to a user.

Fix: `sortStringArray` now delegates to
`arr.sortByComparisonFunction(NULL, FALSE, FUNCTION cmpBytes)`, same
one-line shape as `glob.sortBytewise`. No import changes needed —
`cmpBytes` already lives in the same module. All three call sites
(`cli.4gl`, `outdated.4gl`, `resolver.4gl`) get the fix for free; all
18 test programs / 1100+ checks still pass.

## glob.pathMatch: MATCHES fast path (fixed 2026-07-11)

Last char-loop candidate, prompted by Leo: `pathMatch` (the
`filepath.Match` port) is a recursive backtracking matcher at ~12.7 µs
per call — noticeable in aggregate because its hot shape is
ignore-rules × tree-entries during `pack` (200k calls ≈ 2.5 s).

Genero's native `MATCHES` operator is the same glob language and ~140×
faster, but NOT a drop-in: its `*` crosses `/` (filepath.Match's must
not), it is lenient with malformed patterns (Go's never match), and
`NULL MATCHES x` is NULL (Go matches "" against "*"). However, for
**single-segment operands with simple patterns** the two are
equivalent — proven by an exhaustive differential test (238 000
pattern/name pairs over a glob alphabet, zero mismatches under both
length semantics; collation ranges like `[A-Z]` also agreed byte-wise).

Fix: `pathMatch` now delegates to `MATCHES` when pattern and name
contain no `/` and the pattern contains no `[` and no `\`; everything
else keeps the exact slow path. Hot shape: 12.7 µs → 0.36 µs per call
(35×, guard included). The full pattern language still goes through
`matchFrom`, so filepath.Match parity is unchanged (testglob's 66
checks cover classes, escapes, separators and malformed patterns).

## cmpBytes silently corrupted multi-byte characters (fixed 2026-07-13)

Leo pushed back on the earlier "BYTE semantics is correct and fast,
CHAR semantics is correct but slow" framing — testing with a genuinely
multi-byte character proved the first half wrong. Dump for `"aéz"`
(é = U+00E9, UTF-8 `0xC3 0xA9`):

| | `getLength()` | `getCharAt(1)` | `getCharAt(2)` | `getCharAt(3)` | `getCharAt(4)` |
|---|---:|---:|---:|---:|---:|
| BYTE semantics | 4 (bytes) | 97 'a' | 195 (0xC3, é's lead byte) | **32 (SPACE)** | 122 'z' |
| CHAR semantics | 3 (chars) | 97 'a' | 233 (0xE9, é's real code point) | 122 'z' | — |

Under BYTE semantics, `getCharAt` at a byte offset landing on a
**continuation byte doesn't return that raw byte — it silently
substitutes a space** (confirmed via `ORD()`, not a display artifact).
This is the same failure mode behind an old formatting bug (table
right-trim eating a multibyte `─`), but it's general: **any
`getCharAt`-loop over BYTE-semantics content silently corrupts
multi-byte characters**, including `cmpBytes`. For pure-ASCII input
(registry package names, Maven coordinates so far) this never showed
up; for anything with accented/CJK content, `cmpBytes` produced wrong
order relative to Go's true byte-wise `sort.Strings`.

Per [`ORD()`'s documented contract](https://4js.com/online_documentation/fjs-fgl-manual-html/fgl-topics/c_fgl_operators_ORD.html):
with UTF-8 + CHAR semantics, `ORD()` returns the full Unicode code
point of a (possibly multi-byte) character; with BYTE semantics it
returns only the first byte. Since UTF-8 byte order and code-point
order are equivalent for valid UTF-8, comparing whole decoded
characters by code point under CHAR semantics reproduces Go's
byte-wise order exactly — verified: `["café","cafe","café-2","élan",
"zebra","Ärger","ärger","日本語","aardvark","über"]` sorted via the new
`cmpBytes` under forced CHAR semantics is **byte-for-byte identical**
to Go's `sort.Strings` on the same input.

Fix, two parts:

1. **`fglpkgutils.explodeChars(s)`**: decodes a string into one array
   element per character via `s.split("")`, which is UTF-8-safe and
   semantics-independent (unlike `getCharAt`). `split("")` always
   yields exactly `length+2` elements — an empty leading and trailing
   element bracketing one element per real character — for *any* input
   including `""`/NULL (verified: both produce the two empty brackets
   and nothing else); the fix is to drop exactly the first and last
   elements, not to filter all empties. Per Jira DOC-6487's documented
   recipe, this is `arr.deleteElement(arr.getLength())` then
   `arr.deleteElement(1)` (delete the trailing bracket first — deleting
   index 1 first would shift every element down by one, moving the
   last real character into the slot the second `deleteElement` call
   would then remove instead), not a copy into a second array as
   originally written. `cmpBytes`, `quoteRegexp` and
   `manifest.prettyJSON` (previously three separate copies of this
   explode-and-trim logic) now share this one helper.
2. **`g/fglpkg/fglpkg`/`fglpkg.bat`**: the launcher scripts now set
   `FGL_LENGTH_SEMANTICS=CHAR` unconditionally (mirroring gwa's
   `gwabuildtool` wrapper pattern, including `LANG=.fglutf8` on
   Windows) — required so `ORD()` inside the rewritten `cmpBytes`
   resolves full code points. `test/Makefile` now exports the same
   variable so `make test` exercises production's actual semantics
   mode instead of the interpreter default.

Cost of correctness: `explodeChars` calls `split("")` twice per
comparison instead of looping `getCharAt`; measured on the same
10 000 path-like-string benchmark as the sort fix above, `sortBytewise`
went from 0.8 s (fast but wrong for non-ASCII) to 2.4 s (correct) —
still nowhere near the pre-sort-fix insertion sort's 2 min 8 s, and the
slowdown is identical under BYTE and CHAR semantics (confirming it's
`explodeChars`'s two `split()` calls, not CHAR semantics' `getCharAt`
cost, that accounts for it — `split()` itself is semantics-independent
and fast). Switching to the `deleteElement`-based trim (above) cut
that further: `sortBytewise` at 10 000 entries **2.4 s → 1.48 s**
(~40% faster) — mutating the `split()` result in place avoids a second
full-array copy on every comparison.

All 18 test programs / 1100+ checks pass with `FGL_LENGTH_SEMANTICS=CHAR`
forced for the whole suite.

**Follow-up resolved 2026-07-13:** `glob.pathMatch`'s slow path
(`matchFrom`/`matchClass`, hit for patterns containing `[`, `\`, or
`/` — everything else uses the `MATCHES` fast path above) also now
takes pre-decoded `explodeChars` arrays instead of looping `getCharAt`
— the recursive algorithm and index semantics are otherwise unchanged
(each `getCharAt(i)`/`p.getCharAt(pi)` becomes an array index `s[i]`/
`p[pi]`). Correctness re-verified with a 182-case differential test
against Go's real `filepath.Match` (classes, ranges, negation, escapes,
separators, and multi-byte content in both pattern and name) — zero
mismatches. Performance, isolated before/after on a slow-path class
pattern against a 1010-character multi-byte name (2000 calls, forced
CHAR semantics): **9.61 s → 1.11 s (~8.6×)**, then **→ 0.97 s** after
switching `explodeChars` to the `deleteElement`-based trim (a smaller
gain here than for `cmpBytes`, since `pathMatch` decodes once per call
rather than once per comparison). All 18 test programs / 1100+ checks
still pass.
