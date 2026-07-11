#+ port of internal/lockfile/lockfile_test.go
OPTIONS SHORT CIRCUIT
IMPORT os
IMPORT FGL testutils
IMPORT FGL fglpkg.fglpkgutils
IMPORT FGL fglpkg.manifest
IMPORT FGL fglpkg.resolver
IMPORT FGL fglpkg.lockfile
&include "testassert.inc"

MAIN
  CALL testFromPlan()
  CALL testSaveLoad()
  CALL testValidate()
  CALL testFilterForProduction()
  CALL testJSONShape()
  TSUMMARY()
END MAIN

FUNCTION samplePlan() RETURNS resolver.TPlan
  DEFINE plan resolver.TPlan
  --deliberately unsorted
  LET plan.packages[1].name = "zeta"
  LET plan.packages[1].version = "2.0.0"
  LET plan.packages[1].downloadURL = "https://x/zeta-2.0.0.zip"
  LET plan.packages[1].checksum = "cs-zeta"
  LET plan.packages[1].variant = "genero4"
  LET plan.packages[1].requiredBy[1] = "<root>"
  LET plan.packages[1].scope = "prod"
  LET plan.packages[2].name = "alpha"
  LET plan.packages[2].version = "1.0.0"
  LET plan.packages[2].downloadURL = "https://x/alpha-1.0.0.zip"
  LET plan.packages[2].checksum = "cs-alpha"
  LET plan.packages[2].variant = "genero4"
  LET plan.packages[2].requiredBy[1] = "zeta"
  LET plan.packages[2].requiredBy[2] = "<root>"
  LET plan.packages[2].scope = "dev"
  --a webcomponent package routed separately
  LET plan.packages[3].name = "chart3d"
  LET plan.packages[3].version = "0.5.0"
  LET plan.packages[3].downloadURL = "https://x/chart3d-0.5.0.zip"
  LET plan.packages[3].checksum = "cs-chart"
  LET plan.packages[3].variant = "webcomponent"
  LET plan.packages[3].requiredBy[1] = "<root>"
  LET plan.packages[3].scope = "prod"

  LET plan.jars[1].groupId = "org.apache.poi"
  LET plan.jars[1].artifactId = "poi"
  LET plan.jars[1].version = "5.2.3"
  LET plan.jars[2].groupId = "com.google.code.gson"
  LET plan.jars[2].artifactId = "gson"
  LET plan.jars[2].version = "2.10.1"
  LET plan.jarScopes["org.apache.poi:poi"] = "prod"
  LET plan.jarScopes["com.google.code.gson:gson"] = "dev"

  LET plan.generoVersion = "4.01.12"
  LET plan.generoMajor = "4"
  RETURN plan
END FUNCTION

FUNCTION sampleLock() RETURNS lockfile.TLockfile
  VAR root = manifest.newManifest("myapp", "1.0.0", "", "")
  RETURN lockfile.fromPlan(samplePlan(), root)
END FUNCTION

FUNCTION testFromPlan()
  VAR lf = sampleLock()
  TEQ(lf.version, 1)
  TEQ(lf.generoVersion, "4.01.12")
  TEQ(lf.rootManifest.name, "myapp")
  TEQ(lf.rootManifest.version, "1.0.0")
  TOK(lf.generatedAt IS NOT NULL)

  --packages sorted by name, webcomponent routed out
  TEQ(lf.packages.getLength(), 2)
  TEQ(lf.packages[1].name, "alpha")
  TEQ(lf.packages[2].name, "zeta")
  TEQ(lf.packages[1].generoMajor, "4")
  TEQ(lf.packages[1].checksum, "cs-alpha")
  --requiredBy sorted
  TEQ(lf.packages[1].requiredBy[1], "<root>")
  TEQ(lf.packages[1].requiredBy[2], "zeta")
  --scope: prod stored as empty, dev kept
  TOK(lf.packages[2].scope IS NULL)
  TEQ(lf.packages[1].scope, "dev")

  --webcomponents
  TEQ(lf.webcomponents.getLength(), 1)
  TEQ(lf.webcomponents[1].name, "chart3d")
  TEQ(lf.webcomponents[1].checksum, "cs-chart")

  --jars sorted by key with maven URLs and scopes
  TEQ(lf.jars.getLength(), 2)
  TEQ(lf.jars[1].key, "com.google.code.gson:gson")
  TEQ(lf.jars[2].key, "org.apache.poi:poi")
  TEQ(lf.jars[1].scope, "dev")
  TOK(lf.jars[2].scope IS NULL)
  VAR poiPrefix = "https://repo1.maven.org/maven2/org/apache/poi/"
  TOK(fglpkgutils.startsWith(lf.jars[2].downloadUrl, poiPrefix))
END FUNCTION

FUNCTION testSaveLoad()
  DEFINE ok BOOLEAN
  DEFINE err STRING
  DEFINE lf2 lockfile.TLockfile
  VAR dir = fglpkgutils.makeTempDir()
  VAR lf = sampleLock()
  TOK(NOT lockfile.lockExists(dir))
  CALL lockfile.save(lf, dir) RETURNING ok, err
  TOK(ok)
  TOK(lockfile.lockExists(dir))
  CALL lockfile.load(dir) RETURNING ok, lf2, err
  TOK(ok)
  TEQ(lf2.version, 1)
  TEQ(lf2.packages.getLength(), 2)
  TEQ(lf2.packages[1].name, "alpha")
  TEQ(lf2.packages[1].requiredBy.getLength(), 2)
  TEQ(lf2.jars[1].key, "com.google.code.gson:gson")
  TEQ(lf2.webcomponents[1].name, "chart3d")
  --stable serialization
  TEQ(lockfile.toJSONString(lf2), lockfile.toJSONString(lf))
  CALL fglpkgutils.rmrf(dir)

  --missing file
  VAR dir2 = fglpkgutils.makeTempDir()
  CALL lockfile.load(dir2) RETURNING ok, lf2, err
  TOK(NOT ok)
  CALL fglpkgutils.rmrf(dir2)
END FUNCTION

FUNCTION testValidate()
  DEFINE res lockfile.TLockValidation
  VAR lf = sampleLock()
  VAR root = manifest.newManifest("myapp", "1.0.0", "", "")

  --clean when everything matches and no dir checks requested
  LET res = lockfile.validate(lf, root, "4.01.12", NULL, NULL)
  TOK(lockfile.validationIsClean(res))
  TOK(NOT lockfile.validationNeedsResolve(res))

  --genero mismatch is a warning, not a resolve trigger
  LET res = lockfile.validate(lf, root, "6.00.02", NULL, NULL)
  TOK(NOT lockfile.validationIsClean(res))
  TOK(NOT lockfile.validationNeedsResolve(res))
  TOK(fglpkgutils.contains(res.generoMismatch, "4.01.12"))

  --manifest identity change needs a resolve
  VAR root2 = manifest.newManifest("otherapp", "1.0.0", "", "")
  LET res = lockfile.validate(lf, root2, NULL, NULL, NULL)
  TOK(lockfile.validationNeedsResolve(res))
  TOK(fglpkgutils.contains(res.manifestMismatch, "project name"))
  VAR root3 = manifest.newManifest("myapp", "2.0.0", "", "")
  LET res = lockfile.validate(lf, root3, NULL, NULL, NULL)
  TOK(fglpkgutils.contains(res.manifestMismatch, "project version"))

  --unsupported schema version
  VAR lfBad = sampleLock()
  LET lfBad.version = 99
  LET res = lockfile.validate(lfBad, root, NULL, NULL, NULL)
  TOK(res.schemaError IS NOT NULL)
  TOK(lockfile.validationNeedsResolve(res))

  --missing package dirs
  VAR pdir = fglpkgutils.makeTempDir()
  CALL fglpkgutils.mkdirp(os.Path.join(pdir, "alpha"))
  LET res = lockfile.validate(lf, root, NULL, pdir, NULL)
  TEQ(res.missingPackages.getLength(), 1)
  TEQ(res.missingPackages[1], "zeta")
  --empty webcomponents dir counts as missing
  VAR wdir = os.Path.join(pdir, "webcomponents")
  CALL fglpkgutils.mkdirp(wdir)
  LET res = lockfile.validate(lf, root, NULL, pdir, wdir)
  TEQ(res.missingWebcomponents.getLength(), 1)
  CALL fglpkgutils.writeStringToFile(os.Path.join(wdir, "x.html"), "x")
  LET res = lockfile.validate(lf, root, NULL, pdir, wdir)
  TEQ(res.missingWebcomponents.getLength(), 0)
  CALL fglpkgutils.rmrf(pdir)
END FUNCTION

FUNCTION testFilterForProduction()
  VAR lf = sampleLock()
  VAR prod = lockfile.filterForProduction(lf)
  --alpha is dev scoped: dropped; zeta kept
  TEQ(prod.packages.getLength(), 1)
  TEQ(prod.packages[1].name, "zeta")
  --gson jar is dev scoped: dropped
  TEQ(prod.jars.getLength(), 1)
  TEQ(prod.jars[1].key, "org.apache.poi:poi")
  --webcomponent is prod: kept
  TEQ(prod.webcomponents.getLength(), 1)
END FUNCTION

FUNCTION testJSONShape()
  DEFINE plan resolver.TPlan
  VAR lf = sampleLock()
  VAR js = lockfile.toJSONString(lf)
  --canonical key order
  TOK(js.getIndexOf('"lockfileVersion"', 1) > 0)
  TOK(js.getIndexOf('"lockfileVersion"', 1) < js.getIndexOf('"generatedAt"', 1))
  TOK(js.getIndexOf('"generatedAt"', 1) < js.getIndexOf('"root"', 1))
  TOK(js.getIndexOf('"root"', 1) < js.getIndexOf('"packages"', 1))
  TOK(js.getIndexOf('"packages"', 1) < js.getIndexOf('"jars"', 1))
  --prod scope omitted from entries
  TOK(NOT fglpkgutils.contains(js, '"scope": "prod"'))
  TOK(fglpkgutils.contains(js, '"scope": "dev"'))
  --webcomponents omitted when empty
  LET plan.generoVersion = "4.01.12"
  LET plan.generoMajor = "4"
  VAR emptyLf =
      lockfile.fromPlan(plan, manifest.newManifest("m", "1.0.0", "", ""))
  TOK(NOT fglpkgutils.contains(lockfile.toJSONString(emptyLf), "webcomponents"))
  --but packages/jars arrays always present (empty)
  TOK(fglpkgutils.contains(lockfile.toJSONString(emptyLf), '"packages": []'))
END FUNCTION
