#+ tests for oauth.4gl: PKCE, base64url, authorize URL, query parsing
OPTIONS SHORT CIRCUIT
IMPORT FGL testutils
IMPORT FGL fglpkg.fglpkgutils
IMPORT FGL fglpkg.oauth
&include "testassert.inc"

MAIN
  CALL testPKCE()
  CALL testBase64URL()
  CALL testAuthURL()
  CALL testQueryDict()
  CALL testBrowserCmd()
  TSUMMARY()
END MAIN

FUNCTION testPKCE()
  --RFC 7636 appendix B test vector
  VAR verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
  TEQ(oauth.challengeFor(verifier), "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM")
  --generated verifier: 43 base64url chars, no padding/+//
  VAR v = oauth.generateVerifier()
  TEQ(v.getLength(), 43)
  TOK(NOT fglpkgutils.contains(v, "="))
  TOK(NOT fglpkgutils.contains(v, "+"))
  TOK(NOT fglpkgutils.contains(v, "/"))
  --two verifiers differ
  TOK(v != oauth.generateVerifier())
  --state: 16 bytes -> 22 base64url chars
  TEQ(oauth.generateState().getLength(), 22)
END FUNCTION

FUNCTION testBase64URL()
  TEQ(oauth.base64ToURL("a+b/c=="), "a-b_c")
  TEQ(oauth.base64ToURL("plain"), "plain")
END FUNCTION

FUNCTION testAuthURL()
  VAR u = oauth.buildAuthURL("https://reg.example.com", "cid 1",
      "http://127.0.0.1:9101/callback", "registry:read", "st", "ch")
  TOK(fglpkgutils.startsWith(u, "https://reg.example.com/authorize?response_type=code"))
  TOK(fglpkgutils.contains(u, "client_id=cid+1"))
  TOK(fglpkgutils.contains(u, "redirect_uri=http%3A%2F%2F127.0.0.1%3A9101%2Fcallback"))
  TOK(fglpkgutils.contains(u, "scope=registry%3Aread"))
  TOK(fglpkgutils.contains(u, "state=st"))
  TOK(fglpkgutils.contains(u, "code_challenge=ch"))
  TOK(fglpkgutils.contains(u, "code_challenge_method=S256"))
END FUNCTION

FUNCTION testQueryDict()
  VAR d = oauth.getQueryDict("code=abc123&state=xy%2Fz&flag&desc=two+words")
  TEQ(d["code"], "abc123")
  TEQ(d["state"], "xy/z")
  TEQ(d["desc"], "two words")
  TOK(d.contains("flag"))
  --empty query
  VAR e = oauth.getQueryDict("")
  TEQ(e.getLength(), 0)
END FUNCTION

FUNCTION testBrowserCmd()
  VAR cmd = oauth.getOpenBrowserCmd("https://x.example/authorize?a=1&b=2")
  IF fglpkgutils.isMac() THEN
    TEQ(cmd, "open 'https://x.example/authorize?a=1&b=2'")
  ELSE
    TOK(cmd.getLength() > 0)
  END IF
END FUNCTION
