#+ fglpkg outdated — show FGL dependencies with newer versions available
#+ port of internal/cli/outdated.go (exits non-zero when anything is
#+ outdated, for use as a CI gate; Java deps use exact pins, not checked)
OPTIONS SHORT CIRCUIT
PACKAGE fglpkg
IMPORT util
IMPORT FGL fglpkg.fglpkgutils
IMPORT FGL fglpkg.semver
IMPORT FGL fglpkg.manifest
IMPORT FGL fglpkg.lockfile
IMPORT FGL fglpkg.registry
&include "myassert.inc"

PUBLIC TYPE TOutdatedRow RECORD
  name STRING,
  constraint STRING,
  current STRING, --display value: "missing" when not in the lockfile
  wanted STRING, --newest satisfying the constraint
  latest STRING, --newest stable (or newest overall when no stable exists)
  status STRING --ok | update available | major available | not installed |
                --registry error | no published versions
END RECORD

PUBLIC TYPE TOutdatedRows DYNAMIC ARRAY OF TOutdatedRow

#+the pure per-dependency computation (registry errors are handled by the
#+caller); currentVer NULL/empty means the package is not in the lockfile
FUNCTION computeOutdatedRow(
    name STRING, constraint STRING, currentVer STRING,
    versions fglpkgutils.TStringArr)
    RETURNS TOutdatedRow
  DEFINE row TOutdatedRow
  DEFINE i INT
  DEFINE ok, cok BOOLEAN
  DEFINE v, bestStable, bestAny semver.TSemver
  DEFINE stableSet, anySet BOOLEAN
  DEFINE c semver.TConstraint
  DEFINE err STRING

  LET row.name = name
  LET row.constraint = constraint
  LET row.current = IIF(currentVer IS NULL OR currentVer.getLength() == 0,
      "missing", currentVer)

  FOR i = 1 TO versions.getLength()
    CALL semver.parseVersion(versions[i]) RETURNING ok, v, err
    IF NOT ok THEN
      CONTINUE FOR --invalid versions silently dropped (Go parity)
    END IF
    IF NOT anySet OR semver.compare(v, bestAny) > 0 THEN
      LET bestAny = v
      LET anySet = TRUE
    END IF
    IF v.pre IS NULL THEN
      IF NOT stableSet OR semver.compare(v, bestStable) > 0 THEN
        LET bestStable = v
        LET stableSet = TRUE
      END IF
    END IF
  END FOR
  IF NOT anySet THEN
    LET row.status = "no published versions"
    RETURN row
  END IF
  IF stableSet THEN
    LET row.latest = semver.versionToString(bestStable)
  ELSE
    LET row.latest = semver.versionToString(bestAny)
  END IF

  CALL semver.parseConstraint(constraint) RETURNING cok, c, err
  IF cok THEN
    LET row.wanted = semver.latest(versions, c) --NULL when nothing matches
  END IF

  CASE
    WHEN currentVer IS NULL OR currentVer.getLength() == 0
      LET row.status = "not installed"
    WHEN row.wanted IS NOT NULL AND row.wanted != currentVer
      LET row.status = "update available"
    WHEN row.latest IS NOT NULL AND row.latest != currentVer
      LET row.status = "major available"
    OTHERWISE
      LET row.status = "ok"
  END CASE
  RETURN row
END FUNCTION

#+the outdated command; returns the process exit code
FUNCTION cmdOutdated(args fglpkgutils.TStringArr) RETURNS INT
  DEFINE jsonOut, ok BOOLEAN
  DEFINE m manifest.TManifest
  DEFINE lf lockfile.TLockfile
  DEFINE installed DICTIONARY OF STRING
  DEFINE rows TOutdatedRows
  DEFINE vl registry.TVersionList
  DEFINE err STRING
  DEFINE i INT

  FOR i = 1 TO args.getLength()
    IF args[i] == "--json" THEN
      LET jsonOut = TRUE
    ELSE
      CALL fglpkgutils.printStderr(SFMT('unknown argument "%1"', args[i]))
      RETURN 1
    END IF
  END FOR

  IF NOT manifest.manifestExists(".") THEN
    CALL fglpkgutils.printStderr("no fglpkg.json in current directory")
    RETURN 1
  END IF
  CALL manifest.load(".") RETURNING ok, m, err
  IF NOT ok THEN
    CALL fglpkgutils.printStderr(SFMT("failed to load fglpkg.json: %1", err))
    RETURN 1
  END IF
  IF m.dependencies.fgl.getLength() == 0 THEN
    DISPLAY "No FGL dependencies declared."
    RETURN 0
  END IF

  --current versions from the lockfile (missing lockfile: all missing)
  IF lockfile.lockExists(".") THEN
    CALL lockfile.load(".") RETURNING ok, lf, err
    IF ok THEN
      FOR i = 1 TO lf.packages.getLength()
        LET installed[lf.packages[i].name] = lf.packages[i].version
      END FOR
    END IF
  END IF

  VAR names = m.dependencies.fgl.getKeys()
  CALL fglpkgutils.sortStringArray(names)
  FOR i = 1 TO names.getLength()
    VAR name = names[i]
    VAR constraint = m.dependencies.fgl[name]
    VAR cur = ""
    IF installed.contains(name) THEN
      LET cur = installed[name]
    END IF
    CALL registry.fetchVersionList(name) RETURNING ok, vl, err
    IF NOT ok THEN
      VAR ri = rows.getLength() + 1
      LET rows[ri].name = name
      LET rows[ri].constraint = constraint
      LET rows[ri].current =
          IIF(cur.getLength() == 0, "missing", cur)
      LET rows[ri].status = "registry error"
      CONTINUE FOR
    END IF
    LET rows[rows.getLength() + 1] =
        computeOutdatedRow(name, constraint, cur, vl.versions)
  END FOR

  IF jsonOut THEN
    CALL printRowsJSON(rows)
  ELSE
    CALL printOutdatedTable(rows)
  END IF

  VAR outdatedCount = 0
  FOR i = 1 TO rows.getLength()
    IF rows[i].status != "ok" THEN
      LET outdatedCount = outdatedCount + 1
    END IF
  END FOR
  IF outdatedCount > 0 THEN
    CALL fglpkgutils.printStderr(
        SFMT("%1 dependenc%2%3 out of date",
            outdatedCount,
            IIF(outdatedCount == 1, "y", "ie"),
            IIF(outdatedCount > 1, "s", "")))
    RETURN 1
  END IF
  RETURN 0
END FUNCTION

PRIVATE FUNCTION printRowsJSON(rows TOutdatedRows)
  DEFINE i INT
  VAR arr = util.JSONArray.create()
  FOR i = 1 TO rows.getLength()
    VAR obj = util.JSONObject.create()
    CALL obj.put("name", NVL(rows[i].name, ""))
    CALL obj.put("constraint", NVL(rows[i].constraint, ""))
    CALL obj.put("current", NVL(rows[i].current, ""))
    CALL obj.put("wanted", NVL(rows[i].wanted, ""))
    CALL obj.put("latest", NVL(rows[i].latest, ""))
    CALL obj.put("status", NVL(rows[i].status, ""))
    CALL arr.put(arr.getLength() + 1, obj)
  END FOR
  --empty STRINGs are NULL in 4GL, so put() emitted JSON null: the Go
  --binary emits "" — patch the serialized form (all values are strings)
  DISPLAY manifest.prettyJSON(
      fglpkgutils.replace(arr.toString(), ":null", ':""'))
END FUNCTION

#+renders the table with dynamic column widths, two-space gaps,
#+right-trimmed rows and a box-drawing divider (Go parity)
FUNCTION printOutdatedTable(rows TOutdatedRows)
  DEFINE headers, cells, divider fglpkgutils.TStringArr
  DEFINE widths DYNAMIC ARRAY OF INT
  DEFINE i, j INT
  LET headers[1] = "Package"
  LET headers[2] = "Current"
  LET headers[3] = "Wanted"
  LET headers[4] = "Latest"
  LET headers[5] = "Status"
  FOR j = 1 TO 5
    LET widths[j] = headers[j].getLength()
  END FOR
  FOR i = 1 TO rows.getLength()
    CALL rowCells(rows[i], cells)
    FOR j = 1 TO 5
      IF cells[j].getLength() > widths[j] THEN
        LET widths[j] = cells[j].getLength()
      END IF
    END FOR
  END FOR
  DISPLAY tableLine(headers, widths)
  FOR j = 1 TO 5
    LET divider[j] = fglpkgutils.repeatStr(fglpkgutils.C_LINE, widths[j])
  END FOR
  DISPLAY tableLine(divider, widths)
  FOR i = 1 TO rows.getLength()
    CALL rowCells(rows[i], cells)
    DISPLAY tableLine(cells, widths)
  END FOR
END FUNCTION

PRIVATE FUNCTION rowCells(row TOutdatedRow, cells fglpkgutils.TStringArr)
  CALL cells.clear()
  LET cells[1] = NVL(row.name, "")
  LET cells[2] = NVL(row.current, "")
  LET cells[3] = NVL(row.wanted, "")
  LET cells[4] = NVL(row.latest, "")
  LET cells[5] = NVL(row.status, "")
END FUNCTION

PRIVATE FUNCTION tableLine(cells fglpkgutils.TStringArr, widths DYNAMIC ARRAY OF INT)
    RETURNS STRING
  DEFINE j INT
  VAR sb = base.StringBuffer.create()
  --the last column is appended unpadded: same result as Go's
  --pad-then-TrimRight, without a right-trim loop (getCharAt on a byte
  --offset inside a multibyte ─ would misbehave under byte semantics)
  FOR j = 1 TO 4
    IF j > 1 THEN
      CALL sb.append("  ")
    END IF
    CALL sb.append(fglpkgutils.padRight(cells[j], widths[j]))
  END FOR
  CALL sb.append("  ")
  CALL sb.append(NVL(cells[5], ""))
  RETURN sb.toString()
END FUNCTION
