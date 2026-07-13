#!/usr/bin/env python3
"""Mock Genero package registry for fglpkg E2E smoke tests.

Implements just enough of the /registry/... protocol plus the OAuth
endpoints (/register, /authorize, /token) to exercise the fglpkg
publish/login/whoami/outdated flows headlessly.

Usage: python3 mock_registry.py <port> <statedir>
State (uploaded artifacts, seen requests) is written under <statedir>.
"""
import hashlib
import json
import os
import sys
import time
import urllib.parse
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

PORT = int(sys.argv[1]) if len(sys.argv) > 1 else 18800
STATEDIR = sys.argv[2] if len(sys.argv) > 2 else "/tmp/fglpkg-mock"
VALID_TOKENS = {"gpr_e2e_pat", "at_oauth_1", "at_oauth_2"}
# opt-in artificial per-artifact-download delay, for proving concurrent
# downloads actually overlap (see FGLPKG_INSTALL_CONCURRENCY tests);
# 0 by default so it never affects the normal E2E suite
ARTIFACT_DELAY = float(os.environ.get("FGLPKG_MOCK_DOWNLOAD_DELAY", "0"))

os.makedirs(STATEDIR, exist_ok=True)

# in-memory registry state: slug -> {description, visibility, versions:
#   {version: {meta, artifacts: {variant: {filename, sha256, size}}}}}
packages = {}


def bearer_of(handler):
    auth = handler.headers.get("Authorization", "")
    return auth[7:] if auth.startswith("Bearer ") else ""


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *a):
        pass

    def _send(self, code, body=b"", ctype="application/json", extra=None):
        if isinstance(body, (dict, list)):
            body = json.dumps(body).encode()
        elif isinstance(body, str):
            body = body.encode()
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(body)))
        for k, v in (extra or {}).items():
            self.send_header(k, v)
        self.end_headers()
        self.wfile.write(body)

    def _body(self):
        n = int(self.headers.get("Content-Length", 0))
        return self.rfile.read(n) if n else b""

    def _detail(self, slug):
        pkg = packages[slug]
        versions = []
        for ver, vd in pkg["versions"].items():
            arts = [
                {
                    "variant": variant,
                    "filename": a["filename"],
                    "size_bytes": a["size"],
                    "sha256": a["sha256"],
                    "download_url": f"/registry/packages/{slug}/versions/{ver}/artifacts/{variant}",
                }
                for variant, a in vd["artifacts"].items()
            ]
            entry = {"version": ver, "status": "published", "artifacts": arts}
            entry.update({k: v for k, v in vd["meta"].items()
                          if k in ("repository", "author", "license", "genero",
                                   "dependencies", "readme", "userguide")})
            versions.append(entry)
        return {
            "slug": slug,
            "name": pkg.get("name", slug),
            "description": pkg.get("description", ""),
            "visibility": pkg.get("visibility", "public"),
            "owner": {"partner_id": "p1", "name": "MockPartner"},
            "status": "active",
            "latest_version": max(pkg["versions"]) if pkg["versions"] else "",
            "downloads": 0,
            "versions": versions,
        }

    # ── GET ──────────────────────────────────────────────────────────────
    def do_GET(self):
        url = urllib.parse.urlparse(self.path)
        parts = [p for p in url.path.split("/") if p]
        qs = urllib.parse.parse_qs(url.query)

        if url.path == "/registry/whoami":
            tok = bearer_of(self)
            if tok not in VALID_TOKENS:
                return self._send(401, {"error": "invalid token"})
            return self._send(200, {
                "user": {"id": "u-1", "email": "leo@example.com", "name": "Leo"},
                "partner": {"id": "p1", "name": "MockPartner"},
                "scopes": ["registry:read", "registry:publish"],
            })

        if url.path == "/authorize":
            # immediately "approve": redirect back with a fixed code
            redirect = qs["redirect_uri"][0]
            state = qs.get("state", [""])[0]
            with open(os.path.join(STATEDIR, "authorize.json"), "w") as f:
                json.dump({k: v[0] for k, v in qs.items()}, f)
            loc = f"{redirect}?code=mock-code-42&state={urllib.parse.quote(state)}"
            return self._send(302, b"", extra={"Location": loc})

        if url.path == "/registry/packages" or url.path == "/registry/packages/":
            listed = [{
                "slug": s,
                "name": p.get("name", s),
                "description": p.get("description", ""),
                "owner": {"partner_id": "p1", "name": "MockPartner"},
                "latest_version": max(p["versions"]) if p["versions"] else "",
            } for s, p in sorted(packages.items())]
            return self._send(200, {"packages": listed, "page": 1,
                                    "pageSize": 20, "total": len(listed)})

        if len(parts) == 3 and parts[:2] == ["registry", "packages"]:
            slug = parts[2]
            if slug not in packages:
                return self._send(404, {"error": "not found"})
            return self._send(200, self._detail(slug))

        # artifact download
        if (len(parts) == 7 and parts[:2] == ["registry", "packages"]
                and parts[3] == "versions" and parts[5] == "artifacts"):
            slug, ver, variant = parts[2], parts[4], parts[6]
            art = packages.get(slug, {}).get("versions", {}).get(ver, {}) \
                .get("artifacts", {}).get(variant)
            if not art:
                return self._send(404, {"error": "no artifact"})
            if ARTIFACT_DELAY:
                time.sleep(ARTIFACT_DELAY)
            with open(art["path"], "rb") as f:
                return self._send(200, f.read(), ctype="application/zip")

        return self._send(404, {"error": "not found"})

    # ── POST ─────────────────────────────────────────────────────────────
    def do_POST(self):
        url = urllib.parse.urlparse(self.path)
        parts = [p for p in url.path.split("/") if p]
        body = self._body()

        if url.path == "/register":
            payload = json.loads(body)
            with open(os.path.join(STATEDIR, "register.json"), "w") as f:
                json.dump(payload, f)
            return self._send(201, {"client_id": "mock-client-1"})

        if url.path == "/token":
            form = urllib.parse.parse_qs(body.decode())
            with open(os.path.join(STATEDIR, "token.json"), "w") as f:
                json.dump({k: v[0] for k, v in form.items()}, f)
            grant = form.get("grant_type", [""])[0]
            if grant == "authorization_code":
                if form.get("code", [""])[0] != "mock-code-42":
                    return self._send(400, {"error": "bad code"})
                if not form.get("code_verifier", [""])[0]:
                    return self._send(400, {"error": "missing verifier"})
                return self._send(200, {"access_token": "at_oauth_1",
                                        "refresh_token": "rt_oauth_1",
                                        "expires_in": 3600,
                                        "token_type": "Bearer",
                                        "scope": "registry:read"})
            if grant == "refresh_token":
                if form.get("refresh_token", [""])[0] != "rt_oauth_1":
                    return self._send(400, {"error": "bad refresh token"})
                return self._send(200, {"access_token": "at_oauth_2",
                                        "refresh_token": "rt_oauth_2",
                                        "expires_in": 3600})
            return self._send(400, {"error": "bad grant"})

        if url.path == "/v1/query":
            # OSV.dev stand-in: canned vulns per purl from <statedir>/osv.json
            # ({purl: {vulns: [...]}}); unknown purls get the empty object,
            # matching the real service. No auth required.
            payload = json.loads(body)
            purl = payload.get("package", {}).get("purl", "")
            canned = {}
            osv_file = os.path.join(STATEDIR, "osv.json")
            if os.path.exists(osv_file):
                with open(osv_file) as f:
                    canned = json.load(f)
            return self._send(200, canned.get(purl, {}))

        if bearer_of(self) not in VALID_TOKENS:
            return self._send(401, {"error": "unauthorised"})

        if url.path == "/registry/packages":
            payload = json.loads(body)
            slug = payload["slug"]
            if slug in packages:
                return self._send(409, {"error": "exists"})
            packages[slug] = {"name": payload.get("name", slug),
                              "description": payload.get("description", ""),
                              "visibility": payload.get("visibility", "public"),
                              "versions": {}}
            return self._send(201, {"ok": True})

        if (len(parts) == 4 and parts[3] == "versions"):
            slug = parts[2]
            payload = json.loads(body)
            ver = payload["version"]
            if slug not in packages:
                return self._send(404, {"error": "no package"})
            if ver in packages[slug]["versions"]:
                return self._send(409, {"error": "version exists"})
            packages[slug]["versions"][ver] = {"meta": payload, "artifacts": {}}
            with open(os.path.join(STATEDIR, "version-meta.json"), "w") as f:
                json.dump(payload, f)
            return self._send(201, {"ok": True})

        if (len(parts) == 6 and parts[5] == "submit"):
            slug, ver = parts[2], parts[4]
            with open(os.path.join(STATEDIR, "submitted.json"), "w") as f:
                json.dump({"slug": slug, "version": ver}, f)
            return self._send(200, {"status": "pending"})

        return self._send(404, {"error": "not found"})

    # ── PUT (artifact upload) ────────────────────────────────────────────
    def do_PUT(self):
        url = urllib.parse.urlparse(self.path)
        parts = [p for p in url.path.split("/") if p]
        qs = urllib.parse.parse_qs(url.query)
        if bearer_of(self) not in VALID_TOKENS:
            return self._send(401, {"error": "unauthorised"})
        if (len(parts) == 7 and parts[3] == "versions" and parts[5] == "artifacts"):
            slug, ver, variant = parts[2], parts[4], parts[6]
            data = self._body()
            path = os.path.join(STATEDIR, f"{slug}-{ver}-{variant}.zip")
            with open(path, "wb") as f:
                f.write(data)
            packages[slug]["versions"][ver]["artifacts"][variant] = {
                "filename": qs.get("filename", ["a.zip"])[0],
                "sha256": hashlib.sha256(data).hexdigest(),
                "size": len(data),
                "path": path,
            }
            return self._send(201, {"ok": True})
        return self._send(404, {"error": "not found"})


if __name__ == "__main__":
    # threaded: a delayed artifact download must not block other
    # concurrent requests server-side, or a concurrency test against
    # this mock would be measuring the mock, not the client
    ThreadingHTTPServer(("127.0.0.1", PORT), Handler).serve_forever()
