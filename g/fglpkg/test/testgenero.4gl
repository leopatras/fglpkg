#+ tests for genero.4gl version detection + checksum.4gl digests
OPTIONS SHORT CIRCUIT
IMPORT os
IMPORT FGL testutils
IMPORT FGL fglpkg.fglpkgutils
IMPORT FGL fglpkg.genero
IMPORT FGL fglpkg.checksum
&include "testassert.inc"

MAIN
  CALL testNormalise()
  CALL testParse()
  CALL testExtract()
  CALL testDetectViaEnv()
  CALL testSatisfies()
  CALL testChecksum()
  TSUMMARY()
END MAIN

FUNCTION testNormalise()
  TEQ(genero.normaliseVersionString("4.01.12"), "4.1.12")
  TEQ(genero.normaliseVersionString("3.20.05"), "3.20.5")
  TEQ(genero.normaliseVersionString("4.0.0"), "4.0.0")
  TEQ(genero.normaliseVersionString("04.00.00"), "4.0.0")
END FUNCTION

FUNCTION testParse()
  DEFINE ok BOOLEAN
  DEFINE v genero.TGeneroVersion
  DEFINE err STRING
  CALL genero.parseGenero("4.01.12") RETURNING ok, v, err
  TOK(ok)
  TEQ(genero.versionString(v), "4.01.12")
  TEQ(genero.majorString(v), "4")
  TEQ(genero.majorOf(v), 4)
  TEQ(v.sv.minor, 1)
  TEQ(v.sv.patch, 12)
  CALL genero.parseGenero("junk") RETURNING ok, v, err
  TOK(NOT ok)
  TOK(fglpkgutils.contains(err, "invalid Genero version"))
END FUNCTION

FUNCTION testExtract()
  DEFINE ok BOOLEAN
  DEFINE v genero.TGeneroVersion
  DEFINE err STRING
  CALL genero.extractVersion("Genero BDL Version 4.01.12 target x") RETURNING ok, v, err
  TOK(ok)
  TEQ(genero.versionString(v), "4.01.12")
  CALL genero.extractVersion("fglcomp 6.00.02 rev-5054478") RETURNING ok, v, err
  TOK(ok)
  TEQ(genero.majorString(v), "6")
  CALL genero.extractVersion("no version here") RETURNING ok, v, err
  TOK(NOT ok)
END FUNCTION

FUNCTION testDetectViaEnv()
  DEFINE ok BOOLEAN
  DEFINE v genero.TGeneroVersion
  DEFINE err STRING
  CALL fgl_setenv("FGLPKG_GENERO_VERSION", "4.01.12")
  CALL genero.detect() RETURNING ok, v, err
  TOK(ok)
  TEQ(genero.versionString(v), "4.01.12")
  CALL fgl_setenv("FGLPKG_GENERO_VERSION", NULL)
  --without the override, detection should find the local fglcomp
  CALL genero.detect() RETURNING ok, v, err
  TOK(ok)
  TOK(genero.majorOf(v) >= 4)
END FUNCTION

FUNCTION testSatisfies()
  DEFINE sat BOOLEAN
  DEFINE err STRING
  VAR v = genero.mustParseGenero("4.01.12")
  CALL genero.satisfiesGenero(v, "^4.0.0") RETURNING sat, err
  TOK(sat)
  CALL genero.satisfiesGenero(v, "^5.0.0") RETURNING sat, err
  TOK(NOT sat)
  CALL genero.satisfiesGenero(v, "") RETURNING sat, err
  TOK(sat)
  CALL genero.satisfiesGenero(v, "*") RETURNING sat, err
  TOK(sat)
  CALL genero.satisfiesGenero(v, ">>bad") RETURNING sat, err
  TOK(NOT sat)
  TOK(fglpkgutils.contains(err, "invalid genero constraint"))
END FUNCTION

FUNCTION testChecksum()
  DEFINE ok BOOLEAN
  DEFINE err STRING
  VAR dir = fglpkgutils.makeTempDir()
  VAR f = os.Path.join(dir, "data.txt")
  --known digest: sha256("hello\n")
  CALL fglpkgutils.writeStringToFile(f, "hello\n")
  VAR want = "5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03"
  TEQ(checksum.sha256File(f), want)
  --empty file
  VAR fe = os.Path.join(dir, "empty.txt")
  CALL fglpkgutils.writeStringToFile(fe, "")
  VAR wantEmpty = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
  TEQ(checksum.sha256File(fe), wantEmpty)
  --verify: match, mismatch, skip
  CALL checksum.verifyFile(f, want) RETURNING ok, err
  TOK(ok)
  CALL checksum.verifyFile(f, want.toUpperCase()) RETURNING ok, err
  TOK(ok) --case insensitive
  CALL checksum.verifyFile(f, wantEmpty) RETURNING ok, err
  TOK(NOT ok)
  TOK(fglpkgutils.contains(err, "checksum mismatch"))
  CALL checksum.verifyFile(f, "") RETURNING ok, err
  TOK(ok) --empty expected skips verification
  CALL checksum.verifyFile(f, NULL) RETURNING ok, err
  TOK(ok)
  CALL fglpkgutils.rmrf(dir)
END FUNCTION
