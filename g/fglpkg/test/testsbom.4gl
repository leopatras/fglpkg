#+ tests for sbom.4gl (CycloneDX document shape)
OPTIONS SHORT CIRCUIT
IMPORT util
IMPORT FGL testutils
IMPORT FGL fglpkg.fglpkgutils
IMPORT FGL fglpkg.lockfile
IMPORT FGL fglpkg.sbom
&include "testassert.inc"

MAIN
  CALL testDocumentShape()
  CALL testProductionFilter()
  CALL testEdges()
  CALL testUnsortedInput()
  TSUMMARY()
END MAIN

#+a hand-written lock may not be sorted; components must come out in
#+byte-wise name/key order like Go (internal/sbom sorts copies on build)
FUNCTION testUnsortedInput()
  DEFINE lf lockfile.TLockfile
  DEFINE doc, comp util.JSONObject
  DEFINE comps util.JSONArray
  LET lf.rootManifest.name = "app"
  LET lf.packages[1].name = "zeta"
  LET lf.packages[1].version = "1.0.0"
  LET lf.packages[2].name = "Alpha" --byte-wise: uppercase sorts first
  LET lf.packages[2].version = "1.0.0"
  LET lf.jars[1].key = "org.z:zz"
  LET lf.jars[1].groupId = "org.z"
  LET lf.jars[1].artifactId = "zz"
  LET lf.jars[1].version = "1.0"
  LET lf.jars[2].key = "COM.a:aa"
  LET lf.jars[2].groupId = "COM.a"
  LET lf.jars[2].artifactId = "aa"
  LET lf.jars[2].version = "1.0"
  VAR js = sbom.buildSbom(lf, FALSE, "9.9.9")
  LET doc = util.JSONObject.parse(js)
  LET comps = doc.get("components")
  TEQ(comps.getLength(), 4)
  LET comp = comps.get(1)
  TEQ(comp.get("name"), "Alpha")
  LET comp = comps.get(2)
  TEQ(comp.get("name"), "zeta")
  LET comp = comps.get(3)
  TEQ(comp.get("purl"), "pkg:maven/COM.a/aa@1.0")
  LET comp = comps.get(4)
  TEQ(comp.get("purl"), "pkg:maven/org.z/zz@1.0")
  --input lockfile must not be mutated (sorted copies only)
  TEQ(lf.packages[1].name, "zeta")
  TEQ(lf.jars[1].key, "org.z:zz")
END FUNCTION

FUNCTION fixtureLock() RETURNS lockfile.TLockfile
  DEFINE lf lockfile.TLockfile
  LET lf.version = 1
  LET lf.generatedAt = "2026-07-10T00:00:00Z"
  LET lf.generoVersion = "6.00.02"
  LET lf.rootManifest.name = "app"
  LET lf.rootManifest.version = "1.0.0"
  LET lf.packages[1].name = "alpha"
  LET lf.packages[1].version = "1.0.0"
  LET lf.packages[1].downloadUrl = "https://x/alpha.zip"
  LET lf.packages[1].checksum = "aa11"
  LET lf.packages[1].generoMajor = "6"
  LET lf.packages[1].requiredBy[1] = "<root>"
  LET lf.packages[2].name = "beta"
  LET lf.packages[2].version = "2.0.0"
  LET lf.packages[2].downloadUrl = "https://x/beta.zip"
  LET lf.packages[2].requiredBy[1] = "alpha"
  LET lf.jars[1].key = "com.g:gson"
  LET lf.jars[1].groupId = "com.g"
  LET lf.jars[1].artifactId = "gson"
  LET lf.jars[1].version = "2.10.1"
  LET lf.jars[1].downloadUrl = "https://m/gson.jar"
  LET lf.jars[1].checksum = "cc33"
  LET lf.jars[1].scope = "dev"
  LET lf.jars[2].key = "org.a:poi"
  LET lf.jars[2].groupId = "org.a"
  LET lf.jars[2].artifactId = "poi"
  LET lf.jars[2].version = "5.2.3"
  LET lf.jars[2].downloadUrl = "https://m/poi.jar"
  RETURN lf
END FUNCTION

FUNCTION testDocumentShape()
  DEFINE doc, metadata, tool, comp, h util.JSONObject
  DEFINE tools, comps, hashes, props util.JSONArray
  VAR js = sbom.buildSbom(fixtureLock(), FALSE, "9.9.9")
  LET doc = util.JSONObject.parse(js)
  TEQ(doc.get("bomFormat"), "CycloneDX")
  TEQ(doc.get("specVersion"), "1.5")
  VAR serial STRING = doc.get("serialNumber")
  TOK(fglpkgutils.startsWith(serial, "urn:uuid:"))
  TEQ(serial.getLength(), 45) --urn:uuid: + 36 char uuid
  TEQ(serial, serial.toLowerCase())

  LET metadata = doc.get("metadata")
  LET tools = metadata.get("tools")
  LET tool = tools.get(1)
  TEQ(tool.get("vendor"), "Four Js")
  TEQ(tool.get("name"), "fglpkg")
  TEQ(tool.get("version"), "9.9.9")
  LET comp = metadata.get("component")
  TEQ(comp.get("bom-ref"), "root")
  TEQ(comp.get("type"), "application")
  TEQ(comp.get("name"), "app")

  --components: 2 packages + 2 jars, canonical field order
  LET comps = doc.get("components")
  TEQ(comps.getLength(), 4)
  LET comp = comps.get(1)
  TEQ(comp.get("purl"), "pkg:fglpkg/alpha@1.0.0")
  TEQ(comp.name(1), "bom-ref")
  TEQ(comp.name(2), "type")
  TEQ(comp.name(3), "name")
  LET hashes = comp.get("hashes")
  LET h = hashes.get(1)
  TEQ(h.get("alg"), "SHA-256")
  TEQ(h.get("content"), "aa11")
  LET props = comp.get("properties")
  LET h = props.get(1)
  TEQ(h.get("name"), "fglpkg:generoMajor")
  TEQ(h.get("value"), "6")
  --beta has no checksum/generoMajor: no hashes/properties keys
  LET comp = comps.get(2)
  TOK(NOT comp.has("hashes"))
  TOK(NOT comp.has("properties"))
  --jars sorted by key: gson before poi, with group field
  LET comp = comps.get(3)
  TEQ(comp.get("purl"), "pkg:maven/com.g/gson@2.10.1")
  TEQ(comp.get("group"), "com.g")
  TEQ(comp.get("name"), "gson")
END FUNCTION

FUNCTION testProductionFilter()
  DEFINE doc util.JSONObject
  DEFINE comps util.JSONArray
  DEFINE comp util.JSONObject
  DEFINE i INT
  VAR js = sbom.buildSbom(fixtureLock(), TRUE, "9.9.9")
  LET doc = util.JSONObject.parse(js)
  LET comps = doc.get("components")
  TEQ(comps.getLength(), 3) --gson (dev) dropped
  FOR i = 1 TO comps.getLength()
    LET comp = comps.get(i)
    TOK(NOT fglpkgutils.contains(comp.get("purl"), "gson"))
  END FOR
END FUNCTION

FUNCTION testEdges()
  DEFINE doc, edge util.JSONObject
  DEFINE deps, dependsOn util.JSONArray
  VAR js = sbom.buildSbom(fixtureLock(), FALSE, "9.9.9")
  LET doc = util.JSONObject.parse(js)
  LET deps = doc.get("dependencies")
  --root first (alpha + jars), then alpha (beta)
  LET edge = deps.get(1)
  TEQ(edge.get("ref"), "root")
  LET dependsOn = edge.get("dependsOn")
  TEQ(dependsOn.getLength(), 3) --alpha + 2 jars
  TEQ(dependsOn.get(1), "pkg:fglpkg/alpha@1.0.0")
  TEQ(dependsOn.get(2), "pkg:maven/com.g/gson@2.10.1")
  LET edge = deps.get(2)
  TEQ(edge.get("ref"), "pkg:fglpkg/alpha@1.0.0")
  LET dependsOn = edge.get("dependsOn")
  TEQ(dependsOn.get(1), "pkg:fglpkg/beta@2.0.0")
END FUNCTION
