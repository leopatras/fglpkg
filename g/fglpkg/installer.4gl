#+ package download, verification and extraction into the fglpkg home
#+ port of internal/installer/installer.go — zip handling shells out to
#+ unzip (Unix) / tar (Windows); downloads for one phase (BDL packages,
#+ webcomponents, or JARs) run concurrently, bounded by
#+ FGLPKG_INSTALL_CONCURRENCY, by shelling out to a single `curl
#+ --parallel` invocation (falls back to one-at-a-time com.HttpRequest
#+ downloads when curl isn't on PATH) — see "downloadAllAndVerify"
#+ below and g/BENCHMARKS.md
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

#+installs every entry of the lock file using pinned URLs and checksums.
#+Each phase (packages, webcomponents, jars) downloads as one
#+concurrent batch, then finalizes+prints every entry in original
#+order; a hard failure lets its phase-siblings run to completion (only
#+the first hard error is returned, after the phase finishes) but skips
#+the phases after it — matching the Go installer's runParallel
#+semantics (see g/BENCHMARKS.md)
FUNCTION installFromLock(lf lockfile.TLockfile, home STRING, production BOOLEAN)
    RETURNS(BOOLEAN, STRING)
  DEFINE i, j INT
  DEFINE ok BOOLEAN
  DEFINE err, firstErr STRING
  DEFINE info registry.TPackageInfo
  DEFINE dep manifest.TJavaDependency
  DEFINE infos DYNAMIC ARRAY OF registry.TPackageInfo
  DEFINE tasks TDownloadTasks
  DEFINE results TDownloadResults
  DEFINE taskForIndex DYNAMIC ARRAY OF INT --0 = already installed/present

  IF production THEN
    LET lf = lockfile.filterForProduction(lf)
  END IF
  CALL ensureDirs(home)

  --── BDL packages ──────────────────────────────────────────────────────
  FOR i = 1 TO lf.packages.getLength()
    IF os.Path.exists(os.Path.join(fglpkgutils.packagesDir(home),
            lf.packages[i].name)) THEN
      DISPLAY SFMT("  %1 %2@%3 (already installed)",
          fglpkgutils.C_CHECK, lf.packages[i].name, lf.packages[i].version)
      LET taskForIndex[i] = 0
      CONTINUE FOR
    END IF
    INITIALIZE info TO NULL
    LET info.name = lf.packages[i].name
    LET info.version = lf.packages[i].version
    LET info.downloadUrl = lf.packages[i].downloadUrl
    LET info.checksum = lf.packages[i].checksum
    LET infos[i] = info
    LET j = tasks.getLength() + 1
    LET tasks[j].url = registry.absoluteDownloadURL(info.downloadUrl)
    LET tasks[j].checksum = info.checksum
    LET tasks[j].name = info.name
    LET tasks[j].dest = fglpkgutils.makeTempName() || ".zip"
    LET taskForIndex[i] = j
  END FOR
  LET results = downloadAllAndVerify(tasks)
  LET firstErr = NULL
  FOR i = 1 TO lf.packages.getLength()
    IF taskForIndex[i] == 0 THEN
      CONTINUE FOR
    END IF
    LET j = taskForIndex[i]
    IF results[j].ok THEN
      CALL finalizeBDL(infos[i], home, tasks[j].dest) RETURNING ok, err
    ELSE
      LET ok = FALSE
      LET err = results[j].err
    END IF
    IF NOT ok THEN
      IF firstErr IS NULL THEN
        LET firstErr = SFMT("failed to install %1: %2", infos[i].name, err)
      END IF
      CONTINUE FOR
    END IF
    DISPLAY SFMT("  %1 %2@%3", fglpkgutils.C_CHECK, infos[i].name, infos[i].version)
  END FOR
  IF firstErr IS NOT NULL THEN
    RETURN FALSE, firstErr
  END IF

  --── webcomponents ─────────────────────────────────────────────────────
  CALL tasks.clear()
  CALL results.clear()
  CALL infos.clear()
  FOR i = 1 TO lf.webcomponents.getLength()
    INITIALIZE info TO NULL
    LET info.name = lf.webcomponents[i].name
    LET info.version = lf.webcomponents[i].version
    LET info.downloadUrl = lf.webcomponents[i].downloadUrl
    LET info.checksum = lf.webcomponents[i].checksum
    LET info.variant = "webcomponent"
    LET infos[i] = info
    LET tasks[i].url = registry.absoluteDownloadURL(info.downloadUrl)
    LET tasks[i].checksum = info.checksum
    LET tasks[i].name = info.name
    LET tasks[i].dest = fglpkgutils.makeTempName() || ".zip"
  END FOR
  LET results = downloadAllAndVerify(tasks)
  LET firstErr = NULL
  FOR i = 1 TO lf.webcomponents.getLength()
    IF results[i].ok THEN
      CALL finalizeWebcomponent(home, tasks[i].dest) RETURNING ok, err
    ELSE
      LET ok = FALSE
      LET err = results[i].err
    END IF
    IF NOT ok THEN
      IF firstErr IS NULL THEN
        LET firstErr = SFMT("failed to install webcomponent %1: %2", infos[i].name, err)
      END IF
      CONTINUE FOR
    END IF
    DISPLAY SFMT("  %1 %2@%3 (webcomponent)",
        fglpkgutils.C_CHECK, infos[i].name, infos[i].version)
  END FOR
  IF firstErr IS NOT NULL THEN
    RETURN FALSE, firstErr
  END IF

  --── JARs ──────────────────────────────────────────────────────────────
  CALL tasks.clear()
  CALL results.clear()
  CALL taskForIndex.clear()
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
      LET taskForIndex[i] = 0
      CONTINUE FOR
    END IF
    LET j = tasks.getLength() + 1
    LET tasks[j].url = manifest.mavenURL(dep)
    LET tasks[j].checksum = dep.checksum
    LET tasks[j].name = manifest.jarFileName(dep)
    LET tasks[j].dest = os.Path.join(fglpkgutils.jarsDir(home), manifest.jarFileName(dep))
    LET taskForIndex[i] = j
  END FOR
  LET results = downloadAllAndVerify(tasks)
  LET firstErr = NULL
  FOR i = 1 TO lf.jars.getLength()
    IF taskForIndex[i] == 0 THEN
      CONTINUE FOR
    END IF
    LET j = taskForIndex[i]
    IF NOT results[j].ok THEN
      IF firstErr IS NULL THEN
        LET firstErr = SFMT("failed to install JAR %1: %2", lf.jars[i].key, results[j].err)
      END IF
      CONTINUE FOR
    END IF
    DISPLAY SFMT("  %1 %2", fglpkgutils.C_CHECK, lf.jars[i].key)
  END FOR
  IF firstErr IS NOT NULL THEN
    RETURN FALSE, firstErr
  END IF
  RETURN TRUE, NULL
END FUNCTION

#+installs every entry of a freshly resolved plan; optional-scope
#+failures warn and are skipped, hard-scope failures let phase-siblings
#+run to completion before aborting (see installFromLock)
FUNCTION installFromPlan(plan resolver.TPlan, home STRING)
    RETURNS(BOOLEAN, STRING)
  DEFINE i, j, n INT
  DEFINE ok BOOLEAN
  DEFINE err, firstErr STRING
  DEFINE info registry.TPackageInfo
  DEFINE infos DYNAMIC ARRAY OF registry.TPackageInfo
  DEFINE tasks TDownloadTasks
  DEFINE results TDownloadResults
  DEFINE taskForIndex DYNAMIC ARRAY OF INT --0 = already on disk (JARs only)

  CALL ensureDirs(home)

  --── BDL packages + webcomponents (one mixed-variant phase) ───────────
  LET n = plan.packages.getLength()
  FOR i = 1 TO n
    INITIALIZE info TO NULL
    LET info.name = plan.packages[i].name
    LET info.version = plan.packages[i].version
    LET info.downloadUrl = plan.packages[i].downloadURL
    LET info.checksum = plan.packages[i].checksum
    LET info.variant = plan.packages[i].variant
    LET infos[i] = info
    LET tasks[i].url = registry.absoluteDownloadURL(info.downloadUrl)
    LET tasks[i].checksum = info.checksum
    LET tasks[i].name = info.name
    LET tasks[i].dest = fglpkgutils.makeTempName() || ".zip"
  END FOR
  LET results = downloadAllAndVerify(tasks)
  LET firstErr = NULL
  FOR i = 1 TO n
    IF results[i].ok THEN
      IF infos[i].variant == "webcomponent" THEN
        CALL finalizeWebcomponent(home, tasks[i].dest) RETURNING ok, err
      ELSE
        CALL finalizeBDL(infos[i], home, tasks[i].dest) RETURNING ok, err
      END IF
    ELSE
      LET ok = FALSE
      LET err = results[i].err
    END IF
    IF NOT ok THEN
      IF plan.packages[i].scope == manifest.SCOPE_OPTIONAL THEN
        DISPLAY SFMT("  warning: skipping optional package %1: %2", infos[i].name, err)
        CONTINUE FOR
      END IF
      IF firstErr IS NULL THEN
        LET firstErr = SFMT("failed to install %1: %2", infos[i].name, err)
      END IF
      CONTINUE FOR
    END IF
    VAR kindHint =
        IIF(resolver.isWebcomponentPackage(plan.packages[i]), " (webcomponent)", "")
    IF plan.packages[i].requiredBy.getLength() > 0 THEN
      DISPLAY SFMT("  %1 %2@%3%4  (required by: %5)",
          fglpkgutils.C_CHECK, infos[i].name, infos[i].version, kindHint,
          fglpkgutils.joinArr(plan.packages[i].requiredBy, ", "))
    ELSE
      DISPLAY SFMT("  %1 %2@%3%4",
          fglpkgutils.C_CHECK, infos[i].name, infos[i].version, kindHint)
    END IF
  END FOR
  IF firstErr IS NOT NULL THEN
    RETURN FALSE, firstErr
  END IF

  --── JARs ──────────────────────────────────────────────────────────────
  CALL tasks.clear()
  CALL results.clear()
  LET n = plan.jars.getLength()
  FOR i = 1 TO n
    VAR dest = os.Path.join(fglpkgutils.jarsDir(home), manifest.jarFileName(plan.jars[i]))
    IF os.Path.exists(dest) THEN
      LET taskForIndex[i] = 0 --already on disk, matches the old installJar's silent skip
      CONTINUE FOR
    END IF
    LET j = tasks.getLength() + 1
    LET tasks[j].url = manifest.mavenURL(plan.jars[i])
    LET tasks[j].checksum = plan.jars[i].checksum
    LET tasks[j].name = manifest.jarFileName(plan.jars[i])
    LET tasks[j].dest = dest
    LET taskForIndex[i] = j
  END FOR
  LET results = downloadAllAndVerify(tasks)
  LET firstErr = NULL
  FOR i = 1 TO n
    VAR key = manifest.javaKey(plan.jars[i])
    IF taskForIndex[i] > 0 AND NOT results[taskForIndex[i]].ok THEN
      IF plan.jarScopes.contains(key)
          AND plan.jarScopes[key] == manifest.SCOPE_OPTIONAL THEN
        DISPLAY SFMT("  warning: skipping optional JAR %1: %2", key, results[taskForIndex[i]].err)
        CONTINUE FOR
      END IF
      IF firstErr IS NULL THEN
        LET firstErr = SFMT("failed to install JAR %1: %2", key, results[taskForIndex[i]].err)
      END IF
      CONTINUE FOR
    END IF
    DISPLAY SFMT("  %1 %2", fglpkgutils.C_CHECK, manifest.jarFileName(plan.jars[i]))
  END FOR
  IF firstErr IS NOT NULL THEN
    RETURN FALSE, firstErr
  END IF
  RETURN TRUE, NULL
END FUNCTION

#+finalizes an already-downloaded-and-verified BDL package zip: routes
#+COMPONENTTYPE bundles of mixed packages into webcomponents/, extracts,
#+makes bin scripts executable
PRIVATE FUNCTION finalizeBDL(info registry.TPackageInfo, home STRING, tmpName STRING)
    RETURNS(BOOLEAN, STRING)
  DEFINE ok, mok BOOLEAN
  DEFINE err STRING
  DEFINE pkgManifest manifest.TManifest
  DEFINE wcNames fglpkgutils.TStringArr

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

#+finalizes an already-downloaded-and-verified webcomponent zip: drops
#+straight into webcomponents/<COMPONENTTYPE>/ — the in-zip fglpkg.json
#+is intentionally not extracted (it would collide between multiple
#+webcomponent packages)
PRIVATE FUNCTION finalizeWebcomponent(home STRING, tmpName STRING)
    RETURNS(BOOLEAN, STRING)
  DEFINE ok BOOLEAN
  DEFINE err STRING
  CALL extractWebcomponentZip(tmpName, fglpkgutils.webcomponentsDir(home))
      RETURNING ok, err
  CALL os.Path.delete(tmpName) RETURNING status
  RETURN ok, err
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

--─── download + verify (concurrent) ─────────────────────────────────────────

PUBLIC TYPE TDownloadTask RECORD
  url STRING,
  checksum STRING,
  name STRING, --for error messages
  dest STRING
END RECORD

PUBLIC TYPE TDownloadTasks DYNAMIC ARRAY OF TDownloadTask

PUBLIC TYPE TDownloadResult RECORD
  ok BOOLEAN,
  err STRING
END RECORD

PUBLIC TYPE TDownloadResults DYNAMIC ARRAY OF TDownloadResult

#+worker cap for one concurrent download phase; a small fixed pool
#+keeps the registry and Maven Central honest without flooding the
#+local network stack (same default as the Go implementation)
PUBLIC CONSTANT DEFAULT_INSTALL_CONCURRENCY = 4

#+reads FGLPKG_INSTALL_CONCURRENCY each call; a bad/unset value falls
#+back to the default rather than refusing to install
FUNCTION installConcurrency() RETURNS INT
  DEFINE n INT
  VAR raw = fgl_getenv("FGLPKG_INSTALL_CONCURRENCY")
  IF raw IS NULL OR raw.trim().getLength() == 0 THEN
    RETURN DEFAULT_INSTALL_CONCURRENCY
  END IF
  LET n = raw
  IF n IS NULL OR n < 1 THEN
    RETURN DEFAULT_INSTALL_CONCURRENCY
  END IF
  RETURN n
END FUNCTION

#+fetches url into dest and verifies the SHA256 — single-item
#+convenience wrapper over the concurrent batch path below
PRIVATE FUNCTION downloadAndVerify(
    url STRING, expectedChecksum STRING, name STRING, dest STRING)
    RETURNS(BOOLEAN, STRING)
  DEFINE tasks TDownloadTasks
  DEFINE results TDownloadResults
  LET tasks[1].url = url
  LET tasks[1].checksum = expectedChecksum
  LET tasks[1].name = name
  LET tasks[1].dest = dest
  LET results = downloadAllAndVerify(tasks)
  RETURN results[1].ok, results[1].err
END FUNCTION

DEFINE _curlChecked, _curlAvailable BOOLEAN

#+BDL has no threads, and GWS/core have no sub-second SLEEP either, so
#+there's no honest way to overlap several com.HttpRequest calls from
#+the interpreter itself (only a tight busy-poll, and even that just
#+overlaps within one process — it can't get HTTP/2 multiplexing, since
#+GWS's HTTP client is HTTP/1.1 only). curl already solves both: `curl
#+--parallel` runs the transfers in its own process, and negotiates
#+HTTP/2 with any server that supports it. So the real "catch up to Go"
#+move is to shell out to curl when it's available, and only fall back
#+to the original one-at-a-time com.HttpRequest path (via
#+registry.downloadToFileAuth) when it isn't.
PRIVATE FUNCTION checkCurlAvailable() RETURNS BOOLEAN
  DEFINE out, err STRING
  IF _curlChecked THEN
    RETURN _curlAvailable
  END IF
  LET _curlChecked = TRUE
  IF fglpkgutils.isWin() THEN
    CALL fglpkgutils.getProgramOutputWithErr("where curl") RETURNING out, err
  ELSE
    CALL fglpkgutils.getProgramOutputWithErr("command -v curl") RETURNING out, err
  END IF
  LET _curlAvailable = (err IS NULL)
  RETURN _curlAvailable
END FUNCTION

#+downloads and verifies every task, bounded by installConcurrency();
#+every task gets a result, one task's failure never stops the others
#+(matching the Go implementation's runParallel). Prefers curl (see
#+downloadAllViaCurl); falls back to downloadAllSequential when curl
#+isn't on PATH.
FUNCTION downloadAllAndVerify(tasks TDownloadTasks) RETURNS TDownloadResults
  DEFINE results TDownloadResults
  IF tasks.getLength() == 0 THEN
    RETURN results
  END IF
  IF checkCurlAvailable() THEN
    RETURN downloadAllViaCurl(tasks)
  END IF
  RETURN downloadAllSequential(tasks)
END FUNCTION

#+one `curl --parallel` process handles every task in this batch: each
#+task becomes its own `--next` segment (its own URL, auth headers and
#+-o destination), and `--parallel-max` caps how many curl runs at
#+once. A single -w format is appended to each segment and all of them
#+land in one status log, one line per completed transfer, in
#+completion order (not submission order) — curl already does the
#+"fire N, harvest as they finish" bookkeeping the Go pool does with
#+goroutines, so 4GL doesn't need to reimplement it.
#+
#+Lines are matched back to their task by filename_effective (the -o
#+path we chose ourselves), not by URL — GitHub/Maven Central routinely
#+302 to a signed CDN URL, so url_effective would not match what we
#+requested.
PRIVATE FUNCTION downloadAllViaCurl(tasks TDownloadTasks) RETURNS TDownloadResults
  CONSTANT WRITE_OUT_FMT = "%{filename_effective}\t%{http_code}\t%{exitcode}\t%{errormsg}\n"
  DEFINE results TDownloadResults
  DEFINE destIndex DICTIONARY OF INT
  DEFINE cmd STRING
  DEFINE statusLog, errLog STRING
  DEFINE i, cap, code, httpCode, exitCode INT
  DEFINE lines, fields fglpkgutils.TStringArr
  DEFINE line, filename STRING

  LET cap = installConcurrency()
  IF cap > tasks.getLength() THEN
    LET cap = tasks.getLength()
  END IF
  LET statusLog = fglpkgutils.makeTempName()
  LET errLog = fglpkgutils.makeTempName()

  ----parallel-immediate: without it, curl delays firing the rest of the
  --batch until it has found out (one round trip in) whether the first
  --transfer's connection can be multiplexed (HTTP/2) — for plain
  --HTTP/1.1 registries that's a wasted RTT-sized stall before the other
  --transfers even start, measured as roughly one extra download's worth
  --of latency tacked onto the whole batch
  LET cmd = SFMT("curl --parallel --parallel-immediate --parallel-max %1 -sS", cap)
  FOR i = 1 TO tasks.getLength()
    LET destIndex[tasks[i].dest] = i
    IF i > 1 THEN
      LET cmd = cmd, " --next"
    END IF
    LET cmd = cmd, curlAuthArgs(tasks[i].url)
    LET cmd = cmd, " -o ", fglpkgutils.quoteForce(tasks[i].dest)
    LET cmd = cmd, " -w ", fglpkgutils.quoteForce(WRITE_OUT_FMT)
    LET cmd = cmd, " ", fglpkgutils.quoteUrl(tasks[i].url)
  END FOR
  LET cmd = cmd, " > ", fglpkgutils.quoteForce(statusLog),
      " 2> ", fglpkgutils.quoteForce(errLog)
  RUN cmd RETURNING code --curl's own exit code is a single scalar for
      --the whole batch; per-task outcome comes from the status log below

  --every task defaults to "never showed up in the log" until proven
  --otherwise, so a curl crash mid-batch still yields a result per task
  FOR i = 1 TO tasks.getLength()
    LET results[i].ok = FALSE
    LET results[i].err = SFMT("download failed for %1: curl produced no result (exit %2)",
        tasks[i].name, code)
  END FOR

  LET lines = fglpkgutils.splitOnChar(fglpkgutils.readTextFile(statusLog), "\n")
  FOR i = 1 TO lines.getLength()
    LET line = lines[i]
    IF line.getLength() == 0 THEN
      CONTINUE FOR
    END IF
    LET fields = fglpkgutils.splitOnChar(line, "\t")
    IF fields.getLength() < 4 OR NOT destIndex.contains(fields[1]) THEN
      CONTINUE FOR
    END IF
    LET filename = fields[1]
    LET httpCode = fields[2]
    LET exitCode = fields[3]
    CALL finishCurlResult(tasks[destIndex[filename]], httpCode, exitCode, fields[4])
        RETURNING results[destIndex[filename]].ok, results[destIndex[filename]].err
  END FOR

  CALL os.Path.delete(statusLog) RETURNING status
  CALL os.Path.delete(errLog) RETURNING status
  RETURN results
END FUNCTION

#+builds the curl auth args for one URL: bearer token (GitHub or
#+registry, matching registry.downloadToFileAuth's token selection) and
#+the Accept header GitHub release assets need to come back as binary
PRIVATE FUNCTION curlAuthArgs(url STRING) RETURNS STRING
  DEFINE args STRING
  LET args = ""
  VAR tok = IIF(glob.isGitHubURL(url), _githubToken, _registryToken)
  IF tok IS NOT NULL AND tok.getLength() > 0 THEN
    LET args = args, " -H ",
        fglpkgutils.quoteForce(SFMT("Authorization: Bearer %1", tok))
  END IF
  IF glob.isGitHubURL(url) THEN
    LET args = args, " -H ", fglpkgutils.quoteForce("Accept: application/octet-stream")
  END IF
  RETURN args
END FUNCTION

#+turns one status-log line into a result: status check first (401
#+gets the friendly "run fglpkg login" hint, matching the pre-curl
#+behaviour), then checksum verification of the file curl already wrote
PRIVATE FUNCTION finishCurlResult(
    task TDownloadTask, httpCode INT, exitCode INT, errMsg STRING)
    RETURNS(BOOLEAN, STRING)
  DEFINE ok BOOLEAN
  DEFINE err STRING
  CASE
    WHEN httpCode >= 200 AND httpCode < 300 AND exitCode == 0
      CALL checksum.verifyFile(task.dest, task.checksum) RETURNING ok, err
      IF NOT ok THEN
        CALL os.Path.delete(task.dest) RETURNING status
        RETURN FALSE, err
      END IF
      RETURN TRUE, NULL
    WHEN httpCode == 401
      CALL os.Path.delete(task.dest) RETURNING status
      RETURN FALSE,
          SFMT("HTTP 401 downloading %1: Not authorised — run 'fglpkg login' or set FGLPKG_TOKEN",
              task.name)
    WHEN httpCode >= 400
      CALL os.Path.delete(task.dest) RETURNING status
      RETURN FALSE, SFMT("download failed for %1: HTTP %2", task.name, httpCode)
    OTHERWISE
      --transport-level failure: no response at all (connect refused,
      --TLS error, timeout, ...)
      CALL os.Path.delete(task.dest) RETURNING status
      RETURN FALSE, SFMT("download failed for %1: %2",
          task.name, NVL(errMsg, SFMT("curl exit %1", exitCode)))
  END CASE
END FUNCTION

#+curl-unavailable fallback: exactly today's pre-batch behaviour, one
#+request at a time via registry.downloadToFileAuth (com.HttpRequest
#+under the hood) — no concurrency, but correct and dependency-free
PRIVATE FUNCTION downloadAllSequential(tasks TDownloadTasks) RETURNS TDownloadResults
  DEFINE results TDownloadResults
  DEFINE i, code INT
  DEFINE ok BOOLEAN
  DEFINE err STRING
  FOR i = 1 TO tasks.getLength()
    VAR tok = IIF(glob.isGitHubURL(tasks[i].url), _githubToken, _registryToken)
    CALL registry.downloadToFileAuth(tasks[i].url, tok,
            glob.isGitHubURL(tasks[i].url), tasks[i].dest)
        RETURNING ok, code, err
    IF NOT ok THEN
      IF code == 401 THEN
        LET results[i].err =
            SFMT("HTTP 401 downloading %1: Not authorised — run 'fglpkg login' or set FGLPKG_TOKEN",
                tasks[i].name)
      ELSE
        LET results[i].err = SFMT("download failed for %1: %2", tasks[i].name, err)
      END IF
      LET results[i].ok = FALSE
      CONTINUE FOR
    END IF
    CALL checksum.verifyFile(tasks[i].dest, tasks[i].checksum) RETURNING ok, err
    IF NOT ok THEN
      CALL os.Path.delete(tasks[i].dest) RETURNING status
      LET results[i].ok = FALSE
      LET results[i].err = err
      CONTINUE FOR
    END IF
    LET results[i].ok = TRUE
  END FOR
  RETURN results
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
