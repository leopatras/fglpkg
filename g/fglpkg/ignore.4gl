#+ .fglpkgignore handling (port of internal/cli/ignore.go)
#+ gitignore subset: rules in file order, last match wins, '!' negates
#+ (re-includes), trailing '/' marks directory-only rules, leading '/'
#+ anchors to the project root
OPTIONS SHORT CIRCUIT
PACKAGE fglpkg
IMPORT os
IMPORT FGL fglpkg.fglpkgutils
IMPORT FGL fglpkg.glob
&include "myassert.inc"

PUBLIC CONSTANT IGNORE_FILENAME = ".fglpkgignore"

PUBLIC TYPE TIgnoreRule RECORD
  pattern STRING,
  negate BOOLEAN,
  dirOnly BOOLEAN
END RECORD

PUBLIC TYPE TIgnoreRules DYNAMIC ARRAY OF TIgnoreRule

#+reads .fglpkgignore from root; a missing file is not an error and
#+returns an empty rule set
FUNCTION loadIgnore(root STRING) RETURNS TIgnoreRules
  DEFINE rules TIgnoreRules
  DEFINE line STRING
  DEFINE ch base.Channel
  VAR path = os.Path.join(root, IGNORE_FILENAME)
  IF NOT os.Path.exists(path) THEN
    RETURN rules
  END IF
  LET ch = base.Channel.create()
  CALL ch.openFile(path, "r")
  WHILE (line := ch.readLine()) IS NOT NULL
    LET line = line.trim()
    --an empty STRING is not necessarily NULL: test the length
    IF line.getLength() == 0 OR fglpkgutils.startsWith(line, "#") THEN
      CONTINUE WHILE
    END IF
    VAR rule TIgnoreRule
    LET rule.pattern = line
    LET rule.negate = FALSE
    LET rule.dirOnly = FALSE
    IF fglpkgutils.startsWith(rule.pattern, "!") THEN
      LET rule.negate = TRUE
      LET rule.pattern = rule.pattern.subString(2, rule.pattern.getLength())
    END IF
    IF fglpkgutils.endsWith(rule.pattern, "/") THEN
      LET rule.dirOnly = TRUE
      WHILE rule.pattern.getLength() > 0
          AND rule.pattern.getCharAt(rule.pattern.getLength()) == "/"
        LET rule.pattern = rule.pattern.subString(1, rule.pattern.getLength() - 1)
      END WHILE
    END IF
    IF rule.pattern.getLength() == 0 THEN
      CONTINUE WHILE
    END IF
    LET rules[rules.getLength() + 1] = rule
  END WHILE
  CALL ch.close()
  RETURN rules
END FUNCTION

#+reports whether a relative path should be omitted from the zip;
#+relPath is normalised to forward slashes before matching, rules are
#+evaluated in file order and the last matching rule decides
FUNCTION shouldExclude(rules TIgnoreRules, relPath STRING, isDir BOOLEAN)
    RETURNS BOOLEAN
  DEFINE i INT
  DEFINE excluded BOOLEAN
  IF rules.getLength() == 0 THEN
    RETURN FALSE
  END IF
  VAR rel = fglpkgutils.backslash2slash(relPath)
  LET excluded = FALSE
  FOR i = 1 TO rules.getLength()
    IF rules[i].dirOnly AND NOT isDir THEN
      CONTINUE FOR
    END IF
    IF NOT ignoreMatch(rules[i].pattern, rel) THEN
      CONTINUE FOR
    END IF
    LET excluded = NOT rules[i].negate
  END FOR
  RETURN excluded
END FUNCTION

#+gitignore matching subset: a leading "/" anchors the pattern to the
#+project root; otherwise the pattern is tried against the full relative
#+path and (for patterns without "/") against every path segment
FUNCTION ignoreMatch(pattern STRING, rel STRING) RETURNS BOOLEAN
  DEFINE i INT
  LET pattern = fglpkgutils.backslash2slash(pattern)

  IF fglpkgutils.startsWith(pattern, "/") THEN
    --anchored to root: match the full rel path only, no basename fallback
    VAR anchored = pattern.subString(2, pattern.getLength())
    RETURN glob.pathMatch(anchored, rel)
  END IF

  IF glob.matchGlob(pattern, rel) THEN
    RETURN TRUE
  END IF
  IF NOT fglpkgutils.contains(pattern, "/") THEN
    --unanchored simple pattern: try every path segment so that "build"
    --matches both "build" and "nested/build/x.txt"
    VAR segs = fglpkgutils.splitOnChar(rel, "/")
    FOR i = 1 TO segs.getLength()
      IF glob.pathMatch(pattern, segs[i]) THEN
        RETURN TRUE
      END IF
    END FOR
  END IF
  RETURN FALSE
END FUNCTION
