#+ glob matching (port of matchGlob in internal/cli/cli.go, Go filepath.Match)
#+ + the tiny internal/github helpers
OPTIONS SHORT CIRCUIT
PACKAGE fglpkg
IMPORT os
IMPORT FGL fglpkg.fglpkgutils
&include "myassert.inc"

#+matches a path against a glob pattern with support for "**" matching any
#+number of directory levels; patterns are anchored to the project root:
#+"USERGUIDE.md" matches only the root level file, use "**/USERGUIDE.md"
#+to match at any depth (no basename fallback)
FUNCTION matchGlob(pattern STRING, path STRING) RETURNS BOOLEAN
  --normalise separators
  LET pattern = fglpkgutils.backslash2slash(pattern)
  LET path = fglpkgutils.backslash2slash(path)

  IF NOT fglpkgutils.contains(pattern, "**") THEN
    RETURN pathMatch(pattern, path)
  END IF

  --split on the first "**" occurrence
  VAR idx = pattern.getIndexOf("**", 1)
  VAR prefix = trimRightChar(pattern.subString(1, idx - 1), "/")
  VAR suffix = trimLeftChar(pattern.subString(idx + 2, pattern.getLength()), "/")

  --check prefix: the path must start with the prefix directory (if any)
  IF prefix IS NOT NULL THEN
    IF NOT fglpkgutils.startsWith(path, prefix || "/") AND path != prefix THEN
      RETURN FALSE
    END IF
  END IF

  IF suffix IS NULL THEN
    RETURN TRUE
  END IF

  --the remaining path (after prefix) must end with a segment matching suffix
  VAR remaining = path
  IF prefix IS NOT NULL AND fglpkgutils.startsWith(path, prefix || "/") THEN
    LET remaining = path.subString(prefix.getLength() + 2, path.getLength())
  END IF
  RETURN pathMatch(suffix, os.Path.baseName(remaining))
END FUNCTION

#+reports whether a pattern is well formed (mirrors Go filepath.Match
#+returning ErrBadPattern when probed against a sample name)
FUNCTION patternValid(pattern STRING) RETURNS BOOLEAN
  RETURN matchFrom(fglpkgutils.explodeChars(pattern), 1,
      fglpkgutils.explodeChars("test"), 1) != -1
END FUNCTION

#+shell style pattern matching like Go filepath.Match:
#+'*' matches any sequence of non-separator chars, '?' one non-separator
#+char, '[...]' a character class (with '^' negation and ranges),
#+'\' escapes the next character; malformed patterns never match
FUNCTION pathMatch(pattern STRING, name STRING) RETURNS BOOLEAN
  --fast path: for single-segment operands and simple patterns the
  --native MATCHES operator is equivalent (verified exhaustively against
  --matchFrom, 238k pattern/name pairs) and ~140x faster — this is the
  --hot shape: ignore rules and doc patterns against every tree entry.
  --Excluded so the slow path keeps exact filepath.Match semantics:
  --'/' ('*'/'?' must not cross it; MATCHES '*' does), '[' (MATCHES is
  --lenient with malformed classes, Go never matches them), '\' escapes
  --(trailing '\' is malformed in Go), NULL operands (NULL MATCHES is
  --NULL, but Go matches "" against "*")
  IF pattern IS NOT NULL AND name IS NOT NULL
      AND pattern.getIndexOf("/", 1) == 0
      AND pattern.getIndexOf("[", 1) == 0
      AND pattern.getIndexOf("\\", 1) == 0
      AND name.getIndexOf("/", 1) == 0 THEN
    RETURN name MATCHES pattern
  END IF
  RETURN matchFrom(fglpkgutils.explodeChars(pattern), 1,
      fglpkgutils.explodeChars(name), 1) == 1
END FUNCTION

#+returns 1 on match, 0 on mismatch, -1 on malformed pattern
#+p/s are pre-decoded character arrays (fglpkgutils.explodeChars), not
#+raw strings: looping getCharAt corrupts multi-byte UTF-8 content
#+under BYTE semantics (see g/BENCHMARKS.md) — indexing a decoded array
#+is correct under either semantics and avoids the getCharAt call
#+entirely, so the CHAR-semantics O(i)-per-call cost doesn't apply here
PRIVATE FUNCTION matchFrom(
    p fglpkgutils.TStringArr, pi INT, s fglpkgutils.TStringArr, si INT)
    RETURNS INT
  DEFINE c STRING
  DEFINE k, r INT
  VAR plen = p.getLength()
  VAR slen = s.getLength()
  WHILE pi <= plen
    LET c = p[pi]
    CASE c
      WHEN "*"
        --try every split point; '*' cannot consume a '/'
        FOR k = si TO slen + 1
          LET r = matchFrom(p, pi + 1, s, k)
          IF r != 0 THEN
            RETURN r
          END IF
          IF k <= slen AND s[k] == "/" THEN
            RETURN 0
          END IF
        END FOR
        RETURN 0
      WHEN "?"
        IF si > slen OR s[si] == "/" THEN
          RETURN 0
        END IF
        LET pi = pi + 1
        LET si = si + 1
      WHEN "["
        LET r = matchClass(p, pi, s, si)
        IF r <= 0 THEN
          RETURN r
        END IF
        LET pi = r --matchClass returns the index after ']'
        LET si = si + 1
      WHEN "\\"
        LET pi = pi + 1
        IF pi > plen THEN
          RETURN -1
        END IF
        IF si > slen OR s[si] != p[pi] THEN
          RETURN 0
        END IF
        LET pi = pi + 1
        LET si = si + 1
      OTHERWISE
        IF si > slen OR s[si] != c THEN
          RETURN 0
        END IF
        LET pi = pi + 1
        LET si = si + 1
    END CASE
  END WHILE
  RETURN IIF(si > slen, 1, 0)
END FUNCTION

#+matches s[si] against the character class starting at p[pi] (== '[');
#+returns the pattern index after the closing ']' when the char matches,
#+0 when it doesn't, -1 for a malformed class. p/s are pre-decoded
#+character arrays — see matchFrom.
PRIVATE FUNCTION matchClass(
    p fglpkgutils.TStringArr, pi INT, s fglpkgutils.TStringArr, si INT)
    RETURNS INT
  DEFINE lo, hi, ch STRING
  DEFINE negate, matched, any BOOLEAN
  VAR plen = p.getLength()
  IF si > s.getLength() THEN
    RETURN 0
  END IF
  LET ch = s[si]
  LET pi = pi + 1
  IF pi <= plen AND p[pi] == "^" THEN
    LET negate = TRUE
    LET pi = pi + 1
  END IF
  WHILE TRUE
    IF pi > plen THEN
      RETURN -1 --unterminated class
    END IF
    IF p[pi] == "]" AND any THEN
      EXIT WHILE
    END IF
    --lo char (with escape)
    IF p[pi] == "\\" THEN
      LET pi = pi + 1
      IF pi > plen THEN
        RETURN -1
      END IF
    END IF
    LET lo = p[pi]
    LET hi = lo
    LET pi = pi + 1
    IF pi <= plen AND p[pi] == "-" THEN
      LET pi = pi + 1
      IF pi > plen THEN
        RETURN -1
      END IF
      IF p[pi] == "\\" THEN
        LET pi = pi + 1
        IF pi > plen THEN
          RETURN -1
        END IF
      END IF
      LET hi = p[pi]
      LET pi = pi + 1
    END IF
    IF ORD(ch) >= ORD(lo) AND ORD(ch) <= ORD(hi) THEN
      LET matched = TRUE
    END IF
    LET any = TRUE
  END WHILE
  --pi points at ']'
  IF matched != negate THEN
    RETURN pi + 1
  END IF
  RETURN 0
END FUNCTION

#+collects all regular files below rootDir as sorted relative paths with
#+forward slashes
FUNCTION collectFiles(rootDir STRING) RETURNS fglpkgutils.TStringArr
  DEFINE arr fglpkgutils.TStringArr
  CALL collectFilesInt(rootDir, NULL, arr)
  CALL sortBytewise(arr)
  RETURN arr
END FUNCTION

PRIVATE FUNCTION collectFilesInt(
    dir STRING, rel STRING, arr fglpkgutils.TStringArr)
  DEFINE child, childRel STRING
  VAR h = os.Path.dirOpen(dir)
  WHILE h > 0
    LET child = os.Path.dirNext(h)
    IF child IS NULL THEN
      EXIT WHILE
    END IF
    IF child == "." OR child == ".." THEN
      CONTINUE WHILE
    END IF
    LET childRel = IIF(rel IS NULL, child, SFMT("%1/%2", rel, child))
    VAR full = os.Path.join(dir, child)
    IF os.Path.isDirectory(full) THEN
      CALL collectFilesInt(full, childRel, arr)
    ELSE
      LET arr[arr.getLength() + 1] = childRel
    END IF
  END WHILE
  IF h > 0 THEN
    CALL os.Path.dirClose(h)
  END IF
END FUNCTION

#+sorts a string array byte-wise (deterministic, locale independent)
#+native sort driver (since 4.01.03, FGL-5904): the previous insertion
#+sort was O(n^2) interpreted comparisons — 2min+ for a 10k-entry file
#+list (see g/BENCHMARKS.md)
FUNCTION sortBytewise(arr fglpkgutils.TStringArr)
  CALL arr.sortByComparisonFunction(NULL, FALSE, FUNCTION fglpkgutils.cmpBytes)
END FUNCTION

PRIVATE FUNCTION trimRightChar(s STRING, c STRING) RETURNS STRING
  WHILE s.getLength() > 0 AND s.getCharAt(s.getLength()) == c
    LET s = s.subString(1, s.getLength() - 1)
  END WHILE
  RETURN s
END FUNCTION

PRIVATE FUNCTION trimLeftChar(s STRING, c STRING) RETURNS STRING
  WHILE s.getLength() > 0 AND s.getCharAt(1) == c
    LET s = s.subString(2, s.getLength())
  END WHILE
  RETURN s
END FUNCTION

--─── internal/github helpers ────────────────────────────────────────────────

#+asset name for a Genero version variant: name-version-genero<major>.zip
FUNCTION variantAssetName(name STRING, version STRING, generoMajor INT)
    RETURNS STRING
  RETURN SFMT("%1-%2-genero%3.zip", name, version, generoMajor)
END FUNCTION

FUNCTION isGitHubURL(url STRING) RETURNS BOOLEAN
  RETURN fglpkgutils.startsWith(url, "https://api.github.com/")
END FUNCTION
