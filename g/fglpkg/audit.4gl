#+ fglpkg audit — check installed Java JARs against the OSV.dev
#+ vulnerability database (report-only; BDL packages are not scanned)
#+ port of internal/cli/audit.go + internal/audit/audit.go
#+ exit codes: 0 = clean, 1 = findings at/above the severity floor,
#+ 2 = the audit itself failed
OPTIONS SHORT CIRCUIT
PACKAGE fglpkg
IMPORT com
IMPORT util
IMPORT FGL fglpkg.fglpkgutils
IMPORT FGL fglpkg.manifest
IMPORT FGL fglpkg.lockfile
IMPORT FGL fglpkg.sbom
&include "myassert.inc"

PUBLIC CONSTANT DEFAULT_OSV_URL = "https://api.osv.dev/v1/query"
PUBLIC CONSTANT SOURCE_LABEL = "osv.dev"
PUBLIC CONSTANT BDL_NOT_SCANNED =
    "BDL packages were not scanned (no advisory database available yet)."

PUBLIC TYPE TFinding RECORD
  coordinate STRING, --pkg:maven/<g>/<a>@<v>
  groupId STRING,
  artifactId STRING,
  version STRING,
  id STRING, --advisory id (e.g. GHSA-...)
  cve STRING,
  title STRING,
  description STRING,
  cvssVector STRING,
  severity STRING, --low | medium | high | critical
  reference STRING
END RECORD

PUBLIC TYPE TFindings DYNAMIC ARRAY OF TFinding

--OSV.dev wire format (unmatched fields ignored by util.JSON)
PUBLIC TYPE TOsvVulnerability RECORD
  id STRING,
  summary STRING,
  details STRING,
  aliases fglpkgutils.TStringArr,
  severity DYNAMIC ARRAY OF RECORD
    typ STRING ATTRIBUTES(json_name = "type"),
    score STRING
  END RECORD,
  references DYNAMIC ARRAY OF RECORD
    typ STRING ATTRIBUTES(json_name = "type"),
    url STRING
  END RECORD,
  databaseSpecific RECORD ATTRIBUTES(json_name = "database_specific")
    severity STRING
  END RECORD
END RECORD

PUBLIC TYPE TOsvResponse RECORD
  vulns DYNAMIC ARRAY OF TOsvVulnerability
END RECORD

PUBLIC TYPE TAuditFlags RECORD
  jsonOut BOOLEAN,
  production BOOLEAN,
  offline BOOLEAN,
  severity STRING
END RECORD

#+the audit command; returns the process exit code (0/1/2)
FUNCTION cmdAudit(args fglpkgutils.TStringArr) RETURNS INT
  DEFINE flags TAuditFlags
  DEFINE ok BOOLEAN
  DEFINE lf lockfile.TLockfile
  DEFINE findings TFindings
  DEFINE err STRING
  DEFINE i INT

  CALL parseAuditFlags(args) RETURNING ok, flags, err
  IF NOT ok THEN
    RETURN auditFail(err, 2)
  END IF
  IF flags.offline THEN
    RETURN auditFail("--offline mode not yet supported", 2)
  END IF

  IF NOT lockfile.lockExists(".") THEN
    RETURN auditFail("no fglpkg.lock in current directory; run `fglpkg install` first", 2)
  END IF
  CALL lockfile.load(".") RETURNING ok, lf, err
  IF NOT ok THEN
    RETURN auditFail(SFMT("failed to load fglpkg.lock: %1", err), 2)
  END IF

  VAR jars = filterAuditJARs(lf, flags.production)
  IF NOT flags.jsonOut AND jars.getLength() == 0 THEN
    DISPLAY "No Java JARs to audit."
    DISPLAY BDL_NOT_SCANNED
    RETURN 0
  END IF

  VAR url = fglpkgutils.getEnvDefault("FGLPKG_AUDIT_URL", DEFAULT_OSV_URL)
  CALL auditJARs(jars, url) RETURNING ok, findings, err
  IF NOT ok THEN
    RETURN auditFail(SFMT("audit failed: %1", err), 2)
  END IF
  CALL sortFindings(findings)

  IF flags.jsonOut THEN
    CALL writeAuditJSON(findings, jars.getLength())
  ELSE
    CALL writeAuditTable(findings, jars.getLength())
  END IF

  VAR threshold = severityRank(flags.severity)
  VAR atOrAbove = 0
  FOR i = 1 TO findings.getLength()
    IF severityRank(findings[i].severity) >= threshold THEN
      LET atOrAbove = atOrAbove + 1
    END IF
  END FOR
  IF atOrAbove > 0 THEN
    --"ie" for n != 1 replicates the Go binary's pluralY bug (it forgets
    --the trailing "s" outside of cmdOutdated); byte-parity wins here
    CALL fglpkgutils.printStderr(
        SFMT("%1 vulnerabilit%2 found at severity >= %3",
            atOrAbove, IIF(atOrAbove == 1, "y", "ie"), flags.severity))
    RETURN 1
  END IF
  RETURN 0
END FUNCTION

PRIVATE FUNCTION auditFail(msg STRING, code INT) RETURNS INT
  CALL fglpkgutils.printStderr(msg)
  RETURN code
END FUNCTION

FUNCTION parseAuditFlags(args fglpkgutils.TStringArr)
    RETURNS(BOOLEAN, TAuditFlags, STRING)
  DEFINE f TAuditFlags
  DEFINE i INT
  LET f.severity = "medium"
  FOR i = 1 TO args.getLength()
    CASE
      WHEN args[i] == "--json"
        LET f.jsonOut = TRUE
      WHEN args[i] == "--production" OR args[i] == "--prod"
        LET f.production = TRUE
      WHEN args[i] == "--offline"
        LET f.offline = TRUE
      WHEN fglpkgutils.startsWith(args[i], "--severity=")
        VAR sev = args[i].subString(12, args[i].getLength())
        IF NOT validSeverity(sev) THEN
          RETURN FALSE, f,
              SFMT('invalid --severity "%1" (want: low, medium, high, critical)',
                  NVL(sev, ""))
        END IF
        LET f.severity = sev
      OTHERWISE
        RETURN FALSE, f, SFMT('unknown argument "%1"', args[i])
    END CASE
  END FOR
  RETURN TRUE, f, NULL
END FUNCTION

FUNCTION validSeverity(s STRING) RETURNS BOOLEAN
  RETURN severityRank(s) > 0
END FUNCTION

#+critical=4 high=3 medium=2 low=1 unknown=0
FUNCTION severityRank(s STRING) RETURNS INT
  CASE s
    WHEN "critical"
      RETURN 4
    WHEN "high"
      RETURN 3
    WHEN "medium"
      RETURN 2
    WHEN "low"
      RETURN 1
  END CASE
  RETURN 0
END FUNCTION

#+GHSA database_specific.severity buckets; unknown maps to ""
FUNCTION severityFromGHSA(s STRING) RETURNS STRING
  VAR u = s.toUpperCase()
  CASE u
    WHEN "CRITICAL"
      RETURN "critical"
    WHEN "HIGH"
      RETURN "high"
    WHEN "MODERATE"
      RETURN "medium"
    WHEN "MEDIUM"
      RETURN "medium"
    WHEN "LOW"
      RETURN "low"
  END CASE
  RETURN NULL
END FUNCTION

#+the lock JARs to audit (production drops dev-scoped entries)
FUNCTION filterAuditJARs(lf lockfile.TLockfile, production BOOLEAN)
    RETURNS lockfile.TLockedJARs
  DEFINE out lockfile.TLockedJARs
  DEFINE i INT
  FOR i = 1 TO lf.jars.getLength()
    IF production AND lf.jars[i].scope == "dev" THEN
      CONTINUE FOR
    END IF
    LET out[out.getLength() + 1] = lf.jars[i]
  END FOR
  RETURN out
END FUNCTION

#+queries OSV.dev once per deduplicated Maven coordinate; any transport,
#+HTTP or parse error fails the whole audit (fail-closed)
FUNCTION auditJARs(jars lockfile.TLockedJARs, url STRING)
    RETURNS(BOOLEAN, TFindings, STRING)
  DEFINE findings, empty TFindings
  DEFINE seen DICTIONARY OF BOOLEAN
  DEFINE resp TOsvResponse
  DEFINE i, j INT
  DEFINE ok BOOLEAN
  DEFINE body, err STRING
  FOR i = 1 TO jars.getLength()
    VAR purl = sbom.mavenPurl(jars[i].groupId, jars[i].artifactId,
        jars[i].version)
    IF seen.contains(purl) THEN
      CONTINUE FOR
    END IF
    LET seen[purl] = TRUE
    CALL osvQuery(url, purl) RETURNING ok, body, err
    IF NOT ok THEN
      RETURN FALSE, empty, err
    END IF
    --clear before parse: a response without "vulns" leaves stale entries
    CALL resp.vulns.clear()
    TRY
      CALL util.JSON.parse(body, resp)
    CATCH
      RETURN FALSE, empty, "invalid OSV.dev response"
    END TRY
    FOR j = 1 TO resp.vulns.getLength()
      LET findings[findings.getLength() + 1] =
          vulnToFinding(resp.vulns[j], jars[i], purl)
    END FOR
  END FOR
  RETURN TRUE, findings, NULL
END FUNCTION

PRIVATE FUNCTION osvQuery(url STRING, purl STRING)
    RETURNS(BOOLEAN, STRING, STRING)
  DEFINE req com.HttpRequest
  DEFINE resp com.HttpResponse
  DEFINE code INT
  DEFINE body STRING
  VAR payload = util.JSONObject.create()
  VAR pkg = util.JSONObject.create()
  CALL pkg.put("purl", purl)
  CALL payload.put("package", pkg)
  TRY
    LET req = com.HttpRequest.Create(url)
    CALL req.setMethod("POST")
    CALL req.setHeader("Content-Type", "application/json")
    CALL req.setHeader("Accept", "application/json")
    CALL req.setTimeOut(30)
    CALL req.doTextRequest(payload.toString())
    LET resp = req.getResponse()
    LET code = resp.getStatusCode()
    LET body = resp.getTextResponse()
  CATCH
    RETURN FALSE, NULL, SFMT("OSV.dev request failed for %1: %2",
        purl, err_get(status))
  END TRY
  IF code < 200 OR code >= 300 THEN
    RETURN FALSE, NULL,
        SFMT("OSV.dev returned HTTP %1 for %2: %3", code, purl,
            NVL(body.trim(), ""))
  END IF
  RETURN TRUE, body, NULL
END FUNCTION

#+maps one OSV vulnerability to a finding (pure, testable)
FUNCTION vulnToFinding(
    v TOsvVulnerability, jar lockfile.TLockedJAR, purl STRING)
    RETURNS TFinding
  DEFINE f TFinding
  DEFINE i INT
  LET f.coordinate = purl
  LET f.groupId = jar.groupId
  LET f.artifactId = jar.artifactId
  LET f.version = jar.version
  LET f.id = v.id
  LET f.title = v.summary
  LET f.description = v.details
  --severity bucket; unknown defaults to medium (fail-conservative)
  LET f.severity = severityFromGHSA(v.databaseSpecific.severity)
  IF f.severity IS NULL THEN
    LET f.severity = "medium"
  END IF
  --first CVE alias
  FOR i = 1 TO v.aliases.getLength()
    IF fglpkgutils.startsWith(v.aliases[i], "CVE-") THEN
      LET f.cve = v.aliases[i]
      EXIT FOR
    END IF
  END FOR
  --first ADVISORY reference, else the first reference with a url
  FOR i = 1 TO v.references.getLength()
    IF v.references[i].url IS NULL THEN
      CONTINUE FOR
    END IF
    IF v.references[i].typ.toUpperCase() == "ADVISORY" THEN
      LET f.reference = v.references[i].url
      EXIT FOR
    END IF
    IF f.reference IS NULL THEN
      LET f.reference = v.references[i].url
    END IF
  END FOR
  --first non-empty CVSS vector
  FOR i = 1 TO v.severity.getLength()
    IF v.severity[i].score IS NOT NULL THEN
      LET f.cvssVector = v.severity[i].score
      EXIT FOR
    END IF
  END FOR
  RETURN f
END FUNCTION

#+sorts findings severity-desc, then coordinate asc, then id asc (stable)
FUNCTION sortFindings(findings TFindings)
  DEFINE i, j INT
  DEFINE tmp TFinding
  FOR i = 2 TO findings.getLength()
    LET j = i
    WHILE j > 1 AND findingLess(findings[j], findings[j - 1])
      LET tmp = findings[j]
      LET findings[j] = findings[j - 1]
      LET findings[j - 1] = tmp
      LET j = j - 1
    END WHILE
  END FOR
END FUNCTION

PRIVATE FUNCTION findingLess(a TFinding, b TFinding) RETURNS BOOLEAN
  IF severityRank(a.severity) != severityRank(b.severity) THEN
    RETURN severityRank(a.severity) > severityRank(b.severity)
  END IF
  VAR c = fglpkgutils.cmpBytes(NVL(a.coordinate, ""), NVL(b.coordinate, ""))
  IF c != 0 THEN
    RETURN c < 0
  END IF
  RETURN fglpkgutils.cmpBytes(NVL(a.id, ""), NVL(b.id, "")) < 0
END FUNCTION

--─── output ─────────────────────────────────────────────────────────────────

FUNCTION writeAuditTable(findings TFindings, jarsAudited INT)
  DEFINE i, g INT
  DEFINE nc, nh, nm, nl INT
  DEFINE groupsOrder fglpkgutils.TStringArr
  DEFINE seen DICTIONARY OF BOOLEAN
  IF findings.getLength() == 0 THEN
    DISPLAY SFMT("Audited %1 Java JAR%2 against OSV.dev.",
        jarsAudited, IIF(jarsAudited == 1, "", "s"))
    DISPLAY "No known vulnerabilities found."
    DISPLAY BDL_NOT_SCANNED
    RETURN
  END IF
  FOR i = 1 TO findings.getLength()
    IF NOT seen.contains(findings[i].coordinate) THEN
      LET seen[findings[i].coordinate] = TRUE
      LET groupsOrder[groupsOrder.getLength() + 1] = findings[i].coordinate
    END IF
  END FOR
  DISPLAY SFMT("%1 vulnerabilit%2 found in %3 package%4:\n",
      findings.getLength(),
      IIF(findings.getLength() == 1, "y", "ie"), --Go pluralY bug, see above
      groupsOrder.getLength(),
      IIF(groupsOrder.getLength() == 1, "", "s"))
  FOR g = 1 TO groupsOrder.getLength()
    VAR headerDone = FALSE
    FOR i = 1 TO findings.getLength()
      IF findings[i].coordinate != groupsOrder[g] THEN
        CONTINUE FOR
      END IF
      IF NOT headerDone THEN
        DISPLAY SFMT("  %1:%2  %3",
            findings[i].groupId, findings[i].artifactId, findings[i].version)
        LET headerDone = TRUE
      END IF
      VAR displayId = findings[i].cve
      IF displayId IS NULL THEN
        LET displayId = findings[i].id
      END IF
      DISPLAY SFMT("    %1  %2  %3",
          displayId, fglpkgutils.padRight(findings[i].severity, 8),
          NVL(findings[i].title, ""))
      IF findings[i].reference IS NOT NULL THEN
        DISPLAY SFMT("        %1", findings[i].reference)
      END IF
    END FOR
  END FOR
  CALL countBySeverity(findings) RETURNING nc, nh, nm, nl
  DISPLAY SFMT("\nSummary: %1 critical, %2 high, %3 medium, %4 low",
      nc, nh, nm, nl)
  DISPLAY BDL_NOT_SCANNED
END FUNCTION

FUNCTION writeAuditJSON(findings TFindings, jarsAudited INT)
  DEFINE i INT
  DEFINE nc, nh, nm, nl INT
  VAR doc = util.JSONObject.create()
  CALL doc.put("schemaVersion", 1)
  CALL doc.put("auditedAt",
      util.Datetime.format(
          util.Datetime.getCurrentAsUTC(), "%Y-%m-%dT%H:%M:%SZ"))
  CALL doc.put("source", SOURCE_LABEL)
  CALL doc.put("jarsAudited", jarsAudited)
  VAR arr = util.JSONArray.create()
  FOR i = 1 TO findings.getLength()
    CALL arr.put(arr.getLength() + 1, findingToJSON(findings[i]))
  END FOR
  CALL doc.put("findings", arr)
  CALL countBySeverity(findings) RETURNING nc, nh, nm, nl
  VAR summaryObj = util.JSONObject.create()
  CALL summaryObj.put("critical", nc)
  CALL summaryObj.put("high", nh)
  CALL summaryObj.put("medium", nm)
  CALL summaryObj.put("low", nl)
  CALL doc.put("summary", summaryObj)
  VAR notes = util.JSONArray.create()
  CALL notes.put(1, BDL_NOT_SCANNED)
  CALL doc.put("notes", notes)
  DISPLAY manifest.prettyJSON(doc.toString())
END FUNCTION

PRIVATE FUNCTION findingToJSON(f TFinding) RETURNS util.JSONObject
  VAR obj = util.JSONObject.create()
  CALL obj.put("coordinate", NVL(f.coordinate, ""))
  CALL obj.put("groupId", NVL(f.groupId, ""))
  CALL obj.put("artifactId", NVL(f.artifactId, ""))
  CALL obj.put("version", NVL(f.version, ""))
  CALL obj.put("id", NVL(f.id, ""))
  IF f.cve IS NOT NULL THEN
    CALL obj.put("cve", f.cve)
  END IF
  CALL obj.put("title", NVL(f.title, ""))
  IF f.description IS NOT NULL THEN
    CALL obj.put("description", f.description)
  END IF
  IF f.cvssVector IS NOT NULL THEN
    CALL obj.put("cvssVector", f.cvssVector)
  END IF
  CALL obj.put("severity", NVL(f.severity, ""))
  IF f.reference IS NOT NULL THEN
    CALL obj.put("reference", f.reference)
  END IF
  RETURN obj
END FUNCTION

PRIVATE FUNCTION countBySeverity(findings TFindings)
    RETURNS(INT, INT, INT, INT)
  DEFINE i, nc, nh, nm, nl INT
  FOR i = 1 TO findings.getLength()
    CASE findings[i].severity
      WHEN "critical"
        LET nc = nc + 1
      WHEN "high"
        LET nh = nh + 1
      WHEN "medium"
        LET nm = nm + 1
      WHEN "low"
        LET nl = nl + 1
    END CASE
  END FOR
  RETURN nc, nh, nm, nl
END FUNCTION
