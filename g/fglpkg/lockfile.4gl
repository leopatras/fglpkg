#+ fglpkg.lock management — the reproducible install record
#+ port of internal/lockfile/lockfile.go (JSON, committed to VCS)
OPTIONS SHORT CIRCUIT
PACKAGE fglpkg
IMPORT os
IMPORT util
IMPORT FGL fglpkg.fglpkgutils
IMPORT FGL fglpkg.glob
IMPORT FGL fglpkg.manifest
IMPORT FGL fglpkg.resolver
&include "myassert.inc"

PUBLIC CONSTANT LOCK_FILENAME = "fglpkg.lock"
PUBLIC CONSTANT LOCK_VERSION = 1

PUBLIC TYPE TLockedPackage RECORD
  name STRING,
  version STRING,
  genero STRING, --Genero constraint declared by this package version
  downloadUrl STRING,
  checksum STRING,
  generoMajor STRING, --variant selected, e.g. "4"; empty for legacy
  requiredBy fglpkgutils.TStringArr,
  scope STRING --"", "dev" or "optional"
END RECORD

PUBLIC TYPE TLockedPackages DYNAMIC ARRAY OF TLockedPackage

PUBLIC TYPE TLockedWebcomponent RECORD
  name STRING,
  version STRING,
  downloadUrl STRING,
  checksum STRING,
  requiredBy fglpkgutils.TStringArr,
  scope STRING
END RECORD

PUBLIC TYPE TLockedWebcomponents DYNAMIC ARRAY OF TLockedWebcomponent

PUBLIC TYPE TLockedJAR RECORD
  key STRING, --groupId:artifactId
  groupId STRING,
  artifactId STRING,
  version STRING,
  downloadUrl STRING,
  checksum STRING,
  scope STRING
END RECORD

PUBLIC TYPE TLockedJARs DYNAMIC ARRAY OF TLockedJAR

PUBLIC TYPE TLockfile RECORD
  version INT ATTRIBUTES(json_name = "lockfileVersion"),
  generatedAt STRING,
  generoVersion STRING,
  rootManifest RECORD ATTRIBUTES(json_name = "root")
    name STRING,
    version STRING
  END RECORD,
  packages TLockedPackages,
  jars TLockedJARs,
  webcomponents TLockedWebcomponents
END RECORD

PUBLIC TYPE TLockValidation RECORD
  schemaError STRING,
  generoMismatch STRING, --formatted warning; empty when versions match
  manifestMismatch STRING, --formatted error; empty when identity matches
  missingPackages fglpkgutils.TStringArr,
  missingWebcomponents fglpkgutils.TStringArr
END RECORD

FUNCTION lockPath(dir STRING) RETURNS STRING
  RETURN os.Path.join(dir, LOCK_FILENAME)
END FUNCTION

FUNCTION lockExists(dir STRING) RETURNS BOOLEAN
  RETURN os.Path.exists(lockPath(dir))
END FUNCTION

#+builds a lock file from a resolved plan and the root manifest; packages
#+with variant "webcomponent" land in webcomponents, everything else in
#+packages; all arrays sorted for stable diffs
FUNCTION fromPlan(plan resolver.TPlan, root manifest.TManifest)
    RETURNS TLockfile
  DEFINE lf TLockfile
  DEFINE i, j INT
  DEFINE requiredBy fglpkgutils.TStringArr
  LET lf.version = LOCK_VERSION
  LET lf.generatedAt =
      util.Datetime.format(
          util.Datetime.getCurrentAsUTC(), "%Y-%m-%dT%H:%M:%SZ")
  LET lf.generoVersion = plan.generoVersion
  LET lf.rootManifest.name = root.name
  LET lf.rootManifest.version = root.version

  FOR i = 1 TO plan.packages.getLength()
    CALL requiredBy.clear()
    FOR j = 1 TO plan.packages[i].requiredBy.getLength()
      LET requiredBy[j] = plan.packages[i].requiredBy[j]
    END FOR
    CALL glob.sortBytewise(requiredBy)
    IF resolver.isWebcomponentPackage(plan.packages[i]) THEN
      VAR wi = lf.webcomponents.getLength() + 1
      LET lf.webcomponents[wi].name = plan.packages[i].name
      LET lf.webcomponents[wi].version = plan.packages[i].version
      LET lf.webcomponents[wi].downloadUrl = plan.packages[i].downloadURL
      LET lf.webcomponents[wi].checksum = plan.packages[i].checksum
      CALL requiredBy.copyTo(lf.webcomponents[wi].requiredBy)
      LET lf.webcomponents[wi].scope = scopeLockString(plan.packages[i].scope)
    ELSE
      VAR pi = lf.packages.getLength() + 1
      LET lf.packages[pi].name = plan.packages[i].name
      LET lf.packages[pi].version = plan.packages[i].version
      LET lf.packages[pi].downloadUrl = plan.packages[i].downloadURL
      LET lf.packages[pi].checksum = plan.packages[i].checksum
      LET lf.packages[pi].generoMajor = plan.generoMajor
      CALL requiredBy.copyTo(lf.packages[pi].requiredBy)
      LET lf.packages[pi].scope = scopeLockString(plan.packages[i].scope)
    END IF
  END FOR
  CALL sortPackagesByName(lf.packages)
  CALL sortWebcomponentsByName(lf.webcomponents)

  FOR i = 1 TO plan.jars.getLength()
    LET lf.jars[i].key = manifest.javaKey(plan.jars[i])
    LET lf.jars[i].groupId = plan.jars[i].groupId
    LET lf.jars[i].artifactId = plan.jars[i].artifactId
    LET lf.jars[i].version = plan.jars[i].version
    LET lf.jars[i].downloadUrl = manifest.mavenURL(plan.jars[i])
    LET lf.jars[i].checksum = plan.jars[i].checksum
    IF plan.jarScopes.contains(lf.jars[i].key) THEN
      LET lf.jars[i].scope = scopeLockString(plan.jarScopes[lf.jars[i].key])
    END IF
  END FOR
  CALL sortJarsByKey(lf.jars)
  RETURN lf
END FUNCTION

#+production is stored as "" (omitted) so the lock file stays compact
PRIVATE FUNCTION scopeLockString(s STRING) RETURNS STRING
  IF s == manifest.SCOPE_DEV THEN
    RETURN "dev"
  END IF
  IF s == manifest.SCOPE_OPTIONAL THEN
    RETURN "optional"
  END IF
  RETURN NULL
END FUNCTION

#+writes the lock file as formatted JSON to dir/fglpkg.lock
FUNCTION save(lf TLockfile, dir STRING) RETURNS(BOOLEAN, STRING)
  VAR path = lockPath(dir)
  TRY
    CALL fglpkgutils.writeStringToFile(path, toJSONString(lf) || "\n")
  CATCH
    RETURN FALSE, SFMT("cannot write %1: %2", path, err_get(status))
  END TRY
  RETURN TRUE, NULL
END FUNCTION

#+serializes with canonical field order and Go-compatible omission rules
FUNCTION toJSONString(lf TLockfile) RETURNS STRING
  DEFINE i INT
  VAR obj = util.JSONObject.create()
  CALL obj.put("lockfileVersion", lf.version)
  CALL obj.put("generatedAt", NVL(lf.generatedAt, ""))
  CALL obj.put("generoVersion", NVL(lf.generoVersion, ""))
  VAR rootObj = util.JSONObject.create()
  CALL rootObj.put("name", NVL(lf.rootManifest.name, ""))
  CALL rootObj.put("version", NVL(lf.rootManifest.version, ""))
  CALL obj.put("root", rootObj)
  VAR pkgs = util.JSONArray.create()
  FOR i = 1 TO lf.packages.getLength()
    CALL pkgs.put(pkgs.getLength() + 1, lockedPackageToJSON(lf.packages[i]))
  END FOR
  CALL obj.put("packages", pkgs)
  VAR jars = util.JSONArray.create()
  FOR i = 1 TO lf.jars.getLength()
    CALL jars.put(jars.getLength() + 1, lockedJarToJSON(lf.jars[i]))
  END FOR
  CALL obj.put("jars", jars)
  IF lf.webcomponents.getLength() > 0 THEN
    VAR wcs = util.JSONArray.create()
    FOR i = 1 TO lf.webcomponents.getLength()
      CALL wcs.put(wcs.getLength() + 1,
          lockedWebcomponentToJSON(lf.webcomponents[i]))
    END FOR
    CALL obj.put("webcomponents", wcs)
  END IF
  RETURN manifest.prettyJSON(obj.toString())
END FUNCTION

PRIVATE FUNCTION lockedPackageToJSON(p TLockedPackage) RETURNS util.JSONObject
  DEFINE i INT
  VAR obj = util.JSONObject.create()
  CALL obj.put("name", NVL(p.name, ""))
  CALL obj.put("version", NVL(p.version, ""))
  IF p.genero IS NOT NULL THEN
    CALL obj.put("genero", p.genero)
  END IF
  CALL obj.put("downloadUrl", NVL(p.downloadUrl, ""))
  IF p.checksum IS NOT NULL THEN
    CALL obj.put("checksum", p.checksum)
  END IF
  IF p.generoMajor IS NOT NULL THEN
    CALL obj.put("generoMajor", p.generoMajor)
  END IF
  VAR arr = util.JSONArray.create()
  FOR i = 1 TO p.requiredBy.getLength()
    CALL arr.put(arr.getLength() + 1, p.requiredBy[i])
  END FOR
  CALL obj.put("requiredBy", arr)
  IF p.scope IS NOT NULL THEN
    CALL obj.put("scope", p.scope)
  END IF
  RETURN obj
END FUNCTION

PRIVATE FUNCTION lockedWebcomponentToJSON(w TLockedWebcomponent)
    RETURNS util.JSONObject
  DEFINE i INT
  VAR obj = util.JSONObject.create()
  CALL obj.put("name", NVL(w.name, ""))
  CALL obj.put("version", NVL(w.version, ""))
  CALL obj.put("downloadUrl", NVL(w.downloadUrl, ""))
  IF w.checksum IS NOT NULL THEN
    CALL obj.put("checksum", w.checksum)
  END IF
  VAR arr = util.JSONArray.create()
  FOR i = 1 TO w.requiredBy.getLength()
    CALL arr.put(arr.getLength() + 1, w.requiredBy[i])
  END FOR
  CALL obj.put("requiredBy", arr)
  IF w.scope IS NOT NULL THEN
    CALL obj.put("scope", w.scope)
  END IF
  RETURN obj
END FUNCTION

PRIVATE FUNCTION lockedJarToJSON(j TLockedJAR) RETURNS util.JSONObject
  VAR obj = util.JSONObject.create()
  CALL obj.put("key", NVL(j.key, ""))
  CALL obj.put("groupId", NVL(j.groupId, ""))
  CALL obj.put("artifactId", NVL(j.artifactId, ""))
  CALL obj.put("version", NVL(j.version, ""))
  CALL obj.put("downloadUrl", NVL(j.downloadUrl, ""))
  IF j.checksum IS NOT NULL THEN
    CALL obj.put("checksum", j.checksum)
  END IF
  IF j.scope IS NOT NULL THEN
    CALL obj.put("scope", j.scope)
  END IF
  RETURN obj
END FUNCTION

#+reads and parses the lock file from dir/fglpkg.lock
FUNCTION load(dir STRING) RETURNS(BOOLEAN, TLockfile, STRING)
  DEFINE lf, empty TLockfile
  VAR path = lockPath(dir)
  IF NOT os.Path.exists(path) THEN
    RETURN FALSE, empty, SFMT("%1: no such file", path)
  END IF
  TRY
    CALL util.JSON.parse(fglpkgutils.readTextFile(path), lf)
  CATCH
    RETURN FALSE, empty, SFMT("invalid %1", LOCK_FILENAME)
  END TRY
  RETURN TRUE, lf, NULL
END FUNCTION

#+checks whether the lock file is consistent with the current environment
#+and manifest; currentGenero may be NULL to skip that check, likewise
#+packagesDir / webcomponentsDir for the on-disk presence checks
FUNCTION validate(
    lf TLockfile, root manifest.TManifest, currentGenero STRING,
    packagesDir STRING, webcomponentsDir STRING)
    RETURNS TLockValidation
  DEFINE res TLockValidation
  DEFINE i INT

  IF lf.version != LOCK_VERSION THEN
    LET res.schemaError =
        SFMT("lock file schema version %1 is not supported (expected %2)",
            lf.version, LOCK_VERSION)
    RETURN res --nothing else makes sense to check
  END IF

  --Genero version check: warning only
  IF currentGenero IS NOT NULL AND lf.generoVersion != currentGenero THEN
    LET res.generoMismatch =
        SFMT("lock file was generated with Genero %1 but current runtime is %2.\nRun 'fglpkg install' to re-resolve for the current Genero version.",
            lf.generoVersion, currentGenero)
  END IF

  --root manifest identity check
  IF lf.rootManifest.name != root.name THEN
    LET res.manifestMismatch = staleMsg("project name",
        lf.rootManifest.name, root.name)
  ELSE
    IF lf.rootManifest.version != root.version THEN
      LET res.manifestMismatch = staleMsg("project version",
          lf.rootManifest.version, root.version)
    END IF
  END IF

  --on-disk presence checks
  IF packagesDir IS NOT NULL THEN
    FOR i = 1 TO lf.packages.getLength()
      IF NOT os.Path.exists(os.Path.join(packagesDir, lf.packages[i].name)) THEN
        LET res.missingPackages[res.missingPackages.getLength() + 1] =
            lf.packages[i].name
      END IF
    END FOR
  END IF
  IF webcomponentsDir IS NOT NULL THEN
    FOR i = 1 TO lf.webcomponents.getLength()
      IF NOT dirNonEmpty(webcomponentsDir) THEN
        LET res.missingWebcomponents[res.missingWebcomponents.getLength() + 1] =
            lf.webcomponents[i].name
      END IF
    END FOR
  END IF
  RETURN res
END FUNCTION

PRIVATE FUNCTION staleMsg(field STRING, inLock STRING, inManifest STRING)
    RETURNS STRING
  RETURN SFMT('lock file is stale: %1 changed from "%2" (lock) to "%3" (manifest).\nRun \'fglpkg install\' to update the lock file.',
      field, inLock, inManifest)
END FUNCTION

PRIVATE FUNCTION dirNonEmpty(dir STRING) RETURNS BOOLEAN
  DEFINE entry STRING
  IF NOT os.Path.exists(dir) THEN
    RETURN FALSE
  END IF
  VAR h = os.Path.dirOpen(dir)
  WHILE h > 0
    LET entry = os.Path.dirNext(h)
    IF entry IS NULL THEN
      EXIT WHILE
    END IF
    IF entry == "." OR entry == ".." THEN
      CONTINUE WHILE
    END IF
    CALL os.Path.dirClose(h)
    RETURN TRUE
  END WHILE
  IF h > 0 THEN
    CALL os.Path.dirClose(h)
  END IF
  RETURN FALSE
END FUNCTION

FUNCTION validationIsClean(res TLockValidation) RETURNS BOOLEAN
  RETURN res.schemaError IS NULL
      AND res.generoMismatch IS NULL
      AND res.manifestMismatch IS NULL
      AND res.missingPackages.getLength() == 0
      AND res.missingWebcomponents.getLength() == 0
END FUNCTION

#+a full re-resolution is required (schema incompatible or manifest changed)
FUNCTION validationNeedsResolve(res TLockValidation) RETURNS BOOLEAN
  RETURN res.schemaError IS NOT NULL OR res.manifestMismatch IS NOT NULL
END FUNCTION

#+returns the subset to install for `--production`: everything except
#+dev-scoped entries (optional entries are retained)
FUNCTION filterForProduction(lf TLockfile) RETURNS TLockfile
  DEFINE out TLockfile
  DEFINE i INT
  LET out.version = lf.version
  LET out.generatedAt = lf.generatedAt
  LET out.generoVersion = lf.generoVersion
  LET out.rootManifest = lf.rootManifest
  FOR i = 1 TO lf.packages.getLength()
    IF lf.packages[i].scope == "dev" THEN
      CONTINUE FOR
    END IF
    LET out.packages[out.packages.getLength() + 1] = lf.packages[i]
  END FOR
  FOR i = 1 TO lf.jars.getLength()
    IF lf.jars[i].scope == "dev" THEN
      CONTINUE FOR
    END IF
    LET out.jars[out.jars.getLength() + 1] = lf.jars[i]
  END FOR
  FOR i = 1 TO lf.webcomponents.getLength()
    IF lf.webcomponents[i].scope == "dev" THEN
      CONTINUE FOR
    END IF
    LET out.webcomponents[out.webcomponents.getLength() + 1] =
        lf.webcomponents[i]
  END FOR
  RETURN out
END FUNCTION

--─── sorting helpers ────────────────────────────────────────────────────────

PRIVATE FUNCTION sortPackagesByName(arr TLockedPackages)
  DEFINE i, j INT
  DEFINE tmp TLockedPackage
  FOR i = 2 TO arr.getLength()
    LET j = i
    WHILE j > 1 AND fglpkgutils.cmpBytes(arr[j].name, arr[j - 1].name) < 0
      LET tmp = arr[j]
      LET arr[j] = arr[j - 1]
      LET arr[j - 1] = tmp
      LET j = j - 1
    END WHILE
  END FOR
END FUNCTION

PRIVATE FUNCTION sortWebcomponentsByName(arr TLockedWebcomponents)
  DEFINE i, j INT
  DEFINE tmp TLockedWebcomponent
  FOR i = 2 TO arr.getLength()
    LET j = i
    WHILE j > 1 AND fglpkgutils.cmpBytes(arr[j].name, arr[j - 1].name) < 0
      LET tmp = arr[j]
      LET arr[j] = arr[j - 1]
      LET arr[j - 1] = tmp
      LET j = j - 1
    END WHILE
  END FOR
END FUNCTION

PRIVATE FUNCTION sortJarsByKey(arr TLockedJARs)
  DEFINE i, j INT
  DEFINE tmp TLockedJAR
  FOR i = 2 TO arr.getLength()
    LET j = i
    WHILE j > 1 AND fglpkgutils.cmpBytes(arr[j].key, arr[j - 1].key) < 0
      LET tmp = arr[j]
      LET arr[j] = arr[j - 1]
      LET arr[j - 1] = tmp
      LET j = j - 1
    END WHILE
  END FOR
END FUNCTION
