#+ tests for workspace.4gl + the resolver's workspace integration
OPTIONS SHORT CIRCUIT
IMPORT os
IMPORT FGL testutils
IMPORT FGL fglpkg.fglpkgutils
IMPORT FGL fglpkg.manifest
IMPORT FGL fglpkg.genero
IMPORT FGL fglpkg.registry
IMPORT FGL fglpkg.workspace
IMPORT FGL fglpkg.resolver
&include "testassert.inc"

TYPE TDbEntry RECORD
  name STRING,
  version STRING,
  info registry.TPackageInfo
END RECORD

DEFINE _db DYNAMIC ARRAY OF TDbEntry

MAIN
  CALL testInitAddFindRoot()
  CALL testLoadAndTopoSort()
  CALL testCycleAndDuplicates()
  CALL testQueries()
  CALL testResolverIntegration()
  TSUMMARY()
END MAIN

#+creates a member dir with a manifest; deps as "a:^1.0.0,b:2.0.0" CSV
FUNCTION addMemberDir(root STRING, rel STRING, name STRING, deps STRING)
  DEFINE i INT
  DEFINE ok BOOLEAN
  DEFINE err STRING
  VAR dir = os.Path.join(root, rel)
  CALL fglpkgutils.mkdirp(dir)
  VAR m = manifest.newManifest(name, "1.0.0", "", "")
  IF deps IS NOT NULL AND deps.getLength() > 0 THEN
    VAR pairs = fglpkgutils.splitOnChar(deps, ",")
    FOR i = 1 TO pairs.getLength()
      VAR colon = pairs[i].getIndexOf(":", 1)
      LET m.dependencies.fgl[pairs[i].subString(1, colon - 1)] =
          pairs[i].subString(colon + 1, pairs[i].getLength())
    END FOR
  END IF
  CALL manifest.save(m, dir) RETURNING ok, err
  TOK(ok)
END FUNCTION

FUNCTION memberNames(ws workspace.TWorkspace) RETURNS STRING
  DEFINE names fglpkgutils.TStringArr
  DEFINE i INT
  FOR i = 1 TO ws.members.getLength()
    LET names[i] = ws.members[i].m.name
  END FOR
  RETURN fglpkgutils.joinArr(names, ",")
END FUNCTION

FUNCTION testInitAddFindRoot()
  DEFINE members fglpkgutils.TStringArr
  VAR root = fglpkgutils.makeTempDir()
  LET members[1] = "liba"
  TOK(workspace.init(root, members) IS NULL)
  TOK(workspace.workspaceExists(root))
  --init refuses to overwrite
  VAR err = workspace.init(root, members)
  TOK(fglpkgutils.contains(err, "already exists"))
  --addMember appends, rejects duplicates
  TOK(workspace.addMember(root, "libb") IS NULL)
  LET err = workspace.addMember(root, "libb")
  TEQ(err, '"libb" is already a workspace member')
  --findRoot walks up from a nested dir
  CALL fglpkgutils.mkdirp(os.Path.join(root, "liba/deep"))
  VAR foundRoot = workspace.findRoot(os.Path.join(root, "liba/deep"))
  TEQ(foundRoot, os.Path.fullPath(root))
  TOK(workspace.findRoot("/") IS NULL)
  CALL fglpkgutils.rmrf(root)
END FUNCTION

FUNCTION testLoadAndTopoSort()
  DEFINE ok BOOLEAN
  DEFINE ws workspace.TWorkspace
  DEFINE err STRING
  DEFINE members fglpkgutils.TStringArr
  VAR root = fglpkgutils.makeTempDir()
  --app depends on libb which depends on liba (declared app-first)
  CALL addMemberDir(root, "app", "app", "libb:^1.0.0,extdep:^2.0.0")
  CALL addMemberDir(root, "libb", "libb", "liba:^1.0.0")
  CALL addMemberDir(root, "liba", "liba", "")
  LET members[1] = "app"
  LET members[2] = "libb"
  LET members[3] = "liba"
  TOK(workspace.init(root, members) IS NULL)
  CALL workspace.load(root) RETURNING ok, ws, err
  TOK(ok)
  --topo order: dependencies before dependents
  TEQ(memberNames(ws), "liba,libb,app")
  --fglldpathEntries in the same order
  VAR entries = workspace.fglldpathEntries(ws)
  TOK(fglpkgutils.endsWith(entries[1], "/liba"))
  TOK(fglpkgutils.endsWith(entries[3], "/app"))
  CALL fglpkgutils.rmrf(root)
END FUNCTION

FUNCTION testCycleAndDuplicates()
  DEFINE ok BOOLEAN
  DEFINE ws workspace.TWorkspace
  DEFINE err STRING
  DEFINE members fglpkgutils.TStringArr
  --cycle a -> b -> a
  VAR root = fglpkgutils.makeTempDir()
  CALL addMemberDir(root, "a", "a", "b:^1.0.0")
  CALL addMemberDir(root, "b", "b", "a:^1.0.0")
  LET members[1] = "a"
  LET members[2] = "b"
  TOK(workspace.init(root, members) IS NULL)
  CALL workspace.load(root) RETURNING ok, ws, err
  TOK(NOT ok)
  TOK(fglpkgutils.contains(err, "workspace dependency cycle:"))
  TOK(fglpkgutils.contains(err, 'cycle detected at "a"'))
  CALL fglpkgutils.rmrf(root)

  --duplicate member names
  VAR root2 = fglpkgutils.makeTempDir()
  CALL addMemberDir(root2, "one", "same", "")
  CALL addMemberDir(root2, "two", "same", "")
  CALL members.clear()
  LET members[1] = "one"
  LET members[2] = "two"
  TOK(workspace.init(root2, members) IS NULL)
  CALL workspace.load(root2) RETURNING ok, ws, err
  TOK(NOT ok)
  TEQ(err, 'workspace has two members with name "same"')
  CALL fglpkgutils.rmrf(root2)

  --empty members list
  VAR root3 = fglpkgutils.makeTempDir()
  CALL fglpkgutils.writeStringToFile(
      workspace.workspacePath(root3), '{"members":[]}')
  CALL workspace.load(root3) RETURNING ok, ws, err
  TOK(NOT ok)
  TOK(fglpkgutils.contains(err, "members list is empty"))
  CALL fglpkgutils.rmrf(root3)
END FUNCTION

FUNCTION testQueries()
  DEFINE ok BOOLEAN
  DEFINE ws workspace.TWorkspace
  DEFINE err STRING
  DEFINE members fglpkgutils.TStringArr
  VAR root = fglpkgutils.makeTempDir()
  CALL addMemberDir(root, "app", "app", "libb:^1.0.0,gson-tools:^2.0.0")
  CALL addMemberDir(root, "libb", "libb", "liba:^1.0.0,gson-tools:^1.5.0")
  CALL addMemberDir(root, "liba", "liba", "")
  LET members[1] = "app"
  LET members[2] = "libb"
  LET members[3] = "liba"
  TOK(workspace.init(root, members) IS NULL)
  CALL workspace.load(root) RETURNING ok, ws, err
  TOK(ok)

  TOK(workspace.isLocal(ws, "liba"))
  TOK(NOT workspace.isLocal(ws, "gson-tools"))

  --localDeps of app: only libb (sorted)
  VAR appIdx = workspace.memberIndex(ws, "app")
  VAR deps = workspace.localDeps(ws, ws.members[appIdx].m)
  TEQ(fglpkgutils.joinArr(deps, ","), "libb")

  --externalDeps: gson-tools only, first-seen constraint wins (topo order
  --puts libb before app, so ^1.5.0 is seen first)
  VAR merged = workspace.externalDeps(ws)
  TEQ(merged.dependencies.fgl.getLength(), 1)
  TEQ(merged.dependencies.fgl["gson-tools"], "^1.5.0")

  --summary format
  VAR sum = workspace.summary(ws)
  TOK(fglpkgutils.startsWith(sum, "Workspace root: "))
  TOK(fglpkgutils.contains(sum, "Members (3):"))
  TOK(fglpkgutils.contains(sum, "[local deps: libb]"))
  TOK(fglpkgutils.contains(sum, "[local deps: liba]"))
  CALL fglpkgutils.rmrf(root)
END FUNCTION

--─── resolver integration ───────────────────────────────────────────────────

FUNCTION fakeVersions(name STRING)
    RETURNS(BOOLEAN, resolver.TCandidateVersions, STRING)
  DEFINE out, empty resolver.TCandidateVersions
  DEFINE i INT
  FOR i = 1 TO _db.getLength()
    IF _db[i].name == name THEN
      LET out[out.getLength() + 1].version = _db[i].version
      LET out[out.getLength()].genero = ""
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

FUNCTION testResolverIntegration()
  DEFINE ok BOOLEAN
  DEFINE ws workspace.TWorkspace
  DEFINE plan resolver.TPlan
  DEFINE err STRING
  DEFINE members, gotPkgs fglpkgutils.TStringArr
  DEFINE i INT

  --workspace: app depends on local libb + external extpkg;
  --libb itself depends on external extpkg2
  VAR root = fglpkgutils.makeTempDir()
  CALL addMemberDir(root, "app", "app", "libb:^1.0.0,extpkg:^1.0.0")
  CALL addMemberDir(root, "libb", "libb", "extpkg2:^1.0.0")
  LET members[1] = "app"
  LET members[2] = "libb"
  TOK(workspace.init(root, members) IS NULL)
  CALL workspace.load(root) RETURNING ok, ws, err
  TOK(ok)

  --fake registry serves only the external packages
  CALL _db.clear()
  LET _db[1].name = "extpkg"
  LET _db[1].version = "1.2.0"
  LET _db[1].info.name = "extpkg"
  LET _db[1].info.version = "1.2.0"
  LET _db[1].info.downloadUrl = "https://x/extpkg.zip"
  LET _db[2].name = "extpkg2"
  LET _db[2].version = "1.1.0"
  LET _db[2].info.name = "extpkg2"
  LET _db[2].info.version = "1.1.0"
  LET _db[2].info.downloadUrl = "https://x/extpkg2.zip"

  CALL resolver.setFetchers(
      FUNCTION fakeVersions, FUNCTION fakeInfo,
      genero.mustParseGenero("6.00.02"))
  CALL resolver.setWorkspace(ws)

  --the root project (not itself a member) depends on libb + extpkg
  VAR rootM = manifest.newManifest("consumer", "1.0.0", "", "")
  CALL manifest.addFGLDependency(rootM, "libb", "^1.0.0")
  CALL manifest.addFGLDependency(rootM, "extpkg", "^1.0.0")
  CALL resolver.resolve(rootM) RETURNING ok, plan, err
  TOK(ok)
  --libb is satisfied locally: not in packages, listed as a local member
  TEQ(plan.localMembers.getLength(), 1)
  TEQ(plan.localMembers[1].name, "libb")
  TOK(fglpkgutils.endsWith(plan.localMembers[1].path, "/libb"))
  --libb's own external dep and the root's external dep both resolved
  FOR i = 1 TO plan.packages.getLength()
    LET gotPkgs[i] = plan.packages[i].name
  END FOR
  TEQ(fglpkgutils.joinArr(gotPkgs, ","), "extpkg2,extpkg")
  --extpkg2 was required by libb (the member walk), not by <root>
  TEQ(plan.packages[1].requiredBy[1], "libb")

  --hermetic again for other test programs
  CALL resolver.setFetchers(
      FUNCTION fakeVersions, FUNCTION fakeInfo,
      genero.mustParseGenero("6.00.02"))
  CALL fglpkgutils.rmrf(root)
END FUNCTION
