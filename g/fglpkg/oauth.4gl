#+ browser OAuth (authorization code + PKCE) against the registry
#+ port of internal/oauth (flow.go, server.go, pkce.go, browser.go,
#+ tokens.go); the loopback callback server follows the gwa
#+ gwahttp/miniws idioms (base.Channel server socket + util.Channels)
OPTIONS SHORT CIRCUIT
PACKAGE fglpkg
IMPORT com
IMPORT util
IMPORT security
IMPORT FGL fglpkg.fglpkgutils
IMPORT FGL fglpkg.credentials
IMPORT FGL fglpkg.registry
&include "myassert.inc"

PUBLIC CONSTANT DEFAULT_SCOPE = "registry:read"
PRIVATE CONSTANT CLIENT_NAME = "fglpkg CLI"
PRIVATE CONSTANT CLIENT_URI = "https://github.com/4js-mikefolcher/fglpkg"

--loopback port scan range (Go uses an ephemeral port; a scanned range is
--an accepted deviation, gwautils.findFreeServerChannel style)
PRIVATE CONSTANT PORT_SCAN_FROM = 9101
PRIVATE CONSTANT PORT_SCAN_TO = 9300

#+injectable browser opener so tests can substitute a curl-based fake
PUBLIC TYPE TBrowserOpener FUNCTION(url STRING) RETURNS BOOLEAN
DEFINE _browserOpener TBrowserOpener

FUNCTION setBrowserOpener(f TBrowserOpener)
  LET _browserOpener = f
END FUNCTION

--─── PKCE (RFC 7636) ────────────────────────────────────────────────────────

#+converts a standard base64 string to base64url without padding
FUNCTION base64ToURL(b64 STRING) RETURNS STRING
  LET b64 = fglpkgutils.replace(b64, "+", "-")
  LET b64 = fglpkgutils.replace(b64, "/", "_")
  LET b64 = fglpkgutils.replace(b64, "=", "")
  RETURN b64
END FUNCTION

#+a PKCE code verifier: 32 random bytes, base64url (43 chars)
FUNCTION generateVerifier() RETURNS STRING
  RETURN base64ToURL(security.RandomGenerator.CreateRandomString(32))
END FUNCTION

#+the S256 code challenge: base64url(SHA256(ASCII(verifier)))
FUNCTION challengeFor(verifier STRING) RETURNS STRING
  DEFINE dgst security.Digest
  LET dgst = security.Digest.CreateDigest("SHA256")
  CALL dgst.AddStringData(verifier)
  RETURN base64ToURL(dgst.DoBase64Digest())
END FUNCTION

#+a CSRF state value: 16 random bytes, base64url
FUNCTION generateState() RETURNS STRING
  RETURN base64ToURL(security.RandomGenerator.CreateRandomString(16))
END FUNCTION

--─── browser ────────────────────────────────────────────────────────────────

#+opens a URL in the default browser (gwabrowser-style commands),
#+fire and forget
FUNCTION openInBrowser(url STRING) RETURNS BOOLEAN
  IF _browserOpener IS NOT NULL THEN
    RETURN _browserOpener(url)
  END IF
  VAR cmd = getOpenBrowserCmd(url)
  TRY
    RUN cmd WITHOUT WAITING
  CATCH
    RETURN FALSE
  END TRY
  RETURN TRUE
END FUNCTION

FUNCTION getOpenBrowserCmd(url STRING) RETURNS STRING
  --FGLPKG_BROWSER overrides the browser command (testing / headless);
  --the URL is appended single-quoted
  VAR override = fgl_getenv("FGLPKG_BROWSER")
  IF override IS NOT NULL AND override.trim().getLength() > 0 THEN
    RETURN SFMT("%1 '%2' >/dev/null 2>&1 &", override.trim(), url)
  END IF
  CASE
    WHEN fglpkgutils.isWin()
      RETURN SFMT("start %1", fglpkgutils.winQuoteUrl(url))
    WHEN fglpkgutils.isMac()
      RETURN SFMT("open '%1'", url)
    OTHERWISE
      RETURN SFMT("xdg-open %1 >/dev/null 2>&1", fglpkgutils.quoteUrl(url))
  END CASE
END FUNCTION

--─── the login flow ─────────────────────────────────────────────────────────

#+runs the complete browser login: loopback server, dynamic client
#+registration, PKCE, browser, callback, token exchange
FUNCTION runLogin(base STRING)
    RETURNS(BOOLEAN, credentials.TOAuthTokens, STRING)
  DEFINE tokens, empty credentials.TOAuthTokens
  DEFINE srv base.Channel
  DEFINE port INT
  DEFINE ok BOOLEAN
  DEFINE clientId, clientSecret, err STRING
  DEFINE code, gotState STRING

  WHILE base.getLength() > 1 AND fglpkgutils.endsWith(base, "/")
    LET base = base.subString(1, base.getLength() - 1)
  END WHILE

  CALL bindLoopback() RETURNING srv, port
  IF srv IS NULL THEN
    RETURN FALSE, empty,
        SFMT("cannot bind a loopback port in the range %1-%2",
            PORT_SCAN_FROM, PORT_SCAN_TO)
  END IF
  VAR redirectURI = SFMT("http://127.0.0.1:%1/callback", port)

  CALL registerClient(base, redirectURI) RETURNING ok, clientId, clientSecret, err
  IF NOT ok THEN
    CALL srv.close()
    RETURN FALSE, empty, err
  END IF

  VAR verifier = generateVerifier()
  VAR state = generateState()
  VAR challenge = challengeFor(verifier)
  VAR authURL = buildAuthURL(base, clientId, redirectURI, DEFAULT_SCOPE,
      state, challenge)

  IF NOT openInBrowser(authURL) THEN
    CALL srv.close()
    RETURN FALSE, empty, "open browser: cannot launch a browser"
  END IF

  CALL awaitCallback(srv) RETURNING ok, code, gotState, err
  CALL srv.close()
  IF NOT ok THEN
    RETURN FALSE, empty, err
  END IF
  IF gotState != state THEN
    RETURN FALSE, empty, "OAuth state mismatch — possible CSRF; aborting"
  END IF

  CALL exchangeCode(base, code, redirectURI, clientId, clientSecret, verifier)
      RETURNING ok, tokens, err
  IF NOT ok THEN
    RETURN FALSE, empty, err
  END IF
  RETURN TRUE, tokens, NULL
END FUNCTION

#+refreshes an access token; the refresh token may rotate — persist
#+whatever comes back (signature matches credentials.TRefresherFunc)
FUNCTION refresh(base STRING, prev credentials.TOAuthTokens)
    RETURNS(BOOLEAN, credentials.TOAuthTokens, STRING)
  DEFINE tokens, empty credentials.TOAuthTokens
  DEFINE ok BOOLEAN
  DEFINE err STRING
  IF prev.refreshToken IS NULL THEN
    RETURN FALSE, empty, "no refresh_token on record — log in again"
  END IF
  WHILE base.getLength() > 1 AND fglpkgutils.endsWith(base, "/")
    LET base = base.subString(1, base.getLength() - 1)
  END WHILE
  VAR form = SFMT("grant_type=refresh_token&refresh_token=%1&client_id=%2",
      formValue(prev.refreshToken),
      formValue(NVL(prev.clientId, "")))
  IF prev.clientSecret IS NOT NULL THEN
    LET form = SFMT("%1&client_secret=%2", form, formValue(prev.clientSecret))
  END IF
  CALL postTokenForm(base, form, prev.scope, prev.clientId, prev.clientSecret)
      RETURNING ok, tokens, err
  IF NOT ok THEN
    RETURN FALSE, empty, SFMT("refresh: %1", err)
  END IF
  RETURN TRUE, tokens, NULL
END FUNCTION

--─── dynamic client registration (RFC 7591) ─────────────────────────────────

PRIVATE FUNCTION registerClient(base STRING, redirectURI STRING)
    RETURNS(BOOLEAN, STRING, STRING, STRING)
  DEFINE req com.HttpRequest
  DEFINE resp com.HttpResponse
  DEFINE code INT
  DEFINE body STRING
  DEFINE parsed RECORD
    clientId STRING ATTRIBUTES(json_name = "client_id"),
    clientSecret STRING ATTRIBUTES(json_name = "client_secret")
  END RECORD

  VAR payload = util.JSONObject.create()
  CALL payload.put("client_name", CLIENT_NAME)
  CALL payload.put("client_uri", CLIENT_URI)
  VAR uris = util.JSONArray.create()
  CALL uris.put(1, redirectURI)
  CALL payload.put("redirect_uris", uris)
  CALL payload.put("token_endpoint_auth_method", "none")
  VAR grants = util.JSONArray.create()
  CALL grants.put(1, "authorization_code")
  CALL grants.put(2, "refresh_token")
  CALL payload.put("grant_types", grants)
  VAR rtypes = util.JSONArray.create()
  CALL rtypes.put(1, "code")
  CALL payload.put("response_types", rtypes)
  CALL payload.put("scope", DEFAULT_SCOPE)

  TRY
    LET req = com.HttpRequest.Create(SFMT("%1/register", base))
    CALL req.setMethod("POST")
    CALL req.setHeader("Content-Type", "application/json")
    CALL req.setHeader("Accept", "application/json")
    CALL req.doTextRequest(payload.toString())
    LET resp = req.getResponse()
    LET code = resp.getStatusCode()
    LET body = resp.getTextResponse()
  CATCH
    RETURN FALSE, NULL, NULL,
        SFMT("dynamic client registration: %1", err_get(status))
  END TRY
  IF code < 200 OR code >= 300 THEN
    RETURN FALSE, NULL, NULL,
        SFMT("dynamic client registration: HTTP %1 — %2",
            code, truncateBody(body))
  END IF
  TRY
    CALL util.JSON.parse(body, parsed)
  CATCH
    RETURN FALSE, NULL, NULL, "dynamic client registration: invalid JSON"
  END TRY
  IF parsed.clientId IS NULL THEN
    RETURN FALSE, NULL, NULL,
        "dynamic client registration: response missing client_id"
  END IF
  RETURN TRUE, parsed.clientId, parsed.clientSecret, NULL
END FUNCTION

FUNCTION buildAuthURL(
    base STRING, clientId STRING, redirectURI STRING, scope STRING,
    state STRING, challenge STRING)
    RETURNS STRING
  RETURN SFMT("%1/authorize?response_type=code&client_id=%2&redirect_uri=%3&scope=%4&state=%5&code_challenge=%6&code_challenge_method=S256",
      base,
      registry.urlQueryEscape(clientId),
      registry.urlQueryEscape(redirectURI),
      registry.urlQueryEscape(scope),
      registry.urlQueryEscape(state),
      registry.urlQueryEscape(challenge))
END FUNCTION

--─── token endpoint ─────────────────────────────────────────────────────────

PRIVATE FUNCTION exchangeCode(
    base STRING, code STRING, redirectURI STRING, clientId STRING,
    clientSecret STRING, verifier STRING)
    RETURNS(BOOLEAN, credentials.TOAuthTokens, STRING)
  DEFINE tokens credentials.TOAuthTokens
  DEFINE ok BOOLEAN
  DEFINE err STRING
  VAR form = SFMT("grant_type=authorization_code&code=%1&redirect_uri=%2&client_id=%3&code_verifier=%4",
      formValue(code),
      formValue(redirectURI),
      formValue(clientId),
      formValue(verifier))
  IF clientSecret IS NOT NULL THEN
    LET form = SFMT("%1&client_secret=%2", form, formValue(clientSecret))
  END IF
  CALL postTokenForm(base, form, DEFAULT_SCOPE, clientId, clientSecret)
      RETURNING ok, tokens, err
  RETURN ok, tokens, err
END FUNCTION

PRIVATE FUNCTION postTokenForm(
    base STRING, form STRING, fallbackScope STRING, clientId STRING,
    clientSecret STRING)
    RETURNS(BOOLEAN, credentials.TOAuthTokens, STRING)
  DEFINE tokens, empty credentials.TOAuthTokens
  DEFINE req com.HttpRequest
  DEFINE resp com.HttpResponse
  DEFINE code INT
  DEFINE body STRING
  DEFINE parsed RECORD
    accessToken STRING ATTRIBUTES(json_name = "access_token"),
    refreshToken STRING ATTRIBUTES(json_name = "refresh_token"),
    expiresIn INT ATTRIBUTES(json_name = "expires_in"),
    tokenType STRING ATTRIBUTES(json_name = "token_type"),
    scope STRING
  END RECORD
  TRY
    LET req = com.HttpRequest.Create(SFMT("%1/token", base))
    CALL req.setMethod("POST")
    CALL req.setHeader("Accept", "application/json")
    CALL req.doFormEncodedRequest(form, TRUE)
    LET resp = req.getResponse()
    LET code = resp.getStatusCode()
    LET body = resp.getTextResponse()
  CATCH
    RETURN FALSE, empty, err_get(status)
  END TRY
  IF code < 200 OR code >= 300 THEN
    RETURN FALSE, empty, SFMT("HTTP %1 — %2", code, truncateBody(body))
  END IF
  TRY
    CALL util.JSON.parse(body, parsed)
  CATCH
    RETURN FALSE, empty, "invalid token JSON"
  END TRY
  IF parsed.accessToken IS NULL THEN
    RETURN FALSE, empty, "token response missing access_token"
  END IF
  LET tokens.accessToken = parsed.accessToken
  LET tokens.refreshToken = parsed.refreshToken
  IF parsed.expiresIn IS NULL OR parsed.expiresIn <= 0 THEN
    LET parsed.expiresIn = 3600
  END IF
  VAR nowEpoch =
      util.Datetime.toSecondsSinceEpoch(util.Datetime.getCurrentAsUTC())
  LET tokens.expiresAt =
      util.Datetime.format(
          util.Datetime.fromSecondsSinceEpoch(nowEpoch + parsed.expiresIn),
          "%Y-%m-%dT%H:%M:%SZ")
  LET tokens.scope = NVL(parsed.scope, fallbackScope)
  LET tokens.clientId = clientId
  LET tokens.clientSecret = clientSecret
  RETURN TRUE, tokens, NULL
END FUNCTION

#+a raw form value for doFormEncodedRequest, which URL-encodes values
#+itself; literal separators must be doubled (&& / ==) per the API rule
PRIVATE FUNCTION formValue(v STRING) RETURNS STRING
  LET v = fglpkgutils.replace(NVL(v, ""), "&", "&&")
  LET v = fglpkgutils.replace(v, "=", "==")
  RETURN v
END FUNCTION

PRIVATE FUNCTION truncateBody(body STRING) RETURNS STRING
  IF body.getLength() > 240 THEN
    RETURN body.subString(1, 240) || "…"
  END IF
  RETURN NVL(body, "")
END FUNCTION

--─── loopback callback server ───────────────────────────────────────────────

#+binds 127.0.0.1 on a free port in the scan range;
#+returns (NULL, -1) when none is available
FUNCTION bindLoopback() RETURNS(base.Channel, INT)
  DEFINE srv base.Channel
  DEFINE port INT
  LET srv = base.Channel.create()
  FOR port = PORT_SCAN_FROM TO PORT_SCAN_TO
    TRY
      CALL srv.openServerSocket("127.0.0.1", port, "u")
      RETURN srv, port
    CATCH
      CALL fglpkgutils.log(SFMT("can't bind port %1", port))
    END TRY
  END FOR
  RETURN NULL, -1
END FUNCTION

#+waits for the single GET /callback request, answers it with the Go
#+success/error HTML and returns (ok, code, state, err); blocks until
#+the browser redirect arrives (Ctrl-C aborts the program)
FUNCTION awaitCallback(srv base.Channel)
    RETURNS(BOOLEAN, STRING, STRING, STRING)
  DEFINE conn base.Channel
  DEFINE chans DYNAMIC ARRAY OF base.Channel
  DEFINE line, query STRING

  LET chans[1] = srv
  CALL util.Channels.select(chans) RETURNING status
  LET conn = util.Channels.accept(srv)
  IF conn IS NULL THEN
    RETURN FALSE, NULL, NULL, "callback connection failed"
  END IF

  --request line: "GET /callback?... HTTP/1.1"
  VAR reqline = readHTTPLine(conn)
  WHILE (line := readHTTPLine(conn)) IS NOT NULL
    IF line.getLength() == 0 THEN
      EXIT WHILE
    END IF
  END WHILE

  VAR parts = fglpkgutils.splitFields(NVL(reqline, ""))
  VAR path = IIF(parts.getLength() >= 2, parts[2], "")
  VAR qIdx = path.getIndexOf("?", 1)
  IF qIdx > 0 THEN
    LET query = path.subString(qIdx + 1, path.getLength())
    LET path = path.subString(1, qIdx - 1)
  END IF

  VAR params = getQueryDict(query)
  VAR oauthErr = dictGet(params, "error")
  VAR errDesc = dictGet(params, "error_description")
  VAR code = dictGet(params, "code")
  VAR state = dictGet(params, "state")

  CASE
    WHEN oauthErr IS NOT NULL
      CALL writeCallbackResponse(conn, FALSE, oauthErr, errDesc)
      CALL conn.close()
      RETURN FALSE, NULL, NULL,
          SFMT("authorisation failed: %1 — %2", oauthErr, NVL(errDesc, ""))
    WHEN code IS NULL
      CALL writeCallbackResponse(conn, FALSE, "missing_code",
          "Authorisation server did not return a code.")
      CALL conn.close()
      RETURN FALSE, NULL, NULL, "authorisation server returned no code"
    OTHERWISE
      CALL writeCallbackResponse(conn, TRUE, NULL, NULL)
      CALL conn.close()
      RETURN TRUE, code, state, NULL
  END CASE
END FUNCTION

#+reads one CRLF-terminated line ("" for the blank header terminator)
PRIVATE FUNCTION readHTTPLine(conn base.Channel) RETURNS STRING
  DEFINE line STRING
  TRY
    LET line = conn.readLine()
  CATCH
    RETURN NULL
  END TRY
  IF line IS NOT NULL
      AND line.getLength() > 0
      AND line.getCharAt(line.getLength()) == "\r" THEN
    LET line = line.subString(1, line.getLength() - 1)
  END IF
  RETURN line
END FUNCTION

PRIVATE FUNCTION writeCallbackResponse(
    conn base.Channel, success BOOLEAN, errCode STRING, errDesc STRING)
  DEFINE body, statusLine STRING
  IF success THEN
    LET statusLine = "HTTP/1.1 200 OK"
    LET body = '<!doctype html><meta charset="utf-8"><title>fglpkg signed in</title>\n'
        || "<style>body{font-family:system-ui,sans-serif;max-width:32rem;margin:4rem auto;color:#222}</style>\n"
        || "<h1>You're signed in.</h1>\n"
        || "<p>You can close this tab and return to your terminal.</p>\n"
  ELSE
    LET statusLine = "HTTP/1.1 400 Bad Request"
    LET body = '<!doctype html><meta charset="utf-8"><title>fglpkg sign-in failed</title>\n'
        || "<style>body{font-family:system-ui,sans-serif;max-width:32rem;margin:4rem auto;color:#222}</style>\n"
        || "<h1>Sign-in failed.</h1>\n"
        || SFMT("<p><code>%1</code>: %2</p>\n", NVL(errCode, ""), NVL(errDesc, ""))
        || "<p>You can close this tab and re-run <code>fglpkg login</code>.</p>\n"
  END IF
  TRY
    CALL conn.writeNoNL(statusLine || "\r\n")
    CALL conn.writeNoNL("Content-Type: text/html; charset=utf-8\r\n")
    CALL conn.writeNoNL(SFMT("Content-Length: %1\r\n", body.getLength()))
    CALL conn.writeNoNL("Connection: close\r\n\r\n")
    CALL conn.writeNoNL(body)
    CALL conn.readOctets(length: 0) RETURNING status --flush (gwautils trick)
  CATCH
    CALL fglpkgutils.log("callback response write failed")
  END TRY
END FUNCTION

--─── query string parsing ───────────────────────────────────────────────────

#+splits "a=1&b=2" into a dictionary, percent-decoding each value
FUNCTION getQueryDict(query STRING) RETURNS fglpkgutils.TStringDict
  DEFINE d fglpkgutils.TStringDict
  DEFINE i INT
  IF query IS NULL OR query.getLength() == 0 THEN
    RETURN d
  END IF
  VAR pairs = fglpkgutils.splitOnChar(query, "&")
  FOR i = 1 TO pairs.getLength()
    IF pairs[i].getLength() == 0 THEN
      CONTINUE FOR
    END IF
    VAR eq = pairs[i].getIndexOf("=", 1)
    IF eq > 0 THEN
      LET d[pairs[i].subString(1, eq - 1)] =
          urlDecodeValue(pairs[i].subString(eq + 1, pairs[i].getLength()))
    ELSE
      LET d[pairs[i]] = ""
    END IF
  END FOR
  RETURN d
END FUNCTION

PRIVATE FUNCTION dictGet(d fglpkgutils.TStringDict, key STRING)
    RETURNS STRING
  IF d.contains(key) AND d[key].getLength() > 0 THEN
    RETURN d[key]
  END IF
  RETURN NULL
END FUNCTION

#+percent-decodes a query value ('+' becomes a space)
FUNCTION urlDecodeValue(v STRING) RETURNS STRING
  IF v IS NULL THEN
    RETURN v
  END IF
  LET v = fglpkgutils.replace(v, "+", " ")
  IF v.getIndexOf("%", 1) > 0 THEN
    TRY
      RETURN util.Strings.urlDecode(v)
    CATCH
      RETURN v
    END TRY
  END IF
  RETURN v
END FUNCTION
