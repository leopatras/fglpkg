#+ tests for outdated.4gl (status matrix + table format)
OPTIONS SHORT CIRCUIT
IMPORT FGL testutils
IMPORT FGL fglpkg.fglpkgutils
IMPORT FGL fglpkg.outdated
&include "testassert.inc"

MAIN
  CALL testStatusMatrix()
  CALL testLatestSelection()
  CALL testWantedSelection()
  TSUMMARY()
END MAIN

FUNCTION vs3() RETURNS fglpkgutils.TStringArr
  DEFINE v fglpkgutils.TStringArr
  LET v[1] = "1.0.0"
  LET v[2] = "1.2.0"
  LET v[3] = "2.0.0"
  RETURN v
END FUNCTION

FUNCTION testStatusMatrix()
  DEFINE row outdated.TOutdatedRow
  DEFINE junk fglpkgutils.TStringArr

  --up to date within constraint but a major is available
  LET row = outdated.computeOutdatedRow("p", "^1.0.0", "1.2.0", vs3())
  TEQ(row.status, "major available")
  TEQ(row.wanted, "1.2.0")
  TEQ(row.latest, "2.0.0")
  TEQ(row.current, "1.2.0")

  --behind within the constraint
  LET row = outdated.computeOutdatedRow("p", "^1.0.0", "1.0.0", vs3())
  TEQ(row.status, "update available")
  TEQ(row.wanted, "1.2.0")

  --fully up to date
  LET row = outdated.computeOutdatedRow("p", "^2.0.0", "2.0.0", vs3())
  TEQ(row.status, "ok")
  TEQ(row.wanted, "2.0.0")
  TEQ(row.latest, "2.0.0")

  --not installed
  LET row = outdated.computeOutdatedRow("p", "^1.0.0", "", vs3())
  TEQ(row.status, "not installed")
  TEQ(row.current, "missing")
  LET row = outdated.computeOutdatedRow("p", "^1.0.0", NULL, vs3())
  TEQ(row.status, "not installed")
  TEQ(row.current, "missing")

  --no published versions (nothing parses)
  LET junk[1] = "not-a-version"
  LET row = outdated.computeOutdatedRow("p", "^1.0.0", "1.0.0", junk)
  TEQ(row.status, "no published versions")
  TOK(row.latest IS NULL)

  --constraint that matches nothing: wanted empty, falls through to
  --major-available because latest differs
  LET row = outdated.computeOutdatedRow("p", "^9.0.0", "1.0.0", vs3())
  TOK(row.wanted IS NULL)
  TEQ(row.status, "major available")
END FUNCTION

FUNCTION testLatestSelection()
  DEFINE row outdated.TOutdatedRow
  DEFINE v fglpkgutils.TStringArr
  --latest prefers the newest stable over a newer prerelease
  LET v[1] = "1.0.0"
  LET v[2] = "2.0.0-alpha"
  LET row = outdated.computeOutdatedRow("p", "*", "1.0.0", v)
  TEQ(row.latest, "1.0.0")
  TEQ(row.status, "ok")
  --only prereleases: newest prerelease wins
  CALL v.clear()
  LET v[1] = "2.0.0-alpha"
  LET v[2] = "2.0.0-beta"
  LET row = outdated.computeOutdatedRow("p", "*", "", v)
  TEQ(row.latest, "2.0.0-beta")
  TEQ(row.status, "not installed")
END FUNCTION

FUNCTION testWantedSelection()
  DEFINE row outdated.TOutdatedRow
  DEFINE v fglpkgutils.TStringArr
  --invalid constraint: wanted stays empty, status falls back to latest rule
  LET row = outdated.computeOutdatedRow("p", ">>bad", "2.0.0", vs3())
  TOK(row.wanted IS NULL)
  TEQ(row.status, "ok")
  --prerelease excluded from wanted unless the constraint names one
  LET v[1] = "1.0.0"
  LET v[2] = "1.1.0-rc.1"
  LET row = outdated.computeOutdatedRow("p", "^1.0.0", "1.0.0", v)
  TEQ(row.wanted, "1.0.0")
  TEQ(row.status, "ok")
END FUNCTION
