#+ fglpkg publish — build the package zip, upload it and submit the
#+ version for admin review
#+ port of cmdPublish + publishPackage (internal/cli/cli.go),
#+ publish_validation.go and readme.go
OPTIONS SHORT CIRCUIT
PACKAGE fglpkg
IMPORT os
IMPORT FGL fglpkg.fglpkgutils
IMPORT FGL fglpkg.manifest
IMPORT FGL fglpkg.genero
IMPORT FGL fglpkg.pack
IMPORT FGL fglpkg.registry
IMPORT FGL fglpkg.credentials
IMPORT FGL fglpkg.hooks
&include "myassert.inc"

--registry hard cap is 2x this; larger doc bodies are truncated
PUBLIC CONSTANT MAX_DOC_BYTES = 262144 --256 KB

PUBLIC TYPE TPublishFlags RECORD
  dryRun BOOLEAN,
  ci BOOLEAN,
  visibilityOverride STRING --"", "private" or "public"
END RECORD

#+the publish command; returns the process exit code
FUNCTION cmdPublish(args fglpkgutils.TStringArr) RETURNS INT
  DEFINE flags TPublishFlags
  DEFINE ok, gok BOOLEAN
  DEFINE m manifest.TManifest
  DEFINE gv genero.TGeneroVersion
  DEFINE err STRING

  CALL parsePublishFlags(args) RETURNING ok, flags, err
  IF NOT ok THEN
    RETURN pubFail(err)
  END IF

  VAR home = fglpkgutils.globalHome()
  IF NOT manifest.manifestExists(".") THEN
    RETURN pubFail(SFMT("failed to load %1: no such file",
            manifest.MANIFEST_FILENAME))
  END IF
  CALL manifest.load(".") RETURNING ok, m, err
  IF NOT ok THEN
    RETURN pubFail(SFMT("failed to load %1: %2",
            manifest.MANIFEST_FILENAME, err))
  END IF
  CALL manifest.validateForPublish(m) RETURNING ok, err
  IF NOT ok THEN
    RETURN pubFail(err)
  END IF

  CALL genero.detect() RETURNING gok, gv, err
  IF NOT gok THEN
    RETURN pubFail(SFMT("cannot detect Genero version: %1", err))
  END IF
  VAR generoMajor = genero.majorString(gv)

  LET err = checkVariantNotPublished(m, generoMajor)
  IF err IS NOT NULL THEN
    RETURN pubFail(err)
  END IF

  VAR registryURL = fglpkgutils.registryBaseURL()

  --bearer gate: CI = env only (never suggests login), else the stored
  --credentials chain (publish itself authenticates via registry.bearer())
  IF flags.ci THEN
    VAR ciTok = credentials.consumerEnvBearer()
    IF ciTok IS NULL THEN
      RETURN pubFail("--ci: no registry token in the environment; set FGLPKG_TOKEN")
    END IF
  ELSE
    VAR tok = credentials.activeBearer(home, registryURL)
    IF tok IS NULL THEN
      RETURN pubFail(SFMT("not logged in to %1\nRun 'fglpkg login' (or set FGLPKG_TOKEN) first",
              registryURL))
    END IF
  END IF

  IF flags.dryRun THEN
    DISPLAY "DRY RUN — no network calls will be made\n"
  END IF

  VAR variant = pack.artifactVariant(m, generoMajor)
  DISPLAY SFMT("Publishing %1@%2 (%3) to %4...",
      m.name, m.version, pack.variantDescription(variant), registryURL)

  IF NOT runPublishHook(m, "prepublish") THEN
    RETURN 1
  END IF

  LET err = publishPackage(m, variant, flags)
  IF err IS NOT NULL THEN
    RETURN pubFail(SFMT("publish failed: %1", err))
  END IF

  IF flags.dryRun THEN
    DISPLAY SFMT("%1 Dry run complete for %2@%3 — no changes made",
        fglpkgutils.C_CHECK, m.name, m.version)
  ELSE
    DISPLAY SFMT("%1 Published %2@%3 — pending admin review",
        fglpkgutils.C_CHECK, m.name, m.version)
    IF flags.ci THEN
      DISPLAY SFMT("fglpkg-published name=%1 version=%2 variant=%3 status=pending",
          m.name, m.version, variant)
    END IF
  END IF

  IF NOT runPublishHook(m, "postpublish") THEN
    RETURN 1
  END IF
  RETURN 0
END FUNCTION

PRIVATE FUNCTION pubFail(msg STRING) RETURNS INT
  CALL fglpkgutils.printStderr(msg)
  RETURN 1
END FUNCTION

PRIVATE FUNCTION runPublishHook(m manifest.TManifest, event STRING)
    RETURNS BOOLEAN
  DEFINE ok BOOLEAN
  DEFINE err STRING
  IF NOT m.hooks.contains(event) OR m.hooks[event].getLength() == 0 THEN
    RETURN TRUE
  END IF
  DISPLAY SFMT("Running %1 hook (%2 op(s))...",
      event, m.hooks[event].getLength())
  CALL hooks.runHooks(m, event, ".") RETURNING ok, err
  IF NOT ok THEN
    CALL fglpkgutils.printStderr(err)
  END IF
  RETURN ok
END FUNCTION

FUNCTION parsePublishFlags(args fglpkgutils.TStringArr)
    RETURNS(BOOLEAN, TPublishFlags, STRING)
  DEFINE f TPublishFlags
  DEFINE wantPrivate, wantPublic BOOLEAN
  DEFINE i INT
  FOR i = 1 TO args.getLength()
    CASE args[i]
      WHEN "--dry-run"
        LET f.dryRun = TRUE
      WHEN "-n"
        LET f.dryRun = TRUE
      WHEN "--ci"
        LET f.ci = TRUE
      WHEN "--private"
        LET wantPrivate = TRUE
      WHEN "--public"
        LET wantPublic = TRUE
      OTHERWISE
        RETURN FALSE, f, SFMT('unexpected argument "%1"', args[i])
    END CASE
  END FOR
  IF wantPrivate AND wantPublic THEN
    RETURN FALSE, f, "--private and --public are mutually exclusive"
  END IF
  IF wantPrivate THEN
    LET f.visibilityOverride = "private"
  ELSE
    IF wantPublic THEN
      LET f.visibilityOverride = "public"
    END IF
  END IF
  RETURN TRUE, f, NULL
END FUNCTION

#+pre-flight: refuses to clobber an already-published genero<major>
#+variant of this version (a webcomponent variant never blocks);
#+network/server errors abort — never silently allow
FUNCTION checkVariantNotPublished(m manifest.TManifest, generoMajor STRING)
    RETURNS STRING
  DEFINE ok BOOLEAN
  DEFINE variants fglpkgutils.TStringArr
  DEFINE err STRING
  CALL registry.variantsFor(m.name, m.version) RETURNING ok, variants, err
  IF NOT ok THEN
    IF registry.isNotFoundErr(err) THEN
      RETURN NULL --nothing to clobber
    END IF
    RETURN SFMT("cannot check whether version %1 is already published: %2",
        m.version, err)
  END IF
  RETURN checkVariantAgainstList(m.name, m.version, generoMajor, variants)
END FUNCTION

#+the pure variant-clash check (testable without HTTP)
FUNCTION checkVariantAgainstList(
    name STRING, version STRING, generoMajor STRING,
    variants fglpkgutils.TStringArr)
    RETURNS STRING
  DEFINE i INT
  VAR want = SFMT("genero%1", generoMajor)
  FOR i = 1 TO variants.getLength()
    IF variants[i] == want THEN
      RETURN SFMT("version %1 of %2 is already published for Genero %3\n",
              version, name, generoMajor)
          || "bump the version before publishing again:\n"
          || SFMT("    fglpkg version patch     # %1 -> next patch\n",
              version)
          || "    fglpkg version minor     # next minor\n"
          || "    fglpkg version major     # next major"
    END IF
  END FOR
  RETURN NULL
END FUNCTION

#+builds the zip and performs (or previews) the registry calls
PRIVATE FUNCTION publishPackage(
    m manifest.TManifest, variant STRING, flags TPublishFlags)
    RETURNS STRING
  DEFINE ok, versionExists BOOLEAN
  DEFINE res pack.TPackResult
  DEFINE meta registry.TVersionMeta
  DEFINE err STRING
  DEFINE readme, userguide STRING

  CALL pack.buildPackageZip(m) RETURNING ok, res, err
  IF NOT ok THEN
    RETURN err
  END IF
  DISPLAY SFMT("  Package zip: %1 bytes (SHA256: %2)", res.size, res.checksum)

  VAR slug = m.name
  VAR filename = pack.artifactFilename(m.name, m.version, variant)
  VAR visibility = flags.visibilityOverride
  IF visibility IS NULL OR visibility.getLength() == 0 THEN
    LET visibility = m.visibility
  END IF
  IF visibility IS NULL OR visibility.getLength() == 0 THEN
    LET visibility = "public"
  END IF

  --doc scan root is the project dir (not m.root)
  CALL collectDoc(".", "README") RETURNING ok, readme, err
  IF NOT ok THEN
    CALL cleanupZip(res.zipPath)
    RETURN err
  END IF
  CALL collectDoc(".", "USERGUIDE") RETURNING ok, userguide, err
  IF NOT ok THEN
    CALL cleanupZip(res.zipPath)
    RETURN err
  END IF

  LET meta.repository = m.repository
  LET meta.author = m.author
  LET meta.license = m.license
  LET meta.genero = m.genero
  LET meta.dependencies = m.dependencies
  LET meta.readme = readme
  LET meta.userguide = userguide

  VAR base = fglpkgutils.registryBaseURL()

  IF flags.dryRun THEN
    DISPLAY SFMT("  [dry-run] would POST   %1/registry/packages", base)
    DISPLAY SFMT('            body: {slug:"%1", name:"%2", description:"%3", visibility:"%4"}',
        slug, m.name, NVL(m.description, ""), visibility)
    DISPLAY SFMT("  [dry-run] would POST   %1/registry/packages/%2/versions",
        base, slug)
    DISPLAY SFMT('            body: {version:"%1", changelog:""}', m.version)
    DISPLAY "            metadata:"
    DISPLAY SFMT("              repository:   %1", dryRunScalar(m.repository))
    DISPLAY SFMT("              author:       %1", dryRunScalar(m.author))
    DISPLAY SFMT("              license:      %1", dryRunScalar(m.license))
    DISPLAY SFMT("              genero:       %1", dryRunScalar(m.genero))
    DISPLAY SFMT("              dependencies: %1 fgl, %2 java",
        m.dependencies.fgl.getLength(), m.dependencies.java.getLength())
    DISPLAY SFMT("              readme:       %1",
        docSizeLabel(readme, "*(README truncated at 256 KB)*\n"))
    DISPLAY SFMT("              userguide:    %1",
        docSizeLabel(userguide, "*(USERGUIDE truncated at 256 KB)*\n"))
    DISPLAY SFMT("  [dry-run] would PUT    %1/registry/packages/%2/versions/%3/artifacts/%4?filename=%5",
        base, slug, m.version, variant, filename)
    DISPLAY SFMT("            body: <%1 bytes zip>", res.size)
    DISPLAY SFMT("  [dry-run] would POST   %1/registry/packages/%2/versions/%3/submit",
        base, slug, m.version)
    CALL cleanupZip(res.zipPath)
    RETURN NULL
  END IF

  --1. create the package slug (409 = already exists, fine)
  DISPLAY "  → POST   /registry/packages"
  LET err = registry.publishCreatePackage(slug, m.name, m.description,
      visibility)
  IF err IS NOT NULL THEN
    CALL cleanupZip(res.zipPath)
    RETURN err
  END IF

  --2. create the version (409 = exists: add a new variant, keep metadata)
  DISPLAY SFMT("  → POST   /registry/packages/%1/versions", slug)
  CALL registry.publishCreateVersion(slug, m.version, "", meta)
      RETURNING ok, versionExists, err
  IF NOT ok THEN
    IF versionExists THEN
      DISPLAY "    (version exists; adding variant)"
    ELSE
      CALL cleanupZip(res.zipPath)
      RETURN err
    END IF
  END IF

  --3. upload the zip (server computes size + sha256)
  DISPLAY SFMT("  → PUT    /registry/packages/%1/versions/%2/artifacts/%3",
      slug, m.version, variant)
  LET err = registry.publishUploadArtifact(slug, m.version, variant,
      filename, res.zipPath)
  IF err IS NOT NULL THEN
    CALL cleanupZip(res.zipPath)
    RETURN err
  END IF

  --4. submit for review
  DISPLAY SFMT("  → POST   /registry/packages/%1/versions/%2/submit",
      slug, m.version)
  LET err = registry.publishSubmit(slug, m.version)
  CALL cleanupZip(res.zipPath)
  RETURN err
END FUNCTION

PRIVATE FUNCTION cleanupZip(zipPath STRING)
  IF zipPath IS NOT NULL AND os.Path.exists(zipPath) THEN
    CALL os.Path.delete(zipPath) RETURNING status
  END IF
END FUNCTION

PRIVATE FUNCTION dryRunScalar(v STRING) RETURNS STRING
  IF v IS NULL OR v.getLength() == 0 THEN
    RETURN "(none)"
  END IF
  RETURN v
END FUNCTION

#+"<size>.<d> KB" label for a doc body, "(none)" when empty, plus
#+" (truncated)" when the cap marker was appended
PRIVATE FUNCTION docSizeLabel(content STRING, marker STRING) RETURNS STRING
  DEFINE kb DECIMAL(12,1)
  IF content IS NULL OR content.getLength() == 0 THEN
    RETURN "(none)"
  END IF
  LET kb = content.getLength() / 1024
  VAR lbl = SFMT("%1 KB", kb)
  IF fglpkgutils.endsWith(content, marker) THEN
    LET lbl = lbl || " (truncated)"
  END IF
  RETURN lbl
END FUNCTION

#+finds README/USERGUIDE in dir (case-insensitive, first candidate wins:
#+.md, .markdown, .rst, .txt, bare name); missing file -> ""; bodies over
#+256 KB are truncated with the exact Go marker
FUNCTION collectDoc(dir STRING, kind STRING)
    RETURNS(BOOLEAN, STRING, STRING)
  DEFINE index DICTIONARY OF STRING --lowercased basename -> actual name
  DEFINE candidates fglpkgutils.TStringArr
  DEFINE entry, content STRING
  DEFINE i INT

  VAR h = os.Path.dirOpen(dir)
  IF h <= 0 THEN
    RETURN FALSE, NULL, SFMT("cannot scan %1 for %2: not a directory",
        dir, kind)
  END IF
  WHILE TRUE
    LET entry = os.Path.dirNext(h)
    IF entry IS NULL THEN
      EXIT WHILE
    END IF
    IF entry == "." OR entry == ".." THEN
      CONTINUE WHILE
    END IF
    IF NOT os.Path.isDirectory(os.Path.join(dir, entry)) THEN
      LET index[entry.toLowerCase()] = entry
    END IF
  END WHILE
  CALL os.Path.dirClose(h)

  LET candidates[1] = SFMT("%1.md", kind)
  LET candidates[2] = SFMT("%1.markdown", kind)
  LET candidates[3] = SFMT("%1.rst", kind)
  LET candidates[4] = SFMT("%1.txt", kind)
  LET candidates[5] = kind
  FOR i = 1 TO candidates.getLength()
    VAR key = candidates[i].toLowerCase()
    IF NOT index.contains(key) THEN
      CONTINUE FOR
    END IF
    VAR path = os.Path.join(dir, index[key])
    TRY
      LET content = fglpkgutils.readTextFile(path)
    CATCH
      RETURN FALSE, NULL, SFMT("cannot read %1", path)
    END TRY
    IF content.getLength() > MAX_DOC_BYTES THEN
      LET content = content.subString(1, MAX_DOC_BYTES)
          || SFMT("\n\n*(%1 truncated at 256 KB)*\n", kind)
    END IF
    RETURN TRUE, content, NULL
  END FOR
  RETURN TRUE, NULL, NULL --no doc file is not an error
END FUNCTION
