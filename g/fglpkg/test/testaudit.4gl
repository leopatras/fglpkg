#+ tests for audit.4gl (OSV mapping, sorting, flag parsing)
OPTIONS SHORT CIRCUIT
IMPORT util
IMPORT FGL testutils
IMPORT FGL fglpkg.fglpkgutils
IMPORT FGL fglpkg.lockfile
IMPORT FGL fglpkg.audit
&include "testassert.inc"

MAIN
  CALL testSeverityMapping()
  CALL testVulnToFinding()
  CALL testSortFindings()
  CALL testParseFlags()
  CALL testProductionFilter()
  TSUMMARY()
END MAIN

FUNCTION testSeverityMapping()
  TEQ(audit.severityFromGHSA("CRITICAL"), "critical")
  TEQ(audit.severityFromGHSA("High"), "high")
  TEQ(audit.severityFromGHSA("MODERATE"), "medium")
  TEQ(audit.severityFromGHSA("medium"), "medium")
  TEQ(audit.severityFromGHSA("low"), "low")
  TOK(audit.severityFromGHSA("WEIRD") IS NULL)
  TOK(audit.severityFromGHSA(NULL) IS NULL)
  TEQ(audit.severityRank("critical"), 4)
  TEQ(audit.severityRank("high"), 3)
  TEQ(audit.severityRank("medium"), 2)
  TEQ(audit.severityRank("low"), 1)
  TEQ(audit.severityRank("bogus"), 0)
  TOK(audit.validSeverity("low"))
  TOK(audit.validSeverity("critical"))
  TOK(NOT audit.validSeverity("moderate"))
  TOK(NOT audit.validSeverity(NULL))
END FUNCTION

FUNCTION fixtureJar() RETURNS lockfile.TLockedJAR
  DEFINE jar lockfile.TLockedJAR
  LET jar.key = "com.g:gson"
  LET jar.groupId = "com.g"
  LET jar.artifactId = "gson"
  LET jar.version = "2.8.5"
  RETURN jar
END FUNCTION

FUNCTION testVulnToFinding()
  DEFINE v audit.TOsvVulnerability
  DEFINE f audit.TFinding
  --full GHSA-style record: ADVISORY ref wins even when listed later
  VAR js = '{"id":"GHSA-1111","summary":"Deserialization of Untrusted Data",'
      || '"details":"long text",'
      || '"aliases":["GHSA-x","CVE-2022-25647"],'
      || '"severity":[{"type":"CVSS_V3","score":"CVSS:3.1/AV:N/AC:H"}],'
      || '"references":[{"type":"WEB","url":"https://web.example"},'
      || '{"type":"ADVISORY","url":"https://adv.example"}],'
      || '"database_specific":{"severity":"HIGH"}}'
  CALL util.JSON.parse(js, v)
  LET f = audit.vulnToFinding(v, fixtureJar(), "pkg:maven/com.g/gson@2.8.5")
  TEQ(f.coordinate, "pkg:maven/com.g/gson@2.8.5")
  TEQ(f.groupId, "com.g")
  TEQ(f.artifactId, "gson")
  TEQ(f.version, "2.8.5")
  TEQ(f.id, "GHSA-1111")
  TEQ(f.cve, "CVE-2022-25647")
  TEQ(f.title, "Deserialization of Untrusted Data")
  TEQ(f.description, "long text")
  TEQ(f.severity, "high")
  TEQ(f.reference, "https://adv.example")
  TEQ(f.cvssVector, "CVSS:3.1/AV:N/AC:H")

  --minimal record: no CVE alias, no advisory ref (first url used),
  --unknown severity defaults to medium
  INITIALIZE v TO NULL
  CALL v.aliases.clear()
  CALL v.severity.clear()
  CALL v.references.clear()
  LET js = '{"id":"OSV-2","summary":"t","aliases":["GHSA-y"],'
      || '"references":[{"type":"WEB","url":"https://first.example"},'
      || '{"type":"WEB","url":"https://second.example"}]}'
  CALL util.JSON.parse(js, v)
  LET f = audit.vulnToFinding(v, fixtureJar(), "pkg:maven/com.g/gson@2.8.5")
  TOK(f.cve IS NULL)
  TEQ(f.severity, "medium")
  TEQ(f.reference, "https://first.example")
  TOK(f.cvssVector IS NULL)
END FUNCTION

FUNCTION testSortFindings()
  DEFINE fs audit.TFindings
  LET fs[1].severity = "low"
  LET fs[1].coordinate = "pkg:maven/a/a@1"
  LET fs[1].id = "B"
  LET fs[2].severity = "critical"
  LET fs[2].coordinate = "pkg:maven/z/z@1"
  LET fs[2].id = "A"
  LET fs[3].severity = "low"
  LET fs[3].coordinate = "pkg:maven/a/a@1"
  LET fs[3].id = "A"
  LET fs[4].severity = "low"
  LET fs[4].coordinate = "pkg:maven/a/a@0"
  LET fs[4].id = "Z"
  CALL audit.sortFindings(fs)
  TEQ(fs[1].severity, "critical")
  TEQ(fs[2].coordinate, "pkg:maven/a/a@0") --coordinate asc within severity
  TEQ(fs[3].id, "A") --id asc within coordinate
  TEQ(fs[4].id, "B")
END FUNCTION

FUNCTION testParseFlags()
  DEFINE args fglpkgutils.TStringArr
  DEFINE ok, jsonOut, production, offline BOOLEAN
  DEFINE severity, err STRING
  --defaults
  CALL parseWrap(args) RETURNING ok, jsonOut, production, offline, severity, err
  TOK(ok)
  TOK(NOT jsonOut)
  TEQ(severity, "medium")
  --all flags
  LET args[1] = "--json"
  LET args[2] = "--prod"
  LET args[3] = "--severity=critical"
  CALL parseWrap(args) RETURNING ok, jsonOut, production, offline, severity, err
  TOK(ok)
  TOK(jsonOut)
  TOK(production)
  TEQ(severity, "critical")
  --invalid severity
  CALL args.clear()
  LET args[1] = "--severity=moderate"
  CALL parseWrap(args) RETURNING ok, jsonOut, production, offline, severity, err
  TOK(NOT ok)
  TEQ(err, 'invalid --severity "moderate" (want: low, medium, high, critical)')
  --unknown argument
  CALL args.clear()
  LET args[1] = "--nope"
  CALL parseWrap(args) RETURNING ok, jsonOut, production, offline, severity, err
  TOK(NOT ok)
  TEQ(err, 'unknown argument "--nope"')
  --offline flag parses fine (rejection happens in cmdAudit)
  CALL args.clear()
  LET args[1] = "--offline"
  CALL parseWrap(args) RETURNING ok, jsonOut, production, offline, severity, err
  TOK(ok)
  TOK(offline)
END FUNCTION

--flattens the flags record so TEQ/TOK stay single-value
PRIVATE FUNCTION parseWrap(args fglpkgutils.TStringArr)
    RETURNS(BOOLEAN, BOOLEAN, BOOLEAN, BOOLEAN, STRING, STRING)
  DEFINE ok BOOLEAN
  DEFINE err STRING
  DEFINE f audit.TAuditFlags
  CALL audit.parseAuditFlags(args) RETURNING ok, f, err
  RETURN ok, f.jsonOut, f.production, f.offline, f.severity, err
END FUNCTION

FUNCTION testProductionFilter()
  DEFINE lf lockfile.TLockfile
  DEFINE jars lockfile.TLockedJARs
  LET lf.jars[1].key = "a:a"
  LET lf.jars[1].scope = "dev"
  LET lf.jars[2].key = "b:b"
  LET lf.jars[3].key = "c:c"
  LET lf.jars[3].scope = "optional"
  LET jars = audit.filterAuditJARs(lf, FALSE)
  TEQ(jars.getLength(), 3)
  LET jars = audit.filterAuditJARs(lf, TRUE)
  TEQ(jars.getLength(), 2) --dev dropped, optional kept
  TEQ(jars[1].key, "b:b")
  TEQ(jars[2].key, "c:c")
END FUNCTION
