#+ monorepo workspace support (fglpkg.workspace.json)
#+ port of internal/workspace/workspace.go — local member packages depend
#+ on each other from disk without being published/installed
OPTIONS SHORT CIRCUIT
PACKAGE fglpkg
IMPORT os
IMPORT util
IMPORT FGL fglpkg.fglpkgutils
IMPORT FGL fglpkg.glob
IMPORT FGL fglpkg.manifest
&include "myassert.inc"

PUBLIC CONSTANT WORKSPACE_FILENAME = "fglpkg.workspace.json"

PUBLIC TYPE TMember RECORD
  path STRING, --absolute member directory
  relPath STRING, --as listed in the workspace file
  m manifest.TManifest
END RECORD

PUBLIC TYPE TMembers DYNAMIC ARRAY OF TMember

PUBLIC TYPE TWorkspace RECORD
  rootDir STRING,
  members TMembers, --topologically sorted: deps before dependents
  loaded BOOLEAN
END RECORD

PRIVATE TYPE TWorkspaceFile RECORD
  members fglpkgutils.TStringArr,
  exclude fglpkgutils.TStringArr --parsed but unused (Go parity)
END RECORD

FUNCTION workspacePath(dir STRING) RETURNS STRING
  RETURN os.Path.join(dir, WORKSPACE_FILENAME)
END FUNCTION

FUNCTION workspaceExists(dir STRING) RETURNS BOOLEAN
  RETURN os.Path.exists(workspacePath(dir))
END FUNCTION

#+walks up from dir looking for fglpkg.workspace.json;
#+returns the containing directory or NULL at the filesystem root
FUNCTION findRoot(dir STRING) RETURNS STRING
  VAR d = os.Path.fullPath(dir)
  WHILE TRUE
    IF workspaceExists(d) THEN
      RETURN d
    END IF
    VAR parent = os.Path.dirName(d)
    IF parent == d OR parent IS NULL THEN
      RETURN NULL
    END IF
    LET d = parent
  END WHILE
  RETURN NULL
END FUNCTION

#+loads and validates the workspace rooted at rootDir
FUNCTION load(rootDir STRING) RETURNS(BOOLEAN, TWorkspace, STRING)
  DEFINE ws, empty TWorkspace
  DEFINE wf TWorkspaceFile
  DEFINE ok BOOLEAN
  DEFINE err STRING
  DEFINE i, j INT
  DEFINE seen DICTIONARY OF BOOLEAN

  LET ws.rootDir = os.Path.fullPath(rootDir)
  VAR path = workspacePath(ws.rootDir)
  IF NOT os.Path.exists(path) THEN
    RETURN FALSE, empty,
        SFMT("cannot read %1: no such file", WORKSPACE_FILENAME)
  END IF
  TRY
    CALL util.JSON.parse(fglpkgutils.readTextFile(path), wf)
  CATCH
    RETURN FALSE, empty, SFMT("invalid %1", WORKSPACE_FILENAME)
  END TRY
  IF wf.members.getLength() == 0 THEN
    RETURN FALSE, empty,
        SFMT("%1: members list is empty", WORKSPACE_FILENAME)
  END IF

  FOR i = 1 TO wf.members.getLength()
    VAR mi = ws.members.getLength() + 1
    LET ws.members[mi].relPath = wf.members[i]
    LET ws.members[mi].path =
        os.Path.fullPath(os.Path.join(ws.rootDir, wf.members[i]))
    IF NOT manifest.manifestExists(ws.members[mi].path) THEN
      RETURN FALSE, empty,
          SFMT('workspace member "%1": no %2', wf.members[i],
              manifest.MANIFEST_FILENAME)
    END IF
    CALL manifest.load(ws.members[mi].path) RETURNING ok, ws.members[mi].m, err
    IF NOT ok THEN
      RETURN FALSE, empty,
          SFMT('workspace member "%1": %2', wf.members[i], err)
    END IF
    IF seen.contains(ws.members[mi].m.name) THEN
      RETURN FALSE, empty,
          SFMT('workspace has two members with name "%1"', ws.members[mi].m.name)
    END IF
    LET seen[ws.members[mi].m.name] = TRUE
    UNUSED_VAR(j)
  END FOR

  LET err = topoSort(ws)
  IF err IS NOT NULL THEN
    RETURN FALSE, empty, SFMT("workspace dependency cycle: %1", err)
  END IF
  LET ws.loaded = TRUE
  RETURN TRUE, ws, NULL
END FUNCTION

#+creates fglpkg.workspace.json in rootDir
FUNCTION init(rootDir STRING, members fglpkgutils.TStringArr) RETURNS STRING
  IF workspaceExists(rootDir) THEN
    RETURN SFMT("%1 already exists", WORKSPACE_FILENAME)
  END IF
  RETURN writeWorkspaceFile(rootDir, members)
END FUNCTION

#+adds a member path to an existing workspace file
FUNCTION addMember(rootDir STRING, relPath STRING) RETURNS STRING
  DEFINE wf TWorkspaceFile
  DEFINE i INT
  TRY
    CALL util.JSON.parse(
        fglpkgutils.readTextFile(workspacePath(rootDir)), wf)
  CATCH
    RETURN SFMT("invalid %1", WORKSPACE_FILENAME)
  END TRY
  FOR i = 1 TO wf.members.getLength()
    IF wf.members[i] == relPath THEN
      RETURN SFMT('"%1" is already a workspace member', relPath)
    END IF
  END FOR
  LET wf.members[wf.members.getLength() + 1] = relPath
  RETURN writeWorkspaceFile(rootDir, wf.members)
END FUNCTION

PRIVATE FUNCTION writeWorkspaceFile(
    rootDir STRING, members fglpkgutils.TStringArr)
    RETURNS STRING
  DEFINE i INT
  VAR obj = util.JSONObject.create()
  VAR arr = util.JSONArray.create()
  FOR i = 1 TO members.getLength()
    CALL arr.put(arr.getLength() + 1, members[i])
  END FOR
  CALL obj.put("members", arr)
  TRY
    CALL fglpkgutils.writeStringToFile(workspacePath(rootDir),
        manifest.prettyJSON(obj.toString()) || "\n")
  CATCH
    RETURN SFMT("cannot write %1", WORKSPACE_FILENAME)
  END TRY
  RETURN NULL
END FUNCTION

--─── queries ────────────────────────────────────────────────────────────────

#+the index of the member with the given manifest name, 0 when absent
FUNCTION memberIndex(ws TWorkspace, name STRING) RETURNS INT
  DEFINE i INT
  FOR i = 1 TO ws.members.getLength()
    IF ws.members[i].m.name == name THEN
      RETURN i
    END IF
  END FOR
  RETURN 0
END FUNCTION

FUNCTION isLocal(ws TWorkspace, name STRING) RETURNS BOOLEAN
  RETURN ws.loaded AND memberIndex(ws, name) > 0
END FUNCTION

#+the member names of m's FGL deps that are workspace members, sorted
FUNCTION localDeps(ws TWorkspace, m manifest.TManifest)
    RETURNS fglpkgutils.TStringArr
  DEFINE out fglpkgutils.TStringArr
  DEFINE i INT
  VAR names = m.dependencies.fgl.getKeys()
  FOR i = 1 TO names.getLength()
    IF isLocal(ws, names[i]) THEN
      LET out[out.getLength() + 1] = names[i]
    END IF
  END FOR
  CALL glob.sortBytewise(out)
  RETURN out
END FUNCTION

#+a merged manifest of every member's NON-local deps, for a single
#+resolution pass: fgl first-seen wins, java deduped by key
FUNCTION externalDeps(ws TWorkspace) RETURNS manifest.TManifest
  DEFINE merged manifest.TManifest
  DEFINE seenJava DICTIONARY OF BOOLEAN
  DEFINE i, j INT
  LET merged = manifest.newManifest("__workspace__", "0.0.0", "", "")
  FOR i = 1 TO ws.members.getLength()
    VAR names = ws.members[i].m.dependencies.fgl.getKeys()
    FOR j = 1 TO names.getLength()
      IF isLocal(ws, names[j]) THEN
        CONTINUE FOR
      END IF
      IF NOT merged.dependencies.fgl.contains(names[j]) THEN
        LET merged.dependencies.fgl[names[j]] =
            ws.members[i].m.dependencies.fgl[names[j]]
      END IF
    END FOR
    FOR j = 1 TO ws.members[i].m.dependencies.java.getLength()
      VAR key = manifest.javaKey(ws.members[i].m.dependencies.java[j])
      IF NOT seenJava.contains(key) THEN
        CALL manifest.addJavaDependency(merged,
            ws.members[i].m.dependencies.java[j])
        LET seenJava[key] = TRUE
      END IF
    END FOR
  END FOR
  RETURN merged
END FUNCTION

#+member source directories (topo order) to prepend to FGLLDPATH
FUNCTION fglldpathEntries(ws TWorkspace) RETURNS fglpkgutils.TStringArr
  DEFINE out fglpkgutils.TStringArr
  DEFINE i INT
  FOR i = 1 TO ws.members.getLength()
    LET out[i] = ws.members[i].path
  END FOR
  RETURN out
END FUNCTION

#+the human summary used by `fglpkg workspace info`
FUNCTION summary(ws TWorkspace) RETURNS STRING
  DEFINE i INT
  VAR sb = base.StringBuffer.create()
  CALL sb.append(SFMT("Workspace root: %1\n", ws.rootDir))
  CALL sb.append(SFMT("Members (%1):\n", ws.members.getLength()))
  FOR i = 1 TO ws.members.getLength()
    VAR names = localDeps(ws, ws.members[i].m)
    VAR line = SFMT("  %1 v%2",
        fglpkgutils.padRight(ws.members[i].m.name, 30),
        fglpkgutils.padRight(ws.members[i].m.version, 10))
    IF names.getLength() > 0 THEN
      LET line = SFMT("%1  [local deps: %2]",
          line, fglpkgutils.joinArr(names, ", "))
    END IF
    CALL sb.append(line || "\n")
  END FOR
  RETURN sb.toString()
END FUNCTION

--─── topological sort ───────────────────────────────────────────────────────

#+sorts members so dependencies precede dependents (DFS, post-order);
#+returns a cycle description or NULL
PRIVATE FUNCTION topoSort(ws TWorkspace) RETURNS STRING
  DEFINE state DICTIONARY OF INT --0/absent=unvisited, 1=visiting, 2=visited
  DEFINE sorted TMembers
  DEFINE i INT
  DEFINE err STRING
  FOR i = 1 TO ws.members.getLength()
    LET err = topoVisit(ws, i, state, sorted)
    IF err IS NOT NULL THEN
      RETURN err
    END IF
  END FOR
  CALL sorted.copyTo(ws.members)
  RETURN NULL
END FUNCTION

PRIVATE FUNCTION topoVisit(
    ws TWorkspace, idx INT, state DICTIONARY OF INT, sorted TMembers)
    RETURNS STRING
  DEFINE i INT
  DEFINE err STRING
  VAR name = ws.members[idx].m.name
  IF state.contains(name) THEN
    IF state[name] == 1 THEN
      RETURN SFMT('cycle detected at "%1"', name)
    END IF
    RETURN NULL --visited
  END IF
  LET state[name] = 1
  VAR deps = ws.members[idx].m.dependencies.fgl.getKeys()
  FOR i = 1 TO deps.getLength()
    VAR di = memberIndex(ws, deps[i])
    IF di == 0 THEN
      CONTINUE FOR --external deps are not followed
    END IF
    LET err = topoVisit(ws, di, state, sorted)
    IF err IS NOT NULL THEN
      RETURN SFMT("%1 → %2", name, err)
    END IF
  END FOR
  LET state[name] = 2
  LET sorted[sorted.getLength() + 1] = ws.members[idx]
  RETURN NULL
END FUNCTION
