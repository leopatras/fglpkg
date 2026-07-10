#+ tests for publish.4gl: flags, doc collection, variant clash check
OPTIONS SHORT CIRCUIT
IMPORT os
IMPORT FGL testutils
IMPORT FGL fglpkg.fglpkgutils
IMPORT FGL fglpkg.publish
&include "testassert.inc"

MAIN
  CALL testFlags()
  CALL testVariantCheck()
  CALL testCollectDoc()
  TSUMMARY()
END MAIN

FUNCTION parseF(a1 STRING, a2 STRING)
    RETURNS(BOOLEAN, publish.TPublishFlags, STRING)
  DEFINE args fglpkgutils.TStringArr
  DEFINE ok BOOLEAN
  DEFINE f publish.TPublishFlags
  DEFINE err STRING
  IF a1 IS NOT NULL THEN
    LET args[1] = a1
  END IF
  IF a2 IS NOT NULL THEN
    LET args[2] = a2
  END IF
  CALL publish.parsePublishFlags(args) RETURNING ok, f, err
  RETURN ok, f, err
END FUNCTION

FUNCTION testFlags()
  DEFINE ok BOOLEAN
  DEFINE f publish.TPublishFlags
  DEFINE err STRING
  CALL parseF(NULL, NULL) RETURNING ok, f, err
  TOK(ok)
  TOK(NOT f.dryRun)
  TOK(f.visibilityOverride IS NULL)
  CALL parseF("-n", "--ci") RETURNING ok, f, err
  TOK(ok)
  TOK(f.dryRun)
  TOK(f.ci)
  CALL parseF("--private", NULL) RETURNING ok, f, err
  TOK(ok)
  TEQ(f.visibilityOverride, "private")
  CALL parseF("--public", NULL) RETURNING ok, f, err
  TOK(ok)
  TEQ(f.visibilityOverride, "public")
  CALL parseF("--private", "--public") RETURNING ok, f, err
  TOK(NOT ok)
  TEQ(err, "--private and --public are mutually exclusive")
  CALL parseF("--bogus", NULL) RETURNING ok, f, err
  TOK(NOT ok)
  TEQ(err, 'unexpected argument "--bogus"')
END FUNCTION

FUNCTION testVariantCheck()
  DEFINE variants fglpkgutils.TStringArr
  --no variants published yet
  VAR e0 = publish.checkVariantAgainstList("p", "1.0.0", "6", variants)
  TOK(e0 IS NULL)
  --other-major and webcomponent variants never block
  LET variants[1] = "genero4"
  LET variants[2] = "webcomponent"
  VAR e1 = publish.checkVariantAgainstList("p", "1.0.0", "6", variants)
  TOK(e1 IS NULL)
  --matching genero<major> blocks with the bump hint
  LET variants[3] = "genero6"
  VAR err = publish.checkVariantAgainstList("p", "1.0.0", "6", variants)
  TOK(err IS NOT NULL)
  VAR wantHdr = "version 1.0.0 of p is already published for Genero 6"
  TOK(fglpkgutils.contains(err, wantHdr))
  TOK(fglpkgutils.contains(err, "fglpkg version patch     # 1.0.0 -> next patch"))
  TOK(fglpkgutils.contains(err, "fglpkg version major"))
END FUNCTION

FUNCTION testCollectDoc()
  DEFINE ok BOOLEAN
  DEFINE content, err STRING
  VAR dir = fglpkgutils.makeTempDir()

  --missing doc is not an error
  CALL publish.collectDoc(dir, "README") RETURNING ok, content, err
  TOK(ok)
  TOK(content IS NULL)

  --candidate priority: .md wins over .txt and the bare name
  CALL fglpkgutils.writeStringToFile(os.Path.join(dir, "README"), "bare")
  CALL fglpkgutils.writeStringToFile(os.Path.join(dir, "README.txt"), "txt")
  CALL publish.collectDoc(dir, "README") RETURNING ok, content, err
  TOK(ok)
  TEQ(content, "txt")
  CALL fglpkgutils.writeStringToFile(os.Path.join(dir, "README.md"), "md")
  CALL publish.collectDoc(dir, "README") RETURNING ok, content, err
  TOK(ok)
  TEQ(content, "md")

  --case-insensitive lookup
  VAR dir2 = fglpkgutils.makeTempDir()
  CALL fglpkgutils.writeStringToFile(os.Path.join(dir2, "readme.MD"), "ci")
  CALL publish.collectDoc(dir2, "README") RETURNING ok, content, err
  TOK(ok)
  TEQ(content, "ci")
  --USERGUIDE uses its own candidates
  CALL fglpkgutils.writeStringToFile(
      os.Path.join(dir2, "USERGUIDE.markdown"), "ug")
  CALL publish.collectDoc(dir2, "USERGUIDE") RETURNING ok, content, err
  TOK(ok)
  TEQ(content, "ug")

  --oversized content is truncated with the exact marker
  VAR big = fglpkgutils.repeatStr("x", publish.MAX_DOC_BYTES + 100)
  CALL fglpkgutils.writeStringToFile(os.Path.join(dir2, "README.md"), big)
  CALL publish.collectDoc(dir2, "README") RETURNING ok, content, err
  TOK(ok)
  VAR marker = "\n\n*(README truncated at 256 KB)*\n"
  TEQ(content.getLength(), publish.MAX_DOC_BYTES + marker.getLength())
  TOK(fglpkgutils.endsWith(content, "*(README truncated at 256 KB)*\n"))

  CALL fglpkgutils.rmrf(dir)
  CALL fglpkgutils.rmrf(dir2)
END FUNCTION
