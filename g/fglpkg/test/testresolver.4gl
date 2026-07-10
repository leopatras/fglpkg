#+ port of internal/resolver/resolver_test.go with a fake registry
OPTIONS SHORT CIRCUIT
IMPORT FGL testutils
IMPORT FGL fglpkg.fglpkgutils
IMPORT FGL fglpkg.manifest
IMPORT FGL fglpkg.genero
IMPORT FGL fglpkg.registry
IMPORT FGL fglpkg.resolver
&include "testassert.inc"

--fake registry: flat array of (name, version, generoConstraint, info)
TYPE TDbEntry RECORD
  name STRING,
  version STRING,
  genero STRING,
  info registry.TPackageInfo
END RECORD

DEFINE _db DYNAMIC ARRAY OF TDbEntry

MAIN
  CALL testNoDeps()
  CALL testDirectDeps()
  CALL testTransitiveDeps()
  CALL testSharedDepCompatible()
  CALL testSharedDepConflict()
  CALL testGeneroFiltering()
  CALL testRootGeneroRejected()
  CALL testJARCollection()
  CALL testCycleSafety()
  CALL testScopes()
  CALL testOptionalSkipped()
  TSUMMARY()
END MAIN

FUNCTION fakeVersions(name STRING)
    RETURNS(BOOLEAN, resolver.TCandidateVersions, STRING)
  DEFINE out, empty resolver.TCandidateVersions
  DEFINE i INT
  FOR i = 1 TO _db.getLength()
    IF _db[i].name == name THEN
      LET out[out.getLength() + 1].version = _db[i].version
      LET out[out.getLength()].genero = _db[i].genero
    END IF
  END FOR
  IF out.getLength() == 0 THEN
    RETURN FALSE, empty, SFMT("package not found: %1", name)
  END IF
  RETURN TRUE, out, NULL
END FUNCTION

FUNCTION fakeInfo(name STRING, version STRING, generoMajor STRING)
    RETURNS(BOOLEAN, registry.TPackageInfo, STRING)
  DEFINE empty registry.TPackageInfo
  DEFINE i INT
  IF generoMajor IS NULL THEN
  END IF
  FOR i = 1 TO _db.getLength()
    IF _db[i].name == name AND _db[i].version == version THEN
      RETURN TRUE, _db[i].info, NULL
    END IF
  END FOR
  RETURN FALSE, empty, SFMT("version not found: %1@%2", name, version)
END FUNCTION

#+adds a package version to the fake db and returns its index
FUNCTION addPkg(name STRING, version STRING, generoConstraint STRING)
    RETURNS INT
  VAR i = _db.getLength() + 1
  LET _db[i].name = name
  LET _db[i].version = version
  LET _db[i].genero = generoConstraint
  LET _db[i].info.name = name
  LET _db[i].info.version = version
  LET _db[i].info.downloadUrl =
      SFMT("https://example.com/%1-%2.zip", name, version)
  LET _db[i].info.checksum = "deadbeef"
  LET _db[i].info.variant = "genero4"
  RETURN i
END FUNCTION

FUNCTION resetDb()
  CALL _db.clear()
  CALL resolver.setFetchers(
      FUNCTION fakeVersions, FUNCTION fakeInfo,
      genero.mustParseGenero("4.01.12"))
END FUNCTION

FUNCTION planVersionOf(plan resolver.TPlan, name STRING) RETURNS STRING
  DEFINE i INT
  FOR i = 1 TO plan.packages.getLength()
    IF plan.packages[i].name == name THEN
      RETURN plan.packages[i].version
    END IF
  END FOR
  RETURN NULL
END FUNCTION

FUNCTION planScopeOf(plan resolver.TPlan, name STRING) RETURNS STRING
  DEFINE i INT
  FOR i = 1 TO plan.packages.getLength()
    IF plan.packages[i].name == name THEN
      RETURN plan.packages[i].scope
    END IF
  END FOR
  RETURN NULL
END FUNCTION

FUNCTION testNoDeps()
  DEFINE ok BOOLEAN
  DEFINE plan resolver.TPlan
  DEFINE err STRING
  CALL resetDb()
  VAR root = manifest.newManifest("myapp", "1.0.0", "", "")
  CALL resolver.resolve(root) RETURNING ok, plan, err
  TOK(ok)
  TEQ(plan.packages.getLength(), 0)
  TEQ(plan.generoVersion, "4.01.12")
  TEQ(plan.generoMajor, "4")
END FUNCTION

FUNCTION testDirectDeps()
  DEFINE ok BOOLEAN
  DEFINE plan resolver.TPlan
  DEFINE err STRING
  CALL resetDb()
  CALL addPkg("utils", "1.0.0", "") RETURNING status
  CALL addPkg("utils", "1.1.0", "") RETURNING status
  CALL addPkg("utils", "1.2.0", "") RETURNING status
  CALL addPkg("dbtools", "2.0.0", "") RETURNING status
  CALL addPkg("dbtools", "2.1.0", "") RETURNING status
  VAR root = manifest.newManifest("myapp", "1.0.0", "", "")
  CALL manifest.addFGLDependency(root, "utils", "^1.0.0")
  CALL manifest.addFGLDependency(root, "dbtools", "^2.0.0")
  CALL resolver.resolve(root) RETURNING ok, plan, err
  TOK(ok)
  TEQ(planVersionOf(plan, "utils"), "1.2.0")
  TEQ(planVersionOf(plan, "dbtools"), "2.1.0")
END FUNCTION

FUNCTION testTransitiveDeps()
  DEFINE ok BOOLEAN
  DEFINE plan resolver.TPlan
  DEFINE err STRING
  CALL resetDb()
  VAR ia = addPkg("a", "1.0.0", "")
  LET _db[ia].info.fglDeps["b"] = "^1.0.0"
  VAR ib = addPkg("b", "1.0.0", "")
  LET _db[ib].info.fglDeps["c"] = "^2.0.0"
  CALL addPkg("c", "2.0.0", "") RETURNING status
  CALL addPkg("c", "2.1.0", "") RETURNING status
  VAR root = manifest.newManifest("myapp", "1.0.0", "", "")
  CALL manifest.addFGLDependency(root, "a", "^1.0.0")
  CALL resolver.resolve(root) RETURNING ok, plan, err
  TOK(ok)
  TEQ(plan.packages.getLength(), 3)
  TEQ(planVersionOf(plan, "c"), "2.1.0")
  --BFS discovery order preserved: a before b before c
  TEQ(plan.packages[1].name, "a")
  TEQ(plan.packages[2].name, "b")
  TEQ(plan.packages[3].name, "c")
END FUNCTION

FUNCTION testSharedDepCompatible()
  DEFINE ok BOOLEAN
  DEFINE plan resolver.TPlan
  DEFINE err STRING
  CALL resetDb()
  VAR ia = addPkg("a", "1.0.0", "")
  LET _db[ia].info.fglDeps["shared"] = "^1.0.0"
  VAR ib = addPkg("b", "1.0.0", "")
  LET _db[ib].info.fglDeps["shared"] = "^1.2.0"
  CALL addPkg("shared", "1.1.0", "") RETURNING status
  CALL addPkg("shared", "1.3.0", "") RETURNING status
  VAR root = manifest.newManifest("myapp", "1.0.0", "", "")
  CALL manifest.addFGLDependency(root, "a", "^1.0.0")
  CALL manifest.addFGLDependency(root, "b", "^1.0.0")
  CALL resolver.resolve(root) RETURNING ok, plan, err
  TOK(ok)
  --1.3.0 satisfies both ^1.0.0 and ^1.2.0
  TEQ(planVersionOf(plan, "shared"), "1.3.0")
END FUNCTION

FUNCTION testSharedDepConflict()
  DEFINE ok BOOLEAN
  DEFINE plan resolver.TPlan
  DEFINE err STRING
  CALL resetDb()
  VAR ia = addPkg("a", "1.0.0", "")
  LET _db[ia].info.fglDeps["shared"] = "^1.0.0"
  VAR ib = addPkg("b", "1.0.0", "")
  LET _db[ib].info.fglDeps["shared"] = "^2.0.0"
  CALL addPkg("shared", "1.5.0", "") RETURNING status
  CALL addPkg("shared", "2.0.0", "") RETURNING status
  VAR root = manifest.newManifest("myapp", "1.0.0", "", "")
  CALL manifest.addFGLDependency(root, "a", "^1.0.0")
  CALL manifest.addFGLDependency(root, "b", "^1.0.0")
  CALL resolver.resolve(root) RETURNING ok, plan, err
  TOK(NOT ok)
  TOK(fglpkgutils.contains(err, "conflict"))
  TOK(fglpkgutils.contains(err, "shared"))
END FUNCTION

FUNCTION testGeneroFiltering()
  DEFINE ok BOOLEAN
  DEFINE plan resolver.TPlan
  DEFINE err STRING
  --2.0.0 requires Genero ^6.0.0 (incompatible with 4.01.12): excluded
  CALL resetDb()
  CALL addPkg("tools", "1.0.0", "^4.0.0") RETURNING status
  CALL addPkg("tools", "1.5.0", "^4.0.0") RETURNING status
  CALL addPkg("tools", "2.0.0", "^6.0.0") RETURNING status
  VAR root = manifest.newManifest("myapp", "1.0.0", "", "")
  CALL manifest.addFGLDependency(root, "tools", "*")
  CALL resolver.resolve(root) RETURNING ok, plan, err
  TOK(ok)
  TEQ(planVersionOf(plan, "tools"), "1.5.0")

  --no compatible version at all
  CALL resetDb()
  CALL addPkg("tools", "2.0.0", "^6.0.0") RETURNING status
  VAR root2 = manifest.newManifest("myapp", "1.0.0", "", "")
  CALL manifest.addFGLDependency(root2, "tools", "*")
  CALL resolver.resolve(root2) RETURNING ok, plan, err
  TOK(NOT ok)
  TOK(fglpkgutils.contains(err, "compatible with Genero"))
END FUNCTION

FUNCTION testRootGeneroRejected()
  DEFINE ok BOOLEAN
  DEFINE plan resolver.TPlan
  DEFINE err STRING
  CALL resetDb()
  VAR root = manifest.newManifest("myapp", "1.0.0", "", "")
  LET root.genero = "^6.0.0"
  CALL resolver.resolve(root) RETURNING ok, plan, err
  TOK(NOT ok)
  TOK(fglpkgutils.contains(err, "requires Genero"))
END FUNCTION

FUNCTION testJARCollection()
  DEFINE ok BOOLEAN
  DEFINE plan resolver.TPlan
  DEFINE err STRING
  DEFINE dep manifest.TJavaDependency
  DEFINE i INT
  CALL resetDb()
  VAR ia = addPkg("a", "1.0.0", "")
  LET _db[ia].info.javaDeps[1].groupId = "com.google.code.gson"
  LET _db[ia].info.javaDeps[1].artifactId = "gson"
  LET _db[ia].info.javaDeps[1].version = "2.10.1"
  VAR root = manifest.newManifest("myapp", "1.0.0", "", "")
  CALL manifest.addFGLDependency(root, "a", "^1.0.0")
  --root also declares the same jar at a lower version + one more
  LET dep.groupId = "com.google.code.gson"
  LET dep.artifactId = "gson"
  LET dep.version = "2.8.0"
  CALL manifest.addJavaDependency(root, dep)
  LET dep.groupId = "org.apache.commons"
  LET dep.artifactId = "commons-lang3"
  LET dep.version = "3.12.0"
  CALL manifest.addJavaDependency(root, dep)
  CALL resolver.resolve(root) RETURNING ok, plan, err
  TOK(ok)
  TEQ(plan.jars.getLength(), 2)
  --dedup: higher gson version wins
  FOR i = 1 TO plan.jars.getLength()
    IF plan.jars[i].artifactId == "gson" THEN
      TEQ(plan.jars[i].version, "2.10.1")
    END IF
  END FOR
END FUNCTION

FUNCTION testCycleSafety()
  DEFINE ok BOOLEAN
  DEFINE plan resolver.TPlan
  DEFINE err STRING
  CALL resetDb()
  VAR ia = addPkg("a", "1.0.0", "")
  LET _db[ia].info.fglDeps["b"] = "^1.0.0"
  VAR ib = addPkg("b", "1.0.0", "")
  LET _db[ib].info.fglDeps["a"] = "^1.0.0"
  VAR root = manifest.newManifest("myapp", "1.0.0", "", "")
  CALL manifest.addFGLDependency(root, "a", "^1.0.0")
  CALL resolver.resolve(root) RETURNING ok, plan, err
  TOK(ok)
  TEQ(plan.packages.getLength(), 2)
END FUNCTION

FUNCTION testScopes()
  DEFINE ok BOOLEAN
  DEFINE plan resolver.TPlan
  DEFINE err STRING
  --dev subtree excluded with production options
  CALL resetDb()
  CALL addPkg("prodlib", "1.0.0", "") RETURNING status
  CALL addPkg("testlib", "1.0.0", "") RETURNING status
  VAR root = manifest.newManifest("myapp", "1.0.0", "", "")
  CALL manifest.addFGLDependency(root, "prodlib", "^1.0.0")
  CALL manifest.addFGLDependencyScoped(root, "testlib", "^1.0.0", "dev")
  CALL resolver.resolveWithOptions(root, FALSE, TRUE) RETURNING ok, plan, err
  TOK(ok)
  TEQ(plan.packages.getLength(), 1)
  TEQ(plan.packages[1].name, "prodlib")
  --default options include dev
  CALL resolver.resolveWithOptions(root, TRUE, TRUE) RETURNING ok, plan, err
  TOK(ok)
  TEQ(plan.packages.getLength(), 2)
  TEQ(planScopeOf(plan, "testlib"), "dev")
  TEQ(planScopeOf(plan, "prodlib"), "prod")

  --scope promotion: reachable via prod and dev -> prod wins
  CALL resetDb()
  VAR ip = addPkg("prodlib", "1.0.0", "")
  LET _db[ip].info.fglDeps["shared"] = "^1.0.0"
  CALL addPkg("shared", "1.0.0", "") RETURNING status
  VAR root2 = manifest.newManifest("myapp", "1.0.0", "", "")
  CALL manifest.addFGLDependency(root2, "prodlib", "^1.0.0")
  CALL manifest.addFGLDependencyScoped(root2, "shared", "^1.0.0", "dev")
  CALL resolver.resolve(root2) RETURNING ok, plan, err
  TOK(ok)
  TEQ(planScopeOf(plan, "shared"), "prod")
END FUNCTION

FUNCTION testOptionalSkipped()
  DEFINE ok BOOLEAN
  DEFINE plan resolver.TPlan
  DEFINE err STRING
  CALL resetDb()
  CALL addPkg("present", "1.0.0", "") RETURNING status
  VAR root = manifest.newManifest("myapp", "1.0.0", "", "")
  CALL manifest.addFGLDependency(root, "present", "^1.0.0")
  CALL manifest.addFGLDependencyScoped(root, "ghost", "^1.0.0", "optional")
  CALL resolver.resolve(root) RETURNING ok, plan, err
  TOK(ok)
  TEQ(plan.packages.getLength(), 1)
  TEQ(plan.optionalSkipped.getLength(), 1)
  TOK(fglpkgutils.contains(plan.optionalSkipped[1], "ghost"))

  --a missing non-optional dependency is fatal
  CALL manifest.addFGLDependencyScoped(root, "ghost", "^1.0.0", "prod")
  CALL resolver.resolve(root) RETURNING ok, plan, err
  TOK(NOT ok)
  TOK(fglpkgutils.contains(err, "ghost"))
END FUNCTION
