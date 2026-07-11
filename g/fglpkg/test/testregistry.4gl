#+ tests for registry.4gl (pure parsing/picking) and credentials.4gl
OPTIONS SHORT CIRCUIT
IMPORT os
IMPORT FGL testutils
IMPORT FGL fglpkg.fglpkgutils
IMPORT FGL fglpkg.registry
IMPORT FGL fglpkg.credentials
&include "testassert.inc"

MAIN
  CALL testParseDetail()
  CALL testBuildInfo()
  CALL testPickArtifact()
  CALL testAbsoluteURL()
  CALL testBrowseResponse()
  CALL testEscaping()
  CALL testCredentials()
  CALL testVariantsFromDetail()
  CALL testWhoamiSubject()
  CALL testRefresh()
  TSUMMARY()
END MAIN

FUNCTION detailFixture() RETURNS STRING
  RETURN '{"slug":"poiapi","name":"poiapi","description":"POI API",'
      || '"visibility":"public","owner":{"partner_id":"p1","name":"FourJs"},'
      || '"status":"active","latest_version":"1.1.0","downloads":42,'
      || '"versions":['
      || '{"version":"1.0.0","status":"published","genero":"^4.0.0",'
      || '"author":"Mike","license":"MIT","published_at":"2026-01-01T00:00:00Z",'
      || '"dependencies":{"fgl":{"myutils":"^1.0.0"},"java":['
      || '{"groupId":"org.apache.poi","artifactId":"poi","version":"5.2.3"}]},'
      || '"artifacts":['
      || '{"variant":"genero4","filename":"poiapi-1.0.0-genero4.zip",'
      || '"size_bytes":1234,"sha256":"aa11",'
      || '"download_url":"/registry/packages/poiapi/versions/1.0.0/artifacts/genero4"},'
      || '{"variant":"genero6","filename":"poiapi-1.0.0-genero6.zip",'
      || '"size_bytes":1250,"sha256":"bb22",'
      || '"download_url":"https://cdn.example.com/poiapi-1.0.0-genero6.zip"}]},'
      || '{"version":"1.1.0","status":"published",'
      || '"artifacts":[{"variant":"default","filename":"poiapi-1.1.0.zip",'
      || '"size_bytes":1300,"sha256":"cc33","download_url":"/dl/poiapi-1.1.0.zip"}]}'
      || ']}'
END FUNCTION

FUNCTION testParseDetail()
  DEFINE ok BOOLEAN
  DEFINE d registry.TApiPackageDetail
  DEFINE err STRING
  CALL registry.parsePackageDetail(detailFixture(), "poiapi")
      RETURNING ok, d, err
  TOK(ok)
  TEQ(d.slug, "poiapi")
  TEQ(d.owner.name, "FourJs")
  TEQ(d.latestVersion, "1.1.0")
  TEQ(d.versions.getLength(), 2)
  TEQ(d.versions[1].genero, "^4.0.0")
  TEQ(d.versions[1].artifacts.getLength(), 2)
  TEQ(d.versions[1].artifacts[1].sizeBytes, 1234)
  TEQ(d.versions[1].dependencies.fgl["myutils"], "^1.0.0")
  TEQ(d.versions[1].dependencies.java[1].artifactId, "poi")
  --slug fallback when missing
  CALL registry.parsePackageDetail('{"versions":[]}', "fallback")
      RETURNING ok, d, err
  TOK(ok)
  TEQ(d.slug, "fallback")
  --malformed
  CALL registry.parsePackageDetail("{oops", "x") RETURNING ok, d, err
  TOK(NOT ok)
END FUNCTION

FUNCTION testBuildInfo()
  DEFINE ok BOOLEAN
  DEFINE d registry.TApiPackageDetail
  DEFINE info registry.TPackageInfo
  DEFINE err STRING
  CALL registry.parsePackageDetail(detailFixture(), "poiapi")
      RETURNING ok, d, err
  TOK(ok)

  --genero major 6 picks the genero6 variant (absolute URL kept as is)
  CALL registry.buildInfoFromDetail(d, "1.0.0", "6") RETURNING ok, info, err
  TOK(ok)
  TEQ(info.variant, "genero6")
  TEQ(info.checksum, "bb22")
  TEQ(info.downloadUrl, "https://cdn.example.com/poiapi-1.0.0-genero6.zip")
  TEQ(info.author, "Mike")
  TEQ(info.license, "MIT")
  TEQ(info.genero, "^4.0.0")
  TEQ(info.fglDeps["myutils"], "^1.0.0")
  TEQ(info.javaDeps[1].groupId, "org.apache.poi")
  TEQ(info.variants.getLength(), 2)
  TEQ(info.variants[1].generoMajor, "4")

  --genero major 4 picks genero4 and makes the URL absolute
  CALL fgl_setenv("FGLPKG_REGISTRY", "https://reg.example.com")
  CALL registry.buildInfoFromDetail(d, "1.0.0", "4") RETURNING ok, info, err
  TOK(ok)
  TEQ(info.variant, "genero4")
  VAR wantURL = "https://reg.example.com/registry/packages/poiapi/versions/1.0.0/artifacts/genero4"
  TEQ(info.downloadUrl, wantURL)
  CALL fgl_setenv("FGLPKG_REGISTRY", NULL)

  --unmatched major falls back to default artifact
  CALL registry.buildInfoFromDetail(d, "1.1.0", "9") RETURNING ok, info, err
  TOK(ok)
  TEQ(info.variant, "default")
  --author falls back to the owner name when the version has none
  TEQ(info.author, "FourJs")

  --unknown version reports not-found
  CALL registry.buildInfoFromDetail(d, "9.9.9", "4") RETURNING ok, info, err
  TOK(NOT ok)
  TOK(registry.isNotFoundErr(err))
END FUNCTION

FUNCTION testPickArtifact()
  DEFINE arts registry.TApiArtifacts
  --empty list
  TEQ(registry.pickArtifact(arts, "4"), 0)
  --webcomponent always wins
  LET arts[1].variant = "genero4"
  LET arts[2].variant = "webcomponent"
  TEQ(registry.pickArtifact(arts, "4"), 2)
  --exact genero match
  CALL arts.clear()
  LET arts[1].variant = "genero4"
  LET arts[2].variant = "genero6"
  TEQ(registry.pickArtifact(arts, "6"), 2)
  --default fallback
  TEQ(registry.pickArtifact(arts, "5"), 1) --no default -> first
  LET arts[3].variant = "default"
  TEQ(registry.pickArtifact(arts, "5"), 3)
END FUNCTION

FUNCTION testAbsoluteURL()
  CALL fgl_setenv("FGLPKG_REGISTRY", "https://reg.example.com/")
  TEQ(registry.absoluteDownloadURL("/a/b.zip"), "https://reg.example.com/a/b.zip")
  TEQ(registry.absoluteDownloadURL("a/b.zip"), "https://reg.example.com/a/b.zip")
  TEQ(registry.absoluteDownloadURL("https://x.com/a.zip"), "https://x.com/a.zip")
  TOK(registry.absoluteDownloadURL(NULL) IS NULL)
  CALL fgl_setenv("FGLPKG_REGISTRY", NULL)
END FUNCTION

FUNCTION testBrowseResponse()
  DEFINE ok BOOLEAN
  DEFINE results DYNAMIC ARRAY OF registry.TSearchResult
  DEFINE err STRING
  VAR body = '{"packages":[{"slug":"a","description":"da",'
      || '"owner":{"name":"oa"},"latest_version":"1.0.0"},'
      || '{"slug":"b","description":"db","owner":{"name":"ob"},'
      || '"latest_version":"2.0.0"}],"page":1,"pageSize":20,"total":2}'
  CALL registry.parseBrowseResponse(body) RETURNING ok, results, err
  TOK(ok)
  TEQ(results.getLength(), 2)
  TEQ(results[1].name, "a")
  TEQ(results[1].latestVersion, "1.0.0")
  TEQ(results[2].author, "ob")
END FUNCTION

FUNCTION testEscaping()
  TEQ(registry.urlQueryEscape("json tools"), "json+tools")
  TEQ(registry.urlQueryEscape("a&b=c"), "a%26b%3Dc")
  TEQ(registry.urlQueryEscape("my-pkg_1.0~x"), "my-pkg_1.0~x")
  TEQ(registry.urlPathEscape("my pkg"), "my%20pkg")
END FUNCTION

FUNCTION testCredentials()
  DEFINE tokens credentials.TOAuthTokens
  --key normalization
  TEQ(credentials.normalizeKey("https://Reg.Example.com/"), "https://reg.example.com")
  TEQ(credentials.normalizeKey("https://x.com"), "https://x.com")

  VAR home = fglpkgutils.makeTempDir()
  VAR url = "https://reg.example.com"

  --empty store: no bearer (make sure env token is not set)
  CALL fgl_setenv("FGLPKG_TOKEN", NULL)
  TOK(credentials.activeBearer(home, url) IS NULL)

  --stored PAT
  CALL credentials.setPat(home, url, "gpr_secret", "leo")
  TEQ(credentials.activeBearer(home, url), "gpr_secret")
  VAR creds = credentials.getCreds(home, "https://REG.example.com/")
  TEQ(creds.pat, "gpr_secret")
  TEQ(creds.username, "leo")
  TOK(creds.savedAt IS NOT NULL)
  --file permissions 0600
  TEQ(os.Path.rwx(credentials.credentialsPath(home)), 384)

  --env token overrides stored PAT
  CALL fgl_setenv("FGLPKG_TOKEN", "envtok")
  TEQ(credentials.activeBearer(home, url), "envtok")
  CALL fgl_setenv("FGLPKG_TOKEN", NULL)

  --unexpired oauth access token beats PAT
  LET tokens.accessToken = "oauth_at"
  LET tokens.refreshToken = "oauth_rt"
  LET tokens.expiresAt = "2099-01-01T00:00:00Z"
  CALL credentials.setOAuth(home, url, tokens)
  TEQ(credentials.activeBearer(home, url), "oauth_at")
  --expired oauth falls back to PAT
  LET tokens.expiresAt = "2001-01-01T00:00:00Z"
  CALL credentials.setOAuth(home, url, tokens)
  TEQ(credentials.activeBearer(home, url), "gpr_secret")

  --legacy token migration on load
  CALL fglpkgutils.writeStringToFile(credentials.credentialsPath(home),
      '{"registries":{"https://legacy.com":{"token":"oldtok"}}}')
  VAR f = credentials.loadCreds(home)
  TEQ(f.registries["https://legacy.com"].pat, "oldtok")

  --delete
  TOK(NOT credentials.deleteCreds(home, "https://nope.com"))
  TOK(credentials.deleteCreds(home, "https://legacy.com"))
  CALL fglpkgutils.rmrf(home)
END FUNCTION

FUNCTION testVariantsFromDetail()
  DEFINE ok BOOLEAN
  DEFINE d registry.TApiPackageDetail
  DEFINE variants fglpkgutils.TStringArr
  DEFINE err STRING
  CALL registry.parsePackageDetail(detailFixture(), "poiapi")
      RETURNING ok, d, err
  TOK(ok)
  CALL registry.variantsFromDetail(d, "1.0.0") RETURNING ok, variants, err
  TOK(ok)
  TEQ(fglpkgutils.joinArr(variants, ","), "genero4,genero6")
  CALL registry.variantsFromDetail(d, "1.1.0") RETURNING ok, variants, err
  TOK(ok)
  TEQ(fglpkgutils.joinArr(variants, ","), "default")
  --unknown version reports not-found
  CALL registry.variantsFromDetail(d, "9.9.9") RETURNING ok, variants, err
  TOK(NOT ok)
  TOK(registry.isNotFoundErr(err))
  TOK(fglpkgutils.contains(err, "version 9.9.9 of poiapi not found"))
END FUNCTION

FUNCTION testWhoamiSubject()
  DEFINE who registry.TWhoami
  --empty identity
  TEQ(registry.whoamiSubject(who), "(user)")
  LET who.user.id = "u-1"
  TEQ(registry.whoamiSubject(who), "u-1")
  LET who.user.name = "Leo"
  TEQ(registry.whoamiSubject(who), "Leo")
  LET who.user.email = "leo@4js.com"
  TEQ(registry.whoamiSubject(who), "Leo <leo@4js.com>")
  INITIALIZE who.user.name TO NULL
  TEQ(registry.whoamiSubject(who), "leo@4js.com")
END FUNCTION

--fake refresher used by testRefresh
DEFINE _refreshCalls INT
DEFINE _refreshFail BOOLEAN

FUNCTION fakeRefresher(base STRING, prev credentials.TOAuthTokens)
    RETURNS(BOOLEAN, credentials.TOAuthTokens, STRING)
  DEFINE fresh credentials.TOAuthTokens
  LET _refreshCalls = _refreshCalls + 1
  IF base IS NULL THEN
  END IF
  IF _refreshFail THEN
    RETURN FALSE, fresh, "refresh denied"
  END IF
  LET fresh.accessToken = SFMT("fresh-%1", _refreshCalls)
  LET fresh.refreshToken = prev.refreshToken
  LET fresh.expiresAt = "2099-01-01T00:00:00Z"
  RETURN TRUE, fresh, NULL
END FUNCTION

FUNCTION testRefresh()
  DEFINE tokens credentials.TOAuthTokens
  DEFINE nullRefresher credentials.TRefresherFunc
  VAR home = fglpkgutils.makeTempDir()
  VAR url = "https://reg.example.com"
  CALL fgl_setenv("FGLPKG_TOKEN", NULL)
  CALL credentials.setRefresher(FUNCTION fakeRefresher)
  LET _refreshCalls = 0
  LET _refreshFail = FALSE

  --expired token with refresh token: refresher runs and persists
  LET tokens.accessToken = "stale"
  LET tokens.refreshToken = "rt-1"
  LET tokens.expiresAt = "2001-01-01T00:00:00Z"
  CALL credentials.setOAuth(home, url, tokens)
  TEQ(credentials.activeBearer(home, url), "fresh-1")
  TEQ(_refreshCalls, 1)
  --the fresh tokens were persisted: next call needs no refresh
  TEQ(credentials.activeBearer(home, url), "fresh-1")
  TEQ(_refreshCalls, 1)

  --failing refresh falls back to the PAT
  LET tokens.accessToken = "stale2"
  LET tokens.refreshToken = "rt-2"
  LET tokens.expiresAt = "2001-01-01T00:00:00Z"
  CALL credentials.setOAuth(home, url, tokens)
  CALL credentials.setPat(home, url, "gpr_fallback", "leo")
  LET _refreshFail = TRUE
  TEQ(credentials.activeBearer(home, url), "gpr_fallback")

  --forceRefresh persists unconditionally
  LET _refreshFail = FALSE
  TOK(credentials.forceRefresh(home, url))
  VAR creds = credentials.getCreds(home, url)
  TEQ(creds.oauth.accessToken, "fresh-3")
  --no refresh token stored -> forceRefresh FALSE
  VAR home2 = fglpkgutils.makeTempDir()
  TOK(NOT credentials.forceRefresh(home2, url))

  --publish bearer: env > pat > legacy token, never OAuth
  TEQ(credentials.activePublishBearer(home, url), "gpr_fallback")
  CALL fgl_setenv("FGLPKG_TOKEN", "envwins")
  TEQ(credentials.activePublishBearer(home, url), "envwins")
  CALL fgl_setenv("FGLPKG_TOKEN", NULL)
  CALL fglpkgutils.writeStringToFile(credentials.credentialsPath(home2),
      '{"registries":{"https://reg.example.com":{"token":"legacy-tok"}}}')
  TEQ(credentials.activePublishBearer(home2, url), "legacy-tok")

  --detach the fake refresher for other tests
  CALL credentials.setRefresher(nullRefresher)
  CALL fglpkgutils.rmrf(home)
  CALL fglpkgutils.rmrf(home2)
END FUNCTION
