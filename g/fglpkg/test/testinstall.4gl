#+ tests for installer.4gl zip handling and env.4gl line building
OPTIONS SHORT CIRCUIT
IMPORT os
IMPORT FGL testutils
IMPORT FGL fglpkg.fglpkgutils
IMPORT FGL fglpkg.installer
IMPORT FGL fglpkg.env
&include "testassert.inc"

MAIN
  IF fglpkgutils.isWin() THEN
    DISPLAY "testinstall: zip fixture tests need a Unix shell — skipped"
    EXIT PROGRAM 0
  END IF
  CALL testZipEntryList()
  CALL testZipSlipRejected()
  CALL testExtract()
  CALL testWebcomponentRouting()
  CALL testEnvLines()
  TSUMMARY()
END MAIN

#+creates a zip from a directory's contents (entries relative to dir)
FUNCTION makeZip(dir STRING, zipPath STRING)
  CALL fglpkgutils.checkRUN(
      SFMT("cd %1 && zip -r -q %2 .",
          fglpkgutils.quote(dir), fglpkgutils.quote(zipPath)))
END FUNCTION

FUNCTION testZipEntryList()
  DEFINE ok BOOLEAN
  DEFINE entries fglpkgutils.TStringArr
  DEFINE err STRING
  VAR src = fglpkgutils.makeTempDir()
  CALL fglpkgutils.mkdirp(os.Path.join(src, "sub"))
  CALL fglpkgutils.writeStringToFile(os.Path.join(src, "a.42m"), "a")
  CALL fglpkgutils.writeStringToFile(os.Path.join(src, "sub/b.42m"), "b")
  VAR zipPath = fglpkgutils.makeTempName() || ".zip"
  CALL makeZip(src, zipPath)
  CALL installer.zipEntryList(zipPath) RETURNING ok, entries, err
  TOK(ok)
  VAR listed = fglpkgutils.joinArr(entries, ",")
  TOK(fglpkgutils.contains(listed, "a.42m"))
  TOK(fglpkgutils.contains(listed, "sub/b.42m"))
  CALL fglpkgutils.rmrf(src)
  CALL os.Path.delete(zipPath) RETURNING status
END FUNCTION

FUNCTION testZipSlipRejected()
  DEFINE ok BOOLEAN
  DEFINE entries fglpkgutils.TStringArr
  DEFINE err STRING
  --craft a zip containing a ../ entry (zip normally refuses, so build
  --the evil name inside and rename the entry via a python one-liner)
  VAR src = fglpkgutils.makeTempDir()
  CALL fglpkgutils.writeStringToFile(os.Path.join(src, "ok.txt"), "x")
  VAR zipPath = fglpkgutils.makeTempName() || ".zip"
  CALL makeZip(src, zipPath)
  CALL fglpkgutils.checkRUN(
      SFMT("python3 -c \"import zipfile; z=zipfile.ZipFile('%1','a'); z.writestr('../evil.txt','pwned'); z.close()\"",
          zipPath))
  CALL installer.zipEntryList(zipPath) RETURNING ok, entries, err
  TOK(NOT ok)
  TOK(fglpkgutils.contains(err, "unsafe path"))
  --extraction must refuse too
  VAR dest = fglpkgutils.makeTempDir()
  CALL installer.extractZipTo(zipPath, dest) RETURNING ok, err
  TOK(NOT ok)
  CALL fglpkgutils.rmrf(src)
  CALL fglpkgutils.rmrf(dest)
  CALL os.Path.delete(zipPath) RETURNING status
END FUNCTION

FUNCTION testExtract()
  DEFINE ok BOOLEAN
  DEFINE err STRING
  VAR src = fglpkgutils.makeTempDir()
  CALL fglpkgutils.mkdirp(os.Path.join(src, "com/fourjs/x"))
  CALL fglpkgutils.writeStringToFile(os.Path.join(src, "fglpkg.json"),
      '{"name":"p","version":"1.0.0"}')
  CALL fglpkgutils.writeStringToFile(
      os.Path.join(src, "com/fourjs/x/mod.42m"), "bin")
  VAR zipPath = fglpkgutils.makeTempName() || ".zip"
  CALL makeZip(src, zipPath)
  VAR dest = fglpkgutils.makeTempDir()
  CALL installer.extractZipTo(zipPath, dest) RETURNING ok, err
  TOK(ok)
  TOK(os.Path.exists(os.Path.join(dest, "fglpkg.json")))
  TOK(os.Path.exists(os.Path.join(dest, "com/fourjs/x/mod.42m")))
  --re-extraction overwrites cleanly
  CALL installer.extractZipTo(zipPath, dest) RETURNING ok, err
  TOK(ok)
  CALL fglpkgutils.rmrf(src)
  CALL fglpkgutils.rmrf(dest)
  CALL os.Path.delete(zipPath) RETURNING status
END FUNCTION

FUNCTION testWebcomponentRouting()
  DEFINE ok BOOLEAN
  DEFINE names fglpkgutils.TStringArr
  DEFINE err STRING
  --a mixed package: BDL files + one COMPONENTTYPE bundle
  VAR src = fglpkgutils.makeTempDir()
  CALL fglpkgutils.mkdirp(os.Path.join(src, "mychart"))
  CALL fglpkgutils.writeStringToFile(os.Path.join(src, "fglpkg.json"),
      '{"name":"p","version":"1.0.0","webcomponents":["mychart"]}')
  CALL fglpkgutils.writeStringToFile(os.Path.join(src, "wrapper.42m"), "bdl")
  CALL fglpkgutils.writeStringToFile(
      os.Path.join(src, "mychart/mychart.html"), "<html>")
  VAR zipPath = fglpkgutils.makeTempName() || ".zip"
  CALL makeZip(src, zipPath)

  --readWebcomponentsFromZip finds the declared names
  CALL installer.readWebcomponentsFromZip(zipPath) RETURNING ok, names, err
  TOK(ok)
  TEQ(names.getLength(), 1)
  TEQ(names[1], "mychart")

  --routed extraction: BDL into dest, component into webcomponents dir
  VAR dest = fglpkgutils.makeTempDir()
  VAR wcDir = fglpkgutils.makeTempDir()
  CALL installer.extractZipRouted(zipPath, dest, wcDir, names)
      RETURNING ok, err
  TOK(ok)
  TOK(os.Path.exists(os.Path.join(dest, "wrapper.42m")))
  TOK(os.Path.exists(os.Path.join(dest, "fglpkg.json")))
  TOK(NOT os.Path.exists(os.Path.join(dest, "mychart")))
  TOK(os.Path.exists(os.Path.join(wcDir, "mychart/mychart.html")))

  --pure webcomponent extraction: root files skipped
  VAR wcDir2 = fglpkgutils.makeTempDir()
  CALL installer.extractWebcomponentZip(zipPath, wcDir2) RETURNING ok, err
  TOK(ok)
  TOK(os.Path.exists(os.Path.join(wcDir2, "mychart/mychart.html")))
  TOK(NOT os.Path.exists(os.Path.join(wcDir2, "fglpkg.json")))
  TOK(NOT os.Path.exists(os.Path.join(wcDir2, "wrapper.42m")))

  --a pure BDL zip yields an empty webcomponents list
  VAR src2 = fglpkgutils.makeTempDir()
  CALL fglpkgutils.writeStringToFile(os.Path.join(src2, "fglpkg.json"),
      '{"name":"q","version":"1.0.0"}')
  VAR zip2 = fglpkgutils.makeTempName() || ".zip"
  CALL makeZip(src2, zip2)
  CALL installer.readWebcomponentsFromZip(zip2) RETURNING ok, names, err
  TOK(ok)
  TEQ(names.getLength(), 0)

  CALL fglpkgutils.rmrf(src)
  CALL fglpkgutils.rmrf(src2)
  CALL fglpkgutils.rmrf(dest)
  CALL fglpkgutils.rmrf(wcDir)
  CALL fglpkgutils.rmrf(wcDir2)
  CALL os.Path.delete(zipPath) RETURNING status
  CALL os.Path.delete(zip2) RETURNING status
END FUNCTION

FUNCTION testEnvLines()
  --build a fake home with installed packages/jars/webcomponents
  VAR home = fglpkgutils.makeTempDir()
  CALL fglpkgutils.mkdirp(os.Path.join(home, "packages/myutils"))
  CALL fglpkgutils.mkdirp(os.Path.join(home, "packages/dbtools"))
  CALL fglpkgutils.mkdirp(os.Path.join(home, "jars"))
  CALL fglpkgutils.writeStringToFile(
      os.Path.join(home, "jars/gson-2.10.1.jar"), "jar")
  CALL fglpkgutils.writeStringToFile(
      os.Path.join(home, "jars/README.txt"), "not a jar")
  CALL fglpkgutils.mkdirp(os.Path.join(home, "webcomponents/mychart"))

  VAR lines = env.generateExports(home)
  TOK(lines.getLength() >= 3)
  --FGLLDPATH first, sorted package dirs, prepend-preserving syntax
  TOK(fglpkgutils.startsWith(lines[1], "export FGLLDPATH="))
  TOK(fglpkgutils.contains(lines[1], "packages/dbtools"))
  TOK(fglpkgutils.contains(lines[1], "packages/myutils"))
  TOK(fglpkgutils.contains(lines[1], '"${FGLLDPATH:+:$FGLLDPATH}"'))
  VAR dbIdx = lines[1].getIndexOf("packages/dbtools", 1)
  VAR myIdx = lines[1].getIndexOf("packages/myutils", 1)
  TOK(dbIdx < myIdx)
  --CLASSPATH lists only .jar files
  TOK(fglpkgutils.startsWith(lines[2], "export CLASSPATH="))
  TOK(fglpkgutils.contains(lines[2], "gson-2.10.1.jar"))
  TOK(NOT fglpkgutils.contains(lines[2], "README.txt"))
  --FGLIMAGEPATH points at the parent of webcomponents/ + GAS hint comment
  TOK(fglpkgutils.startsWith(lines[3], "export FGLIMAGEPATH="))
  TOK(NOT fglpkgutils.contains(lines[3], "webcomponents"))
  TOK(fglpkgutils.startsWith(lines[4], "# For GAS:"))
  TOK(fglpkgutils.contains(lines[4], "webcomponents"))

  --GWA flags
  VAR gwa = env.generateGWA(home)
  TEQ(gwa.getLength(), 1)
  TOK(fglpkgutils.startsWith(gwa[1], "--webcomponent "))
  TOK(fglpkgutils.contains(gwa[1], "webcomponents/mychart"))

  --mergeEnvVar
  TEQ(env.mergeEnvVar("/a", "/b"), "/a:/b")
  TEQ(env.mergeEnvVar("/a", ""), "/a")
  TEQ(env.mergeEnvVar("", "/b"), "/b")

  --no webcomponents -> no FGLIMAGEPATH lines
  CALL fglpkgutils.rmrf(os.Path.join(home, "webcomponents"))
  LET lines = env.generateExports(home)
  TEQ(lines.getLength(), 2)

  CALL fglpkgutils.rmrf(home)
END FUNCTION
