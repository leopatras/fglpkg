#+ transitive dependency resolution (BFS) producing an install plan
#+ port of internal/resolver/resolver.go
#+
#+ Algorithm:
#+  1. verify the root manifest's genero constraint against the runtime
#+  2. enqueue the root's prod/dev/optional buckets
#+  3. per package: fetch versions, filter by Genero compatibility, pick the
#+     highest version satisfying all accumulated constraints
#+  4. on revisit: promote scope (prod > optional > dev) and check the new
#+     constraint against the chosen version (conflict otherwise)
#+  5. optional-scope failures are recorded in optionalSkipped, not fatal
#+  6. Java JARs are deduplicated by groupId:artifactId, higher version wins
OPTIONS SHORT CIRCUIT
PACKAGE fglpkg
IMPORT FGL fglpkg.fglpkgutils
IMPORT FGL fglpkg.semver
IMPORT FGL fglpkg.manifest
IMPORT FGL fglpkg.genero
IMPORT FGL fglpkg.registry
&include "myassert.inc"

PUBLIC CONSTANT REQUIRED_BY_ROOT = "<root>"

PUBLIC TYPE TResolvedPackage RECORD
  name STRING,
  version STRING,
  downloadURL STRING,
  checksum STRING,
  variant STRING, --"genero<N>", "webcomponent"; empty for local members
  requiredBy fglpkgutils.TStringArr,
  scope STRING --prod, dev or optional
END RECORD

PUBLIC TYPE TLocalMember RECORD
  name STRING,
  version STRING,
  path STRING
END RECORD

PUBLIC TYPE TPlan RECORD
  packages DYNAMIC ARRAY OF TResolvedPackage,
  jars manifest.TJavaDependencies,
  jarScopes DICTIONARY OF STRING, --jar key -> scope
  localMembers DYNAMIC ARRAY OF TLocalMember,
  generoVersion STRING, --as detected, e.g. "4.01.12"
  generoMajor STRING, --e.g. "4"
  optionalSkipped fglpkgutils.TStringArr
END RECORD

PUBLIC TYPE TCandidateVersion RECORD
  version STRING,
  genero STRING --Genero constraint declared by that version
END RECORD

PUBLIC TYPE TCandidateVersions DYNAMIC ARRAY OF TCandidateVersion

#+injectable fetchers (tests use fakes; default goes to the registry)
PUBLIC TYPE TVersionFetcher
    FUNCTION(name STRING) RETURNS(BOOLEAN, TCandidateVersions, STRING)
PUBLIC TYPE TInfoFetcher
    FUNCTION(name STRING, version STRING, generoMajor STRING)
        RETURNS(BOOLEAN, registry.TPackageInfo, STRING)

FUNCTION isWebcomponentPackage(p TResolvedPackage) RETURNS BOOLEAN
  RETURN p.variant == "webcomponent"
END FUNCTION

--─── module state (reset per resolve call) ──────────────────────────────────

PRIVATE TYPE TConstraintSource RECORD
  constraint STRING,
  requiredBy STRING
END RECORD

PRIVATE TYPE TConstraintSources DYNAMIC ARRAY OF TConstraintSource

PRIVATE TYPE TWorkItem RECORD
  name STRING,
  constraint STRING,
  requiredBy STRING,
  scope STRING
END RECORD

PRIVATE TYPE TResolvedEntry RECORD
  version semver.TSemver,
  info registry.TPackageInfo,
  ord INT,
  scope STRING,
  isSet BOOLEAN
END RECORD

DEFINE _fetchVersions TVersionFetcher
DEFINE _fetchInfo TInfoFetcher
DEFINE _gv genero.TGeneroVersion
DEFINE _gvSet BOOLEAN

DEFINE _queue DYNAMIC ARRAY OF TWorkItem
DEFINE _queueHead INT
DEFINE _constraints DICTIONARY OF TConstraintSources
DEFINE _resolved DICTIONARY OF TResolvedEntry
DEFINE _jars DICTIONARY OF manifest.TJavaDependency
DEFINE _jarScopes DICTIONARY OF STRING
DEFINE _localMembers DICTIONARY OF TLocalMember
DEFINE _conflicts fglpkgutils.TStringArr --formatted conflict messages
DEFINE _conflicted DICTIONARY OF BOOLEAN
DEFINE _orderSeq INT
DEFINE _optionalSkipped fglpkgutils.TStringArr

#+injects fetchers and a fixed Genero version (tests)
FUNCTION setFetchers(fv TVersionFetcher, fi TInfoFetcher, gv genero.TGeneroVersion)
  LET _fetchVersions = fv
  LET _fetchInfo = fi
  LET _gv = gv
  LET _gvSet = TRUE
END FUNCTION

#+resets fetchers to the live registry + auto-detection defaults
FUNCTION resetFetchers()
  INITIALIZE _fetchVersions TO NULL
  INITIALIZE _fetchInfo TO NULL
  LET _gvSet = FALSE
END FUNCTION

#+resolves with the default options (dev + optional included)
FUNCTION resolve(root manifest.TManifest) RETURNS(BOOLEAN, TPlan, STRING)
  DEFINE ok BOOLEAN
  DEFINE plan TPlan
  DEFINE err STRING
  CALL resolveWithOptions(root, TRUE, TRUE) RETURNING ok, plan, err
  RETURN ok, plan, err
END FUNCTION

#+resolves all transitive dependencies; transitive deps of packages are
#+always treated as production — a library's devDependencies are never
#+pulled in by consumers
FUNCTION resolveWithOptions(
    root manifest.TManifest, includeDev BOOLEAN, includeOptional BOOLEAN)
    RETURNS(BOOLEAN, TPlan, STRING)
  DEFINE plan, empty TPlan
  DEFINE item TWorkItem
  DEFINE ok, sat BOOLEAN
  DEFINE err STRING
  DEFINE candidates TCandidateVersions
  DEFINE info registry.TPackageInfo
  DEFINE chosen semver.TSemver

  IF _fetchVersions IS NULL THEN
    LET _fetchVersions = FUNCTION registryVersionsFetcher
  END IF
  IF _fetchInfo IS NULL THEN
    LET _fetchInfo = FUNCTION registryInfoFetcher
  END IF
  IF NOT _gvSet THEN
    CALL genero.detect() RETURNING ok, _gv, err
    IF NOT ok THEN
      RETURN FALSE, empty, SFMT("cannot create resolver: %1", err)
    END IF
    LET _gvSet = TRUE
  END IF

  CALL genero.satisfiesGenero(_gv, root.genero) RETURNING sat, err
  IF err IS NOT NULL THEN
    RETURN FALSE, empty,
        SFMT("invalid genero constraint in root manifest: %1", err)
  END IF
  IF NOT sat THEN
    RETURN FALSE, empty,
        SFMT('project requires Genero "%1" but detected version is %2',
            root.genero, genero.versionString(_gv))
  END IF

  CALL resetState()

  CALL enqueueRootBucket(root.dependencies, manifest.SCOPE_PROD)
  IF includeDev THEN
    CALL enqueueRootBucket(root.devDependencies, manifest.SCOPE_DEV)
  END IF
  IF includeOptional THEN
    CALL enqueueRootBucket(root.optionalDependencies, manifest.SCOPE_OPTIONAL)
  END IF

  WHILE _queueHead <= _queue.getLength()
    LET item = _queue[_queueHead]
    LET _queueHead = _queueHead + 1

    --workspace-local short circuit (later phase: always FALSE for now)
    IF lookupLocalMember(item.name) THEN
      CONTINUE WHILE
    END IF

    IF _resolved.contains(item.name) THEN
      CALL promoteScope(item.name, item.scope)
      --mirrors Go: addConstraint AND checkExistingResolution both record
      --the constraint source, so requiredBy lists match the Go lock files
      CALL addConstraint(item.name, item.constraint, item.requiredBy)
      CALL checkExistingResolution(item.name, item.constraint, item.requiredBy)
      CONTINUE WHILE
    END IF

    CALL addConstraint(item.name, item.constraint, item.requiredBy)

    CALL _fetchVersions(item.name) RETURNING ok, candidates, err
    IF NOT ok THEN
      IF item.scope == manifest.SCOPE_OPTIONAL THEN
        CALL skipOptional(item.name, SFMT("fetch versions: %1", err))
        CONTINUE WHILE
      END IF
      RETURN FALSE, empty,
          SFMT('failed to fetch versions for "%1": %2', item.name, err)
    END IF

    VAR compatible = filterByGenero(item.name, candidates)
    IF compatible.getLength() == 0 THEN
      IF item.scope == manifest.SCOPE_OPTIONAL THEN
        CALL skipOptional(item.name,
            SFMT("no version compatible with Genero %1",
                genero.versionString(_gv)))
        CONTINUE WHILE
      END IF
      RETURN FALSE, empty,
          SFMT('no version of "%1" is compatible with Genero %2',
              item.name, genero.versionString(_gv))
    END IF

    CALL bestVersion(item.name, compatible) RETURNING ok, chosen, err
    IF NOT ok THEN
      IF item.scope == manifest.SCOPE_OPTIONAL THEN
        CALL skipOptional(item.name,
            SFMT("no version satisfies constraints: %1", err))
        CONTINUE WHILE
      END IF
      CALL addConflict(item.name)
      CONTINUE WHILE
    END IF

    CALL _fetchInfo(item.name, semver.versionToString(chosen),
            genero.majorString(_gv))
        RETURNING ok, info, err
    IF NOT ok THEN
      IF item.scope == manifest.SCOPE_OPTIONAL THEN
        CALL skipOptional(item.name, SFMT("fetch info: %1", err))
        CONTINUE WHILE
      END IF
      RETURN FALSE, empty,
          SFMT("failed to fetch info for %1@%2: %3",
              item.name, semver.versionToString(chosen), err)
    END IF

    CALL markResolved(item.name, chosen, info, item.scope)

    --walk the resolved package's own production dependencies
    VAR depNames = info.fglDeps.getKeys()
    VAR i INT
    FOR i = 1 TO depNames.getLength()
      VAR depName = depNames[i]
      IF lookupLocalMember(depName) THEN
        CONTINUE FOR
      END IF
      IF _resolved.contains(depName) THEN
        CALL promoteScope(depName, item.scope)
        CALL checkExistingResolution(depName, info.fglDeps[depName], item.name)
        CONTINUE FOR
      END IF
      CALL enqueueWork(depName, info.fglDeps[depName], item.name, item.scope)
    END FOR
    FOR i = 1 TO info.javaDeps.getLength()
      CALL addJARScoped(info.javaDeps[i], item.scope)
    END FOR
  END WHILE

  IF _conflicts.getLength() > 0 THEN
    RETURN FALSE, empty,
        SFMT("%1 dependency conflict(s) found:\n\n%2",
            _conflicts.getLength(), fglpkgutils.joinArr(_conflicts, "\n"))
  END IF

  LET plan = buildPlan()
  LET plan.generoVersion = genero.versionString(_gv)
  LET plan.generoMajor = genero.majorString(_gv)
  RETURN TRUE, plan, NULL
END FUNCTION

--─── state helpers ──────────────────────────────────────────────────────────

PRIVATE FUNCTION resetState()
  CALL _queue.clear()
  LET _queueHead = 1
  CALL _constraints.clear()
  CALL _resolved.clear()
  CALL _jars.clear()
  CALL _jarScopes.clear()
  CALL _localMembers.clear()
  CALL _conflicts.clear()
  CALL _conflicted.clear()
  LET _orderSeq = 0
  CALL _optionalSkipped.clear()
END FUNCTION

#+workspace member lookup — Phase 1 stub, the workspace phase fills this in
PRIVATE FUNCTION lookupLocalMember(name STRING) RETURNS BOOLEAN
  UNUSED_VAR(name)
  RETURN FALSE
END FUNCTION

PRIVATE FUNCTION enqueueWork(
    name STRING, constraint STRING, requiredBy STRING, scope STRING)
  DEFINE item TWorkItem
  LET item.name = name
  LET item.constraint = constraint
  LET item.requiredBy = requiredBy
  LET item.scope = scope
  LET _queue[_queue.getLength() + 1] = item
END FUNCTION

#+adds a single scope's root dependencies to the work queue
PRIVATE FUNCTION enqueueRootBucket(deps manifest.TDependencies, scope STRING)
  DEFINE i INT
  VAR names = deps.fgl.getKeys()
  FOR i = 1 TO names.getLength()
    CALL enqueueWork(names[i], deps.fgl[names[i]], REQUIRED_BY_ROOT, scope)
  END FOR
  FOR i = 1 TO deps.java.getLength()
    CALL addJARScoped(deps.java[i], scope)
  END FOR
END FUNCTION

#+prod (3) > optional (2) > dev (1); unknown/empty treated as prod
PRIVATE FUNCTION scopeRank(s STRING) RETURNS INT
  CASE s
    WHEN manifest.SCOPE_PROD
      RETURN 3
    WHEN manifest.SCOPE_OPTIONAL
      RETURN 2
    WHEN manifest.SCOPE_DEV
      RETURN 1
  END CASE
  RETURN 3
END FUNCTION

PRIVATE FUNCTION strongerScope(a STRING, b STRING) RETURNS STRING
  IF scopeRank(a) >= scopeRank(b) THEN
    RETURN a
  END IF
  RETURN b
END FUNCTION

PRIVATE FUNCTION promoteScope(name STRING, candidate STRING)
  IF NOT _resolved.contains(name) THEN
    RETURN
  END IF
  LET _resolved[name].scope = strongerScope(_resolved[name].scope, candidate)
END FUNCTION

PRIVATE FUNCTION skipOptional(name STRING, reason STRING)
  LET _optionalSkipped[_optionalSkipped.getLength() + 1] =
      SFMT("%1 (%2)", name, reason)
  CALL fglpkgutils.printStderr(
      SFMT("warning: skipping optional dependency %1: %2", name, reason))
END FUNCTION

PRIVATE FUNCTION addConstraint(name STRING, constraint STRING, requiredBy STRING)
  DEFINE cs TConstraintSource
  LET cs.constraint = constraint
  LET cs.requiredBy = requiredBy
  LET _constraints[name][_constraints[name].getLength() + 1] = cs
END FUNCTION

#+picks the highest candidate satisfying every accumulated constraint
PRIVATE FUNCTION bestVersion(name STRING, candidates DYNAMIC ARRAY OF semver.TSemver)
    RETURNS(BOOLEAN, semver.TSemver, STRING)
  DEFINE parsed DYNAMIC ARRAY OF semver.TConstraint
  DEFINE best, empty semver.TSemver
  DEFINE bestSet, ok BOOLEAN
  DEFINE c semver.TConstraint
  DEFINE err STRING
  DEFINE i, j INT
  VAR sources = _constraints[name]
  FOR i = 1 TO sources.getLength()
    CALL semver.parseConstraint(sources[i].constraint) RETURNING ok, c, err
    IF NOT ok THEN
      RETURN FALSE, empty,
          SFMT('invalid constraint "%1" from %2: %3',
              sources[i].constraint, sources[i].requiredBy, err)
    END IF
    LET parsed[parsed.getLength() + 1] = c
  END FOR
  FOR i = 1 TO candidates.getLength()
    VAR allOk = TRUE
    FOR j = 1 TO parsed.getLength()
      IF NOT semver.satisfies(candidates[i], parsed[j]) THEN
        LET allOk = FALSE
        EXIT FOR
      END IF
    END FOR
    IF allOk AND (NOT bestSet OR semver.compare(candidates[i], best) > 0) THEN
      LET best = candidates[i]
      LET bestSet = TRUE
    END IF
  END FOR
  IF NOT bestSet THEN
    RETURN FALSE, empty, "no version satisfies all constraints"
  END IF
  RETURN TRUE, best, NULL
END FUNCTION

#+after a revisit: record the new constraint source and check that the
#+already-chosen version still satisfies it; conflict otherwise
PRIVATE FUNCTION checkExistingResolution(
    name STRING, newConstraint STRING, requiredBy STRING)
  DEFINE ok BOOLEAN
  DEFINE c semver.TConstraint
  DEFINE err STRING
  CALL semver.parseConstraint(newConstraint) RETURNING ok, c, err
  IF NOT ok THEN
    RETURN
  END IF
  CALL addConstraint(name, newConstraint, requiredBy)
  IF NOT semver.satisfies(_resolved[name].version, c) THEN
    CALL addConflict(name)
  END IF
END FUNCTION

PRIVATE FUNCTION markResolved(
    name STRING, v semver.TSemver, info registry.TPackageInfo, scope STRING)
  DEFINE entry TResolvedEntry
  LET entry.version = v
  LET entry.info = info
  LET entry.ord = _orderSeq
  LET entry.scope = scope
  LET entry.isSet = TRUE
  LET _resolved[name] = entry
  LET _orderSeq = _orderSeq + 1
END FUNCTION

PRIVATE FUNCTION addConflict(name STRING)
  DEFINE i INT
  IF _conflicted.contains(name) THEN
    RETURN
  END IF
  LET _conflicted[name] = TRUE
  VAR sb = base.StringBuffer.create()
  CALL sb.append(SFMT('version conflict for "%1":\n', name))
  VAR sources = _constraints[name]
  FOR i = 1 TO sources.getLength()
    CALL sb.append(SFMT('  %1 requires "%2"\n',
        sources[i].requiredBy, sources[i].constraint))
  END FOR
  LET _conflicts[_conflicts.getLength() + 1] = sb.toString()
END FUNCTION

#+adds a JAR with scope promotion; same key at different versions:
#+the higher version wins
PRIVATE FUNCTION addJARScoped(dep manifest.TJavaDependency, scope STRING)
  DEFINE okE, okN BOOLEAN
  DEFINE ev, nv semver.TSemver
  DEFINE err STRING
  VAR key = manifest.javaKey(dep)
  IF _jars.contains(key) THEN
    CALL semver.parseVersion(_jars[key].version) RETURNING okE, ev, err
    CALL semver.parseVersion(dep.version) RETURNING okN, nv, err
    IF okE AND okN AND semver.compare(nv, ev) > 0 THEN
      LET _jars[key] = dep
    END IF
  ELSE
    LET _jars[key] = dep
  END IF
  IF _jarScopes.contains(key) THEN
    LET _jarScopes[key] = strongerScope(_jarScopes[key], scope)
  ELSE
    LET _jarScopes[key] = scope
  END IF
END FUNCTION

#+removes candidates whose Genero constraint is not satisfied by the
#+detected runtime; invalid constraints are skipped with a warning
PRIVATE FUNCTION filterByGenero(pkgName STRING, candidates TCandidateVersions)
    RETURNS DYNAMIC ARRAY OF semver.TSemver
  DEFINE out DYNAMIC ARRAY OF semver.TSemver
  DEFINE i INT
  DEFINE sat, ok BOOLEAN
  DEFINE v semver.TSemver
  DEFINE err STRING
  FOR i = 1 TO candidates.getLength()
    CALL semver.parseVersion(candidates[i].version) RETURNING ok, v, err
    IF NOT ok THEN
      CONTINUE FOR
    END IF
    CALL genero.satisfiesGenero(_gv, candidates[i].genero) RETURNING sat, err
    IF err IS NOT NULL THEN
      CALL fglpkgutils.printStderr(
          SFMT('warning: %1@%2 has invalid genero constraint "%3": %4 — skipping',
              pkgName, candidates[i].version, candidates[i].genero, err))
      CONTINUE FOR
    END IF
    IF sat THEN
      LET out[out.getLength() + 1] = v
    END IF
  END FOR
  RETURN out
END FUNCTION

PRIVATE FUNCTION buildPlan() RETURNS TPlan
  DEFINE plan TPlan
  DEFINE i, j INT
  DEFINE tmp TResolvedPackage
  VAR names = _resolved.getKeys()
  FOR i = 1 TO names.getLength()
    VAR name = names[i]
    VAR idx = plan.packages.getLength() + 1
    LET plan.packages[idx].name = name
    LET plan.packages[idx].version =
        semver.versionToString(_resolved[name].version)
    LET plan.packages[idx].downloadURL = _resolved[name].info.downloadUrl
    LET plan.packages[idx].checksum = _resolved[name].info.checksum
    LET plan.packages[idx].variant = _resolved[name].info.variant
    LET plan.packages[idx].scope = _resolved[name].scope
    VAR sources = _constraints[name]
    FOR j = 1 TO sources.getLength()
      LET plan.packages[idx].requiredBy[j] = sources[j].requiredBy
    END FOR
  END FOR
  --stable order: by discovery order (insertion sort)
  FOR i = 2 TO plan.packages.getLength()
    LET j = i
    WHILE j > 1
        AND _resolved[plan.packages[j].name].ord
            < _resolved[plan.packages[j - 1].name].ord
      LET tmp = plan.packages[j]
      LET plan.packages[j] = plan.packages[j - 1]
      LET plan.packages[j - 1] = tmp
      LET j = j - 1
    END WHILE
  END FOR

  VAR jarKeys = _jars.getKeys()
  CALL fglpkgutils.sortStringArray(jarKeys)
  FOR i = 1 TO jarKeys.getLength()
    LET plan.jars[i] = _jars[jarKeys[i]]
    LET plan.jarScopes[jarKeys[i]] = _jarScopes[jarKeys[i]]
  END FOR

  VAR memberNames = _localMembers.getKeys()
  FOR i = 1 TO memberNames.getLength()
    LET plan.localMembers[i] = _localMembers[memberNames[i]]
  END FOR

  CALL _optionalSkipped.copyTo(plan.optionalSkipped)
  RETURN plan
END FUNCTION

--─── live registry fetchers (defaults) ──────────────────────────────────────

FUNCTION registryVersionsFetcher(name STRING)
    RETURNS(BOOLEAN, TCandidateVersions, STRING)
  DEFINE out, empty TCandidateVersions
  DEFINE vl registry.TVersionList
  DEFINE ok BOOLEAN
  DEFINE err STRING
  DEFINE i INT
  CALL registry.fetchVersionList(name) RETURNING ok, vl, err
  IF NOT ok THEN
    RETURN FALSE, empty, err
  END IF
  FOR i = 1 TO vl.versionEntries.getLength()
    LET out[i].version = vl.versionEntries[i].version
    LET out[i].genero = vl.versionEntries[i].genero
  END FOR
  RETURN TRUE, out, NULL
END FUNCTION

FUNCTION registryInfoFetcher(name STRING, version STRING, generoMajor STRING)
    RETURNS(BOOLEAN, registry.TPackageInfo, STRING)
  DEFINE info registry.TPackageInfo
  DEFINE ok BOOLEAN
  DEFINE err STRING
  CALL registry.fetchInfoForGenero(name, version, generoMajor)
      RETURNING ok, info, err
  RETURN ok, info, err
END FUNCTION

#+wires the default registry fetchers + Genero auto-detection when no
#+fetchers were injected; the first resolve call also does this lazily
FUNCTION useRegistryFetchers()
  LET _fetchVersions = FUNCTION registryVersionsFetcher
  LET _fetchInfo = FUNCTION registryInfoFetcher
  LET _gvSet = FALSE
END FUNCTION
