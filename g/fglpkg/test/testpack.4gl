#+ tests for pack.4gl (zip building) and hooks.4gl (lifecycle operations)
OPTIONS SHORT CIRCUIT
IMPORT os
IMPORT FGL testutils
IMPORT FGL fglpkg.fglpkgutils
IMPORT FGL fglpkg.manifest
IMPORT FGL fglpkg.pack
IMPORT FGL fglpkg.hooks
&include "testassert.inc"

DEFINE _origDir STRING

MAIN
  IF fglpkgutils.isWin() THEN
    DISPLAY "testpack: zip fixture tests need a Unix shell — skipped"
    EXIT PROGRAM 0
  END IF
  LET _origDir = os.Path.pwd()
  CALL testVariantHelpers()
  CALL testBuildZipDefaults()
  CALL testBuildZipIgnoreAndBin()
  CALL testBuildZipWebcomponents()
  CALL testHooks()
  TSUMMARY()
END MAIN

FUNCTION enterTempProject() RETURNS STRING
  VAR dir = fglpkgutils.makeTempDir()
  TOK(os.Path.chDir(dir))
  RETURN dir
END FUNCTION

FUNCTION leaveTempProject(dir STRING)
  TOK(os.Path.chDir(_origDir))
  CALL fglpkgutils.rmrf(dir)
END FUNCTION

FUNCTION entryNames(res pack.TPackResult) RETURNS STRING
  DEFINE names fglpkgutils.TStringArr
  DEFINE i INT
  FOR i = 1 TO res.entries.getLength()
    LET names[i] = res.entries[i].name
  END FOR
  RETURN fglpkgutils.joinArr(names, ",")
END FUNCTION

FUNCTION testVariantHelpers()
  DEFINE m manifest.TManifest
  LET m = manifest.newManifest("p", "1.0.0", "", "")
  --pure BDL
  LET m.main = "Main.42m"
  TEQ(pack.artifactVariant(m, "4"), "genero4")
  --pure webcomponent
  INITIALIZE m TO NULL
  LET m.webcomponents[1] = "mychart"
  TEQ(pack.artifactVariant(m, ""), "webcomponent")
  --mixed forces the genero variant
  LET m.main = "Main.42m"
  TEQ(pack.artifactVariant(m, "6"), "genero6")
  VAR fname = pack.artifactFilename("poiapi", "1.0.0", "genero4")
  TEQ(fname, "poiapi-1.0.0-genero4.zip")
  TEQ(pack.variantDescription("webcomponent"), "webcomponent variant")
  TEQ(pack.variantDescription("genero4"), "Genero 4 variant")
END FUNCTION

FUNCTION testBuildZipDefaults()
  DEFINE ok BOOLEAN
  DEFINE res pack.TPackResult
  DEFINE err STRING
  VAR dir = enterTempProject()
  VAR m = manifest.newManifest("p", "1.0.0", "d", "a")
  --default patterns *.42m/*.42f/*.sch match at any depth (basename match)
  CALL fglpkgutils.mkdirp("com/fourjs/x")
  CALL fglpkgutils.writeStringToFile("main.42m", "m")
  CALL fglpkgutils.writeStringToFile("form.42f", "f")
  CALL fglpkgutils.writeStringToFile("com/fourjs/x/mod.42m", "n")
  CALL fglpkgutils.writeStringToFile("notes.txt", "t")
  --the local package cache never ships
  CALL fglpkgutils.mkdirp(".fglpkg/packages/dep")
  CALL fglpkgutils.writeStringToFile(".fglpkg/packages/dep/dep.42m", "x")
  CALL pack.buildPackageZip(m) RETURNING ok, res, err
  TOK(ok)
  VAR names = entryNames(res)
  TOK(fglpkgutils.contains(names, "main.42m"))
  TOK(fglpkgutils.contains(names, "form.42f"))
  TOK(fglpkgutils.contains(names, "com/fourjs/x/mod.42m"))
  TOK(NOT fglpkgutils.contains(names, "notes.txt"))
  TOK(NOT fglpkgutils.contains(names, ".fglpkg"))
  --manifest always included, checksum/size populated
  TOK(fglpkgutils.contains(names, "fglpkg.json"))
  TEQ(res.checksum.getLength(), 64)
  TOK(res.size > 0)
  TOK(os.Path.exists(res.zipPath))
  --the packed manifest is the publish copy (no devDependencies)
  VAR out = fglpkgutils.getProgramOutput(
      SFMT("unzip -p %1 fglpkg.json", fglpkgutils.quote(res.zipPath)))
  TOK(NOT fglpkgutils.contains(out, "devDependencies"))
  CALL os.Path.delete(res.zipPath) RETURNING status
  CALL leaveTempProject(dir)
END FUNCTION

FUNCTION testBuildZipIgnoreAndBin()
  DEFINE ok BOOLEAN
  DEFINE res pack.TPackResult
  DEFINE err STRING
  VAR dir = enterTempProject()
  VAR m = manifest.newManifest("p", "1.0.0", "d", "a")
  LET m.bin["migrate"] = "scripts/migrate.sh"
  LET m.docs[1] = "README.md"
  CALL fglpkgutils.mkdirp("scripts")
  CALL fglpkgutils.writeStringToFile("main.42m", "m")
  CALL fglpkgutils.writeStringToFile("old.42m", "o")
  CALL fglpkgutils.writeStringToFile("scripts/migrate.sh", "#!/bin/sh")
  CALL fglpkgutils.writeStringToFile("README.md", "# readme")
  CALL fglpkgutils.writeStringToFile("SECRET.md", "# secret")
  --ignore old.42m and (futilely) the declared bin script
  CALL fglpkgutils.writeStringToFile(".fglpkgignore",
      "old.42m\nscripts/\nSECRET.md\n")
  CALL pack.buildPackageZip(m) RETURNING ok, res, err
  TOK(ok)
  VAR names = entryNames(res)
  TOK(fglpkgutils.contains(names, "main.42m"))
  TOK(NOT fglpkgutils.contains(names, "old.42m"))
  --declared bin scripts override the ignore rules
  TOK(fglpkgutils.contains(names, "scripts/migrate.sh"))
  --docs included, ignored docs excluded
  TOK(fglpkgutils.contains(names, "README.md"))
  TOK(NOT fglpkgutils.contains(names, "SECRET.md"))
  CALL os.Path.delete(res.zipPath) RETURNING status
  CALL leaveTempProject(dir)
END FUNCTION

FUNCTION testBuildZipWebcomponents()
  DEFINE ok BOOLEAN
  DEFINE res pack.TPackResult
  DEFINE err STRING
  VAR dir = enterTempProject()
  VAR m = manifest.newManifest("wc", "1.0.0", "d", "a")
  LET m.webcomponents[1] = "mychart"
  --missing directory is an error
  CALL pack.buildPackageZip(m) RETURNING ok, res, err
  TOK(NOT ok)
  TOK(fglpkgutils.contains(err, "directory webcomponents/mychart/ is missing"))
  --missing entry point is an error
  CALL fglpkgutils.mkdirp("webcomponents/mychart")
  CALL fglpkgutils.writeStringToFile("webcomponents/mychart/util.js", "js")
  CALL pack.buildPackageZip(m) RETURNING ok, res, err
  TOK(NOT ok)
  TOK(fglpkgutils.contains(err, "missing required entry point"))
  --with the entry point: files stored with webcomponents/ prefix stripped
  CALL fglpkgutils.writeStringToFile(
      "webcomponents/mychart/mychart.html", "<html>")
  CALL pack.buildPackageZip(m) RETURNING ok, res, err
  TOK(ok)
  VAR names = entryNames(res)
  TOK(fglpkgutils.contains(names, "mychart/mychart.html"))
  TOK(fglpkgutils.contains(names, "mychart/util.js"))
  TOK(NOT fglpkgutils.contains(names, "webcomponents/"))
  CALL os.Path.delete(res.zipPath) RETURNING status
  CALL leaveTempProject(dir)
END FUNCTION

FUNCTION testHooks()
  DEFINE ok BOOLEAN
  DEFINE err STRING
  DEFINE m manifest.TManifest
  DEFINE target, rerr STRING
  VAR dir = enterTempProject()
  LET m = manifest.newManifest("p", "1.0.0", "", "")

  --mkdir + copy-files with glob
  CALL fglpkgutils.mkdirp("tpl")
  CALL fglpkgutils.writeStringToFile("tpl/a.4gl", "a")
  CALL fglpkgutils.writeStringToFile("tpl/b.4gl", "b")
  CALL fglpkgutils.writeStringToFile("tpl/skip.txt", "s")
  LET m.hooks["postinstall"][1].op = "mkdir"
  LET m.hooks["postinstall"][1].path = "gen/deep"
  LET m.hooks["postinstall"][2].op = "copy-files"
  LET m.hooks["postinstall"][2].src = "tpl/*.4gl"
  LET m.hooks["postinstall"][2].dst = "gen"
  CALL hooks.runHooks(m, "postinstall", ".") RETURNING ok, err
  TOK(ok)
  TOK(os.Path.isDirectory("gen/deep"))
  TOK(os.Path.exists("gen/a.4gl"))
  TOK(os.Path.exists("gen/b.4gl"))
  TOK(NOT os.Path.exists("gen/skip.txt"))

  --an event with no hooks is a no-op
  CALL hooks.runHooks(m, "prepublish", ".") RETURNING ok, err
  TOK(ok)

  --single file copy into an existing directory keeps the basename
  LET m.hooks["prepublish"][1].op = "copy-files"
  LET m.hooks["prepublish"][1].src = "tpl/a.4gl"
  LET m.hooks["prepublish"][1].dst = "gen"
  CALL hooks.runHooks(m, "prepublish", ".") RETURNING ok, err
  TOK(ok)

  --directory copy recurses
  LET m.hooks["preinstall"][1].op = "copy-files"
  LET m.hooks["preinstall"][1].src = "tpl"
  LET m.hooks["preinstall"][1].dst = "tpl2"
  CALL hooks.runHooks(m, "preinstall", ".") RETURNING ok, err
  TOK(ok)
  TOK(os.Path.exists("tpl2/skip.txt"))

  --a glob with no matches fails, and the error names the event/op
  LET m.hooks["preuninstall"][1].op = "copy-files"
  LET m.hooks["preuninstall"][1].src = "nada/*.x"
  LET m.hooks["preuninstall"][1].dst = "gen"
  CALL hooks.runHooks(m, "preuninstall", ".") RETURNING ok, err
  TOK(NOT ok)
  TOK(fglpkgutils.contains(err, "hook preuninstall op[0] copy-files"))
  TOK(fglpkgutils.contains(err, "matched no files"))

  --mkdir on an existing file fails
  LET m.hooks["postpublish"][1].op = "mkdir"
  LET m.hooks["postpublish"][1].path = "tpl/a.4gl"
  CALL hooks.runHooks(m, "postpublish", ".") RETURNING ok, err
  TOK(NOT ok)
  TOK(fglpkgutils.contains(err, "exists and is not a directory"))

  --escape attempts are rejected by the executor (defense in depth)
  CALL hooks.resolveInside(os.Path.pwd(), "../evil") RETURNING target, rerr
  TOK(rerr IS NOT NULL)
  CALL hooks.resolveInside(os.Path.pwd(), "/abs") RETURNING target, rerr
  TOK(rerr IS NOT NULL)
  CALL hooks.resolveInside(os.Path.pwd(), "sub/ok") RETURNING target, rerr
  TOK(rerr IS NULL)

  CALL leaveTempProject(dir)
END FUNCTION
