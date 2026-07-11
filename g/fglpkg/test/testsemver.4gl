#+ port of internal/semver/semver_test.go + bump tests
OPTIONS SHORT CIRCUIT
IMPORT FGL testutils
IMPORT FGL fglpkg.fglpkgutils
IMPORT FGL fglpkg.semver
&include "testassert.inc"

MAIN
  CALL testParse()
  CALL testParseErrors()
  CALL testCompare()
  CALL testConstraintMatches()
  CALL testLatest()
  CALL testValidateVersion()
  CALL testBump()
  CALL testSort()
  TSUMMARY()
END MAIN

FUNCTION parseChecked(input STRING) RETURNS semver.TSemver
  DEFINE ok BOOLEAN
  DEFINE v semver.TSemver
  DEFINE err STRING
  CALL semver.parseVersion(input) RETURNING ok, v, err
  TOK(ok)
  RETURN v
END FUNCTION

FUNCTION checkParse(
    input STRING, major BIGINT, minor BIGINT, patch BIGINT, pre STRING)
  VAR v = parseChecked(input)
  TEQ(v.major, major)
  TEQ(v.minor, minor)
  TEQ(v.patch, patch)
  TEQ(NVL(v.pre, ""), NVL(pre, ""))
END FUNCTION

FUNCTION testParse()
  CALL checkParse("1.2.3", 1, 2, 3, "")
  CALL checkParse("0.0.1", 0, 0, 1, "")
  CALL checkParse("v2.10.0", 2, 10, 0, "")
  CALL checkParse("1.2.3-alpha.1", 1, 2, 3, "alpha.1")
  CALL checkParse("1.2.3-beta+build.42", 1, 2, 3, "beta")
  CALL checkParse("10.20.30", 10, 20, 30, "")
  --original string preserved
  VAR v = parseChecked("v2.10.0")
  TEQ(semver.versionToString(v), "v2.10.0")
END FUNCTION

FUNCTION checkParseError(input STRING)
  DEFINE ok BOOLEAN
  DEFINE v semver.TSemver
  DEFINE err STRING
  CALL semver.parseVersion(input) RETURNING ok, v, err
  TOK(NOT ok)
  TOK(err IS NOT NULL)
END FUNCTION

FUNCTION testParseErrors()
  CALL checkParseError("1.2")
  CALL checkParseError("1")
  CALL checkParseError("abc")
  CALL checkParseError("1.2.x")
  CALL checkParseError("")
  CALL checkParseError("1.2.3.4")
END FUNCTION

FUNCTION checkCompare(a STRING, b STRING, want INT)
  VAR va = parseChecked(a)
  VAR vb = parseChecked(b)
  VAR got = semver.compare(va, vb)
  IF got < 0 THEN
    LET got = -1
  END IF
  IF got > 0 THEN
    LET got = 1
  END IF
  TEQ(got, want)
END FUNCTION

FUNCTION testCompare()
  CALL checkCompare("1.0.0", "1.0.0", 0)
  CALL checkCompare("1.0.0", "2.0.0", -1)
  CALL checkCompare("2.0.0", "1.0.0", 1)
  CALL checkCompare("1.2.3", "1.2.4", -1)
  CALL checkCompare("1.3.0", "1.2.9", 1)
  CALL checkCompare("1.0.0-alpha", "1.0.0", -1) --pre-release < release
  CALL checkCompare("1.0.0-alpha", "1.0.0-beta", -1) --alpha < beta
  CALL checkCompare("1.0.0-1", "1.0.0-2", -1) --numeric pre-release
  CALL checkCompare("1.0.0-rc.1", "1.0.0-rc.2", -1)
  CALL checkCompare("1.0.0-alpha.1", "1.0.0-alpha.beta", -1) --numeric < alpha
  CALL checkCompare("1.0.0-beta.11", "1.0.0-beta.2", 1) --11 > 2 numerically
  CALL checkCompare("1.0.0-alpha", "1.0.0-alpha.1", -1) --fewer ids < more ids
  CALL checkCompare("1.0.0+build.1", "1.0.0+build.2", 0) --build ignored
END FUNCTION

FUNCTION checkMatch(constraint STRING, version STRING, want BOOLEAN)
  DEFINE ok BOOLEAN
  DEFINE c semver.TConstraint
  DEFINE err STRING
  CALL semver.parseConstraint(constraint) RETURNING ok, c, err
  TOK(ok)
  VAR v = parseChecked(version)
  VAR got = semver.satisfies(v, c)
  VAR gotDesc = SFMT("'%1' satisfies '%2' -> %3", version, constraint, got)
  VAR wantDesc = SFMT("'%1' satisfies '%2' -> %3", version, constraint, want)
  TEQ(gotDesc, wantDesc)
END FUNCTION

FUNCTION testConstraintMatches()
  --exact
  CALL checkMatch("1.2.3", "1.2.3", TRUE)
  CALL checkMatch("1.2.3", "1.2.4", FALSE)
  CALL checkMatch("=1.2.3", "1.2.3", TRUE)
  CALL checkMatch("=1.2.3", "1.2.4", FALSE)
  --comparison operators
  CALL checkMatch(">1.0.0", "1.0.1", TRUE)
  CALL checkMatch(">1.0.0", "1.0.0", FALSE)
  CALL checkMatch(">=1.0.0", "1.0.0", TRUE)
  CALL checkMatch(">=1.0.0", "0.9.9", FALSE)
  CALL checkMatch("<2.0.0", "1.9.9", TRUE)
  CALL checkMatch("<2.0.0", "2.0.0", FALSE)
  CALL checkMatch("<=2.0.0", "2.0.0", TRUE)
  CALL checkMatch("<=2.0.0", "2.0.1", FALSE)
  --tilde
  CALL checkMatch("~1.2.3", "1.2.3", TRUE)
  CALL checkMatch("~1.2.3", "1.2.9", TRUE)
  CALL checkMatch("~1.2.3", "1.3.0", FALSE)
  CALL checkMatch("~1.2.3", "1.2.2", FALSE)
  CALL checkMatch("~1.2", "1.2.0", TRUE)
  CALL checkMatch("~1.2", "1.2.99", TRUE)
  CALL checkMatch("~1.2", "1.3.0", FALSE)
  CALL checkMatch("~1", "1.0.0", TRUE)
  CALL checkMatch("~1", "1.99.99", TRUE)
  CALL checkMatch("~1", "2.0.0", FALSE)
  --caret
  CALL checkMatch("^1.2.3", "1.2.3", TRUE)
  CALL checkMatch("^1.2.3", "1.99.99", TRUE)
  CALL checkMatch("^1.2.3", "2.0.0", FALSE)
  CALL checkMatch("^1.2.3", "1.2.2", FALSE)
  CALL checkMatch("^0.2.3", "0.2.3", TRUE)
  CALL checkMatch("^0.2.3", "0.2.99", TRUE)
  CALL checkMatch("^0.2.3", "0.3.0", FALSE)
  CALL checkMatch("^0.0.3", "0.0.3", TRUE)
  CALL checkMatch("^0.0.3", "0.0.4", FALSE)
  --wildcards
  CALL checkMatch("*", "1.2.3", TRUE)
  CALL checkMatch("*", "99.0.0", TRUE)
  CALL checkMatch("latest", "1.2.3", TRUE)
  CALL checkMatch("1.2.x", "1.2.0", TRUE)
  CALL checkMatch("1.2.x", "1.2.99", TRUE)
  CALL checkMatch("1.2.x", "1.3.0", FALSE)
  CALL checkMatch("1.x", "1.0.0", TRUE)
  CALL checkMatch("1.x", "1.99.0", TRUE)
  CALL checkMatch("1.x", "2.0.0", FALSE)
  --AND (space-separated)
  CALL checkMatch(">=1.0.0 <2.0.0", "1.5.0", TRUE)
  CALL checkMatch(">=1.0.0 <2.0.0", "2.0.0", FALSE)
  CALL checkMatch(">=1.0.0 <2.0.0", "0.9.0", FALSE)
  --OR (pipe-separated)
  CALL checkMatch("^1.2.0 || ^2.0.0", "1.5.0", TRUE)
  CALL checkMatch("^1.2.0 || ^2.0.0", "2.0.0", TRUE)
  CALL checkMatch("^1.2.0 || ^2.0.0", "3.0.0", FALSE)
  --pre-release filtering
  CALL checkMatch("^1.0.0", "1.0.0-alpha", FALSE)
  CALL checkMatch("^1.0.0-alpha", "1.0.0-alpha", TRUE)
  CALL checkMatch("^1.0.0-alpha", "1.0.0-beta", TRUE) --still in range
  CALL checkMatch(">=1.0.0-alpha", "1.0.0-alpha", TRUE)
END FUNCTION

FUNCTION checkLatest(constraint STRING, want STRING)
  DEFINE cands fglpkgutils.TStringArr
  LET cands[1] = "1.0.0"
  LET cands[2] = "1.1.0"
  LET cands[3] = "1.2.0"
  LET cands[4] = "1.2.3"
  LET cands[5] = "2.0.0"
  LET cands[6] = "2.1.0"
  LET cands[7] = "3.0.0-alpha"
  VAR c = semver.mustParseConstraint(constraint)
  TEQ(semver.latest(cands, c), want)
END FUNCTION

FUNCTION testLatest()
  CALL checkLatest("^1.0.0", "1.2.3")
  CALL checkLatest("^2.0.0", "2.1.0")
  CALL checkLatest("~1.2.0", "1.2.3")
  CALL checkLatest(">=1.0.0 <2.0.0", "1.2.3")
  CALL checkLatest("*", "2.1.0") --pre-release excluded
  CALL checkLatest("^3.0.0-alpha", "3.0.0-alpha")
  CALL checkLatest("^4.0.0", NULL) --no match
END FUNCTION

FUNCTION testValidateVersion()
  TOK(semver.validateVersion("1.2.3"))
  TOK(semver.validateVersion("1.2.3-alpha.1"))
  TOK(semver.validateVersion("1.2.3-alpha.1+build.5"))
  TOK(semver.validateVersion("0.1.0"))
  TOK(NOT semver.validateVersion("v1.2.3")) --leading v rejected
  TOK(NOT semver.validateVersion("1.02.3")) --leading zero rejected
  TOK(NOT semver.validateVersion("1.2")) --partial rejected
  TOK(NOT semver.validateVersion("1.2.3-01")) --leading zero pre id
  TOK(NOT semver.validateVersion(" 1.2.3")) --whitespace rejected
END FUNCTION

FUNCTION checkBump(cur STRING, kind STRING, want STRING)
  DEFINE ok BOOLEAN
  DEFINE nxt semver.TSemver
  DEFINE err STRING
  VAR v = parseChecked(cur)
  CALL semver.bump(v, kind) RETURNING ok, nxt, err
  TOK(ok)
  TEQ(semver.versionToString(nxt), want)
END FUNCTION

FUNCTION testBump()
  DEFINE ok BOOLEAN
  DEFINE nxt semver.TSemver
  DEFINE err STRING
  CALL checkBump("1.2.3", "patch", "1.2.4")
  CALL checkBump("1.2.3", "minor", "1.3.0")
  CALL checkBump("1.2.3", "major", "2.0.0")
  CALL checkBump("1.2.3", "prerelease", "1.2.4-0")
  CALL checkBump("1.2.4-0", "prerelease", "1.2.4-1")
  CALL checkBump("1.2.4-alpha.0", "prerelease", "1.2.4-alpha.1")
  CALL checkBump("1.2.4-alpha", "prerelease", "1.2.4-alpha.0")
  CALL checkBump("1.2.3", "2.0.0-rc.1", "2.0.0-rc.1")
  --invalid bump kind
  VAR v = parseChecked("1.2.3")
  CALL semver.bump(v, "bogus") RETURNING ok, nxt, err
  TOK(NOT ok)
  TOK(fglpkgutils.contains(err, "unknown bump kind"))
END FUNCTION

FUNCTION testSort()
  DEFINE arr fglpkgutils.TStringArr
  LET arr[1] = "2.0.0"
  LET arr[2] = "1.0.0-alpha"
  LET arr[3] = "1.0.0"
  LET arr[4] = "1.10.0"
  LET arr[5] = "1.2.0"
  CALL semver.sortVersionStrings(arr)
  TEQ(fglpkgutils.joinArr(arr, ","), "1.0.0-alpha,1.0.0,1.2.0,1.10.0,2.0.0")
END FUNCTION
