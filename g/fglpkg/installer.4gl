#+ package download, verification and extraction into the fglpkg home
#+ port of internal/installer/installer.go — zip handling shells out to
#+ unzip (Unix) / tar (Windows); downloads run sequentially
#+ (FGLPKG_INSTALL_CONCURRENCY is read but ignored in the 4GL port)
OPTIONS SHORT CIRCUIT
PACKAGE fglpkg
IMPORT os
IMPORT util
IMPORT FGL fglpkg.fglpkgutils
IMPORT FGL fglpkg.glob
IMPORT FGL fglpkg.checksum
IMPORT FGL fglpkg.genero
IMPORT FGL fglpkg.manifest
IMPORT FGL fglpkg.registry
IMPORT FGL fglpkg.resolver
IMPORT FGL fglpkg.lockfile
&include "myassert.inc"

PUBLIC TYPE TInstalledPackage RECORD
  name STRING,
  version STRING
END RECORD

PUBLIC TYPE TInstalledPackages DYNAMIC ARRAY OF TInstalledPackage

--tokens used for authenticated downloads; wired by the CLI at startup
DEFINE _githubToken STRING
DEFINE _registryToken STRING

FUNCTION setTokens(githubToken STRING, registryToken STRING)
  LET _githubToken = githubToken
  LET _registryToken = registryToken
END FUNCTION

#+resolves or reads from the lock file, then installs every BDL package
#+and Java JAR; forceResolve bypasses the lock (fglpkg update)
FUNCTION installAll(
    m manifest.TManifest, projectDir STRING, home STRING,
    forceResolve BOOLEAN, production BOOLEAN)
    RETURNS(BOOLEAN, STRING)
  DEFINE ok, gok BOOLEAN
  DEFINE err STRING
  DEFINE gv genero.TGeneroVersion
  DEFINE lf lockfile.TLockfile
  DEFINE plan resolver.TPlan

  CALL ensureDirs(home)

  CALL genero.detect() RETURNING gok, gv, err
  IF NOT gok THEN
    RETURN FALSE, SFMT("cannot detect Genero version: %1", err)
  END IF

  --try to use an existing lock file
  IF NOT forceResolve AND lockfile.lockExists(projectDir) THEN
    CALL lockfile.load(projectDir) RETURNING ok, lf, err
    IF NOT ok THEN
      DISPLAY SFMT("warning: cannot read lock file: %1 — re-resolving", err)
    ELSE
      VAR vr = lockfile.validate(lf, m, genero.versionString(gv),
          fglpkgutils.packagesDir(home), fglpkgutils.webcomponentsDir(home))
      IF lockfile.validationNeedsResolve(vr) THEN
        DISPLAY SFMT("Lock file is stale (%1) — re-resolving...",
            vr.manifestMismatch)
      ELSE
        IF vr.generoMismatch IS NOT NULL THEN
          DISPLAY SFMT("warning: %1", vr.generoMismatch)
        END IF
        IF lockfile.validationIsClean(vr) THEN
          DISPLAY SFMT("Lock file is up to date (Genero %1). Nothing to install.",
              genero.versionString(gv))
          RETURN TRUE, NULL
        END IF
        DISPLAY SFMT("Installing from lock file (Genero %1)...",
            genero.versionString(gv))
        CALL installFromLock(lf, home, production) RETURNING ok, err
        RETURN ok, err
      END IF
    END IF
  END IF

  --resolve the full dependency graph
  DISPLAY SFMT("Resolving dependency graph (Genero %1)...",
      genero.versionString(gv))
  CALL resolver.resolveWithOptions(m, NOT production, TRUE)
      RETURNING ok, plan, err
  IF NOT ok THEN
    RETURN FALSE, SFMT("dependency resolution failed:\n%1", err)
  END IF
  DISPLAY SFMT("Resolved %1 package(s), %2 JAR(s)\n",
      plan.packages.getLength(), plan.jars.getLength())

  --write the lock before installing (not with --production: it would
  --drop dev entries that should remain recorded)
  IF NOT production THEN
    VAR lfNew = lockfile.fromPlan(plan, m)
    CALL lockfile.save(lfNew, projectDir) RETURNING ok, err
    IF NOT ok THEN
      DISPLAY SFMT("warning: could not write lock file: %1", err)
    ELSE
      DISPLAY SFMT("Wrote %1\n", lockfile.LOCK_FILENAME)
    END IF
  END IF

  CALL installFromPlan(plan, home) RETURNING ok, err
  RETURN ok, err
END FUNCTION

#+installs every entry of the lock file using pinned URLs and checksums
FUNCTION installFromLock(lf lockfile.TLockfile, home STRING, production BOOLEAN)
    RETURNS(BOOLEAN, STRING)
  DEFINE i INT
  DEFINE ok BOOLEAN
  DEFINE err STRING
  DEFINE info registry.TPackageInfo
  DEFINE dep manifest.TJavaDependency
  IF production THEN
    LET lf = lockfile.filterForProduction(lf)
  END IF
  FOR i = 1 TO lf.packages.getLength()
    IF os.Path.exists(os.Path.join(fglpkgutils.packagesDir(home),
            lf.packages[i].name)) THEN
      DISPLAY SFMT("  %1 %2@%3 (already installed)",
          fglpkgutils.C_CHECK, lf.packages[i].name, lf.packages[i].version)
      CONTINUE FOR
    END IF
    INITIALIZE info TO NULL
    LET info.name = lf.packages[i].name
    LET info.version = lf.packages[i].version
    LET info.downloadUrl = lf.packages[i].downloadUrl
    LET info.checksum = lf.packages[i].checksum
    CALL installPackage(info, home) RETURNING ok, err
    IF NOT ok THEN
      RETURN FALSE, SFMT("failed to install %1: %2", info.name, err)
    END IF
    DISPLAY SFMT("  %1 %2@%3", fglpkgutils.C_CHECK, info.name, info.version)
  END FOR
  FOR i = 1 TO lf.webcomponents.getLength()
    INITIALIZE info TO NULL
    LET info.name = lf.webcomponents[i].name
    LET info.version = lf.webcomponents[i].version
    LET info.downloadUrl = lf.webcomponents[i].downloadUrl
    LET info.checksum = lf.webcomponents[i].checksum
    LET info.variant = "webcomponent"
    CALL installPackage(info, home) RETURNING ok, err
    IF NOT ok THEN
      RETURN FALSE,
          SFMT("failed to install webcomponent %1: %2", info.name, err)
    END IF
    DISPLAY SFMT("  %1 %2@%3 (webcomponent)",
        fglpkgutils.C_CHECK, info.name, info.version)
  END FOR
  FOR i = 1 TO lf.jars.getLength()
    INITIALIZE dep TO NULL
    LET dep.groupId = lf.jars[i].groupId
    LET dep.artifactId = lf.jars[i].artifactId
    LET dep.version = lf.jars[i].version
    LET dep.checksum = lf.jars[i].checksum
    LET dep.url = lf.jars[i].downloadUrl
    IF os.Path.exists(os.Path.join(fglpkgutils.jarsDir(home),
            manifest.jarFileName(dep))) THEN
      DISPLAY SFMT("  %1 %2 (already present)",
          fglpkgutils.C_CHECK, lf.jars[i].key)
      CONTINUE FOR
    END IF
    CALL installJar(dep, home) RETURNING ok, err
    IF NOT ok THEN
      RETURN FALSE, SFMT("failed to install JAR %1: %2", lf.jars[i].key, err)
    END IF
    DISPLAY SFMT("  %1 %2", fglpkgutils.C_CHECK, lf.jars[i].key)
  END FOR
  RETURN TRUE, NULL
END FUNCTION

#+installs every entry of a freshly resolved plan; optional-scope
#+failures warn and are skipped, hard-scope failures abort
FUNCTION installFromPlan(plan resolver.TPlan, home STRING)
    RETURNS(BOOLEAN, STRING)
  DEFINE i INT
  DEFINE ok BOOLEAN
  DEFINE err STRING
  DEFINE info registry.TPackageInfo
  FOR i = 1 TO plan.packages.getLength()
    INITIALIZE info TO NULL
    LET info.name = plan.packages[i].name
    LET info.version = plan.packages[i].version
    LET info.downloadUrl = plan.packages[i].downloadURL
    LET info.checksum = plan.packages[i].checksum
    LET info.variant = plan.packages[i].variant
    CALL installPackage(info, home) RETURNING ok, err
    IF NOT ok THEN
      IF plan.packages[i].scope == manifest.SCOPE_OPTIONAL THEN
        DISPLAY SFMT("  warning: skipping optional package %1: %2",
            info.name, err)
        CONTINUE FOR
      END IF
      RETURN FALSE, SFMT("failed to install %1: %2", info.name, err)
    END IF
    VAR kindHint =
        IIF(resolver.isWebcomponentPackage(plan.packages[i]),
            " (webcomponent)", "")
    IF plan.packages[i].requiredBy.getLength() > 0 THEN
      DISPLAY SFMT("  %1 %2@%3%4  (required by: %5)",
          fglpkgutils.C_CHECK, info.name, info.version, kindHint,
          fglpkgutils.joinArr(plan.packages[i].requiredBy, ", "))
    ELSE
      DISPLAY SFMT("  %1 %2@%3%4",
          fglpkgutils.C_CHECK, info.name, info.version, kindHint)
    END IF
  END FOR
  FOR i = 1 TO plan.jars.getLength()
    CALL installJar(plan.jars[i], home) RETURNING ok, err
    VAR key = manifest.javaKey(plan.jars[i])
    IF NOT ok THEN
      IF plan.jarScopes.contains(key)
          AND plan.jarScopes[key] == manifest.SCOPE_OPTIONAL THEN
        DISPLAY SFMT("  warning: skipping optional JAR %1: %2", key, err)
        CONTINUE FOR
      END IF
      RETURN FALSE, SFMT("failed to install JAR %1: %2", key, err)
    END IF
    DISPLAY SFMT("  %1 %2", fglpkgutils.C_CHECK,
        manifest.jarFileName(plan.jars[i]))
  END FOR
  RETURN TRUE, NULL
END FUNCTION

#+downloads, verifies and unpacks a single package — dispatching to the
#+BDL or webcomponent install layout based on the variant
FUNCTION installPackage(info registry.TPackageInfo, home STRING)
    RETURNS(BOOLEAN, STRING)
  DEFINE ok BOOLEAN
  DEFINE err STRING
  IF info.variant == "webcomponent" THEN
    CALL installWebcomponent(info, home) RETURNING ok, err
  ELSE
    CALL installBDL(info, home) RETURNING ok, err
  END IF
  RETURN ok, err
END FUNCTION

PRIVATE FUNCTION installBDL(info registry.TPackageInfo, home STRING)
    RETURNS(BOOLEAN, STRING)
  DEFINE ok, mok BOOLEAN
  DEFINE err STRING
  DEFINE pkgManifest manifest.TManifest
  DEFINE wcNames fglpkgutils.TStringArr
  CALL ensureDirs(home)
  VAR tmpName = fglpkgutils.makeTempName() || ".zip"
  CALL downloadAndVerify(registry.absoluteDownloadURL(info.downloadUrl),
          info.checksum, info.name, tmpName)
      RETURNING ok, err
  IF NOT ok THEN
    RETURN FALSE, err
  END IF

  --route COMPONENTTYPE bundles of mixed packages into webcomponents/
  CALL readWebcomponentsFromZip(tmpName) RETURNING ok, wcNames, err
  IF NOT ok THEN
    CALL os.Path.delete(tmpName) RETURNING status
    RETURN FALSE, SFMT("cannot read manifest from zip: %1", err)
  END IF

  VAR destDir = os.Path.join(fglpkgutils.packagesDir(home), info.name)
  CALL fglpkgutils.rmrf(destDir)
  CALL extractZipRouted(tmpName, destDir,
          fglpkgutils.webcomponentsDir(home), wcNames)
      RETURNING ok, err
  CALL os.Path.delete(tmpName) RETURNING status
  IF NOT ok THEN
    RETURN FALSE, err
  END IF

  --make bin scripts executable after extraction
  IF manifest.manifestExists(destDir) THEN
    CALL manifest.load(destDir) RETURNING mok, pkgManifest, err
    IF mok AND pkgManifest.bin.getLength() > 0 THEN
      LET err = makeBinScriptsExecutable(destDir, pkgManifest)
      IF err IS NOT NULL THEN
        RETURN FALSE, SFMT("cannot set bin script permissions: %1", err)
      END IF
    END IF
  END IF
  RETURN TRUE, NULL
END FUNCTION

#+webcomponent bundles drop straight into webcomponents/<COMPONENTTYPE>/;
#+the in-zip fglpkg.json is intentionally not extracted (it would collide
#+between multiple webcomponent packages)
PRIVATE FUNCTION installWebcomponent(info registry.TPackageInfo, home STRING)
    RETURNS(BOOLEAN, STRING)
  DEFINE ok BOOLEAN
  DEFINE err STRING
  CALL ensureDirs(home)
  VAR tmpName = fglpkgutils.makeTempName() || ".zip"
  CALL downloadAndVerify(registry.absoluteDownloadURL(info.downloadUrl),
          info.checksum, info.name, tmpName)
      RETURNING ok, err
  IF NOT ok THEN
    RETURN FALSE, err
  END IF
  CALL extractWebcomponentZip(tmpName, fglpkgutils.webcomponentsDir(home))
      RETURNING ok, err
  CALL os.Path.delete(tmpName) RETURNING status
  RETURN ok, err
END FUNCTION

#+downloads and verifies a Java JAR into the jars directory; a missing
#+checksum skips the integrity check (Maven Central is trusted)
FUNCTION installJar(dep manifest.TJavaDependency, home STRING)
    RETURNS(BOOLEAN, STRING)
  DEFINE ok BOOLEAN
  DEFINE err STRING
  CALL ensureDirs(home)
  VAR dest = os.Path.join(fglpkgutils.jarsDir(home), manifest.jarFileName(dep))
  IF os.Path.exists(dest) THEN
    RETURN TRUE, NULL --already on disk
  END IF
  CALL downloadAndVerify(manifest.mavenURL(dep), dep.checksum,
          manifest.jarFileName(dep), dest)
      RETURNING ok, err
  IF NOT ok THEN
    CALL os.Path.delete(dest) RETURNING status
    RETURN FALSE, err
  END IF
  RETURN TRUE, NULL
END FUNCTION

#+deletes an installed BDL package directory
FUNCTION removePackage(name STRING, home STRING) RETURNS(BOOLEAN, STRING)
  VAR dir = os.Path.join(fglpkgutils.packagesDir(home), name)
  IF NOT os.Path.exists(dir) THEN
    RETURN FALSE, SFMT('package "%1" is not installed', name)
  END IF
  CALL fglpkgutils.rmrf(dir)
  RETURN TRUE, NULL
END FUNCTION

#+all currently installed BDL packages (scans the packages dir)
FUNCTION listInstalled(home STRING) RETURNS TInstalledPackages
  DEFINE pkgs TInstalledPackages
  DEFINE entry STRING
  DEFINE names fglpkgutils.TStringArr
  DEFINE i INT
  DEFINE ok BOOLEAN
  DEFINE m manifest.TManifest
  DEFINE err STRING
  VAR dir = fglpkgutils.packagesDir(home)
  IF NOT os.Path.exists(dir) THEN
    RETURN pkgs
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
    IF os.Path.isDirectory(os.Path.join(dir, entry)) THEN
      LET names[names.getLength() + 1] = entry
    END IF
  END WHILE
  IF h > 0 THEN
    CALL os.Path.dirClose(h)
  END IF
  CALL glob.sortBytewise(names)
  FOR i = 1 TO names.getLength()
    LET pkgs[i].name = names[i]
    LET pkgs[i].version = "unknown"
    IF manifest.manifestExists(os.Path.join(dir, names[i])) THEN
      CALL manifest.load(os.Path.join(dir, names[i])) RETURNING ok, m, err
      IF ok THEN
        LET pkgs[i].version = m.version
      END IF
    END IF
  END FOR
  RETURN pkgs
END FUNCTION

FUNCTION ensureDirs(home STRING)
  CALL fglpkgutils.mkdirp(fglpkgutils.packagesDir(home))
  CALL fglpkgutils.mkdirp(fglpkgutils.jarsDir(home))
  CALL fglpkgutils.mkdirp(fglpkgutils.webcomponentsDir(home))
END FUNCTION

--─── download + verify ──────────────────────────────────────────────────────

#+fetches url into dest and verifies the SHA256; token selection:
#+GitHub URL -> github token, otherwise -> registry token
PRIVATE FUNCTION downloadAndVerify(
    url STRING, expectedChecksum STRING, name STRING, dest STRING)
    RETURNS(BOOLEAN, STRING)
  DEFINE ok BOOLEAN
  DEFINE code INT
  DEFINE err STRING
  VAR tok = IIF(glob.isGitHubURL(url), _githubToken, _registryToken)
  CALL registry.downloadToFileAuth(url, tok, glob.isGitHubURL(url), dest)
      RETURNING ok, code, err
  IF NOT ok THEN
    IF code == 401 THEN
      RETURN FALSE,
          SFMT("HTTP 401 downloading %1: Not authorised — run 'fglpkg login' or set FGLPKG_TOKEN",
              name)
    END IF
    RETURN FALSE, SFMT("download failed for %1: %2", name, err)
  END IF
  CALL checksum.verifyFile(dest, expectedChecksum) RETURNING ok, err
  IF NOT ok THEN
    CALL os.Path.delete(dest) RETURNING status
    RETURN FALSE, err
  END IF
  RETURN TRUE, NULL
END FUNCTION

--─── zip handling (shell based) ─────────────────────────────────────────────

#+lists zip entries (unzip -Z1 / tar -tf) with zip-slip sanitisation:
#+absolute paths, ".." segments and backslashes are rejected
FUNCTION zipEntryList(zipPath STRING)
    RETURNS(BOOLEAN, fglpkgutils.TStringArr, STRING)
  DEFINE arr, empty fglpkgutils.TStringArr
  DEFINE out, err STRING
  DEFINE i, j INT
  CALL checkZipTools()
  VAR cmd =
      IIF(fglpkgutils.isWin(),
          SFMT("tar -tf %1", fglpkgutils.quote(zipPath)),
          SFMT("unzip -Z1 %1", fglpkgutils.quote(zipPath)))
  CALL fglpkgutils.getProgramOutputWithErr(cmd) RETURNING out, err
  IF err IS NOT NULL THEN
    RETURN FALSE, empty, SFMT("cannot list zip %1%2", zipPath, err)
  END IF
  VAR lines = fglpkgutils.splitOnChar(
      fglpkgutils.replace(out, "\r", ""), "\n")
  FOR i = 1 TO lines.getLength()
    VAR entry = lines[i]
    IF entry.getLength() == 0 THEN
      CONTINUE FOR
    END IF
    IF fglpkgutils.contains(entry, "\\") THEN
      RETURN FALSE, empty, SFMT("unsafe path in zip: %1", entry)
    END IF
    IF fglpkgutils.startsWith(entry, "/") THEN
      RETURN FALSE, empty, SFMT("unsafe path in zip: %1", entry)
    END IF
    VAR segs = fglpkgutils.splitOnChar(entry, "/")
    FOR j = 1 TO segs.getLength()
      IF segs[j] == ".." THEN
        RETURN FALSE, empty, SFMT("unsafe path in zip: %1", entry)
      END IF
    END FOR
    LET arr[arr.getLength() + 1] = entry
  END FOR
  RETURN TRUE, arr, NULL
END FUNCTION

#+extracts a whole zip into destDir (created if needed)
FUNCTION extractZipTo(zipPath STRING, destDir STRING) RETURNS(BOOLEAN, STRING)
  DEFINE ok BOOLEAN
  DEFINE entries fglpkgutils.TStringArr
  DEFINE err STRING
  --pre-scan for zip-slip before handing the file to the external tool
  CALL zipEntryList(zipPath) RETURNING ok, entries, err
  IF NOT ok THEN
    RETURN FALSE, err
  END IF
  CALL fglpkgutils.mkdirp(destDir)
  VAR cmd =
      IIF(fglpkgutils.isWin(),
          SFMT("tar -xf %1 -C %2",
              fglpkgutils.quote(zipPath), fglpkgutils.quote(destDir)),
          SFMT("unzip -o -q %1 -d %2",
              fglpkgutils.quote(zipPath), fglpkgutils.quote(destDir)))
  VAR code = 0
  RUN cmd RETURNING code
  IF code THEN
    RETURN FALSE, SFMT("extraction failed (%1)", cmd)
  END IF
  RETURN TRUE, NULL
END FUNCTION

#+reads the manifest's webcomponents list from the zip root; a missing
#+manifest yields an empty list and no error (pure BDL install)
FUNCTION readWebcomponentsFromZip(zipPath STRING)
    RETURNS(BOOLEAN, fglpkgutils.TStringArr, STRING)
  DEFINE names, empty fglpkgutils.TStringArr
  DEFINE ok BOOLEAN
  DEFINE entries fglpkgutils.TStringArr
  DEFINE err STRING
  DEFINE i INT
  DEFINE partial RECORD
    webcomponents DYNAMIC ARRAY OF STRING
  END RECORD
  CALL zipEntryList(zipPath) RETURNING ok, entries, err
  IF NOT ok THEN
    RETURN FALSE, empty, err
  END IF
  VAR found = FALSE
  FOR i = 1 TO entries.getLength()
    IF entries[i] == manifest.MANIFEST_FILENAME THEN
      LET found = TRUE
      EXIT FOR
    END IF
  END FOR
  IF NOT found THEN
    RETURN TRUE, names, NULL
  END IF
  VAR out, rerr STRING
  VAR cmd =
      IIF(fglpkgutils.isWin(),
          SFMT("tar -xOf %1 %2",
              fglpkgutils.quote(zipPath), manifest.MANIFEST_FILENAME),
          SFMT("unzip -p %1 %2",
              fglpkgutils.quote(zipPath), manifest.MANIFEST_FILENAME))
  CALL fglpkgutils.getProgramOutputWithErr(cmd) RETURNING out, rerr
  IF rerr IS NOT NULL THEN
    RETURN FALSE, empty, SFMT("cannot read manifest from zip%1", rerr)
  END IF
  TRY
    --partial decode: only the webcomponents list matters for routing
    CALL util.JSON.parse(out, partial)
  CATCH
    RETURN FALSE, empty, "manifest in zip is not valid JSON"
  END TRY
  CALL partial.webcomponents.copyTo(names)
  RETURN TRUE, names, NULL
END FUNCTION

#+extracts a zip into destDir, diverting entries whose first path
#+component is a declared COMPONENTTYPE into webcomponentsDir instead
#+(mixed BDL + webcomponent artifacts)
FUNCTION extractZipRouted(
    zipPath STRING, destDir STRING, webcomponentsDir STRING,
    wcNames fglpkgutils.TStringArr)
    RETURNS(BOOLEAN, STRING)
  DEFINE ok BOOLEAN
  DEFINE err STRING
  DEFINE i INT
  DEFINE wcSet DICTIONARY OF BOOLEAN
  DEFINE entry STRING
  IF wcNames.getLength() == 0 THEN
    CALL extractZipTo(zipPath, destDir) RETURNING ok, err
    RETURN ok, err
  END IF
  --extract everything to a staging dir, then route the top-level dirs
  VAR staging = fglpkgutils.makeTempDir()
  CALL extractZipTo(zipPath, staging) RETURNING ok, err
  IF NOT ok THEN
    CALL fglpkgutils.rmrf(staging)
    RETURN FALSE, err
  END IF
  FOR i = 1 TO wcNames.getLength()
    LET wcSet[wcNames[i]] = TRUE
    --clear any pre-existing install of these webcomponent dirs
    CALL fglpkgutils.rmrf(os.Path.join(webcomponentsDir, wcNames[i]))
  END FOR
  CALL fglpkgutils.mkdirp(destDir)
  CALL fglpkgutils.mkdirp(webcomponentsDir)
  VAR h = os.Path.dirOpen(staging)
  WHILE h > 0
    LET entry = os.Path.dirNext(h)
    IF entry IS NULL THEN
      EXIT WHILE
    END IF
    IF entry == "." OR entry == ".." THEN
      CONTINUE WHILE
    END IF
    VAR src = os.Path.join(staging, entry)
    VAR target =
        IIF(wcSet.contains(entry),
            os.Path.join(webcomponentsDir, entry),
            os.Path.join(destDir, entry))
    IF NOT moveTree(src, target) THEN
      CALL os.Path.dirClose(h)
      CALL fglpkgutils.rmrf(staging)
      RETURN FALSE, SFMT("cannot move %1 to %2", src, target)
    END IF
  END WHILE
  IF h > 0 THEN
    CALL os.Path.dirClose(h)
  END IF
  CALL fglpkgutils.rmrf(staging)
  RETURN TRUE, NULL
END FUNCTION

#+extracts a webcomponent zip: only COMPONENTTYPE/ subtrees install,
#+zip-root files (manifest, stray docs) are skipped; each component dir
#+is cleared first so a reinstall replaces stale files cleanly
FUNCTION extractWebcomponentZip(zipPath STRING, destDir STRING)
    RETURNS(BOOLEAN, STRING)
  DEFINE ok BOOLEAN
  DEFINE err STRING
  DEFINE entry STRING
  VAR staging = fglpkgutils.makeTempDir()
  CALL extractZipTo(zipPath, staging) RETURNING ok, err
  IF NOT ok THEN
    CALL fglpkgutils.rmrf(staging)
    RETURN FALSE, err
  END IF
  CALL fglpkgutils.mkdirp(destDir)
  VAR h = os.Path.dirOpen(staging)
  WHILE h > 0
    LET entry = os.Path.dirNext(h)
    IF entry IS NULL THEN
      EXIT WHILE
    END IF
    IF entry == "." OR entry == ".." THEN
      CONTINUE WHILE
    END IF
    VAR src = os.Path.join(staging, entry)
    IF NOT os.Path.isDirectory(src) THEN
      CONTINUE WHILE --zip-root files are not extracted
    END IF
    VAR target = os.Path.join(destDir, entry)
    CALL fglpkgutils.rmrf(target)
    IF NOT moveTree(src, target) THEN
      CALL os.Path.dirClose(h)
      CALL fglpkgutils.rmrf(staging)
      RETURN FALSE, SFMT("cannot move %1 to %2", src, target)
    END IF
  END WHILE
  IF h > 0 THEN
    CALL os.Path.dirClose(h)
  END IF
  CALL fglpkgutils.rmrf(staging)
  RETURN TRUE, NULL
END FUNCTION

#+moves a file or directory, falling back to copy for cross-device moves
PRIVATE FUNCTION moveTree(src STRING, dest STRING) RETURNS BOOLEAN
  IF os.Path.rename(src, dest) THEN
    RETURN TRUE
  END IF
  IF fglpkgutils.isWin() THEN
    RUN SFMT("xcopy /E /I /Q /Y %1 %2 > NUL",
        fglpkgutils.quote(fglpkgutils.backslash2slash(src)),
        fglpkgutils.quote(fglpkgutils.backslash2slash(dest)))
        RETURNING status
  ELSE
    RUN SFMT("cp -R %1 %2",
        fglpkgutils.quote(src), fglpkgutils.quote(dest)) RETURNING status
  END IF
  RETURN status == 0
END FUNCTION

PRIVATE FUNCTION makeBinScriptsExecutable(pkgDir STRING, m manifest.TManifest)
    RETURNS STRING
  DEFINE i INT
  IF fglpkgutils.isWin() THEN
    RETURN NULL
  END IF
  VAR files = manifest.binFiles(m)
  FOR i = 1 TO files.getLength()
    VAR fullPath = os.Path.join(pkgDir, files[i])
    IF NOT os.Path.exists(fullPath) THEN
      RETURN SFMT('bin script "%1" not found in installed package', files[i])
    END IF
    VAR mode = os.Path.rwx(fullPath)
    IF NOT os.Path.chRwx(fullPath, util.Integer.or(mode, 73)) THEN --|0111
      RETURN SFMT("cannot chmod %1", fullPath)
    END IF
  END FOR
  RETURN NULL
END FUNCTION

DEFINE _zipToolsChecked BOOLEAN

#+verifies the external zip tooling exists, with an actionable error
PRIVATE FUNCTION checkZipTools()
  DEFINE out, err STRING
  IF _zipToolsChecked THEN
    RETURN
  END IF
  LET _zipToolsChecked = TRUE
  IF fglpkgutils.isWin() THEN
    CALL fglpkgutils.getProgramOutputWithErr("where tar") RETURNING out, err
    IF err IS NOT NULL THEN
      CALL fglpkgutils.myErr(
          "the 'tar' tool is required to extract packages on Windows — it ships with Windows 10+")
    END IF
  ELSE
    CALL fglpkgutils.getProgramOutputWithErr("command -v unzip")
        RETURNING out, err
    IF err IS NOT NULL THEN
      CALL fglpkgutils.myErr(
          "the 'unzip' tool is required to extract packages — install it with your system package manager")
    END IF
  END IF
END FUNCTION
