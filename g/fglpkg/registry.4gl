#+ HTTP client for the Genero Package Registry (/registry/... protocol)
#+ port of internal/registry/registry.go
#+ base URL: FGLPKG_REGISTRY env or https://service.generointelligence.ai
OPTIONS SHORT CIRCUIT
PACKAGE fglpkg
IMPORT com
IMPORT os
IMPORT util
IMPORT FGL fglpkg.fglpkgutils
IMPORT FGL fglpkg.semver
IMPORT FGL fglpkg.manifest
&include "myassert.inc"

--sentinel error text; check with isNotFoundErr()
PUBLIC CONSTANT ERR_NOT_FOUND = "package not found in registry"

PUBLIC TYPE TPackageInfo RECORD
  name STRING,
  version STRING,
  description STRING,
  author STRING,
  license STRING,
  publishedAt STRING,
  downloadUrl STRING,
  checksum STRING,
  genero STRING, --Genero constraint declared by this version
  fglDeps DICTIONARY OF STRING,
  javaDeps manifest.TJavaDependencies,
  variant STRING, --"genero<N>", "webcomponent", "default"
  variants DYNAMIC ARRAY OF RECORD
    generoMajor STRING,
    downloadUrl STRING,
    checksum STRING
  END RECORD,
  readme STRING
END RECORD

PUBLIC TYPE TVersionEntry RECORD
  version STRING,
  genero STRING,
  variants fglpkgutils.TStringArr
END RECORD

PUBLIC TYPE TVersionList RECORD
  name STRING,
  versions fglpkgutils.TStringArr,
  versionEntries DYNAMIC ARRAY OF TVersionEntry
END RECORD

PUBLIC TYPE TSearchResult RECORD
  name STRING,
  latestVersion STRING,
  description STRING,
  author STRING
END RECORD

--registry wire format (parsed with util.JSON, unmatched fields ignored)
PUBLIC TYPE TApiArtifact RECORD
  variant STRING,
  filename STRING,
  sizeBytes BIGINT ATTRIBUTES(json_name = "size_bytes"),
  sha256 STRING,
  downloadUrl STRING ATTRIBUTES(json_name = "download_url")
END RECORD

PUBLIC TYPE TApiArtifacts DYNAMIC ARRAY OF TApiArtifact

PUBLIC TYPE TApiVersionSummary RECORD
  version STRING,
  status STRING,
  changelog STRING,
  artifacts TApiArtifacts,
  submittedAt STRING ATTRIBUTES(json_name = "submitted_at"),
  publishedAt STRING ATTRIBUTES(json_name = "published_at"),
  reviewComment STRING ATTRIBUTES(json_name = "review_comment"),
  repository STRING,
  author STRING,
  license STRING,
  genero STRING,
  dependencies RECORD
    fgl DICTIONARY OF STRING,
    java manifest.TJavaDependencies
  END RECORD,
  readme STRING,
  userguide STRING
END RECORD

PUBLIC TYPE TApiPackageDetail RECORD
  slug STRING,
  name STRING,
  description STRING,
  visibility STRING,
  owner RECORD
    partnerId STRING ATTRIBUTES(json_name = "partner_id"),
    name STRING
  END RECORD,
  status STRING,
  latestVersion STRING ATTRIBUTES(json_name = "latest_version"),
  downloads BIGINT,
  versions DYNAMIC ARRAY OF TApiVersionSummary
END RECORD

PUBLIC TYPE TApiBrowseResponse RECORD
  packages DYNAMIC ARRAY OF RECORD
    slug STRING,
    name STRING,
    description STRING,
    visibility STRING,
    owner RECORD
      partnerId STRING ATTRIBUTES(json_name = "partner_id"),
      name STRING
    END RECORD,
    status STRING,
    latestVersion STRING ATTRIBUTES(json_name = "latest_version"),
    downloads BIGINT
  END RECORD,
  page INT,
  pageSize INT,
  total INT
END RECORD

--bearer/refresh chokepoints: the CLI wires these to credentials-aware
--closures at startup; defaults read FGLPKG_TOKEN only / never refresh
PUBLIC TYPE TBearerFunc FUNCTION() RETURNS STRING
PUBLIC TYPE TRefreshFunc FUNCTION() RETURNS BOOLEAN
DEFINE _bearerFunc TBearerFunc
DEFINE _refreshFunc TRefreshFunc

FUNCTION setBearerFunc(f TBearerFunc)
  LET _bearerFunc = f
END FUNCTION

FUNCTION setRefreshFunc(f TRefreshFunc)
  LET _refreshFunc = f
END FUNCTION

FUNCTION bearer() RETURNS STRING
  IF _bearerFunc IS NOT NULL THEN
    RETURN _bearerFunc()
  END IF
  VAR tok = fgl_getenv("FGLPKG_TOKEN")
  IF tok IS NOT NULL THEN
    RETURN tok.trim()
  END IF
  RETURN NULL
END FUNCTION

PRIVATE FUNCTION tryRefresh() RETURNS BOOLEAN
  IF _refreshFunc IS NOT NULL THEN
    RETURN _refreshFunc()
  END IF
  RETURN FALSE
END FUNCTION

FUNCTION isNotFoundErr(err STRING) RETURNS BOOLEAN
  RETURN fglpkgutils.contains(NVL(err, ""), ERR_NOT_FOUND)
END FUNCTION

#+turns a possibly site-relative download_url into an absolute URL against
#+the consumer base; idempotent for already-absolute URLs
FUNCTION absoluteDownloadURL(raw STRING) RETURNS STRING
  IF raw IS NULL OR raw.getLength() == 0 THEN
    RETURN raw
  END IF
  IF fglpkgutils.startsWith(raw, "http://")
      OR fglpkgutils.startsWith(raw, "https://") THEN
    RETURN raw
  END IF
  VAR path = raw
  WHILE path.getLength() > 0 AND path.getCharAt(1) == "/"
    LET path = path.subString(2, path.getLength())
  END WHILE
  RETURN SFMT("%1/%2", fglpkgutils.registryBaseURL(), path)
END FUNCTION

--─── consumer API ───────────────────────────────────────────────────────────

#+fetches the package detail document for a slug
FUNCTION fetchPackageDetail(slug STRING)
    RETURNS(BOOLEAN, TApiPackageDetail, STRING)
  DEFINE d, empty TApiPackageDetail
  DEFINE ok BOOLEAN
  DEFINE body, err STRING
  VAR u = SFMT("%1/registry/packages/%2",
      fglpkgutils.registryBaseURL(), urlPathEscape(slug))
  CALL httpGetAuthed(u) RETURNING ok, body, err
  IF NOT ok THEN
    RETURN FALSE, empty, err
  END IF
  CALL parsePackageDetail(body, slug) RETURNING ok, d, err
  RETURN ok, d, err
END FUNCTION

#+parses a package detail JSON document (pure, testable without HTTP)
FUNCTION parsePackageDetail(body STRING, slug STRING)
    RETURNS(BOOLEAN, TApiPackageDetail, STRING)
  DEFINE d, empty TApiPackageDetail
  TRY
    CALL util.JSON.parse(body, d)
  CATCH
    RETURN FALSE, empty, "invalid package detail response"
  END TRY
  IF d.slug IS NULL THEN
    LET d.slug = slug
  END IF
  RETURN TRUE, d, NULL
END FUNCTION

#+returns all published versions of a package
FUNCTION fetchVersionList(name STRING) RETURNS(BOOLEAN, TVersionList, STRING)
  DEFINE vl, empty TVersionList
  DEFINE d TApiPackageDetail
  DEFINE ok BOOLEAN
  DEFINE err STRING
  DEFINE i, j INT
  CALL fetchPackageDetail(name) RETURNING ok, d, err
  IF NOT ok THEN
    RETURN FALSE, empty,
        SFMT('failed to fetch version list for "%1": %2', name, err)
  END IF
  LET vl.name = d.slug
  FOR i = 1 TO d.versions.getLength()
    LET vl.versions[i] = d.versions[i].version
    LET vl.versionEntries[i].version = d.versions[i].version
    LET vl.versionEntries[i].genero = d.versions[i].genero
    FOR j = 1 TO d.versions[i].artifacts.getLength()
      LET vl.versionEntries[i].variants[j] = d.versions[i].artifacts[j].variant
    END FOR
  END FOR
  RETURN TRUE, vl, NULL
END FUNCTION

#+retrieves package metadata, picking the artifact whose variant matches
#+generoMajor; empty generoMajor or no match falls back to "default",
#+then to the first artifact
FUNCTION fetchInfoForGenero(name STRING, version STRING, generoMajor STRING)
    RETURNS(BOOLEAN, TPackageInfo, STRING)
  DEFINE info, empty TPackageInfo
  DEFINE d TApiPackageDetail
  DEFINE ok BOOLEAN
  DEFINE err STRING
  CALL fetchPackageDetail(name) RETURNING ok, d, err
  IF NOT ok THEN
    RETURN FALSE, empty,
        SFMT("failed to fetch package info for %1@%2: %3", name, version, err)
  END IF
  CALL buildInfoFromDetail(d, version, generoMajor) RETURNING ok, info, err
  RETURN ok, info, err
END FUNCTION

FUNCTION fetchInfo(name STRING, version STRING)
    RETURNS(BOOLEAN, TPackageInfo, STRING)
  DEFINE info TPackageInfo
  DEFINE ok BOOLEAN
  DEFINE err STRING
  CALL fetchInfoForGenero(name, version, "") RETURNING ok, info, err
  RETURN ok, info, err
END FUNCTION

#+builds a TPackageInfo from a package detail document (pure, testable)
FUNCTION buildInfoFromDetail(
    d TApiPackageDetail, version STRING, generoMajor STRING)
    RETURNS(BOOLEAN, TPackageInfo, STRING)
  DEFINE info, empty TPackageInfo
  DEFINE i, vi, ai INT
  LET vi = 0
  FOR i = 1 TO d.versions.getLength()
    IF d.versions[i].version == version THEN
      LET vi = i
      EXIT FOR
    END IF
  END FOR
  IF vi == 0 THEN
    RETURN FALSE, empty,
        SFMT('version "%1" not found for package "%2": %3',
            version, d.slug, ERR_NOT_FOUND)
  END IF
  LET ai = pickArtifact(d.versions[vi].artifacts, generoMajor)
  IF ai == 0 THEN
    RETURN FALSE, empty,
        SFMT("no artifact available for %1@%2", d.slug, version)
  END IF
  LET info.name = d.slug
  LET info.version = d.versions[vi].version
  LET info.description = d.description
  LET info.author = d.versions[vi].author
  IF info.author IS NULL THEN
    LET info.author = d.owner.name
  END IF
  LET info.license = d.versions[vi].license
  LET info.publishedAt = d.versions[vi].publishedAt
  LET info.downloadUrl = absoluteDownloadURL(d.versions[vi].artifacts[ai].downloadUrl)
  LET info.checksum = d.versions[vi].artifacts[ai].sha256
  LET info.genero = d.versions[vi].genero
  LET info.fglDeps = d.versions[vi].dependencies.fgl
  LET info.javaDeps = d.versions[vi].dependencies.java
  LET info.variant = d.versions[vi].artifacts[ai].variant
  LET info.readme = d.versions[vi].readme
  FOR i = 1 TO d.versions[vi].artifacts.getLength()
    VAR variantName = d.versions[vi].artifacts[i].variant
    IF fglpkgutils.startsWith(variantName, "genero") THEN
      LET variantName = variantName.subString(7, variantName.getLength())
    END IF
    LET info.variants[i].generoMajor = variantName
    LET info.variants[i].downloadUrl =
        absoluteDownloadURL(d.versions[vi].artifacts[i].downloadUrl)
    LET info.variants[i].checksum = d.versions[vi].artifacts[i].sha256
  END FOR
  RETURN TRUE, info, NULL
END FUNCTION

#+selects the best matching artifact for generoMajor; returns the index
#+or 0 when the list is empty; preference order:
#+ 1. "webcomponent"  2. exact "genero<N>"  3. "default"  4. first listed
FUNCTION pickArtifact(arts TApiArtifacts, generoMajor STRING) RETURNS INT
  DEFINE i INT
  IF arts.getLength() == 0 THEN
    RETURN 0
  END IF
  FOR i = 1 TO arts.getLength()
    IF arts[i].variant == "webcomponent" THEN
      RETURN i
    END IF
  END FOR
  IF generoMajor IS NOT NULL AND generoMajor.getLength() > 0 THEN
    VAR want = SFMT("genero%1", generoMajor)
    FOR i = 1 TO arts.getLength()
      IF arts[i].variant == want THEN
        RETURN i
      END IF
    END FOR
  END IF
  FOR i = 1 TO arts.getLength()
    IF arts[i].variant == "default" THEN
      RETURN i
    END IF
  END FOR
  RETURN 1
END FUNCTION

#+fetches the best matching version of name for the given constraint
#+("latest", "*" or any semver constraint)
FUNCTION resolvePackage(name STRING, constraint STRING, generoMajor STRING)
    RETURNS(BOOLEAN, TPackageInfo, STRING)
  DEFINE info, empty TPackageInfo
  DEFINE vl TVersionList
  DEFINE ok BOOLEAN
  DEFINE err STRING
  DEFINE c semver.TConstraint
  CALL fetchVersionList(name) RETURNING ok, vl, err
  IF NOT ok THEN
    RETURN FALSE, empty, err
  END IF
  CALL semver.parseConstraint(constraint) RETURNING ok, c, err
  IF NOT ok THEN
    RETURN FALSE, empty,
        SFMT('invalid version constraint "%1": %2', constraint, err)
  END IF
  VAR best = semver.latest(vl.versions, c)
  IF best IS NULL THEN
    RETURN FALSE, empty,
        SFMT('no version of "%1" satisfies constraint "%2"', name, constraint)
  END IF
  CALL fetchInfoForGenero(name, best, generoMajor) RETURNING ok, info, err
  RETURN ok, info, err
END FUNCTION

#+queries the registry for packages matching term
FUNCTION search(term STRING)
    RETURNS(BOOLEAN, DYNAMIC ARRAY OF TSearchResult, STRING)
  DEFINE results, empty DYNAMIC ARRAY OF TSearchResult
  DEFINE ok BOOLEAN
  DEFINE body, err STRING
  VAR u = SFMT("%1/registry/packages?q=%2",
      fglpkgutils.registryBaseURL(), urlQueryEscape(NVL(term, "")))
  CALL httpGetAuthed(u) RETURNING ok, body, err
  IF NOT ok THEN
    RETURN FALSE, empty, SFMT("search failed: %1", err)
  END IF
  CALL parseBrowseResponse(body) RETURNING ok, results, err
  RETURN ok, results, err
END FUNCTION

#+parses a browse/search response (pure, testable)
FUNCTION parseBrowseResponse(body STRING)
    RETURNS(BOOLEAN, DYNAMIC ARRAY OF TSearchResult, STRING)
  DEFINE br TApiBrowseResponse
  DEFINE results, empty DYNAMIC ARRAY OF TSearchResult
  DEFINE i INT
  TRY
    CALL util.JSON.parse(body, br)
  CATCH
    RETURN FALSE, empty, "invalid registry response"
  END TRY
  FOR i = 1 TO br.packages.getLength()
    LET results[i].name = br.packages[i].slug
    LET results[i].latestVersion = br.packages[i].latestVersion
    LET results[i].description = br.packages[i].description
    LET results[i].author = br.packages[i].owner.name
  END FOR
  RETURN TRUE, results, NULL
END FUNCTION

#+downloads a URL to destPath sending the current bearer;
#+returns ok, HTTP status (0 on transport error) and an error text
FUNCTION downloadToFile(url STRING, dest STRING)
    RETURNS(BOOLEAN, INT, STRING)
  DEFINE ok BOOLEAN
  DEFINE code INT
  DEFINE err STRING
  CALL downloadToFileAuth(url, bearer(), FALSE, dest) RETURNING ok, code, err
  RETURN ok, code, err
END FUNCTION

#+download variant with an explicit token; octetStream additionally sends
#+"Accept: application/octet-stream" (GitHub release asset downloads)
FUNCTION downloadToFileAuth(
    url STRING, tok STRING, octetStream BOOLEAN, dest STRING)
    RETURNS(BOOLEAN, INT, STRING)
  DEFINE req com.HttpRequest
  DEFINE resp com.HttpResponse
  DEFINE code INT
  DEFINE tmpPath STRING
  TRY
    LET req = com.HttpRequest.Create(url)
    CALL req.setMethod("GET")
    IF tok IS NOT NULL AND tok.getLength() > 0 THEN
      CALL req.setHeader("Authorization", SFMT("Bearer %1", tok))
    END IF
    IF octetStream THEN
      CALL req.setHeader("Accept", "application/octet-stream")
    END IF
    CALL req.doRequest()
    LET resp = req.getResponse()
    LET code = resp.getStatusCode()
    IF code < 200 OR code >= 300 THEN
      IF code == 401 THEN
        RETURN FALSE, code,
            "registry returned 401 Unauthorized — run 'fglpkg login' or set FGLPKG_TOKEN"
      END IF
      RETURN FALSE, code, SFMT("download failed: HTTP %1 for %2", code, url)
    END IF
    LET tmpPath = resp.getFileResponse()
  CATCH
    RETURN FALSE, 0, SFMT("download failed: %1 (%2)", err_get(status), url)
  END TRY
  --getFileResponse stores into the temp dir: move it to dest
  IF os.Path.exists(dest) THEN
    CALL os.Path.delete(dest) RETURNING status
  END IF
  IF NOT os.Path.rename(tmpPath, dest) THEN
    --cross-device fallback
    IF NOT os.Path.copy(tmpPath, dest) THEN
      RETURN FALSE, code, SFMT("cannot store download at %1", dest)
    END IF
    CALL os.Path.delete(tmpPath) RETURNING status
  END IF
  RETURN TRUE, code, NULL
END FUNCTION

--─── publisher API ──────────────────────────────────────────────────────────

#+optional rich metadata pushed alongside a new version on create; empty
#+fields are omitted from the payload
PUBLIC TYPE TVersionMeta RECORD
  repository STRING,
  author STRING,
  license STRING,
  genero STRING,
  dependencies manifest.TDependencies,
  readme STRING,
  userguide STRING
END RECORD

#+creates the slug on the registry if it doesn't exist;
#+NULL on both 201 (created) and 409 (already exists)
FUNCTION publishCreatePackage(
    slug STRING, name STRING, description STRING, visibility STRING)
    RETURNS STRING
  DEFINE code INT
  DEFINE body, err STRING
  IF visibility IS NULL OR visibility.getLength() == 0 THEN
    LET visibility = "public"
  END IF
  VAR payload = util.JSONObject.create()
  CALL payload.put("slug", slug)
  CALL payload.put("name", NVL(name, ""))
  CALL payload.put("description", NVL(description, ""))
  CALL payload.put("visibility", visibility)
  CALL doJSONRequest("POST",
          SFMT("%1/registry/packages", fglpkgutils.registryBaseURL()),
          payload.toString())
      RETURNING code, body, err
  IF err IS NOT NULL THEN
    RETURN SFMT('create package "%1": %2', slug, err)
  END IF
  IF code == 201 OR code == 409 THEN
    RETURN NULL
  END IF
  RETURN SFMT('create package "%1": HTTP %2: %3', slug, code, body)
END FUNCTION

#+adds version under slug; 201 -> ok; 409 -> versionExists=TRUE so the
#+caller can upload a new variant against the existing version
FUNCTION publishCreateVersion(
    slug STRING, version STRING, changelog STRING, meta TVersionMeta)
    RETURNS(BOOLEAN, BOOLEAN, STRING)
  DEFINE code INT
  DEFINE body, err STRING
  VAR payload = util.JSONObject.create()
  CALL payload.put("version", version)
  --"" is NULL in 4GL, so put() would emit JSON null; patch it below
  CALL payload.put("changelog", NVL(changelog, ""))
  IF meta.repository IS NOT NULL THEN
    CALL payload.put("repository", meta.repository)
  END IF
  IF meta.author IS NOT NULL THEN
    CALL payload.put("author", meta.author)
  END IF
  IF meta.license IS NOT NULL THEN
    CALL payload.put("license", meta.license)
  END IF
  IF meta.genero IS NOT NULL THEN
    CALL payload.put("genero", meta.genero)
  END IF
  IF meta.dependencies.fgl.getLength() > 0
      OR meta.dependencies.java.getLength() > 0 THEN
    VAR depsObj = util.JSONObject.parse(
        util.JSON.stringify(meta.dependencies))
    CALL payload.put("dependencies", depsObj)
  END IF
  IF meta.readme IS NOT NULL THEN
    CALL payload.put("readme", meta.readme)
  END IF
  IF meta.userguide IS NOT NULL THEN
    CALL payload.put("userguide", meta.userguide)
  END IF
  VAR payloadStr = fglpkgutils.replace(
      payload.toString(), '"changelog":null', '"changelog":""')
  CALL doJSONRequest("POST",
          SFMT("%1/registry/packages/%2/versions",
              fglpkgutils.registryBaseURL(), urlPathEscape(slug)),
          payloadStr)
      RETURNING code, body, err
  IF err IS NOT NULL THEN
    RETURN FALSE, FALSE, SFMT("create version %1@%2: %3", slug, version, err)
  END IF
  IF code == 201 THEN
    RETURN TRUE, FALSE, NULL
  END IF
  IF code == 409 THEN
    RETURN FALSE, TRUE,
        SFMT("create version %1@%2: version already exists", slug, version)
  END IF
  RETURN FALSE, FALSE,
      SFMT("create version %1@%2: HTTP %3: %4", slug, version, code, body)
END FUNCTION

#+streams the zip body for (slug, version, variant) to the registry;
#+no 401 retry (the body has been consumed, Go parity)
FUNCTION publishUploadArtifact(
    slug STRING, version STRING, variant STRING, filename STRING,
    zipPath STRING)
    RETURNS STRING
  DEFINE req com.HttpRequest
  DEFINE resp com.HttpResponse
  DEFINE code INT
  DEFINE body STRING
  VAR u = SFMT("%1/registry/packages/%2/versions/%3/artifacts/%4?filename=%5",
      fglpkgutils.registryBaseURL(), urlPathEscape(slug),
      urlPathEscape(version), urlPathEscape(variant),
      urlQueryEscape(filename))
  TRY
    LET req = com.HttpRequest.Create(u)
    CALL req.setMethod("PUT")
    CALL req.setHeader("Content-Type", "application/zip")
    CALL req.setHeader("Accept", "application/json")
    VAR tok = bearer()
    IF tok IS NOT NULL AND tok.getLength() > 0 THEN
      CALL req.setHeader("Authorization", SFMT("Bearer %1", tok))
    END IF
    CALL req.doFileRequest(zipPath)
    LET resp = req.getResponse()
    LET code = resp.getStatusCode()
    LET body = resp.getTextResponse()
  CATCH
    RETURN SFMT("upload artifact %1@%2/%3: registry upload failed: %4",
        slug, version, variant, err_get(status))
  END TRY
  IF code >= 200 AND code < 300 THEN
    RETURN NULL
  END IF
  RETURN SFMT("upload artifact %1@%2/%3: HTTP %4: %5",
      slug, version, variant, code, body)
END FUNCTION

#+marks a pending version for admin review (idempotent)
FUNCTION publishSubmit(slug STRING, version STRING) RETURNS STRING
  DEFINE code INT
  DEFINE body, err STRING
  CALL doJSONRequest("POST",
          SFMT("%1/registry/packages/%2/versions/%3/submit",
              fglpkgutils.registryBaseURL(), urlPathEscape(slug),
              urlPathEscape(version)),
          NULL)
      RETURNING code, body, err
  IF err IS NOT NULL THEN
    RETURN SFMT("submit %1@%2: %3", slug, version, err)
  END IF
  IF code >= 200 AND code < 300 THEN
    RETURN NULL
  END IF
  RETURN SFMT("submit %1@%2: HTTP %3: %4", slug, version, code, body)
END FUNCTION

#+reports which variants are already published for (slug, version);
#+a missing package or version yields an ERR_NOT_FOUND error
FUNCTION variantsFor(slug STRING, version STRING)
    RETURNS(BOOLEAN, fglpkgutils.TStringArr, STRING)
  DEFINE out, empty fglpkgutils.TStringArr
  DEFINE d TApiPackageDetail
  DEFINE ok BOOLEAN
  DEFINE err STRING
  CALL fetchPackageDetail(slug) RETURNING ok, d, err
  IF NOT ok THEN
    RETURN FALSE, empty, err
  END IF
  CALL variantsFromDetail(d, version) RETURNING ok, out, err
  RETURN ok, out, err
END FUNCTION

#+the variant list of one version from a detail document (pure, testable)
FUNCTION variantsFromDetail(d TApiPackageDetail, version STRING)
    RETURNS(BOOLEAN, fglpkgutils.TStringArr, STRING)
  DEFINE out, empty fglpkgutils.TStringArr
  DEFINE i, j INT
  FOR i = 1 TO d.versions.getLength()
    IF d.versions[i].version != version THEN
      CONTINUE FOR
    END IF
    FOR j = 1 TO d.versions[i].artifacts.getLength()
      LET out[j] = d.versions[i].artifacts[j].variant
    END FOR
    RETURN TRUE, out, NULL
  END FOR
  RETURN FALSE, empty,
      SFMT("version %1 of %2 not found on registry: %3",
          version, d.slug, ERR_NOT_FOUND)
END FUNCTION

--─── whoami ─────────────────────────────────────────────────────────────────

PUBLIC TYPE TWhoami RECORD
  user RECORD
    id STRING,
    email STRING,
    name STRING
  END RECORD,
  partner RECORD
    id STRING,
    name STRING
  END RECORD,
  scopes fglpkgutils.TStringArr,
  username STRING --legacy servers
END RECORD

#+fetches the authenticated identity; tries /registry/whoami and falls
#+back to /auth/whoami on 404 only
FUNCTION whoamiRequest(tok STRING) RETURNS(BOOLEAN, TWhoami, STRING)
  DEFINE who, empty TWhoami
  DEFINE ok, was404 BOOLEAN
  DEFINE err STRING
  VAR base = fglpkgutils.registryBaseURL()
  CALL whoamiFetch(SFMT("%1/registry/whoami", base), tok)
      RETURNING ok, who, was404, err
  IF ok THEN
    RETURN TRUE, who, NULL
  END IF
  IF NOT was404 THEN
    RETURN FALSE, empty, err
  END IF
  CALL whoamiFetch(SFMT("%1/auth/whoami", base), tok)
      RETURNING ok, who, was404, err
  IF NOT ok THEN
    RETURN FALSE, empty, err
  END IF
  RETURN TRUE, who, NULL
END FUNCTION

PRIVATE FUNCTION whoamiFetch(u STRING, tok STRING)
    RETURNS(BOOLEAN, TWhoami, BOOLEAN, STRING)
  DEFINE who, empty TWhoami
  DEFINE code INT
  DEFINE body, err STRING
  CALL authedGet(u, tok) RETURNING code, body, err
  IF err IS NOT NULL THEN
    RETURN FALSE, empty, FALSE, err
  END IF
  IF code == 404 THEN
    RETURN FALSE, empty, TRUE, ERR_NOT_FOUND
  END IF
  IF code == 401 THEN
    RETURN FALSE, empty, FALSE, "invalid or expired token"
  END IF
  IF code != 200 THEN
    RETURN FALSE, empty, FALSE, registryErrorText(code, body)
  END IF
  TRY
    CALL util.JSON.parse(body, who)
  CATCH
    RETURN FALSE, empty, FALSE, "invalid whoami response"
  END TRY
  --legacy servers only send username
  IF who.username IS NOT NULL AND who.user.name IS NULL THEN
    LET who.user.name = who.username
  END IF
  RETURN TRUE, who, FALSE, NULL
END FUNCTION

#+formats the identity like the Go binary: "Name <email>" / email / name /
#+user id / "(user)"
FUNCTION whoamiSubject(who TWhoami) RETURNS STRING
  CASE
    WHEN who.user.name IS NOT NULL AND who.user.email IS NOT NULL
      RETURN SFMT("%1 <%2>", who.user.name, who.user.email)
    WHEN who.user.email IS NOT NULL
      RETURN who.user.email
    WHEN who.user.name IS NOT NULL
      RETURN who.user.name
    WHEN who.user.id IS NOT NULL
      RETURN who.user.id
  END CASE
  RETURN "(user)"
END FUNCTION

PRIVATE FUNCTION registryErrorText(code INT, body STRING) RETURNS STRING
  DEFINE parsed RECORD
    err STRING ATTRIBUTES(json_name = "error")
  END RECORD
  TRY
    CALL util.JSON.parse(body, parsed)
  CATCH
    LET parsed.err = NULL
  END TRY
  IF parsed.err IS NOT NULL THEN
    RETURN SFMT("registry error (%1): %2", code, parsed.err)
  END IF
  RETURN SFMT("registry returned HTTP %1", code)
END FUNCTION

#+method + url + optional JSON body with Bearer auth; one-shot 401 retry
#+via the refresh hook; returns (status, responseBody, transportError)
FUNCTION doJSONRequest(method STRING, u STRING, jsonBody STRING)
    RETURNS(INT, STRING, STRING)
  DEFINE code INT
  DEFINE body, err STRING
  CALL doJSONRequestOnce(method, u, jsonBody, bearer())
      RETURNING code, body, err
  IF err IS NOT NULL THEN
    RETURN 0, NULL, err
  END IF
  IF code == 401 AND tryRefresh() THEN
    CALL doJSONRequestOnce(method, u, jsonBody, bearer())
        RETURNING code, body, err
    IF err IS NOT NULL THEN
      RETURN 0, NULL, err
    END IF
  END IF
  RETURN code, body, NULL
END FUNCTION

PRIVATE FUNCTION doJSONRequestOnce(
    method STRING, u STRING, jsonBody STRING, tok STRING)
    RETURNS(INT, STRING, STRING)
  DEFINE req com.HttpRequest
  DEFINE resp com.HttpResponse
  DEFINE code INT
  DEFINE body STRING
  TRY
    LET req = com.HttpRequest.Create(u)
    CALL req.setMethod(method)
    CALL req.setHeader("Accept", "application/json")
    IF tok IS NOT NULL AND tok.getLength() > 0 THEN
      CALL req.setHeader("Authorization", SFMT("Bearer %1", tok))
    END IF
    IF jsonBody IS NOT NULL THEN
      CALL req.setHeader("Content-Type", "application/json")
      CALL req.doTextRequest(jsonBody)
    ELSE
      CALL req.doRequest()
    END IF
    LET resp = req.getResponse()
    LET code = resp.getStatusCode()
    LET body = resp.getTextResponse()
  CATCH
    RETURN 0, NULL, SFMT("registry request failed: %1", err_get(status))
  END TRY
  RETURN code, body, NULL
END FUNCTION

--─── internal HTTP helpers ──────────────────────────────────────────────────

#+GET with Authorization header; on 401 tries a refresh once and retries;
#+404 is reported via ERR_NOT_FOUND (test with isNotFoundErr)
PRIVATE FUNCTION httpGetAuthed(u STRING) RETURNS(BOOLEAN, STRING, STRING)
  DEFINE body STRING
  DEFINE code INT
  DEFINE err STRING
  CALL authedGet(u, bearer()) RETURNING code, body, err
  IF err IS NOT NULL THEN
    RETURN FALSE, NULL, err
  END IF
  IF code == 401 AND tryRefresh() THEN
    CALL authedGet(u, bearer()) RETURNING code, body, err
    IF err IS NOT NULL THEN
      RETURN FALSE, NULL, err
    END IF
  END IF
  IF code == 404 THEN
    RETURN FALSE, NULL, ERR_NOT_FOUND
  END IF
  IF code < 200 OR code >= 300 THEN
    RETURN FALSE, NULL, SFMT("registry returned HTTP %1: %2", code, body)
  END IF
  RETURN TRUE, body, NULL
END FUNCTION

PRIVATE FUNCTION authedGet(u STRING, tok STRING)
    RETURNS(INT, STRING, STRING)
  DEFINE req com.HttpRequest
  DEFINE resp com.HttpResponse
  DEFINE body STRING
  DEFINE code INT
  TRY
    LET req = com.HttpRequest.Create(u)
    CALL req.setMethod("GET")
    CALL req.setHeader("Accept", "application/json")
    IF tok IS NOT NULL AND tok.getLength() > 0 THEN
      CALL req.setHeader("Authorization", SFMT("Bearer %1", tok))
    END IF
    CALL req.doRequest()
    LET resp = req.getResponse()
    LET code = resp.getStatusCode()
    LET body = resp.getTextResponse()
  CATCH
    RETURN 0, NULL, SFMT("registry request failed: %1", err_get(status))
  END TRY
  RETURN code, body, NULL
END FUNCTION

#+percent-encodes a query value (space as '+', like Go url.QueryEscape)
FUNCTION urlQueryEscape(s STRING) RETURNS STRING
  DEFINE i INT
  VAR sb = base.StringBuffer.create()
  FOR i = 1 TO s.getLength()
    VAR c = s.getCharAt(i)
    CASE
      WHEN fglpkgutils.isLetter(c) OR fglpkgutils.isDigit(c)
        CALL sb.append(c)
      WHEN c == "-" OR c == "_" OR c == "." OR c == "~"
        CALL sb.append(c)
      WHEN c == " "
        CALL sb.append("+")
      OTHERWISE
        CALL sb.append(pctEncode(c))
    END CASE
  END FOR
  RETURN sb.toString()
END FUNCTION

#+percent-encodes a path segment (space as %20)
FUNCTION urlPathEscape(s STRING) RETURNS STRING
  DEFINE i INT
  VAR sb = base.StringBuffer.create()
  FOR i = 1 TO s.getLength()
    VAR c = s.getCharAt(i)
    CASE
      WHEN fglpkgutils.isLetter(c) OR fglpkgutils.isDigit(c)
        CALL sb.append(c)
      WHEN c == "-" OR c == "_" OR c == "." OR c == "~"
        CALL sb.append(c)
      OTHERWISE
        CALL sb.append(pctEncode(c))
    END CASE
  END FOR
  RETURN sb.toString()
END FUNCTION

PRIVATE FUNCTION pctEncode(c STRING) RETURNS STRING
  DEFINE i INT
  VAR sb = base.StringBuffer.create()
  --encode the UTF-8 bytes of the character
  VAR code INT = ORD(c)
  IF code <= 127 THEN
    CALL sb.append(SFMT("%%%1", hexByte(code)))
  ELSE
    --multi-byte characters: fall back to encoding each octet
    FOR i = 1 TO c.getLength()
      CALL sb.append(SFMT("%%%1", hexByte(ORD(c.getCharAt(i)))))
    END FOR
  END IF
  RETURN sb.toString()
END FUNCTION

PRIVATE FUNCTION hexByte(n INT) RETURNS STRING
  VAR hex STRING = util.Integer.toHexString(n)
  LET hex = hex.toUpperCase()
  IF hex.getLength() == 1 THEN
    RETURN SFMT("0%1", hex)
  END IF
  RETURN hex
END FUNCTION
