#+ environment variable export generation (FGLLDPATH/CLASSPATH/FGLIMAGEPATH)
#+ port of internal/env/env.go
OPTIONS SHORT CIRCUIT
PACKAGE fglpkg
IMPORT os
IMPORT FGL fglpkg.fglpkgutils
IMPORT FGL fglpkg.glob
&include "myassert.inc"

#+shell export lines suitable for eval; fglpkg-managed paths are prepended
#+so user/system entries are never lost
FUNCTION generateExports(home STRING) RETURNS fglpkgutils.TStringArr
  DEFINE lines fglpkgutils.TStringArr
  VAR fglldpath = buildFGLLDPATH(home)
  LET lines[lines.getLength() + 1] = prependExportLine("FGLLDPATH", fglldpath)
  VAR classpath = buildJavaClasspath(home)
  IF classpath.getLength() > 0 THEN
    LET lines[lines.getLength() + 1] = prependExportLine("CLASSPATH", classpath)
  END IF
  VAR fglimagepath = buildFGLIMAGEPATH(home)
  IF fglimagepath.getLength() > 0 THEN
    LET lines[lines.getLength() + 1] =
        prependExportLine("FGLIMAGEPATH", fglimagepath)
    LET lines[lines.getLength() + 1] = gasHintComment(fglimagepath)
  END IF
  RETURN lines
END FUNCTION

#+export lines using only the local project's .fglpkg directories
FUNCTION generateLocal(home STRING) RETURNS fglpkgutils.TStringArr
  DEFINE lines fglpkgutils.TStringArr
  UNUSED_VAR(home)
  VAR localPkgs = os.Path.fullPath(os.Path.join(".fglpkg", "packages"))
  VAR fglldpath = joinPaths(listSubdirs(localPkgs), pathSeparator())
  IF fglldpath.getLength() > 0 THEN
    LET lines[lines.getLength() + 1] = prependExportLine("FGLLDPATH", fglldpath)
  END IF
  VAR localJars = os.Path.fullPath(os.Path.join(".fglpkg", "jars"))
  VAR classpath = joinPaths(listJars(localJars), pathSeparator())
  IF classpath.getLength() > 0 THEN
    LET lines[lines.getLength() + 1] = prependExportLine("CLASSPATH", classpath)
  END IF
  VAR localWC = os.Path.fullPath(os.Path.join(".fglpkg", "webcomponents"))
  IF listEntries(localWC).getLength() > 0 THEN
    VAR fglimagepath = os.Path.dirName(localWC)
    LET lines[lines.getLength() + 1] =
        prependExportLine("FGLIMAGEPATH", fglimagepath)
    LET lines[lines.getLength() + 1] = gasHintComment(fglimagepath)
  END IF
  RETURN lines
END FUNCTION

#+environment assignments in Genero Studio format:
#+$(ProjectDir) base, $(VARIABLE) references, ';' separator (always)
FUNCTION generateGST(home STRING) RETURNS fglpkgutils.TStringArr
  DEFINE lines fglpkgutils.TStringArr
  DEFINE parts fglpkgutils.TStringArr
  DEFINE i INT
  UNUSED_VAR(home)
  VAR pkgs = listSubdirs(os.Path.fullPath(os.Path.join(".fglpkg", "packages")))
  CALL parts.clear()
  FOR i = 1 TO pkgs.getLength()
    LET parts[i] = SFMT("$(ProjectDir)/.fglpkg/packages/%1",
        os.Path.baseName(pkgs[i]))
  END FOR
  IF parts.getLength() > 0 THEN
    LET lines[lines.getLength() + 1] =
        SFMT("FGLLDPATH=%1;$(FGLLDPATH)", fglpkgutils.joinArr(parts, ";"))
  END IF
  VAR jars = listJars(os.Path.fullPath(os.Path.join(".fglpkg", "jars")))
  CALL parts.clear()
  FOR i = 1 TO jars.getLength()
    LET parts[i] = SFMT("$(ProjectDir)/.fglpkg/jars/%1",
        os.Path.baseName(jars[i]))
  END FOR
  IF parts.getLength() > 0 THEN
    LET lines[lines.getLength() + 1] =
        SFMT("CLASSPATH=%1;$(CLASSPATH)", fglpkgutils.joinArr(parts, ";"))
  END IF
  RETURN lines
END FUNCTION

#+one --webcomponent flag per installed component for gwabuildtool;
#+local project components first, then global, deduplicated by name
FUNCTION generateGWA(home STRING) RETURNS fglpkgutils.TStringArr
  DEFINE lines fglpkgutils.TStringArr
  DEFINE seen DICTIONARY OF BOOLEAN
  DEFINE i INT
  VAR globalWC = fglpkgutils.webcomponentsDir(home)
  VAR localWC = os.Path.fullPath(os.Path.join(".fglpkg", "webcomponents"))
  IF localWC != globalWC THEN
    VAR localDirs = listSubdirs(localWC)
    FOR i = 1 TO localDirs.getLength()
      VAR name = os.Path.baseName(localDirs[i])
      IF NOT seen.contains(name) THEN
        LET seen[name] = TRUE
        LET lines[lines.getLength() + 1] =
            SFMT("--webcomponent %1", localDirs[i])
      END IF
    END FOR
  END IF
  VAR globalDirs = listSubdirs(globalWC)
  FOR i = 1 TO globalDirs.getLength()
    VAR gname = os.Path.baseName(globalDirs[i])
    IF NOT seen.contains(gname) THEN
      LET seen[gname] = TRUE
      LET lines[lines.getLength() + 1] =
          SFMT("--webcomponent %1", globalDirs[i])
    END IF
  END FOR
  RETURN lines
END FUNCTION

#+the raw fglpkg-managed FGLLDPATH value (no export prefix);
#+precedence: local .fglpkg/packages entries, then global packages
#+(workspace member paths join here in the workspace phase)
FUNCTION buildFGLLDPATH(home STRING) RETURNS STRING
  DEFINE parts fglpkgutils.TStringArr
  DEFINE seen DICTIONARY OF BOOLEAN
  VAR globalPkgs = fglpkgutils.packagesDir(home)
  VAR localPkgs = os.Path.fullPath(os.Path.join(".fglpkg", "packages"))
  IF localPkgs != globalPkgs THEN
    CALL addAll(parts, seen, listSubdirs(localPkgs))
  END IF
  CALL addAll(parts, seen, listSubdirs(globalPkgs))
  RETURN fglpkgutils.joinArr(parts, pathSeparator())
END FUNCTION

#+the raw fglpkg-managed CLASSPATH value (all .jar files, local first)
FUNCTION buildJavaClasspath(home STRING) RETURNS STRING
  DEFINE parts fglpkgutils.TStringArr
  DEFINE seen DICTIONARY OF BOOLEAN
  VAR globalJars = fglpkgutils.jarsDir(home)
  VAR localJars = os.Path.fullPath(os.Path.join(".fglpkg", "jars"))
  IF localJars != globalJars THEN
    CALL addAll(parts, seen, listJars(localJars))
  END IF
  CALL addAll(parts, seen, listJars(globalJars))
  RETURN fglpkgutils.joinArr(parts, pathSeparator())
END FUNCTION

#+directories to prepend to FGLIMAGEPATH: the *parent* of each non-empty
#+webcomponents/ dir, per Genero's
#+"<fglimagepath-dir>/webcomponents/<COMPONENTTYPE>" search rule
PRIVATE FUNCTION buildFGLIMAGEPATH(home STRING) RETURNS STRING
  DEFINE parts fglpkgutils.TStringArr
  DEFINE seen DICTIONARY OF BOOLEAN
  DEFINE one fglpkgutils.TStringArr
  VAR globalWC = fglpkgutils.webcomponentsDir(home)
  VAR localWC = os.Path.fullPath(os.Path.join(".fglpkg", "webcomponents"))
  IF localWC != globalWC AND listEntries(localWC).getLength() > 0 THEN
    CALL one.clear()
    LET one[1] = os.Path.dirName(localWC)
    CALL addAll(parts, seen, one)
  END IF
  IF listEntries(globalWC).getLength() > 0 THEN
    CALL one.clear()
    LET one[1] = os.Path.dirName(globalWC)
    CALL addAll(parts, seen, one)
  END IF
  RETURN fglpkgutils.joinArr(parts, pathSeparator())
END FUNCTION

#+hint for GAS users: the .xcf <WEB_COMPONENT_DIRECTORY> values matching
#+the generated FGLIMAGEPATH (fglpkg cannot edit .xcf files)
PRIVATE FUNCTION gasHintComment(fglimagepathValue STRING) RETURNS STRING
  DEFINE wcDirs fglpkgutils.TStringArr
  DEFINE i INT
  VAR paths = fglpkgutils.splitOnChar(fglimagepathValue, pathSeparator())
  FOR i = 1 TO paths.getLength()
    IF paths[i].getLength() == 0 THEN
      CONTINUE FOR
    END IF
    LET wcDirs[wcDirs.getLength() + 1] =
        os.Path.join(paths[i], "webcomponents")
  END FOR
  RETURN SFMT("%1For GAS: add to your .xcf's <WEB_COMPONENT_DIRECTORY>: %2",
      IIF(fglpkgutils.isWin(), "REM ", "# "),
      fglpkgutils.joinArr(wcDirs, pathSeparator()))
END FUNCTION

#+prepends value to existing using the OS path separator
FUNCTION mergeEnvVar(fglpkgValue STRING, existingValue STRING) RETURNS STRING
  IF fglpkgValue IS NULL OR fglpkgValue.getLength() == 0 THEN
    RETURN existingValue
  END IF
  IF existingValue IS NULL OR existingValue.getLength() == 0 THEN
    RETURN fglpkgValue
  END IF
  RETURN fglpkgValue || pathSeparator() || existingValue
END FUNCTION

#+shell statement that prepends value to the existing variable:
#+Unix:    export VAR=value"${VAR:+:$VAR}"
#+Windows: SET VAR=value;%VAR%
FUNCTION prependExportLine(key STRING, value STRING) RETURNS STRING
  IF fglpkgutils.isWin() THEN
    RETURN SFMT("SET %1=%2;%%%1%%", key, value)
  END IF
  --${VAR:+:$VAR} expands to ":$VAR" only when VAR is non-empty
  RETURN SFMT('export %1=%2"${%1:+:$%1}"', key, value)
END FUNCTION

FUNCTION pathSeparator() RETURNS STRING
  RETURN IIF(fglpkgutils.isWin(), ";", ":")
END FUNCTION

--─── directory scanning (sorted byte-wise, like Go os.ReadDir) ──────────────

PRIVATE FUNCTION addAll(
    parts fglpkgutils.TStringArr, seen DICTIONARY OF BOOLEAN,
    add fglpkgutils.TStringArr)
  DEFINE i INT
  FOR i = 1 TO add.getLength()
    IF add[i].getLength() > 0 AND NOT seen.contains(add[i]) THEN
      LET parts[parts.getLength() + 1] = add[i]
      LET seen[add[i]] = TRUE
    END IF
  END FOR
END FUNCTION

#+all entries of a directory (sorted), empty when it doesn't exist
FUNCTION listEntries(dir STRING) RETURNS fglpkgutils.TStringArr
  DEFINE arr fglpkgutils.TStringArr
  DEFINE entry STRING
  IF NOT os.Path.exists(dir) THEN
    RETURN arr
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
    LET arr[arr.getLength() + 1] = entry
  END WHILE
  IF h > 0 THEN
    CALL os.Path.dirClose(h)
  END IF
  CALL glob.sortBytewise(arr)
  RETURN arr
END FUNCTION

#+absolute paths of a directory's subdirectories (sorted)
FUNCTION listSubdirs(dir STRING) RETURNS fglpkgutils.TStringArr
  DEFINE arr fglpkgutils.TStringArr
  DEFINE i INT
  VAR entries = listEntries(dir)
  FOR i = 1 TO entries.getLength()
    VAR full = os.Path.join(dir, entries[i])
    IF os.Path.isDirectory(full) THEN
      LET arr[arr.getLength() + 1] = full
    END IF
  END FOR
  RETURN arr
END FUNCTION

#+absolute paths of a directory's *.jar files (sorted)
FUNCTION listJars(dir STRING) RETURNS fglpkgutils.TStringArr
  DEFINE arr fglpkgutils.TStringArr
  DEFINE i INT
  VAR entries = listEntries(dir)
  FOR i = 1 TO entries.getLength()
    VAR full = os.Path.join(dir, entries[i])
    IF NOT os.Path.isDirectory(full)
        AND fglpkgutils.endsWith(entries[i], ".jar") THEN
      LET arr[arr.getLength() + 1] = full
    END IF
  END FOR
  RETURN arr
END FUNCTION

PRIVATE FUNCTION joinPaths(arr fglpkgutils.TStringArr, sep STRING)
    RETURNS STRING
  RETURN fglpkgutils.joinArr(arr, sep)
END FUNCTION
