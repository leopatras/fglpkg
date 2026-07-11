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

**How to apply:** consult the genero-intelligence MCP skills for any API
you're not 100% sure about; when a compile fails with -6609 look for a
mid-function DEFINE or a method call on a function result first.
