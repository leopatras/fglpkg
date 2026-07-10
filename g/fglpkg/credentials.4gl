#+ registry credential storage (~/.fglpkg/credentials.json, mode 0600)
#+ port of internal/credentials (Phase 1 subset: PAT + FGLPKG_TOKEN;
#+ the oauth fields are stored/kept for the later OAuth phase)
OPTIONS SHORT CIRCUIT
PACKAGE fglpkg
IMPORT os
IMPORT util
IMPORT FGL fglpkg.fglpkgutils
&include "myassert.inc"

PUBLIC CONSTANT CREDENTIALS_FILENAME = "credentials.json"

--refresh skew: treat tokens expiring within this many seconds as expired
PUBLIC CONSTANT OAUTH_SKEW_SECONDS = 30

PUBLIC TYPE TOAuthTokens RECORD
  accessToken STRING ATTRIBUTES(json_name = "access_token"),
  refreshToken STRING ATTRIBUTES(json_name = "refresh_token"),
  expiresAt STRING ATTRIBUTES(json_name = "expires_at"), --RFC3339 UTC
  scope STRING,
  clientId STRING ATTRIBUTES(json_name = "client_id"),
  clientSecret STRING ATTRIBUTES(json_name = "client_secret")
END RECORD

PUBLIC TYPE TRegistryCreds RECORD
  oauth TOAuthTokens,
  pat STRING,
  token STRING, --legacy field, migrated to pat on load
  username STRING,
  githubToken STRING,
  savedAt STRING
END RECORD

PUBLIC TYPE TCredentialsFile RECORD
  registries DICTIONARY OF TRegistryCreds
END RECORD

FUNCTION credentialsPath(home STRING) RETURNS STRING
  RETURN os.Path.join(home, CREDENTIALS_FILENAME)
END FUNCTION

#+normalizes a registry URL for use as a dictionary key:
#+lowercase, no trailing slash
FUNCTION normalizeKey(url STRING) RETURNS STRING
  LET url = url.trim().toLowerCase()
  WHILE url.getLength() > 1 AND fglpkgutils.endsWith(url, "/")
    LET url = url.subString(1, url.getLength() - 1)
  END WHILE
  RETURN url
END FUNCTION

#+loads the credentials file; a missing file yields an empty record;
#+the legacy `token` field is migrated to `pat` in memory
FUNCTION loadCreds(home STRING) RETURNS TCredentialsFile
  DEFINE f TCredentialsFile
  DEFINE i INT
  VAR path = credentialsPath(home)
  IF NOT os.Path.exists(path) THEN
    RETURN f
  END IF
  TRY
    CALL util.JSON.parse(fglpkgutils.readTextFile(path), f)
  CATCH
    CALL fglpkgutils.myWarning(SFMT("ignoring invalid %1", path))
    INITIALIZE f TO NULL
    RETURN f
  END TRY
  --migrate legacy token -> pat (in memory only)
  VAR keys = f.registries.getKeys()
  FOR i = 1 TO keys.getLength()
    IF f.registries[keys[i]].pat IS NULL
        AND f.registries[keys[i]].token IS NOT NULL THEN
      LET f.registries[keys[i]].pat = f.registries[keys[i]].token
      LET f.registries[keys[i]].token = NULL
    END IF
  END FOR
  RETURN f
END FUNCTION

#+saves the credentials file with restrictive permissions (0700 dir, 0600 file)
FUNCTION saveCreds(home STRING, f TCredentialsFile)
  IF NOT os.Path.exists(home) THEN
    CALL fglpkgutils.mkdirp(home)
    CALL os.Path.chRwx(home, 448) RETURNING status --0700
  END IF
  VAR path = credentialsPath(home)
  CALL fglpkgutils.writeStringToFile(
      path, manifestStylePretty(util.JSON.stringify(f)) || "\n")
  CALL os.Path.chRwx(path, 384) RETURNING status --0600
END FUNCTION

--pretty print via the shared manifest indenter would create an import
--cycle risk; keep the compact form readable with util.JSON.format
PRIVATE FUNCTION manifestStylePretty(s STRING) RETURNS STRING
  TRY
    RETURN util.JSON.format(s)
  CATCH
    RETURN s
  END TRY
END FUNCTION

#+stores a PAT (+username) for a registry
FUNCTION setPat(home STRING, registryURL STRING, pat STRING, username STRING)
  VAR f = loadCreds(home)
  VAR key = normalizeKey(registryURL)
  LET f.registries[key].pat = pat
  LET f.registries[key].username = username
  LET f.registries[key].savedAt = utcNowRFC3339()
  CALL saveCreds(home, f)
END FUNCTION

#+stores OAuth tokens for a registry (used by the later OAuth phase)
FUNCTION setOAuth(home STRING, registryURL STRING, tokens TOAuthTokens)
  VAR f = loadCreds(home)
  VAR key = normalizeKey(registryURL)
  LET f.registries[key].oauth = tokens
  LET f.registries[key].savedAt = utcNowRFC3339()
  CALL saveCreds(home, f)
END FUNCTION

#+removes stored credentials for a registry;
#+returns TRUE when something was deleted
FUNCTION deleteCreds(home STRING, registryURL STRING) RETURNS BOOLEAN
  VAR f = loadCreds(home)
  VAR key = normalizeKey(registryURL)
  IF NOT f.registries.contains(key) THEN
    RETURN FALSE
  END IF
  CALL f.registries.remove(key)
  CALL saveCreds(home, f)
  RETURN TRUE
END FUNCTION

FUNCTION getCreds(home STRING, registryURL STRING) RETURNS TRegistryCreds
  DEFINE empty TRegistryCreds
  VAR f = loadCreds(home)
  VAR key = normalizeKey(registryURL)
  IF f.registries.contains(key) THEN
    RETURN f.registries[key]
  END IF
  RETURN empty
END FUNCTION

--the OAuth refresher is injected by the CLI (FUNCTION oauth.refresh) so
--this module never imports oauth (mirrors Go's credentials.Refresher)
PUBLIC TYPE TRefresherFunc
    FUNCTION(base STRING, prev TOAuthTokens)
        RETURNS(BOOLEAN, TOAuthTokens, STRING)
DEFINE _refresher TRefresherFunc

FUNCTION setRefresher(f TRefresherFunc)
  LET _refresher = f
END FUNCTION

#+the bearer token used by the FGLPKG_TOKEN env override (trimmed)
FUNCTION consumerEnvBearer() RETURNS STRING
  VAR envTok = fgl_getenv("FGLPKG_TOKEN")
  IF envTok IS NOT NULL AND envTok.trim().getLength() > 0 THEN
    RETURN envTok.trim()
  END IF
  RETURN NULL
END FUNCTION

#+resolves the bearer token for consumer requests, in priority order:
#+FGLPKG_TOKEN env > unexpired OAuth access token > silent refresh
#+(persisting the new tokens) > stored PAT > NULL
FUNCTION activeBearer(home STRING, registryURL STRING) RETURNS STRING
  DEFINE ok BOOLEAN
  DEFINE fresh TOAuthTokens
  DEFINE err STRING
  VAR envTok = consumerEnvBearer()
  IF envTok IS NOT NULL THEN
    RETURN envTok
  END IF
  VAR creds = getCreds(home, registryURL)
  IF creds.oauth.accessToken IS NOT NULL THEN
    IF NOT oauthExpired(creds.oauth) THEN
      RETURN creds.oauth.accessToken
    END IF
    IF creds.oauth.refreshToken IS NOT NULL AND _refresher IS NOT NULL THEN
      CALL _refresher(registryURL, creds.oauth) RETURNING ok, fresh, err
      IF ok THEN
        CALL setOAuth(home, registryURL, fresh)
        RETURN fresh.accessToken
      END IF
      --refresh failed: fall through to the PAT
    END IF
  END IF
  IF creds.pat IS NOT NULL THEN
    RETURN creds.pat
  END IF
  RETURN NULL
END FUNCTION

#+unconditionally refreshes the stored OAuth tokens (the registry 401
#+retry hook); TRUE when a fresh access token was stored
FUNCTION forceRefresh(home STRING, registryURL STRING) RETURNS BOOLEAN
  DEFINE ok BOOLEAN
  DEFINE fresh TOAuthTokens
  DEFINE err STRING
  IF _refresher IS NULL THEN
    RETURN FALSE
  END IF
  VAR creds = getCreds(home, registryURL)
  IF creds.oauth.refreshToken IS NULL THEN
    RETURN FALSE
  END IF
  CALL _refresher(registryURL, creds.oauth) RETURNING ok, fresh, err
  IF NOT ok THEN
    RETURN FALSE
  END IF
  CALL setOAuth(home, registryURL, fresh)
  RETURN TRUE
END FUNCTION

#+publisher-side bearer: env > stored PAT > legacy token > NULL
#+(no OAuth, no refresh — Go parity)
FUNCTION activePublishBearer(home STRING, registryURL STRING) RETURNS STRING
  VAR envTok = consumerEnvBearer()
  IF envTok IS NOT NULL THEN
    RETURN envTok
  END IF
  VAR creds = getCreds(home, registryURL)
  IF creds.pat IS NOT NULL THEN
    RETURN creds.pat
  END IF
  IF creds.token IS NOT NULL THEN
    RETURN creds.token
  END IF
  RETURN NULL
END FUNCTION

#+reports whether the OAuth access token is expired (with skew);
#+a missing expires_at counts as not expired
FUNCTION oauthExpired(tokens TOAuthTokens) RETURNS BOOLEAN
  IF tokens.expiresAt IS NULL THEN
    RETURN FALSE
  END IF
  --both sides are RFC3339 UTC "YYYY-MM-DDTHH:MM:SSZ": compare byte-wise
  VAR nowEpoch =
      util.Datetime.toSecondsSinceEpoch(util.Datetime.getCurrentAsUTC())
  VAR nowPlusSkew =
      util.Datetime.format(
          util.Datetime.fromSecondsSinceEpoch(nowEpoch + OAUTH_SKEW_SECONDS),
          "%Y-%m-%dT%H:%M:%SZ")
  RETURN fglpkgutils.cmpBytes(tokens.expiresAt, nowPlusSkew) <= 0
END FUNCTION

FUNCTION utcNowRFC3339() RETURNS STRING
  RETURN util.Datetime.format(
      util.Datetime.getCurrentAsUTC(), "%Y-%m-%dT%H:%M:%SZ")
END FUNCTION
