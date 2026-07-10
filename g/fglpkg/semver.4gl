#+ semantic versioning parsing, comparison and constraint matching
#+ port of internal/semver (semver.go, helpers.go) + version bump logic
#+
#+ Supported constraint operators (see internal/semver/semver.go):
#+   1.2.3  =1.2.3  >1.2.3  >=1.2.3  <1.2.3  <=1.2.3
#+   ~1.2.3 (patch range)  ^1.2.3 (compatible range, npm 0.x special cases)
#+   * / latest (any)  1.2.x  1.x (wildcards)
#+ AND groups are space separated, OR groups are separated with ||
OPTIONS SHORT CIRCUIT
PACKAGE fglpkg
IMPORT util
IMPORT FGL fglpkg.fglpkgutils
&include "myassert.inc"

PUBLIC TYPE TSemver RECORD
  major BIGINT,
  minor BIGINT,
  patch BIGINT,
  pre STRING, --pre-release identifiers, e.g. "alpha.1", "beta", "rc.2"
  build STRING, --build metadata (ignored in comparisons)
  orig STRING --original string as parsed ("" if constructed)
END RECORD

PUBLIC TYPE TPredicate RECORD
  op STRING, --"=", ">", ">=", "<", "<="
  ver TSemver
END RECORD

PUBLIC TYPE TAndGroup RECORD
  preds DYNAMIC ARRAY OF TPredicate
END RECORD

PUBLIC TYPE TConstraint RECORD
  raw STRING,
  groups DYNAMIC ARRAY OF TAndGroup --OR of AND groups
END RECORD

--strict SemVer 2.0.0 validation pattern (see https://semver.org)
PRIVATE CONSTANT STRICT_SEMVER_RE =
    `^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-((?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?(?:\+([0-9a-zA-Z-]+(?:\.[0-9a-zA-Z-]+)*))?$`
DEFINE _strictRe util.Regexp

#+returns the original version string, or a reconstructed one
FUNCTION versionToString(v TSemver) RETURNS STRING
  IF v.orig IS NOT NULL THEN
    RETURN v.orig
  END IF
  VAR s = SFMT("%1.%2.%3", v.major, v.minor, v.patch)
  IF v.pre IS NOT NULL THEN
    LET s = s, "-", v.pre
  END IF
  RETURN s
END FUNCTION

#+parses a version string, leading "v" is stripped
FUNCTION parseVersion(s STRING) RETURNS(BOOLEAN, TSemver, STRING)
  DEFINE v, empty TSemver
  VAR orig = s
  LET s = s.trim()
  IF fglpkgutils.startsWith(s, "v") THEN
    LET s = s.subString(2, s.getLength())
  END IF

  --strip build metadata
  VAR idx = s.getIndexOf("+", 1)
  IF idx > 0 THEN
    LET v.build = s.subString(idx + 1, s.getLength())
    LET s = s.subString(1, idx - 1)
  END IF

  --split prerelease
  LET idx = s.getIndexOf("-", 1)
  IF idx > 0 THEN
    LET v.pre = s.subString(idx + 1, s.getLength())
    LET s = s.subString(1, idx - 1)
  END IF

  VAR parts = fglpkgutils.splitOnChar(s, ".")
  IF parts.getLength() != 3 THEN
    RETURN FALSE, empty,
        SFMT('invalid version "%1": expected MAJOR.MINOR.PATCH', orig)
  END IF

  LET v.major = parseUint(parts[1])
  IF v.major IS NULL THEN
    RETURN FALSE, empty, SFMT('invalid major in "%1"', orig)
  END IF
  LET v.minor = parseUint(parts[2])
  IF v.minor IS NULL THEN
    RETURN FALSE, empty, SFMT('invalid minor in "%1"', orig)
  END IF
  LET v.patch = parseUint(parts[3])
  IF v.patch IS NULL THEN
    RETURN FALSE, empty, SFMT('invalid patch in "%1"', orig)
  END IF
  LET v.orig = orig
  RETURN TRUE, v, NULL
END FUNCTION

FUNCTION mustParseVersion(s STRING) RETURNS TSemver
  DEFINE ok BOOLEAN
  DEFINE v TSemver
  DEFINE err STRING
  CALL parseVersion(s) RETURNING ok, v, err
  IF NOT ok THEN
    CALL fglpkgutils.myErr(err)
  END IF
  RETURN v
END FUNCTION

#+strict SemVer 2.0.0 validation for the "emit" side (init/publish):
#+rejects leading "v", leading zeros and malformed prerelease/build parts
FUNCTION validateVersion(s STRING) RETURNS BOOLEAN
  IF _strictRe IS NULL THEN
    LET _strictRe = util.Regexp.compile(STRICT_SEMVER_RE)
  END IF
  RETURN _strictRe.matches(s)
END FUNCTION

#+returns -1, 0 or 1 if a is less than, equal to or greater than b;
#+build metadata is ignored, pre-releases sort before releases
FUNCTION compare(a TSemver, b TSemver) RETURNS INT
  IF a.major != b.major THEN
    RETURN IIF(a.major < b.major, -1, 1)
  END IF
  IF a.minor != b.minor THEN
    RETURN IIF(a.minor < b.minor, -1, 1)
  END IF
  IF a.patch != b.patch THEN
    RETURN IIF(a.patch < b.patch, -1, 1)
  END IF
  RETURN cmpPreRelease(a.pre, b.pre)
END FUNCTION

#+compares two pre-release strings per semver spec:
#+release (no pre) > any pre-release; identifiers left-to-right;
#+numeric identifiers < alphanumeric; pure numerics compared as ints
PRIVATE FUNCTION cmpPreRelease(a STRING, b STRING) RETURNS INT
  DEFINE i INT
  --NULL == NULL is FALSE in 4GL: handle the both-NULL case explicitly
  IF a IS NULL AND b IS NULL THEN
    RETURN 0
  END IF
  IF a IS NULL THEN
    RETURN 1 --release > pre-release
  END IF
  IF b IS NULL THEN
    RETURN -1
  END IF
  IF a == b THEN
    RETURN 0
  END IF
  VAR aParts = fglpkgutils.splitOnChar(a, ".")
  VAR bParts = fglpkgutils.splitOnChar(b, ".")
  FOR i = 1 TO IIF(aParts.getLength() < bParts.getLength(),
      aParts.getLength(), bParts.getLength())
    VAR aNum = parseUint(aParts[i])
    VAR bNum = parseUint(bParts[i])
    CASE
      WHEN aNum IS NOT NULL AND bNum IS NOT NULL --both numeric
        IF aNum != bNum THEN
          RETURN IIF(aNum < bNum, -1, 1)
        END IF
      WHEN aNum IS NOT NULL --numeric < alphanumeric
        RETURN -1
      WHEN bNum IS NOT NULL
        RETURN 1
      OTHERWISE --both alphanumeric
        VAR c = fglpkgutils.cmpBytes(aParts[i], bParts[i])
        IF c != 0 THEN
          RETURN c
        END IF
    END CASE
  END FOR
  IF aParts.getLength() == bParts.getLength() THEN
    RETURN 0
  END IF
  RETURN IIF(aParts.getLength() < bParts.getLength(), -1, 1)
END FUNCTION

#+parses a constraint expression such as "^1.2.3" or ">=1.0 <2.0"
FUNCTION parseConstraint(s STRING) RETURNS(BOOLEAN, TConstraint, STRING)
  DEFINE c, empty TConstraint
  DEFINE i INT
  DEFINE ok BOOLEAN
  DEFINE err STRING
  DEFINE grp TAndGroup
  LET c.raw = s
  LET s = s.trim()

  IF s IS NULL OR s == "*" OR s.toLowerCase() == "latest" THEN
    --matches any: one empty AND group
    CALL c.groups.appendElement()
    RETURN TRUE, c, NULL
  END IF

  VAR orParts = fglpkgutils.splitOnString(s, "||")
  FOR i = 1 TO orParts.getLength()
    --note: no reuse/clearing of grp here — record assignment copies the
    --preds DYNAMIC ARRAY by reference, clearing would wipe stored groups
    CALL parseAndGroup(orParts[i].trim()) RETURNING ok, grp, err
    IF NOT ok THEN
      RETURN FALSE, empty, SFMT('invalid constraint "%1": %2', c.raw, err)
    END IF
    LET c.groups[c.groups.getLength() + 1] = grp
  END FOR
  RETURN TRUE, c, NULL
END FUNCTION

FUNCTION mustParseConstraint(s STRING) RETURNS TConstraint
  DEFINE ok BOOLEAN
  DEFINE c TConstraint
  DEFINE err STRING
  CALL parseConstraint(s) RETURNING ok, c, err
  IF NOT ok THEN
    CALL fglpkgutils.myErr(err)
  END IF
  RETURN c
END FUNCTION

#+reports whether version v satisfies the constraint
FUNCTION satisfies(v TSemver, c TConstraint) RETURNS BOOLEAN
  DEFINE i INT
  --skip pre-release versions unless the constraint explicitly includes one
  IF v.pre IS NOT NULL AND NOT allowsPreRelease(c) THEN
    RETURN FALSE
  END IF
  FOR i = 1 TO c.groups.getLength()
    IF groupMatches(c.groups[i], v) THEN
      RETURN TRUE
    END IF
  END FOR
  RETURN FALSE
END FUNCTION

PRIVATE FUNCTION groupMatches(g TAndGroup, v TSemver) RETURNS BOOLEAN
  DEFINE i INT
  FOR i = 1 TO g.preds.getLength()
    IF NOT predMatches(g.preds[i], v) THEN
      RETURN FALSE
    END IF
  END FOR
  RETURN TRUE
END FUNCTION

PRIVATE FUNCTION predMatches(p TPredicate, v TSemver) RETURNS BOOLEAN
  VAR c = compare(v, p.ver)
  CASE p.op
    WHEN "="
      RETURN c == 0
    WHEN ">"
      RETURN c > 0
    WHEN ">="
      RETURN c >= 0
    WHEN "<"
      RETURN c < 0
    WHEN "<="
      RETURN c <= 0
  END CASE
  RETURN FALSE
END FUNCTION

PRIVATE FUNCTION allowsPreRelease(c TConstraint) RETURNS BOOLEAN
  DEFINE i, j INT
  FOR i = 1 TO c.groups.getLength()
    FOR j = 1 TO c.groups[i].preds.getLength()
      IF c.groups[i].preds[j].ver.pre IS NOT NULL THEN
        RETURN TRUE
      END IF
    END FOR
  END FOR
  RETURN FALSE
END FUNCTION

#+returns the highest version string from candidates satisfying the constraint
#+(NULL if none does)
FUNCTION latest(candidates fglpkgutils.TStringArr, c TConstraint)
    RETURNS STRING
  DEFINE best TSemver
  DEFINE bestSet BOOLEAN
  DEFINE i INT
  DEFINE ok BOOLEAN
  DEFINE v TSemver
  DEFINE err STRING
  FOR i = 1 TO candidates.getLength()
    CALL parseVersion(candidates[i]) RETURNING ok, v, err
    IF NOT ok THEN
      CALL fglpkgutils.log(SFMT("latest: skipping unparsable version:%1", err))
      CONTINUE FOR
    END IF
    IF satisfies(v, c) THEN
      IF NOT bestSet OR compare(v, best) > 0 THEN
        LET best = v
        LET bestSet = TRUE
      END IF
    END IF
  END FOR
  IF NOT bestSet THEN
    RETURN NULL
  END IF
  RETURN versionToString(best)
END FUNCTION

#+sorts version strings ascending by semver precedence (in place);
#+unparsable entries sort first
FUNCTION sortVersionStrings(arr fglpkgutils.TStringArr)
  DEFINE i, j INT
  DEFINE tmp STRING
  FOR i = 2 TO arr.getLength()
    LET j = i
    WHILE j > 1 AND cmpVersionStrings(arr[j], arr[j - 1]) < 0
      LET tmp = arr[j]
      LET arr[j] = arr[j - 1]
      LET arr[j - 1] = tmp
      LET j = j - 1
    END WHILE
  END FOR
END FUNCTION

PRIVATE FUNCTION cmpVersionStrings(a STRING, b STRING) RETURNS INT
  DEFINE oka, okb BOOLEAN
  DEFINE va, vb TSemver
  DEFINE err STRING
  CALL parseVersion(a) RETURNING oka, va, err
  CALL parseVersion(b) RETURNING okb, vb, err
  CASE
    WHEN oka AND okb
      RETURN compare(va, vb)
    WHEN oka
      RETURN 1
    WHEN okb
      RETURN -1
  END CASE
  RETURN fglpkgutils.cmpBytes(a, b)
END FUNCTION

#+returns the nxt version for a bump kind:
#+patch|minor|major|prerelease or an explicit semver string
FUNCTION bump(cur TSemver, kind STRING) RETURNS(BOOLEAN, TSemver, STRING)
  DEFINE nxt, empty TSemver
  DEFINE ok BOOLEAN
  DEFINE v TSemver
  DEFINE err STRING
  CASE kind
    WHEN "patch"
      LET nxt.major = cur.major
      LET nxt.minor = cur.minor
      LET nxt.patch = cur.patch + 1
    WHEN "minor"
      LET nxt.major = cur.major
      LET nxt.minor = cur.minor + 1
      LET nxt.patch = 0
    WHEN "major"
      LET nxt.major = cur.major + 1
      LET nxt.minor = 0
      LET nxt.patch = 0
    WHEN "prerelease"
      LET nxt = bumpPrerelease(cur)
    OTHERWISE
      CALL parseVersion(kind) RETURNING ok, v, err
      IF NOT ok THEN
        RETURN FALSE, empty,
            SFMT('unknown bump kind "%1": expected patch|minor|major|prerelease or a valid semver',
                kind)
      END IF
      LET nxt.major = v.major
      LET nxt.minor = v.minor
      LET nxt.patch = v.patch
      LET nxt.pre = v.pre
  END CASE
  RETURN TRUE, nxt, NULL
END FUNCTION

#+npm `prerelease` semantics:
#+  1.2.3         -> 1.2.4-0
#+  1.2.4-0       -> 1.2.4-1
#+  1.2.4-alpha.0 -> 1.2.4-alpha.1
#+  1.2.4-alpha   -> 1.2.4-alpha.0
PRIVATE FUNCTION bumpPrerelease(cur TSemver) RETURNS TSemver
  DEFINE nxt TSemver
  LET nxt.major = cur.major
  LET nxt.minor = cur.minor
  LET nxt.patch = cur.patch
  IF cur.pre IS NULL THEN
    LET nxt.patch = cur.patch + 1
    LET nxt.pre = "0"
    RETURN nxt
  END IF
  VAR idx = fglpkgutils.lastIndexOf(cur.pre, ".")
  VAR prefix = ""
  VAR tail = cur.pre
  IF idx > 0 THEN
    LET prefix = cur.pre.subString(1, idx)
    LET tail = cur.pre.subString(idx + 1, cur.pre.getLength())
  END IF
  VAR n = parseUint(tail)
  CASE
    WHEN n IS NOT NULL AND prefix IS NOT NULL
      LET nxt.pre = SFMT("%1%2", prefix, n + 1)
    WHEN n IS NOT NULL --|| would propagate the NULL prefix
      LET nxt.pre = SFMT("%1", n + 1)
    OTHERWISE
      LET nxt.pre = SFMT("%1.0", cur.pre)
  END CASE
  RETURN nxt
END FUNCTION

--─── constraint parsing helpers ─────────────────────────────────────────────

#+parses a space-separated list of constraint tokens into an AND group
PRIVATE FUNCTION parseAndGroup(s STRING) RETURNS(BOOLEAN, TAndGroup, STRING)
  DEFINE grp, empty TAndGroup
  DEFINE i INT
  DEFINE ok BOOLEAN
  DEFINE preds DYNAMIC ARRAY OF TPredicate
  DEFINE err STRING
  VAR tokens = fglpkgutils.splitFields(s)
  FOR i = 1 TO tokens.getLength()
    CALL parseToken(tokens[i]) RETURNING ok, preds, err
    IF NOT ok THEN
      RETURN FALSE, empty, err
    END IF
    VAR j INT
    FOR j = 1 TO preds.getLength()
      LET grp.preds[grp.preds.getLength() + 1] = preds[j]
    END FOR
  END FOR
  --empty group = match-all
  RETURN TRUE, grp, NULL
END FUNCTION

#+converts a single constraint token into one or more predicates
PRIVATE FUNCTION parseToken(tok STRING)
    RETURNS(BOOLEAN, DYNAMIC ARRAY OF TPredicate, STRING)
  DEFINE preds, empty DYNAMIC ARRAY OF TPredicate
  DEFINE ok BOOLEAN
  DEFINE v TSemver
  DEFINE err STRING
  DEFINE ops fglpkgutils.TStringArr
  DEFINE i INT
  LET tok = tok.trim()

  --wildcard
  IF tok == "*" OR tok.toLowerCase() == "latest" THEN
    RETURN TRUE, empty, NULL
  END IF

  --tilde ~ operator
  IF fglpkgutils.startsWith(tok, "~") THEN
    CALL parseTilde(tok.subString(2, tok.getLength()))
        RETURNING ok, preds, err
    RETURN ok, preds, err
  END IF

  --caret ^ operator
  IF fglpkgutils.startsWith(tok, "^") THEN
    CALL parseCaret(tok.subString(2, tok.getLength()))
        RETURNING ok, preds, err
    RETURN ok, preds, err
  END IF

  --comparison operators (order matters: 2 char ops first)
  LET ops[1] = ">="
  LET ops[2] = "<="
  LET ops[3] = ">"
  LET ops[4] = "<"
  LET ops[5] = "="
  FOR i = 1 TO ops.getLength()
    IF fglpkgutils.startsWith(tok, ops[i]) THEN
      VAR vstr = tok.subString(ops[i].getLength() + 1, tok.getLength())
      CALL parsePartial(vstr) RETURNING ok, v, err
      IF NOT ok THEN
        RETURN FALSE, empty, SFMT('invalid version in "%1": %2', tok, err)
      END IF
      LET preds[1].op = ops[i]
      LET preds[1].ver = v
      RETURN TRUE, preds, NULL
    END IF
  END FOR

  --x-range: 1.2.x, 1.x, x
  IF fglpkgutils.contains(tok, "x")
      OR fglpkgutils.contains(tok, "X")
      OR fglpkgutils.contains(tok, "*") THEN
    CALL parseXRange(tok) RETURNING ok, preds, err
    RETURN ok, preds, err
  END IF

  --bare version -> exact match (partial like "1.2" -> "1.2.0" allowed)
  CALL parsePartial(tok) RETURNING ok, v, err
  IF NOT ok THEN
    RETURN FALSE, empty, SFMT('invalid version "%1": %2', tok, err)
  END IF
  LET preds[1].op = "="
  LET preds[1].ver = v
  RETURN TRUE, preds, NULL
END FUNCTION

#+handles ~MAJOR.MINOR.PATCH, ~MAJOR.MINOR, ~MAJOR;
#+a pre-release suffix (~1.2.3-beta) is attached to the lower bound
PRIVATE FUNCTION parseTilde(s STRING)
    RETURNS(BOOLEAN, DYNAMIC ARRAY OF TPredicate, STRING)
  DEFINE empty DYNAMIC ARRAY OF TPredicate
  DEFINE base, pre STRING
  DEFINE maj, min, patch BIGINT
  CALL splitPreRelease(s) RETURNING base, pre
  VAR parts = fglpkgutils.splitOnChar(base, ".")
  CASE parts.getLength()
    WHEN 1 --~1 -> >=1.0.0 <2.0.0
      LET maj = parseUint(parts[1])
      IF maj IS NULL THEN
        RETURN FALSE, empty, invalidUintErr(parts[1])
      END IF
      RETURN TRUE, rangePreds(maj, 0, 0, pre, maj + 1, 0, 0), NULL
    WHEN 2 --~1.2 -> >=1.2.0 <1.3.0
      LET maj = parseUint(parts[1])
      LET min = parseUint(parts[2])
      IF maj IS NULL OR min IS NULL THEN
        RETURN FALSE, empty, invalidUintErr(base)
      END IF
      RETURN TRUE, rangePreds(maj, min, 0, pre, maj, min + 1, 0), NULL
    WHEN 3 --~1.2.3 -> >=1.2.3 <1.3.0
      LET maj = parseUint(parts[1])
      LET min = parseUint(parts[2])
      LET patch = parseUint(parts[3])
      IF maj IS NULL OR min IS NULL OR patch IS NULL THEN
        RETURN FALSE, empty, invalidUintErr(base)
      END IF
      RETURN TRUE, rangePreds(maj, min, patch, pre, maj, min + 1, 0), NULL
  END CASE
  RETURN FALSE, empty, SFMT("invalid tilde range: ~%1", s)
END FUNCTION

#+handles ^MAJOR.MINOR.PATCH with npm-compatible semantics;
#+a pre-release suffix (^1.0.0-alpha) is attached to the lower bound
PRIVATE FUNCTION parseCaret(s STRING)
    RETURNS(BOOLEAN, DYNAMIC ARRAY OF TPredicate, STRING)
  DEFINE empty DYNAMIC ARRAY OF TPredicate
  DEFINE base, pre STRING
  DEFINE maj, min, patch BIGINT
  CALL splitPreRelease(s) RETURNING base, pre
  VAR parts = fglpkgutils.splitOnChar(base, ".")
  IF parts.getLength() != 3 THEN
    RETURN FALSE, empty,
        SFMT("invalid caret range: ^%1 (expected MAJOR.MINOR.PATCH)", s)
  END IF
  LET maj = parseUint(parts[1])
  LET min = parseUint(parts[2])
  LET patch = parseUint(parts[3])
  IF maj IS NULL OR min IS NULL OR patch IS NULL THEN
    RETURN FALSE, empty, invalidUintErr(base)
  END IF
  CASE
    WHEN maj > 0 --^1.2.3 -> >=1.2.3 <2.0.0
      RETURN TRUE, rangePreds(maj, min, patch, pre, maj + 1, 0, 0), NULL
    WHEN min > 0 --^0.2.3 -> >=0.2.3 <0.3.0
      RETURN TRUE, rangePreds(0, min, patch, pre, 0, min + 1, 0), NULL
    OTHERWISE --^0.0.3 -> >=0.0.3 <0.0.4
      RETURN TRUE, rangePreds(0, 0, patch, pre, 0, 0, patch + 1), NULL
  END CASE
END FUNCTION

#+peels off a -prerelease (and any +build) suffix, returning the bare
#+MAJOR.MINOR.PATCH base and the pre-release identifier (without leading "-")
PRIVATE FUNCTION splitPreRelease(s STRING) RETURNS(STRING, STRING)
  VAR idx = s.getIndexOf("+", 1)
  IF idx > 0 THEN
    LET s = s.subString(1, idx - 1)
  END IF
  LET idx = s.getIndexOf("-", 1)
  IF idx > 0 THEN
    RETURN s.subString(1, idx - 1), s.subString(idx + 1, s.getLength())
  END IF
  RETURN s, NULL
END FUNCTION

#+handles 1.2.x, 1.x.x, 1.x, x
PRIVATE FUNCTION parseXRange(s STRING)
    RETURNS(BOOLEAN, DYNAMIC ARRAY OF TPredicate, STRING)
  DEFINE empty DYNAMIC ARRAY OF TPredicate
  DEFINE maj, min BIGINT
  VAR parts = fglpkgutils.splitOnChar(s, ".")
  CASE
    WHEN parts.getLength() == 1 OR isWildPart(parts[1])
      --x or * -> match all
      RETURN TRUE, empty, NULL
    WHEN parts.getLength() == 2
        OR (parts.getLength() == 3 AND isWildPart(parts[2]))
      --1.x or 1.x.x -> >=1.0.0 <2.0.0
      LET maj = parseUint(parts[1])
      IF maj IS NULL THEN
        RETURN FALSE, empty, invalidUintErr(parts[1])
      END IF
      RETURN TRUE, rangePreds(maj, 0, 0, NULL, maj + 1, 0, 0), NULL
    WHEN parts.getLength() == 3 AND isWildPart(parts[3])
      --1.2.x -> >=1.2.0 <1.3.0
      LET maj = parseUint(parts[1])
      LET min = parseUint(parts[2])
      IF maj IS NULL OR min IS NULL THEN
        RETURN FALSE, empty, invalidUintErr(s)
      END IF
      RETURN TRUE, rangePreds(maj, min, 0, NULL, maj, min + 1, 0), NULL
  END CASE
  RETURN FALSE, empty, SFMT("invalid x-range: %1", s)
END FUNCTION

PRIVATE FUNCTION isWildPart(p STRING) RETURNS BOOLEAN
  RETURN p == "x" OR p == "X" OR p == "*"
END FUNCTION

#+creates a [>=lo, <hi] predicate pair with an optional pre-release
#+identifier on the lower bound
PRIVATE FUNCTION rangePreds(
    loMaj BIGINT, loMin BIGINT, loPatch BIGINT, loPre STRING,
    hiMaj BIGINT, hiMin BIGINT, hiPatch BIGINT)
    RETURNS DYNAMIC ARRAY OF TPredicate
  DEFINE preds DYNAMIC ARRAY OF TPredicate
  LET preds[1].op = ">="
  LET preds[1].ver.major = loMaj
  LET preds[1].ver.minor = loMin
  LET preds[1].ver.patch = loPatch
  LET preds[1].ver.pre = loPre
  LET preds[2].op = "<"
  LET preds[2].ver.major = hiMaj
  LET preds[2].ver.minor = hiMin
  LET preds[2].ver.patch = hiPatch
  RETURN preds
END FUNCTION

#+parses a version that may omit minor/patch (fills with 0)
PRIVATE FUNCTION parsePartial(s STRING) RETURNS(BOOLEAN, TSemver, STRING)
  DEFINE v, empty TSemver
  LET s = s.trim()
  IF fglpkgutils.startsWith(s, "v") THEN
    LET s = s.subString(2, s.getLength())
  END IF

  --strip prerelease first, then build (mirrors Go parsePartial)
  VAR idx = s.getIndexOf("-", 1)
  IF idx > 0 THEN
    LET v.pre = s.subString(idx + 1, s.getLength())
    LET s = s.subString(1, idx - 1)
  END IF
  LET idx = s.getIndexOf("+", 1)
  IF idx > 0 THEN
    LET s = s.subString(1, idx - 1)
  END IF

  VAR parts = fglpkgutils.splitOnChar(s, ".")
  WHILE parts.getLength() < 3
    LET parts[parts.getLength() + 1] = "0"
  END WHILE

  LET v.major = parseUint(parts[1])
  LET v.minor = parseUint(parts[2])
  LET v.patch = parseUint(parts[3])
  IF v.major IS NULL OR v.minor IS NULL OR v.patch IS NULL THEN
    RETURN FALSE, empty, invalidUintErr(s)
  END IF
  RETURN TRUE, v, NULL
END FUNCTION

#+parses a non-negative integer, returns NULL when s isn't one
PRIVATE FUNCTION parseUint(s STRING) RETURNS BIGINT
  DEFINE i INT
  DEFINE n BIGINT
  LET s = s.trim()
  IF s.getLength() == 0 THEN
    RETURN NULL
  END IF
  FOR i = 1 TO s.getLength()
    IF NOT fglpkgutils.isDigit(s.getCharAt(i)) THEN
      RETURN NULL
    END IF
  END FOR
  TRY
    LET n = s
  CATCH
    RETURN NULL
  END TRY
  RETURN n
END FUNCTION

PRIVATE FUNCTION invalidUintErr(s STRING) RETURNS STRING
  RETURN SFMT('"%1" is not a valid non-negative integer', s)
END FUNCTION
