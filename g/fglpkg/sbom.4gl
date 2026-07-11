#+ fglpkg sbom — CycloneDX 1.5 JSON software bill of materials from
#+ fglpkg.lock (deterministic, no network)
#+ port of internal/cli/sbom.go + internal/sbom/cyclonedx.go
OPTIONS SHORT CIRCUIT
PACKAGE fglpkg
IMPORT util
IMPORT security
IMPORT FGL fglpkg.fglpkgutils
IMPORT FGL fglpkg.manifest
IMPORT FGL fglpkg.lockfile
IMPORT FGL fglpkg.commands
&include "myassert.inc"

PRIVATE TYPE TSbomFlags RECORD
  pretty BOOLEAN,
  production BOOLEAN,
  output STRING
END RECORD

#+the sbom command; returns the process exit code
FUNCTION cmdSbom(args fglpkgutils.TStringArr) RETURNS INT
  DEFINE flags TSbomFlags
  DEFINE ok BOOLEAN
  DEFINE lf lockfile.TLockfile
  DEFINE err STRING

  CALL parseSbomFlags(args) RETURNING ok, flags, err
  IF NOT ok THEN
    RETURN sbomFail(err)
  END IF

  IF NOT lockfile.lockExists(".") THEN
    RETURN sbomFail("no fglpkg.lock in current directory; run `fglpkg install` first")
  END IF
  CALL lockfile.load(".") RETURNING ok, lf, err
  IF NOT ok THEN
    RETURN sbomFail(SFMT("failed to load fglpkg.lock: %1", err))
  END IF

  VAR doc = buildSbom(lf, flags.production, commands.TOOL_VERSION)
  IF flags.pretty THEN
    LET doc = manifest.prettyJSON(doc)
  END IF

  IF flags.output IS NULL THEN
    DISPLAY doc
  ELSE
    TRY
      CALL fglpkgutils.writeStringToFile(flags.output, doc || "\n")
    CATCH
      RETURN sbomFail(SFMT("cannot create %1", flags.output))
    END TRY
  END IF
  RETURN 0
END FUNCTION

PRIVATE FUNCTION sbomFail(msg STRING) RETURNS INT
  CALL fglpkgutils.printStderr(msg)
  RETURN 1
END FUNCTION

FUNCTION parseSbomFlags(args fglpkgutils.TStringArr)
    RETURNS(BOOLEAN, TSbomFlags, STRING)
  DEFINE f TSbomFlags
  DEFINE i INT
  LET i = 1
  WHILE i <= args.getLength()
    CASE
      WHEN args[i] == "--json"
        --accepted no-op (parity with the Go binary)
      WHEN args[i] == "--pretty"
        LET f.pretty = TRUE
      WHEN args[i] == "--production" OR args[i] == "--prod"
        LET f.production = TRUE
      WHEN args[i] == "-o" OR args[i] == "--output"
        IF i + 1 > args.getLength() THEN
          RETURN FALSE, f, SFMT("%1 requires a file path", args[i])
        END IF
        LET i = i + 1
        LET f.output = args[i]
      WHEN fglpkgutils.startsWith(args[i], "--output=")
        LET f.output = args[i].subString(10, args[i].getLength())
      WHEN fglpkgutils.startsWith(args[i], "--format=")
        VAR fmt = args[i].subString(10, args[i].getLength())
        IF fmt != "cyclonedx" AND fmt.getLength() > 0 THEN
          RETURN FALSE, f,
              SFMT("%1 format not supported in v1 (use --format=cyclonedx)",
                  fmt)
        END IF
      OTHERWISE
        RETURN FALSE, f, SFMT('unknown argument "%1"', args[i])
    END CASE
    LET i = i + 1
  END WHILE
  RETURN TRUE, f, NULL
END FUNCTION

#+builds the CycloneDX 1.5 document as compact JSON (pure, testable)
FUNCTION buildSbom(lf lockfile.TLockfile, production BOOLEAN,
    toolVersion STRING)
    RETURNS STRING
  DEFINE i INT
  VAR doc = util.JSONObject.create()
  CALL doc.put("bomFormat", "CycloneDX")
  CALL doc.put("specVersion", "1.5")
  VAR uuid STRING = security.RandomGenerator.CreateUUIDString()
  CALL doc.put("serialNumber", SFMT("urn:uuid:%1", uuid.toLowerCase()))
  CALL doc.put("version", 1)

  --metadata
  VAR metadata = util.JSONObject.create()
  CALL metadata.put("timestamp",
      util.Datetime.format(
          util.Datetime.getCurrentAsUTC(), "%Y-%m-%dT%H:%M:%SZ"))
  VAR tools = util.JSONArray.create()
  VAR tool = util.JSONObject.create()
  CALL tool.put("vendor", "Four Js")
  CALL tool.put("name", "fglpkg")
  IF toolVersion IS NOT NULL THEN
    CALL tool.put("version", toolVersion)
  END IF
  CALL tools.put(1, tool)
  CALL metadata.put("tools", tools)
  VAR rootComp = util.JSONObject.create()
  CALL rootComp.put("bom-ref", "root")
  CALL rootComp.put("type", "application")
  CALL rootComp.put("name", NVL(lf.rootManifest.name, ""))
  IF lf.rootManifest.version IS NOT NULL THEN
    CALL rootComp.put("version", lf.rootManifest.version)
  END IF
  CALL metadata.put("component", rootComp)
  CALL doc.put("metadata", metadata)

  --sorted copies of the package/jar lists (the lockfile is already
  --sorted on save, but a hand-written lock may not be); Go sorts both
  --in internal/sbom/cyclonedx.go for stable output
  VAR pkgs lockfile.TLockedPackages
  CALL lf.packages.copyTo(pkgs)
  CALL pkgs.sortByComparisonFunction("name", FALSE, FUNCTION fglpkgutils.cmpBytes)
  VAR jars = sortedJars(lf, production)

  --components
  VAR components = util.JSONArray.create()
  FOR i = 1 TO pkgs.getLength()
    CALL components.put(components.getLength() + 1, bdlComponent(pkgs[i]))
  END FOR
  FOR i = 1 TO jars.getLength()
    CALL components.put(components.getLength() + 1, jarComponent(jars[i]))
  END FOR
  IF components.getLength() > 0 THEN
    CALL doc.put("components", components)
  END IF

  --dependency edges
  VAR deps = buildDependencyEdges(pkgs, jars)
  IF deps.getLength() > 0 THEN
    CALL doc.put("dependencies", deps)
  END IF

  RETURN doc.toString()
END FUNCTION

FUNCTION bdlPurl(name STRING, version STRING) RETURNS STRING
  RETURN SFMT("pkg:fglpkg/%1@%2", name, version)
END FUNCTION

FUNCTION mavenPurl(groupId STRING, artifactId STRING, version STRING)
    RETURNS STRING
  RETURN SFMT("pkg:maven/%1/%2@%3", groupId, artifactId, version)
END FUNCTION

PRIVATE FUNCTION bdlComponent(p lockfile.TLockedPackage)
    RETURNS util.JSONObject
  VAR purl = bdlPurl(p.name, p.version)
  VAR c = util.JSONObject.create()
  CALL c.put("bom-ref", purl)
  CALL c.put("type", "library")
  CALL c.put("name", NVL(p.name, ""))
  IF p.version IS NOT NULL THEN
    CALL c.put("version", p.version)
  END IF
  CALL c.put("purl", purl)
  IF p.checksum IS NOT NULL THEN
    CALL c.put("hashes", hashesArr(p.checksum))
  END IF
  IF p.downloadUrl IS NOT NULL THEN
    CALL c.put("externalReferences", extRefsArr(p.downloadUrl))
  END IF
  IF p.generoMajor IS NOT NULL THEN
    VAR props = util.JSONArray.create()
    VAR prop = util.JSONObject.create()
    CALL prop.put("name", "fglpkg:generoMajor")
    CALL prop.put("value", p.generoMajor)
    CALL props.put(1, prop)
    CALL c.put("properties", props)
  END IF
  RETURN c
END FUNCTION

PRIVATE FUNCTION jarComponent(j lockfile.TLockedJAR) RETURNS util.JSONObject
  VAR purl = mavenPurl(j.groupId, j.artifactId, j.version)
  VAR c = util.JSONObject.create()
  CALL c.put("bom-ref", purl)
  CALL c.put("type", "library")
  CALL c.put("name", NVL(j.artifactId, ""))
  CALL c.put("group", NVL(j.groupId, ""))
  IF j.version IS NOT NULL THEN
    CALL c.put("version", j.version)
  END IF
  CALL c.put("purl", purl)
  IF j.checksum IS NOT NULL THEN
    CALL c.put("hashes", hashesArr(j.checksum))
  END IF
  IF j.downloadUrl IS NOT NULL THEN
    CALL c.put("externalReferences", extRefsArr(j.downloadUrl))
  END IF
  RETURN c
END FUNCTION

PRIVATE FUNCTION hashesArr(checksum STRING) RETURNS util.JSONArray
  VAR arr = util.JSONArray.create()
  VAR h = util.JSONObject.create()
  CALL h.put("alg", "SHA-256")
  CALL h.put("content", checksum)
  CALL arr.put(1, h)
  RETURN arr
END FUNCTION

PRIVATE FUNCTION extRefsArr(url STRING) RETURNS util.JSONArray
  VAR arr = util.JSONArray.create()
  VAR r = util.JSONObject.create()
  CALL r.put("type", "distribution")
  CALL r.put("url", url)
  CALL arr.put(1, r)
  RETURN arr
END FUNCTION

#+the JAR list after the production filter, sorted by key
PRIVATE FUNCTION sortedJars(lf lockfile.TLockfile, production BOOLEAN)
    RETURNS lockfile.TLockedJARs
  DEFINE out lockfile.TLockedJARs
  DEFINE i INT
  FOR i = 1 TO lf.jars.getLength()
    IF production AND lf.jars[i].scope == "dev" THEN
      CONTINUE FOR
    END IF
    LET out[out.getLength() + 1] = lf.jars[i]
  END FOR
  CALL out.sortByComparisonFunction("key", FALSE, FUNCTION fglpkgutils.cmpBytes)
  RETURN out
END FUNCTION

#+dependency edges: per-package from requiredBy ("<root>" -> root;
#+unknown parents collapse to root), all JARs under root; edges appear
#+in first-seen parent order
PRIVATE FUNCTION buildDependencyEdges(
    pkgs lockfile.TLockedPackages, jars lockfile.TLockedJARs)
    RETURNS util.JSONArray
  DEFINE children DICTIONARY OF fglpkgutils.TStringArr
  DEFINE order fglpkgutils.TStringArr
  DEFINE i, j INT

  FOR i = 1 TO pkgs.getLength()
    VAR childPurl = bdlPurl(pkgs[i].name, pkgs[i].version)
    IF pkgs[i].requiredBy.getLength() == 0 THEN
      CALL addEdge(children, order, "root", childPurl)
      CONTINUE FOR
    END IF
    FOR j = 1 TO pkgs[i].requiredBy.getLength()
      VAR parent = pkgs[i].requiredBy[j]
      IF parent == "<root>" THEN
        CALL addEdge(children, order, "root", childPurl)
      ELSE
        VAR pv = findPkgVersion(pkgs, parent)
        IF pv IS NULL THEN
          CALL addEdge(children, order, "root", childPurl)
        ELSE
          CALL addEdge(children, order, bdlPurl(parent, pv), childPurl)
        END IF
      END IF
    END FOR
  END FOR
  FOR i = 1 TO jars.getLength()
    CALL addEdge(children, order, "root",
        mavenPurl(jars[i].groupId, jars[i].artifactId, jars[i].version))
  END FOR

  VAR arr = util.JSONArray.create()
  FOR i = 1 TO order.getLength()
    VAR edge = util.JSONObject.create()
    CALL edge.put("ref", order[i])
    VAR dependsOn = util.JSONArray.create()
    VAR kids = children[order[i]]
    FOR j = 1 TO kids.getLength()
      CALL dependsOn.put(dependsOn.getLength() + 1, kids[j])
    END FOR
    IF dependsOn.getLength() > 0 THEN
      CALL edge.put("dependsOn", dependsOn)
    END IF
    CALL arr.put(arr.getLength() + 1, edge)
  END FOR
  RETURN arr
END FUNCTION

PRIVATE FUNCTION addEdge(
    children DICTIONARY OF fglpkgutils.TStringArr,
    order fglpkgutils.TStringArr, parent STRING, child STRING)
  IF NOT children.contains(parent) THEN
    LET order[order.getLength() + 1] = parent
  END IF
  LET children[parent][children[parent].getLength() + 1] = child
END FUNCTION

PRIVATE FUNCTION findPkgVersion(pkgs lockfile.TLockedPackages, name STRING)
    RETURNS STRING
  DEFINE i INT
  FOR i = 1 TO pkgs.getLength()
    IF pkgs[i].name == name THEN
      RETURN pkgs[i].version
    END IF
  END FOR
  RETURN NULL
END FUNCTION
