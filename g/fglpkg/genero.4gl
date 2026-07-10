#+ Genero BDL runtime version detection and compatibility checks
#+ port of internal/genero/genero.go
#+
#+ Detection strategy (tried in order):
#+  1. FGLPKG_GENERO_VERSION env var  — explicit override, useful in CI
#+  2. `fglcomp --version`            — most reliable when fglcomp is on PATH
#+  3. $FGLDIR/etc/fgl.version        — fallback file present in most installs
#+  4. $FGLDIR/bin/fglcomp --version  — fallback when fglcomp not on PATH
OPTIONS SHORT CIRCUIT
PACKAGE fglpkg
IMPORT os
IMPORT util
IMPORT FGL fglpkg.fglpkgutils
IMPORT FGL fglpkg.semver
&include "myassert.inc"

PUBLIC TYPE TGeneroVersion RECORD
  sv semver.TSemver, --normalized for constraint matching
  orig STRING --as originally detected, e.g. "4.01.12"
END RECORD

DEFINE _versionRe util.Regexp

#+the version as originally detected (e.g. "4.01.12")
FUNCTION versionString(v TGeneroVersion) RETURNS STRING
  RETURN v.orig
END FUNCTION

#+the Genero major version as a string (variant key for package builds)
FUNCTION majorString(v TGeneroVersion) RETURNS STRING
  RETURN SFMT("%1", v.sv.major)
END FUNCTION

FUNCTION majorOf(v TGeneroVersion) RETURNS INT
  RETURN v.sv.major
END FUNCTION

#+determines the installed Genero BDL version
FUNCTION detect() RETURNS(BOOLEAN, TGeneroVersion, STRING)
  DEFINE v, empty TGeneroVersion
  DEFINE ok BOOLEAN
  DEFINE err STRING

  --1. explicit override
  VAR envv = fgl_getenv("FGLPKG_GENERO_VERSION")
  IF envv IS NOT NULL AND envv.trim().getLength() > 0 THEN
    CALL parseGenero(envv) RETURNING ok, v, err
    RETURN ok, v, err
  END IF

  --2. fglcomp on PATH
  CALL fromCommand("fglcomp --version") RETURNING ok, v, err
  IF ok THEN
    RETURN TRUE, v, NULL
  END IF

  VAR fgldir = fgl_getenv("FGLDIR")
  IF fgldir IS NOT NULL AND fgldir.getLength() > 0 THEN
    --3. $FGLDIR/etc/fgl.version file
    CALL fromVersionFile(os.Path.join(os.Path.join(fgldir, "etc"), "fgl.version"))
        RETURNING ok, v, err
    IF ok THEN
      RETURN TRUE, v, NULL
    END IF
    --4. $FGLDIR/bin/fglcomp --version
    CALL fromCommand(fglpkgutils.quote(fglcompPath(fgldir)) || " --version")
        RETURNING ok, v, err
    IF ok THEN
      RETURN TRUE, v, NULL
    END IF
  END IF

  RETURN FALSE, empty,
      "cannot detect Genero BDL version: fglcomp not found on PATH and $FGLDIR is not set.\nSet FGLPKG_GENERO_VERSION (e.g. FGLPKG_GENERO_VERSION=4.01.12) to override"
END FUNCTION

FUNCTION mustDetect() RETURNS TGeneroVersion
  DEFINE ok BOOLEAN
  DEFINE v TGeneroVersion
  DEFINE err STRING
  CALL detect() RETURNING ok, v, err
  IF NOT ok THEN
    CALL fglpkgutils.myErr(err)
  END IF
  RETURN v
END FUNCTION

#+parses a Genero version string such as "4.01.12" (leading zeros in
#+MINOR/PATCH are accepted and normalized for semver matching)
FUNCTION parseGenero(s STRING) RETURNS(BOOLEAN, TGeneroVersion, STRING)
  DEFINE v, empty TGeneroVersion
  DEFINE ok BOOLEAN
  DEFINE err STRING
  LET s = s.trim()
  CALL semver.parseVersion(normaliseVersionString(s)) RETURNING ok, v.sv, err
  IF NOT ok THEN
    RETURN FALSE, empty, SFMT('invalid Genero version "%1": %2', s, err)
  END IF
  LET v.orig = s
  RETURN TRUE, v, NULL
END FUNCTION

FUNCTION mustParseGenero(s STRING) RETURNS TGeneroVersion
  DEFINE ok BOOLEAN
  DEFINE v TGeneroVersion
  DEFINE err STRING
  CALL parseGenero(s) RETURNING ok, v, err
  IF NOT ok THEN
    CALL fglpkgutils.myErr(err)
  END IF
  RETURN v
END FUNCTION

#+reports whether this Genero version satisfies the given constraint;
#+an empty constraint is treated as "any version"
FUNCTION satisfiesGenero(v TGeneroVersion, constraint STRING)
    RETURNS(BOOLEAN, STRING)
  DEFINE ok BOOLEAN
  DEFINE c semver.TConstraint
  DEFINE err STRING
  IF constraint IS NULL OR constraint == "*" THEN
    RETURN TRUE, NULL
  END IF
  CALL semver.parseConstraint(constraint) RETURNING ok, c, err
  IF NOT ok THEN
    RETURN FALSE, SFMT('invalid genero constraint "%1": %2', constraint, err)
  END IF
  RETURN semver.satisfies(v.sv, c), NULL
END FUNCTION

#+the path to the fglrun binary: PATH first, then $FGLDIR/bin/fglrun
FUNCTION fglrunPath() RETURNS(STRING, STRING)
  DEFINE out, err STRING
  VAR lookCmd = IIF(fglpkgutils.isWin(), "where fglrun", "command -v fglrun")
  CALL fglpkgutils.getProgramOutputWithErr(lookCmd) RETURNING out, err
  IF err IS NULL AND out.getLength() > 0 THEN
    --"where" may return several lines: take the first
    VAR lines = fglpkgutils.splitOnChar(fglpkgutils.replace(out, "\r", ""), "\n")
    RETURN lines[1].trim(), NULL
  END IF
  VAR fgldir = fgl_getenv("FGLDIR")
  IF fgldir IS NOT NULL AND fgldir.getLength() > 0 THEN
    VAR p = os.Path.join(os.Path.join(fgldir, "bin"),
        SFMT("fglrun%1", IIF(fglpkgutils.isWin(), ".exe", "")))
    IF os.Path.exists(p) THEN
      RETURN p, NULL
    END IF
  END IF
  RETURN NULL,
      "fglrun not found: ensure Genero BDL is installed and either fglrun is on your PATH or $FGLDIR is set"
END FUNCTION

--─── detection helpers ──────────────────────────────────────────────────────

PRIVATE FUNCTION fglcompPath(fgldir STRING) RETURNS STRING
  RETURN os.Path.join(os.Path.join(fgldir, "bin"),
      SFMT("fglcomp%1", IIF(fglpkgutils.isWin(), ".exe", "")))
END FUNCTION

PRIVATE FUNCTION fromCommand(cmd STRING) RETURNS(BOOLEAN, TGeneroVersion, STRING)
  DEFINE v, empty TGeneroVersion
  DEFINE out, err STRING
  DEFINE ok BOOLEAN
  CALL fglpkgutils.getProgramOutputWithErr(cmd) RETURNING out, err
  --some versions exit non-zero for --version; still try to parse output
  IF out IS NULL OR out.getLength() == 0 THEN
    RETURN FALSE, empty, SFMT('command "%1" failed', cmd)
  END IF
  CALL extractVersion(out) RETURNING ok, v, err
  RETURN ok, v, err
END FUNCTION

PRIVATE FUNCTION fromVersionFile(path STRING)
    RETURNS(BOOLEAN, TGeneroVersion, STRING)
  DEFINE v, empty TGeneroVersion
  DEFINE line STRING
  DEFINE ok BOOLEAN
  DEFINE err STRING
  DEFINE ch base.Channel
  IF NOT os.Path.exists(path) THEN
    RETURN FALSE, empty, SFMT("no such file: %1", path)
  END IF
  LET ch = base.Channel.create()
  CALL ch.openFile(path, "r")
  WHILE (line := ch.readLine()) IS NOT NULL
    LET line = line.trim()
    IF line.getLength() == 0 OR fglpkgutils.startsWith(line, "#") THEN
      CONTINUE WHILE
    END IF
    CALL extractVersion(line) RETURNING ok, v, err
    IF ok THEN
      CALL ch.close()
      RETURN TRUE, v, NULL
    END IF
  END WHILE
  CALL ch.close()
  RETURN FALSE, empty, SFMT("no version found in %1", path)
END FUNCTION

#+extracts the first MAJOR.MINOR.PATCH occurrence from command output like
#+"Genero BDL Version 4.01.12" or "fglcomp 6.00.02 rev-..."
FUNCTION extractVersion(s STRING) RETURNS(BOOLEAN, TGeneroVersion, STRING)
  DEFINE v, empty TGeneroVersion
  DEFINE start, end INT
  DEFINE ok BOOLEAN
  DEFINE err STRING
  IF _versionRe IS NULL THEN
    LET _versionRe = util.Regexp.compile(`\b(\d+)\.(\d+)\.(\d+)\b`)
  END IF
  CALL _versionRe.getMatchIndex(s) RETURNING start, end
  IF start <= 0 THEN
    RETURN FALSE, empty, SFMT('no version pattern found in "%1"', s)
  END IF
  CALL parseGenero(s.subString(start, end)) RETURNING ok, v, err
  RETURN ok, v, err
END FUNCTION

#+strips leading zeros from each dot-separated component (4.01.12 -> 4.1.12)
FUNCTION normaliseVersionString(s STRING) RETURNS STRING
  DEFINE i INT
  VAR parts = fglpkgutils.splitOnChar(s, ".")
  FOR i = 1 TO parts.getLength()
    VAR p = parts[i]
    WHILE p.getLength() > 1 AND p.getCharAt(1) == "0"
      LET p = p.subString(2, p.getLength())
    END WHILE
    IF p == "0" OR p.getLength() > 0 THEN
      LET parts[i] = p
    ELSE
      LET parts[i] = "0"
    END IF
  END FOR
  RETURN fglpkgutils.joinArr(parts, ".")
END FUNCTION
