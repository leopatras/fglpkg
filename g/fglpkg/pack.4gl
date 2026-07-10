#+ building the publishable package zip
#+ port of buildPackageZip and helpers from internal/cli/{cli,pack}.go
#+ the zip is staged in a temp tree and created with the external zip tool
OPTIONS SHORT CIRCUIT
PACKAGE fglpkg
IMPORT os
IMPORT FGL fglpkg.fglpkgutils
IMPORT FGL fglpkg.glob
IMPORT FGL fglpkg.ignore
IMPORT FGL fglpkg.checksum
IMPORT FGL fglpkg.manifest
&include "myassert.inc"

PUBLIC TYPE TPackEntry RECORD
  name STRING, --in-zip path (forward slashes)
  size BIGINT
END RECORD

PUBLIC TYPE TPackResult RECORD
  zipPath STRING, --temp file holding the built zip (caller moves/deletes)
  checksum STRING,
  size BIGINT,
  entries DYNAMIC ARRAY OF TPackEntry --sorted by name
END RECORD

--one collected file: where it is on disk and where it goes in the zip
PRIVATE TYPE TFileMapping RECORD
  diskPath STRING,
  zipPath STRING
END RECORD

PRIVATE TYPE TFileMappings DYNAMIC ARRAY OF TFileMapping

#+the registry variant tag for a package: "webcomponent" for pure-WC
#+packages, "genero<major>" otherwise
FUNCTION artifactVariant(m manifest.TManifest, generoMajor STRING)
    RETURNS STRING
  IF manifest.hasWebcomponents(m) AND NOT manifest.hasBDLContent(m) THEN
    RETURN "webcomponent"
  END IF
  RETURN SFMT("genero%1", generoMajor)
END FUNCTION

FUNCTION artifactFilename(name STRING, version STRING, variant STRING)
    RETURNS STRING
  RETURN SFMT("%1-%2-%3.zip", name, version, variant)
END FUNCTION

FUNCTION variantDescription(variant STRING) RETURNS STRING
  IF variant == "webcomponent" THEN
    RETURN "webcomponent variant"
  END IF
  IF fglpkgutils.startsWith(variant, "genero") THEN
    RETURN SFMT("Genero %1 variant", variant.subString(7, variant.getLength()))
  END IF
  RETURN SFMT("%1 variant", variant)
END FUNCTION

#+builds the publishable zip for the manifest in the current directory;
#+the result's zipPath is a temp file the caller moves or deletes
FUNCTION buildPackageZip(m manifest.TManifest)
    RETURNS(BOOLEAN, TPackResult, STRING)
  DEFINE res, empty TPackResult
  DEFINE files TFileMappings
  DEFINE added DICTIONARY OF BOOLEAN
  DEFINE err STRING
  DEFINE i INT

  VAR rules = ignore.loadIgnore(".")

  --mixed packages run BOTH walkers: BDL files at project-relative paths,
  --webcomponents at <COMPONENTTYPE>/<file> (webcomponents/ prefix stripped)
  IF manifest.hasBDLContent(m) OR NOT manifest.hasWebcomponents(m) THEN
    LET err = collectBDLFiles(m, rules, added, files)
    IF err IS NOT NULL THEN
      RETURN FALSE, empty, err
    END IF
  END IF
  IF manifest.hasWebcomponents(m) THEN
    LET err = collectWebcomponentFiles(m, rules, added, files)
    IF err IS NOT NULL THEN
      RETURN FALSE, empty, err
    END IF
  END IF
  LET err = collectDocFiles(m, rules, added, files)
  IF err IS NOT NULL THEN
    RETURN FALSE, empty, err
  END IF

  --stage the tree and create the zip with the external tool
  VAR staging = fglpkgutils.makeTempDir()
  FOR i = 1 TO files.getLength()
    VAR target = os.Path.join(staging, files[i].zipPath)
    CALL fglpkgutils.mkdirp(os.Path.dirName(target))
    IF NOT os.Path.copy(files[i].diskPath, target) THEN
      CALL fglpkgutils.rmrf(staging)
      RETURN FALSE, empty,
          SFMT("cannot add %1 to zip", files[i].diskPath)
    END IF
  END FOR
  --always include a publish-safe manifest (no devDependencies)
  IF NOT added.contains(manifest.MANIFEST_FILENAME) THEN
    CALL fglpkgutils.writeStringToFile(
        os.Path.join(staging, manifest.MANIFEST_FILENAME),
        manifest.toJSONString(manifest.publishCopy(m)) || "\n")
  END IF

  VAR zipPath = fglpkgutils.makeTempName() || ".zip"
  VAR code = 0
  RUN SFMT("cd %1 && zip -r -X -q %2 .",
      fglpkgutils.quote(staging), fglpkgutils.quote(zipPath))
      RETURNING code
  IF code THEN
    CALL fglpkgutils.rmrf(staging)
    RETURN FALSE, empty, "cannot build package zip (is 'zip' installed?)"
  END IF

  LET res.zipPath = zipPath
  LET res.checksum = checksum.sha256File(zipPath)
  LET res.size = os.Path.size(zipPath)
  LET res.entries = listStagedEntries(staging)
  CALL fglpkgutils.rmrf(staging)
  RETURN TRUE, res, NULL
END FUNCTION

#+the zip entry list (name + size) from the staging tree, sorted by name
PRIVATE FUNCTION listStagedEntries(staging STRING)
    RETURNS DYNAMIC ARRAY OF TPackEntry
  DEFINE entries DYNAMIC ARRAY OF TPackEntry
  DEFINE i INT
  VAR names = glob.collectFiles(staging)
  FOR i = 1 TO names.getLength()
    LET entries[i].name = names[i]
    LET entries[i].size = os.Path.size(os.Path.join(staging, names[i]))
  END FOR
  RETURN entries
END FUNCTION

#+walks the BDL source tree applying the manifest's `files` patterns
#+(default *.42m/*.42f/*.sch, matched against basenames) and includes
#+declared bin scripts (which override .fglpkgignore)
PRIVATE FUNCTION collectBDLFiles(
    m manifest.TManifest, rules ignore.TIgnoreRules,
    added DICTIONARY OF BOOLEAN, files TFileMappings)
    RETURNS STRING
  DEFINE patterns fglpkgutils.TStringArr
  DEFINE i, j INT
  VAR root = m.root
  IF root IS NULL OR root.getLength() == 0 THEN
    LET root = "."
  END IF
  IF m.files.getLength() > 0 THEN
    CALL m.files.copyTo(patterns)
  ELSE
    LET patterns[1] = "*.42m"
    LET patterns[2] = "*.42f"
    LET patterns[3] = "*.sch"
  END IF

  --walk the root tree; paths stay relative to the project directory so
  --the full structure is preserved in the zip
  VAR walked = walkTree(root)
  FOR i = 1 TO walked.getLength()
    VAR relPath = IIF(root == ".", walked[i], SFMT("%1/%2", root, walked[i]))
    VAR base = os.Path.baseName(relPath)
    FOR j = 1 TO patterns.getLength()
      IF glob.pathMatch(patterns[j], base) THEN
        IF added.contains(relPath) THEN
          CONTINUE FOR
        END IF
        IF ignore.shouldExclude(rules, relPath, FALSE) THEN
          CONTINUE FOR
        END IF
        LET added[relPath] = TRUE
        LET files[files.getLength() + 1].diskPath = relPath
        LET files[files.getLength()].zipPath = relPath
      END IF
    END FOR
  END FOR

  --bin scripts named in the manifest take precedence over .fglpkgignore
  VAR bins = manifest.binFiles(m)
  FOR i = 1 TO bins.getLength()
    VAR fullPath = fglpkgutils.backslash2slash(os.Path.join(root, bins[i]))
    IF fglpkgutils.startsWith(fullPath, "./") THEN
      LET fullPath = fullPath.subString(3, fullPath.getLength())
    END IF
    IF added.contains(fullPath) THEN
      CONTINUE FOR
    END IF
    IF NOT os.Path.exists(fullPath) THEN
      RETURN SFMT('bin script "%1" not found', bins[i])
    END IF
    IF os.Path.isDirectory(fullPath) THEN
      RETURN SFMT('bin script "%1" is a directory, not a file', bins[i])
    END IF
    LET added[fullPath] = TRUE
    LET files[files.getLength() + 1].diskPath = fullPath
    LET files[files.getLength()].zipPath = fullPath
  END FOR
  RETURN NULL
END FUNCTION

#+adds each declared webcomponents/<COMPONENTTYPE>/ tree with the leading
#+"webcomponents/" stripped; the <COMPONENTTYPE>.html entry point and the
#+directory itself are required
PRIVATE FUNCTION collectWebcomponentFiles(
    m manifest.TManifest, rules ignore.TIgnoreRules,
    added DICTIONARY OF BOOLEAN, files TFileMappings)
    RETURNS STRING
  DEFINE i, j INT
  FOR i = 1 TO m.webcomponents.getLength()
    VAR name = m.webcomponents[i]
    VAR srcDir = SFMT("webcomponents/%1", name)
    IF NOT os.Path.isDirectory(srcDir) THEN
      RETURN SFMT('webcomponent "%1": directory %2/ is missing', name, srcDir)
    END IF
    IF NOT os.Path.exists(SFMT("%1/%2.html", srcDir, name)) THEN
      RETURN SFMT('webcomponent "%1": missing required entry point %2/%3.html',
          name, srcDir, name)
    END IF
    VAR inside = walkTree(srcDir)
    FOR j = 1 TO inside.getLength()
      VAR relPath = SFMT("%1/%2", srcDir, inside[j])
      IF added.contains(relPath) THEN
        CONTINUE FOR
      END IF
      IF ignore.shouldExclude(rules, relPath, FALSE) THEN
        CONTINUE FOR
      END IF
      LET added[relPath] = TRUE
      --in-zip path is <COMPONENTTYPE>/<file>
      LET files[files.getLength() + 1].diskPath = relPath
      LET files[files.getLength()].zipPath = SFMT("%1/%2", name, inside[j])
    END FOR
  END FOR
  RETURN NULL
END FUNCTION

#+adds files matching the manifest's docs globs at project-relative paths
PRIVATE FUNCTION collectDocFiles(
    m manifest.TManifest, rules ignore.TIgnoreRules,
    added DICTIONARY OF BOOLEAN, files TFileMappings)
    RETURNS STRING
  DEFINE i, j INT
  IF m.docs.getLength() == 0 THEN
    RETURN NULL
  END IF
  VAR walked = walkTree(".")
  FOR i = 1 TO walked.getLength()
    VAR relPath = walked[i]
    IF added.contains(relPath) THEN
      CONTINUE FOR
    END IF
    FOR j = 1 TO m.docs.getLength()
      IF glob.matchGlob(m.docs[j], relPath) THEN
        IF NOT ignore.shouldExclude(rules, relPath, FALSE) THEN
          LET added[relPath] = TRUE
          LET files[files.getLength() + 1].diskPath = relPath
          LET files[files.getLength()].zipPath = relPath
        END IF
        EXIT FOR
      END IF
    END FOR
  END FOR
  RETURN NULL
END FUNCTION

#+collects all files below root as sorted project-relative paths, skipping
#+.fglpkg/ directories (the local package cache never ships in a zip);
#+when root is "." paths are plain relative paths without a "./" prefix
PRIVATE FUNCTION walkTree(root STRING) RETURNS fglpkgutils.TStringArr
  DEFINE arr fglpkgutils.TStringArr
  CALL walkTreeInt(root, NULL, arr)
  CALL glob.sortBytewise(arr)
  RETURN arr
END FUNCTION

PRIVATE FUNCTION walkTreeInt(
    dir STRING, rel STRING, arr fglpkgutils.TStringArr)
  DEFINE entry STRING
  VAR h = os.Path.dirOpen(dir)
  WHILE h > 0
    LET entry = os.Path.dirNext(h)
    IF entry IS NULL THEN
      EXIT WHILE
    END IF
    IF entry == "." OR entry == ".." THEN
      CONTINUE WHILE
    END IF
    VAR full = os.Path.join(dir, entry)
    VAR childRel = IIF(rel IS NULL, entry, SFMT("%1/%2", rel, entry))
    IF os.Path.isDirectory(full) THEN
      IF entry == ".fglpkg" THEN
        CONTINUE WHILE --never pack the local package cache
      END IF
      CALL walkTreeInt(full, childRel, arr)
    ELSE
      LET arr[arr.getLength() + 1] = childRel
    END IF
  END WHILE
  IF h > 0 THEN
    CALL os.Path.dirClose(h)
  END IF
END FUNCTION
