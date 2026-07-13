#+ fglpkg.json parsing, validation and serialization
#+ port of internal/manifest/manifest.go
OPTIONS SHORT CIRCUIT
PACKAGE fglpkg
IMPORT util
IMPORT os
IMPORT FGL fglpkg.fglpkgutils
IMPORT FGL fglpkg.semver
IMPORT FGL fglpkg.glob
&include "myassert.inc"

PUBLIC CONSTANT MANIFEST_FILENAME = "fglpkg.json"

PUBLIC CONSTANT SCOPE_PROD = "prod"
PUBLIC CONSTANT SCOPE_DEV = "dev"
PUBLIC CONSTANT SCOPE_OPTIONAL = "optional"

PUBLIC TYPE TJavaDependency RECORD
  groupId STRING,
  artifactId STRING,
  version STRING,
  checksum STRING, --expected SHA256 hex digest of the JAR (empty: skip check)
  jar STRING, --local jar file name override
  url STRING --download URL override
END RECORD

PUBLIC TYPE TJavaDependencies DYNAMIC ARRAY OF TJavaDependency

PUBLIC TYPE THookOp RECORD
  op STRING, --"copy-files" or "mkdir"
  src STRING ATTRIBUTES(json_name = "from"),
  dst STRING ATTRIBUTES(json_name = "to"),
  path STRING
END RECORD

PUBLIC TYPE THookOps DYNAMIC ARRAY OF THookOp

PUBLIC TYPE TDependencies RECORD
  fgl DICTIONARY OF STRING, --name -> version constraint
  java TJavaDependencies
END RECORD

PUBLIC TYPE TManifest RECORD
  schema STRING ATTRIBUTES(json_name = "$schema"),
  typ STRING ATTRIBUTES(json_name = "type"), --accepted but ignored (legacy)
  name STRING,
  version STRING,
  description STRING,
  author STRING,
  license STRING,
  repository STRING,
  keywords DYNAMIC ARRAY OF STRING,
  main STRING, --primary .42m entry point
  visibility STRING, --"public" (default) or "private"
  genero STRING, --Genero BDL version constraint
  dependencies TDependencies,
  devDependencies TDependencies,
  optionalDependencies TDependencies,
  root STRING, --base directory for package files (default ".")
  files DYNAMIC ARRAY OF STRING, --glob patterns for the package zip
  bin DICTIONARY OF STRING, --command name -> script path
  docs DYNAMIC ARRAY OF STRING, --glob patterns for doc files
  programs DYNAMIC ARRAY OF STRING, --modules with MAIN blocks
  webcomponents DYNAMIC ARRAY OF STRING, --COMPONENTTYPE names
  hooks DICTIONARY OF THookOps --lifecycle event -> ordered operations
END RECORD

#+creates a new manifest with sensible defaults
FUNCTION newManifest(
    name STRING, version STRING, description STRING, author STRING)
    RETURNS TManifest
  DEFINE m TManifest
  LET m.name = name
  LET m.version = version
  LET m.description = description
  LET m.author = author
  LET m.license = "UNLICENSED"
  RETURN m
END FUNCTION

FUNCTION manifestPath(dir STRING) RETURNS STRING
  RETURN os.Path.join(dir, MANIFEST_FILENAME)
END FUNCTION

FUNCTION manifestExists(dir STRING) RETURNS BOOLEAN
  RETURN os.Path.exists(manifestPath(dir))
END FUNCTION

#+reads and parses fglpkg.json from dir; unknown fields anywhere in the
#+schema produce an error rather than being silently ignored
FUNCTION load(dir STRING) RETURNS(BOOLEAN, TManifest, STRING)
  DEFINE m, empty TManifest
  DEFINE ok BOOLEAN
  DEFINE err STRING
  VAR path = manifestPath(dir)
  IF NOT os.Path.exists(path) THEN
    RETURN FALSE, empty, SFMT("%1: no such file", path)
  END IF
  VAR text = fglpkgutils.readTextFile(path)
  CALL loadFromString(text) RETURNING ok, m, err
  RETURN ok, m, err
END FUNCTION

#+parses a manifest from a JSON string with strict unknown field checks
FUNCTION loadFromString(text STRING) RETURNS(BOOLEAN, TManifest, STRING)
  DEFINE m, empty TManifest
  DEFINE obj util.JSONObject
  TRY
    LET obj = util.JSONObject.parse(text)
  CATCH
    RETURN FALSE, empty,
        SFMT("invalid %1: malformed JSON", MANIFEST_FILENAME)
  END TRY
  VAR err = checkUnknownFields(obj)
  IF err IS NOT NULL THEN
    RETURN FALSE, empty, SFMT("invalid %1: %2", MANIFEST_FILENAME, err)
  END IF
  TRY
    CALL util.JSON.parse(text, m)
  CATCH
    RETURN FALSE, empty,
        SFMT("invalid %1: %2", MANIFEST_FILENAME, err_get(status))
  END TRY
  RETURN TRUE, m, NULL
END FUNCTION

#+loads fglpkg.json if it exists, otherwise returns a blank manifest
FUNCTION loadOrNew(dir STRING) RETURNS(BOOLEAN, TManifest, STRING)
  DEFINE m TManifest
  DEFINE ok BOOLEAN
  DEFINE err STRING
  IF NOT manifestExists(dir) THEN
    VAR base = os.Path.baseName(os.Path.fullPath(dir))
    LET m = newManifest(base, "0.1.0", "", "")
    RETURN TRUE, m, NULL
  END IF
  CALL load(dir) RETURNING ok, m, err
  RETURN ok, m, err
END FUNCTION

#+writes the manifest as formatted JSON to dir/fglpkg.json
FUNCTION save(m TManifest, dir STRING) RETURNS(BOOLEAN, STRING)
  TRY
    CALL fglpkgutils.writeStringToFile(
        manifestPath(dir), toJSONString(m) || "\n")
  CATCH
    RETURN FALSE, SFMT("failed to write %1: %2",
        manifestPath(dir), err_get(status))
  END TRY
  RETURN TRUE, NULL
END FUNCTION

#+returns a copy stripped of dev-only fields for publishing
FUNCTION publishCopy(m TManifest) RETURNS TManifest
  DEFINE clone TManifest
  --record assignment shares dictionary/array references: serialize the
  --record and parse it back to get a true deep copy
  CALL util.JSON.parse(util.JSON.stringify(m), clone)
  CALL clone.devDependencies.fgl.clear()
  CALL clone.devDependencies.java.clear()
  RETURN clone
END FUNCTION

--─── dependency mutators ────────────────────────────────────────────────────

#+adds or updates a BDL dependency in the given scope; any declaration in
#+a different scope is removed so a name lives in exactly one bucket
FUNCTION addFGLDependencyScoped(
    m TManifest INOUT, name STRING, version STRING, scope STRING)
  IF scope != SCOPE_PROD THEN
    CALL m.dependencies.fgl.remove(name)
  END IF
  IF scope != SCOPE_DEV THEN
    CALL m.devDependencies.fgl.remove(name)
  END IF
  IF scope != SCOPE_OPTIONAL THEN
    CALL m.optionalDependencies.fgl.remove(name)
  END IF
  CASE scope
    WHEN SCOPE_DEV
      LET m.devDependencies.fgl[name] = version
    WHEN SCOPE_OPTIONAL
      LET m.optionalDependencies.fgl[name] = version
    OTHERWISE
      LET m.dependencies.fgl[name] = version
  END CASE
END FUNCTION

FUNCTION addFGLDependency(m TManifest INOUT, name STRING, version STRING)
  CALL addFGLDependencyScoped(m, name, version, SCOPE_PROD)
END FUNCTION

#+removes a BDL dependency from whichever scope it lives in;
#+returns the scope it was removed from, or NULL if not present
FUNCTION removeFGLDependency(m TManifest INOUT, name STRING) RETURNS STRING
  IF m.dependencies.fgl.contains(name) THEN
    CALL m.dependencies.fgl.remove(name)
    RETURN SCOPE_PROD
  END IF
  IF m.devDependencies.fgl.contains(name) THEN
    CALL m.devDependencies.fgl.remove(name)
    RETURN SCOPE_DEV
  END IF
  IF m.optionalDependencies.fgl.contains(name) THEN
    CALL m.optionalDependencies.fgl.remove(name)
    RETURN SCOPE_OPTIONAL
  END IF
  RETURN NULL
END FUNCTION

#+returns the version constraint and scope for the named package,
#+or NULL, NULL if it is not declared in any scope
FUNCTION findFGLDependency(m TManifest, name STRING) RETURNS(STRING, STRING)
  IF m.dependencies.fgl.contains(name) THEN
    RETURN m.dependencies.fgl[name], SCOPE_PROD
  END IF
  IF m.devDependencies.fgl.contains(name) THEN
    RETURN m.devDependencies.fgl[name], SCOPE_DEV
  END IF
  IF m.optionalDependencies.fgl.contains(name) THEN
    RETURN m.optionalDependencies.fgl[name], SCOPE_OPTIONAL
  END IF
  RETURN NULL, NULL
END FUNCTION

#+unique key of a Java dependency (groupId:artifactId)
FUNCTION javaKey(dep TJavaDependency) RETURNS STRING
  RETURN SFMT("%1:%2", dep.groupId, dep.artifactId)
END FUNCTION

#+adds or replaces a Java dependency by key in the given scope, removing
#+it from other scopes so it appears in exactly one bucket
FUNCTION addJavaDependencyScoped(
    m TManifest INOUT, dep TJavaDependency, scope STRING)
  DEFINE i INT
  IF scope != SCOPE_PROD THEN
    CALL removeJavaKeyFromArr(m.dependencies.java, javaKey(dep)) RETURNING status
  END IF
  IF scope != SCOPE_DEV THEN
    CALL removeJavaKeyFromArr(m.devDependencies.java, javaKey(dep)) RETURNING status
  END IF
  IF scope != SCOPE_OPTIONAL THEN
    CALL removeJavaKeyFromArr(m.optionalDependencies.java, javaKey(dep)) RETURNING status
  END IF
  CASE scope
    WHEN SCOPE_DEV
      CALL putJava(m.devDependencies.java, dep)
    WHEN SCOPE_OPTIONAL
      CALL putJava(m.optionalDependencies.java, dep)
    OTHERWISE
      CALL putJava(m.dependencies.java, dep)
  END CASE
  UNUSED_VAR(i)
END FUNCTION

FUNCTION addJavaDependency(m TManifest INOUT, dep TJavaDependency)
  CALL addJavaDependencyScoped(m, dep, SCOPE_PROD)
END FUNCTION

PRIVATE FUNCTION putJava(arr TJavaDependencies, dep TJavaDependency)
  DEFINE i INT
  FOR i = 1 TO arr.getLength()
    IF javaKey(arr[i]) == javaKey(dep) THEN
      LET arr[i] = dep
      RETURN
    END IF
  END FOR
  LET arr[arr.getLength() + 1] = dep
END FUNCTION

#+removes a Java dependency by key from whichever scope it lives in;
#+returns the scope it was removed from, or NULL if not present
FUNCTION removeJavaDependency(m TManifest INOUT, key STRING) RETURNS STRING
  IF removeJavaKeyFromArr(m.dependencies.java, key) THEN
    RETURN SCOPE_PROD
  END IF
  IF removeJavaKeyFromArr(m.devDependencies.java, key) THEN
    RETURN SCOPE_DEV
  END IF
  IF removeJavaKeyFromArr(m.optionalDependencies.java, key) THEN
    RETURN SCOPE_OPTIONAL
  END IF
  RETURN NULL
END FUNCTION

PRIVATE FUNCTION removeJavaKeyFromArr(arr TJavaDependencies, key STRING)
    RETURNS BOOLEAN
  DEFINE i INT
  DEFINE removed BOOLEAN
  LET i = 1
  WHILE i <= arr.getLength()
    IF javaKey(arr[i]) == key THEN
      CALL arr.deleteElement(i)
      LET removed = TRUE
    ELSE
      LET i = i + 1
    END IF
  END WHILE
  RETURN removed
END FUNCTION

--─── derived info ───────────────────────────────────────────────────────────

#+Maven Central download URL for a JAR
FUNCTION mavenURL(dep TJavaDependency) RETURNS STRING
  IF dep.url IS NOT NULL THEN
    RETURN dep.url
  END IF
  VAR groupPath = fglpkgutils.replace(dep.groupId, ".", "/")
  VAR jar = dep.jar
  IF jar IS NULL THEN
    LET jar = SFMT("%1-%2.jar", dep.artifactId, dep.version)
  END IF
  RETURN SFMT("https://repo1.maven.org/maven2/%1/%2/%3/%4",
      groupPath, dep.artifactId, dep.version, jar)
END FUNCTION

#+local filename to use when saving this JAR
FUNCTION jarFileName(dep TJavaDependency) RETURNS STRING
  IF dep.jar IS NOT NULL THEN
    RETURN dep.jar
  END IF
  RETURN SFMT("%1-%2.jar", dep.artifactId, dep.version)
END FUNCTION

#+deduplicated bin script paths, sorted for deterministic ordering
FUNCTION binFiles(m TManifest) RETURNS fglpkgutils.TStringArr
  DEFINE paths fglpkgutils.TStringArr
  DEFINE seen DICTIONARY OF BOOLEAN
  DEFINE i INT
  VAR keys = m.bin.getKeys()
  FOR i = 1 TO keys.getLength()
    VAR p = m.bin[keys[i]]
    IF NOT seen.contains(p) THEN
      LET seen[p] = TRUE
      LET paths[paths.getLength() + 1] = p
    END IF
  END FOR
  CALL glob.sortBytewise(paths)
  RETURN paths
END FUNCTION

FUNCTION hasWebcomponents(m TManifest) RETURNS BOOLEAN
  RETURN m.webcomponents.getLength() > 0
END FUNCTION

#+reports whether the manifest declares any BDL-side assets (triggers the
#+per-Genero-major variant fan-out at publish time)
FUNCTION hasBDLContent(m TManifest) RETURNS BOOLEAN
  IF m.main IS NOT NULL OR m.root IS NOT NULL THEN
    RETURN TRUE
  END IF
  IF m.files.getLength() > 0
      OR m.programs.getLength() > 0
      OR m.bin.getLength() > 0 THEN
    RETURN TRUE
  END IF
  RETURN m.dependencies.java.getLength() > 0
      OR m.devDependencies.java.getLength() > 0
      OR m.optionalDependencies.java.getLength() > 0
END FUNCTION

--─── validation ─────────────────────────────────────────────────────────────

#+basic sanity checks on the manifest
FUNCTION validate(m TManifest) RETURNS(BOOLEAN, STRING)
  DEFINE i INT
  DEFINE ok BOOLEAN
  DEFINE c semver.TConstraint
  DEFINE cerr STRING
  IF m.name IS NULL THEN
    RETURN FALSE, "manifest missing required field: name"
  END IF
  IF m.version IS NULL THEN
    RETURN FALSE, "manifest missing required field: version"
  END IF
  VAR err = validateWebcomponentNames(m)
  IF err IS NOT NULL THEN
    RETURN FALSE, err
  END IF
  IF m.genero IS NOT NULL AND m.genero != "*" THEN
    CALL semver.parseConstraint(m.genero) RETURNING ok, c, cerr
    IF NOT ok THEN
      RETURN FALSE, SFMT('invalid genero constraint "%1": %2', m.genero, cerr)
    END IF
  END IF
  LET err = validateJavaDeps(m.dependencies.java)
  IF err IS NULL THEN
    LET err = validateJavaDeps(m.devDependencies.java)
  END IF
  IF err IS NULL THEN
    LET err = validateJavaDeps(m.optionalDependencies.java)
  END IF
  IF err IS NOT NULL THEN
    RETURN FALSE, err
  END IF
  VAR cmds = m.bin.getKeys()
  FOR i = 1 TO cmds.getLength()
    VAR cmd = cmds[i]
    VAR scriptPath = m.bin[cmd]
    IF cmd.getLength() == 0 THEN
      RETURN FALSE, "bin command name must not be empty"
    END IF
    IF fglpkgutils.contains(cmd, "/") OR fglpkgutils.contains(cmd, "\\") THEN
      RETURN FALSE,
          SFMT('bin command name "%1" must not contain path separators', cmd)
    END IF
    IF scriptPath.getLength() == 0 THEN
      RETURN FALSE,
          SFMT('bin script path for command "%1" must not be empty', cmd)
    END IF
    IF fglpkgutils.isAbsolutePath(scriptPath) THEN
      RETURN FALSE,
          SFMT('bin script path "%1" for command "%2" must be relative',
              scriptPath, cmd)
    END IF
  END FOR
  FOR i = 1 TO m.docs.getLength()
    --strip doublestar segments for validation (pathMatch has no "**")
    VAR cleaned = fglpkgutils.replace(m.docs[i], "**", "star")
    IF NOT glob.patternValid(cleaned) THEN
      RETURN FALSE, SFMT('invalid docs glob pattern "%1"', m.docs[i])
    END IF
  END FOR
  VAR events = m.hooks.getKeys()
  FOR i = 1 TO events.getLength()
    VAR ops = m.hooks[events[i]]
    VAR j INT
    FOR j = 1 TO ops.getLength()
      LET err = validateHookOp(ops[j])
      IF err IS NOT NULL THEN
        --Go indexes hook ops from 0
        RETURN FALSE, SFMT("hooks.%1[%2]: %3", events[i], j - 1, err)
      END IF
    END FOR
  END FOR
  RETURN TRUE, NULL
END FUNCTION

#+structural checks plus the extra required fields for publishing:
#+description, license, repository, author (all missing fields listed)
FUNCTION validateForPublish(m TManifest) RETURNS(BOOLEAN, STRING)
  DEFINE ok BOOLEAN
  DEFINE err STRING
  DEFINE missing fglpkgutils.TStringArr
  CALL validate(m) RETURNING ok, err
  IF NOT ok THEN
    RETURN FALSE, err
  END IF
  IF m.description.trim().getLength() == 0 THEN
    LET missing[missing.getLength() + 1] = "  - description is required"
  END IF
  IF m.license.trim().getLength() == 0 THEN
    LET missing[missing.getLength() + 1] =
        '  - license is required (e.g. "MIT", "Apache-2.0")'
  END IF
  IF m.repository.trim().getLength() == 0 THEN
    LET missing[missing.getLength() + 1] =
        '  - repository is required (e.g. "https://github.com/owner/repo")'
  END IF
  IF m.author.trim().getLength() == 0 THEN
    LET missing[missing.getLength() + 1] = "  - author is required"
  END IF
  IF missing.getLength() == 0 THEN
    RETURN TRUE, NULL
  END IF
  RETURN FALSE, SFMT("manifest is not ready to publish:\n%1",
      fglpkgutils.joinArr(missing, "\n"))
END FUNCTION

PRIVATE FUNCTION validateJavaDeps(arr TJavaDependencies) RETURNS STRING
  DEFINE i INT
  FOR i = 1 TO arr.getLength()
    IF arr[i].groupId IS NULL
        OR arr[i].artifactId IS NULL
        OR arr[i].version IS NULL THEN
      RETURN SFMT(
          "java dependency missing required fields (groupId, artifactId, version): %1",
          util.JSON.stringify(arr[i]))
    END IF
  END FOR
  RETURN NULL
END FUNCTION

#+COMPONENTTYPE lexical rule: alphanumeric leading character followed by
#+letters, digits, underscore or hyphen
FUNCTION isValidComponentType(name STRING) RETURNS BOOLEAN
  DEFINE i INT
  IF name.getLength() == 0 THEN
    RETURN FALSE
  END IF
  FOR i = 1 TO name.getLength()
    VAR c = name.getCharAt(i)
    IF fglpkgutils.isLetter(c) OR fglpkgutils.isDigit(c) THEN
      CONTINUE FOR
    END IF
    IF i > 1 AND (c == "_" OR c == "-") THEN
      CONTINUE FOR
    END IF
    RETURN FALSE
  END FOR
  RETURN TRUE
END FUNCTION

PRIVATE FUNCTION validateWebcomponentNames(m TManifest) RETURNS STRING
  DEFINE i INT
  DEFINE seen DICTIONARY OF BOOLEAN
  FOR i = 1 TO m.webcomponents.getLength()
    VAR name = m.webcomponents[i]
    IF NOT isValidComponentType(name) THEN
      RETURN SFMT(
          'invalid COMPONENTTYPE "%1" in "webcomponents": must match ^[A-Za-z0-9][A-Za-z0-9_-]*$',
          name)
    END IF
    IF seen.contains(name) THEN
      RETURN SFMT('duplicate COMPONENTTYPE "%1" in "webcomponents"', name)
    END IF
    LET seen[name] = TRUE
  END FOR
  RETURN NULL
END FUNCTION

#+per-operation required fields and the shared path-safety rules
PRIVATE FUNCTION validateHookOp(op THookOp) RETURNS STRING
  DEFINE err STRING
  CASE op.op
    WHEN "copy-files"
      IF op.src IS NULL THEN
        RETURN 'copy-files: "from" is required'
      END IF
      IF op.dst IS NULL THEN
        RETURN 'copy-files: "to" is required'
      END IF
      IF op.path IS NOT NULL THEN
        RETURN 'copy-files: "path" is not valid (use "from"/"to")'
      END IF
      LET err = safeRelPath("from", op.src)
      IF err IS NOT NULL THEN
        RETURN err
      END IF
      LET err = safeRelPath("to", op.dst)
      IF err IS NOT NULL THEN
        RETURN err
      END IF
    WHEN "mkdir"
      IF op.path IS NULL THEN
        RETURN 'mkdir: "path" is required'
      END IF
      IF op.src IS NOT NULL OR op.dst IS NOT NULL THEN
        RETURN 'mkdir: only "path" is valid (got "from"/"to")'
      END IF
      LET err = safeRelPath("path", op.path)
      IF err IS NOT NULL THEN
        RETURN err
      END IF
    OTHERWISE
      RETURN SFMT('unknown op "%1"', op.op)
  END CASE
  RETURN NULL
END FUNCTION

#+rejects absolute paths and any path escaping its base via ".." segments
FUNCTION safeRelPath(field STRING, p STRING) RETURNS STRING
  DEFINE i INT
  IF p.getLength() == 0 THEN
    RETURN SFMT("%1 must not be empty", field)
  END IF
  IF fglpkgutils.isAbsolutePath(p) OR fglpkgutils.startsWith(p, "/") THEN
    RETURN SFMT('%1 "%2" must be relative, not absolute', field, p)
  END IF
  VAR segs = fglpkgutils.splitOnChar(fglpkgutils.backslash2slash(p), "/")
  FOR i = 1 TO segs.getLength()
    IF segs[i] == ".." THEN
      RETURN SFMT('%1 "%2" must not escape the package root with ..',
          field, p)
    END IF
  END FOR
  RETURN NULL
END FUNCTION

--─── strict unknown-field checking ──────────────────────────────────────────

PRIVATE FUNCTION checkUnknownFields(obj util.JSONObject) RETURNS STRING
  DEFINE i INT
  DEFINE err STRING
  DEFINE allowed DICTIONARY OF BOOLEAN
  LET allowed["$schema"] = TRUE
  LET allowed["type"] = TRUE
  LET allowed["name"] = TRUE
  LET allowed["version"] = TRUE
  LET allowed["description"] = TRUE
  LET allowed["author"] = TRUE
  LET allowed["license"] = TRUE
  LET allowed["repository"] = TRUE
  LET allowed["keywords"] = TRUE
  LET allowed["main"] = TRUE
  LET allowed["visibility"] = TRUE
  LET allowed["genero"] = TRUE
  LET allowed["dependencies"] = TRUE
  LET allowed["devDependencies"] = TRUE
  LET allowed["optionalDependencies"] = TRUE
  LET allowed["root"] = TRUE
  LET allowed["files"] = TRUE
  LET allowed["bin"] = TRUE
  LET allowed["docs"] = TRUE
  LET allowed["programs"] = TRUE
  LET allowed["webcomponents"] = TRUE
  LET allowed["hooks"] = TRUE
  FOR i = 1 TO obj.getLength()
    VAR key = obj.name(i)
    IF NOT allowed.contains(key) THEN
      IF key == "scripts" THEN
        RETURN SFMT(
            'the "scripts" field has been replaced by "hooks" with declarative operations — see docs/user-guide.md',
            key)
      END IF
      RETURN SFMT('unknown field "%1"', key)
    END IF
    CASE key
      WHEN "dependencies"
        LET err = checkDependenciesKeys(obj, key)
        IF err IS NOT NULL THEN
          RETURN err
        END IF
      WHEN "devDependencies"
        LET err = checkDependenciesKeys(obj, key)
        IF err IS NOT NULL THEN
          RETURN err
        END IF
      WHEN "optionalDependencies"
        LET err = checkDependenciesKeys(obj, key)
        IF err IS NOT NULL THEN
          RETURN err
        END IF
      WHEN "hooks"
        LET err = checkHooksKeys(obj)
        IF err IS NOT NULL THEN
          RETURN err
        END IF
    END CASE
  END FOR
  RETURN NULL
END FUNCTION

PRIVATE FUNCTION checkDependenciesKeys(obj util.JSONObject, key STRING)
    RETURNS STRING
  DEFINE i INT
  DEFINE sub util.JSONObject
  IF obj.getType(key) != "OBJECT" THEN
    RETURN SFMT('invalid "%1": expected an object', key)
  END IF
  LET sub = obj.get(key)
  FOR i = 1 TO sub.getLength()
    VAR k = sub.name(i)
    CASE k
      WHEN "fgl"
        IF sub.getType(k) != "OBJECT" THEN
          RETURN 'invalid "dependencies.fgl": expected an object'
        END IF
        VAR fgl util.JSONObject
        LET fgl = sub.get(k)
        VAR j INT
        FOR j = 1 TO fgl.getLength()
          IF fgl.getType(fgl.name(j)) != "STRING" THEN
            RETURN SFMT('invalid "dependencies.fgl": value of "%1" must be a string',
                fgl.name(j))
          END IF
        END FOR
      WHEN "java"
        IF sub.getType(k) != "ARRAY" THEN
          RETURN 'invalid "dependencies.java": expected an array'
        END IF
      OTHERWISE
        RETURN SFMT(
            'unknown key "%1" under "dependencies": expected "fgl" or "java". Did you mean "dependencies.fgl.%1"?',
            k)
    END CASE
  END FOR
  RETURN NULL
END FUNCTION

PRIVATE FUNCTION checkHooksKeys(obj util.JSONObject) RETURNS STRING
  DEFINE i, j, k INT
  DEFINE hooksObj, opObj util.JSONObject
  DEFINE opsArr util.JSONArray
  IF obj.getType("hooks") != "OBJECT" THEN
    RETURN 'invalid "hooks": expected an object'
  END IF
  LET hooksObj = obj.get("hooks")
  FOR i = 1 TO hooksObj.getLength()
    VAR event = hooksObj.name(i)
    IF event != "preinstall"
        AND event != "postinstall"
        AND event != "prepublish"
        AND event != "postpublish"
        AND event != "preuninstall" THEN
      RETURN SFMT(
          'unknown hook event "%1": expected one of preinstall, postinstall, prepublish, postpublish, preuninstall',
          event)
    END IF
    IF hooksObj.getType(event) != "ARRAY" THEN
      RETURN SFMT('invalid "hooks.%1": expected an array', event)
    END IF
    LET opsArr = hooksObj.get(event)
    FOR j = 1 TO opsArr.getLength()
      IF opsArr.getType(j) != "OBJECT" THEN
        RETURN SFMT('invalid "hooks.%1": expected operation objects', event)
      END IF
      LET opObj = opsArr.get(j)
      FOR k = 1 TO opObj.getLength()
        VAR f = opObj.name(k)
        IF f != "op" AND f != "from" AND f != "to" AND f != "path" THEN
          RETURN SFMT('unknown field "%1" in hook operation', f)
        END IF
      END FOR
      VAR opName STRING = opObj.get("op")
      IF opName IS NULL
          OR (opName != "copy-files" AND opName != "mkdir") THEN
        RETURN SFMT(
            'unknown hook op "%1": expected one of copy-files, mkdir',
            NVL(opName, ""))
      END IF
    END FOR
  END FOR
  RETURN NULL
END FUNCTION

--─── serialization ──────────────────────────────────────────────────────────

#+serializes the manifest with canonical field order, dropping empty
#+devDependencies/optionalDependencies (2 space pretty printed like Go)
FUNCTION toJSONString(m TManifest) RETURNS STRING
  VAR obj = util.JSONObject.create()
  CALL putStrOpt(obj, "$schema", m.schema)
  CALL putStrOpt(obj, "type", m.typ)
  CALL obj.put("name", NVL(m.name, ""))
  CALL obj.put("version", NVL(m.version, ""))
  CALL putStrOpt(obj, "description", m.description)
  CALL putStrOpt(obj, "author", m.author)
  CALL putStrOpt(obj, "license", m.license)
  CALL putStrOpt(obj, "repository", m.repository)
  CALL putArrOpt(obj, "keywords", m.keywords)
  CALL putStrOpt(obj, "main", m.main)
  CALL putStrOpt(obj, "visibility", m.visibility)
  CALL putStrOpt(obj, "genero", m.genero)
  CALL obj.put("dependencies", depsToJSON(m.dependencies))
  IF NOT depsEmpty(m.devDependencies) THEN
    CALL obj.put("devDependencies", depsToJSON(m.devDependencies))
  END IF
  IF NOT depsEmpty(m.optionalDependencies) THEN
    CALL obj.put("optionalDependencies", depsToJSON(m.optionalDependencies))
  END IF
  CALL putStrOpt(obj, "root", m.root)
  CALL putArrOpt(obj, "files", m.files)
  IF m.bin.getLength() > 0 THEN
    CALL obj.put("bin", dictToJSON(m.bin))
  END IF
  CALL putArrOpt(obj, "docs", m.docs)
  CALL putArrOpt(obj, "programs", m.programs)
  CALL putArrOpt(obj, "webcomponents", m.webcomponents)
  IF m.hooks.getLength() > 0 THEN
    CALL obj.put("hooks", hooksToJSON(m.hooks))
  END IF
  RETURN prettyJSON(obj.toString())
END FUNCTION

PRIVATE FUNCTION depsEmpty(d TDependencies) RETURNS BOOLEAN
  RETURN d.fgl.getLength() == 0 AND d.java.getLength() == 0
END FUNCTION

PRIVATE FUNCTION depsToJSON(d TDependencies) RETURNS util.JSONObject
  DEFINE i INT
  VAR obj = util.JSONObject.create()
  IF d.fgl.getLength() > 0 THEN
    CALL obj.put("fgl", dictToJSON(d.fgl))
  END IF
  IF d.java.getLength() > 0 THEN
    VAR arr = util.JSONArray.create()
    FOR i = 1 TO d.java.getLength()
      CALL arr.put(arr.getLength() + 1, javaToJSON(d.java[i]))
    END FOR
    CALL obj.put("java", arr)
  END IF
  RETURN obj
END FUNCTION

PRIVATE FUNCTION javaToJSON(dep TJavaDependency) RETURNS util.JSONObject
  VAR obj = util.JSONObject.create()
  CALL obj.put("groupId", NVL(dep.groupId, ""))
  CALL obj.put("artifactId", NVL(dep.artifactId, ""))
  CALL obj.put("version", NVL(dep.version, ""))
  CALL putStrOpt(obj, "checksum", dep.checksum)
  CALL putStrOpt(obj, "jar", dep.jar)
  CALL putStrOpt(obj, "url", dep.url)
  RETURN obj
END FUNCTION

PRIVATE FUNCTION hooksToJSON(hooks DICTIONARY OF THookOps)
    RETURNS util.JSONObject
  DEFINE i, j INT
  VAR obj = util.JSONObject.create()
  VAR keys = hooks.getKeys()
  CALL glob.sortBytewise(keys)
  FOR i = 1 TO keys.getLength()
    VAR arr = util.JSONArray.create()
    VAR ops = hooks[keys[i]]
    FOR j = 1 TO ops.getLength()
      VAR opObj = util.JSONObject.create()
      CALL opObj.put("op", NVL(ops[j].op, ""))
      CALL putStrOpt(opObj, "from", ops[j].src)
      CALL putStrOpt(opObj, "to", ops[j].dst)
      CALL putStrOpt(opObj, "path", ops[j].path)
      CALL arr.put(arr.getLength() + 1, opObj)
    END FOR
    CALL obj.put(keys[i], arr)
  END FOR
  RETURN obj
END FUNCTION

#+emits a dictionary as a JSON object with byte-wise sorted keys
#+(Go's encoding/json sorts map keys)
PRIVATE FUNCTION dictToJSON(d DICTIONARY OF STRING) RETURNS util.JSONObject
  DEFINE i INT
  VAR obj = util.JSONObject.create()
  VAR keys = d.getKeys()
  CALL glob.sortBytewise(keys)
  FOR i = 1 TO keys.getLength()
    CALL obj.put(keys[i], d[keys[i]])
  END FOR
  RETURN obj
END FUNCTION

PRIVATE FUNCTION putStrOpt(obj util.JSONObject, key STRING, val STRING)
  IF val IS NOT NULL AND val.getLength() > 0 THEN
    CALL obj.put(key, val)
  END IF
END FUNCTION

PRIVATE FUNCTION putArrOpt(
    obj util.JSONObject, key STRING, arr fglpkgutils.TStringArr)
  DEFINE i INT
  IF arr.getLength() == 0 THEN
    RETURN
  END IF
  VAR jarr = util.JSONArray.create()
  FOR i = 1 TO arr.getLength()
    CALL jarr.put(jarr.getLength() + 1, arr[i])
  END FOR
  CALL obj.put(key, jarr)
END FUNCTION

#+re-indents a compact JSON string with 2 space indentation
#+(string-literal aware; matches Go json.MarshalIndent layout)
#+the input is exploded into a char array once: positional getCharAt/
#+subString cost O(position) in UTF-8 environments, which would make
#+a walk-and-peek loop over the raw string quadratic on large documents
FUNCTION prettyJSON(s STRING) RETURNS STRING
  DEFINE i, len, depth INT
  DEFINE c STRING
  DEFINE inStr BOOLEAN
  VAR sb = base.StringBuffer.create()
  VAR chars = fglpkgutils.explodeChars(s)
  LET len = chars.getLength()
  LET i = 1
  WHILE i <= len
    LET c = chars[i]
    IF inStr THEN
      CALL sb.append(c)
      IF c == "\\" THEN
        --copy the escaped character verbatim
        LET i = i + 1
        IF i <= len THEN
          CALL sb.append(chars[i])
        END IF
      ELSE
        IF c == '"' THEN
          LET inStr = FALSE
        END IF
      END IF
    ELSE
      CASE c
        WHEN '"'
          LET inStr = TRUE
          CALL sb.append(c)
        WHEN "{"
          IF i < len AND chars[i + 1] == "}" THEN
            CALL sb.append("{}")
            LET i = i + 1
          ELSE
            LET depth = depth + 1
            CALL sb.append("{")
            CALL appendNewlineIndent(sb, depth)
          END IF
        WHEN "["
          IF i < len AND chars[i + 1] == "]" THEN
            CALL sb.append("[]")
            LET i = i + 1
          ELSE
            LET depth = depth + 1
            CALL sb.append("[")
            CALL appendNewlineIndent(sb, depth)
          END IF
        WHEN "}"
          LET depth = depth - 1
          CALL appendNewlineIndent(sb, depth)
          CALL sb.append("}")
        WHEN "]"
          LET depth = depth - 1
          CALL appendNewlineIndent(sb, depth)
          CALL sb.append("]")
        WHEN ","
          CALL sb.append(",")
          CALL appendNewlineIndent(sb, depth)
        WHEN ":"
          CALL sb.append(": ")
        OTHERWISE
          CALL sb.append(c)
      END CASE
    END IF
    LET i = i + 1
  END WHILE
  RETURN sb.toString()
END FUNCTION

PRIVATE FUNCTION appendNewlineIndent(sb base.StringBuffer, depth INT)
  DEFINE i INT
  CALL sb.append("\n")
  FOR i = 1 TO depth
    CALL sb.append("  ")
  END FOR
END FUNCTION
