# Manually testing package signatures (Layer 1) from the command line

This is a runbook for verifying that the Genero Intelligence (GI) registry correctly
**signs** the packages it serves — "method A" / **Layer 1** of
[../specs/package-signing.md](../specs/package-signing.md): the registry signs every artifact with
**Ed25519** over an **RFC 8785 (JCS)** canonical payload and publishes its working public key in a
root-signed manifest at `/.well-known/keys.json`.

> **Why this is manual today.** Layer 1 has shipped **GI-side**, but the fglpkg CLI has **no
> `internal/signing/` package yet** (spec milestone M2). So `fglpkg install` verifies only the
> SHA256 — it does **not** yet check the Ed25519 signature. This runbook does, by hand, exactly what
> `internal/signing/` will do: reconstruct the signed payload and verify it. It doubles as the
> reference / golden check for when that Go code lands.
>
> Everything here uses **standard tools only** (`curl` + `jq` + `openssl`, or one zero-dependency
> Node script) — no fglpkg or GI repo checkout required, so it can be handed to anyone.

Layer 2 (Sigstore provenance) is a different test — it needs a CI OIDC environment to mint an
attestation, so it can't be exercised purely from a laptop command line. Not covered here.

---

## What you need from the GI service

| # | Item | Where it comes from | Needed for |
|---|------|---------------------|------------|
| A | **Registry base URL** with signing live | **Test worker:** `https://genero-intelligence-test.michael-folcher.workers.dev`. Prod (`service.generointelligence.ai`) has **no manifest yet** → `keys.json` 404s. | everything |
| B | **Signed keys manifest** | `GET {BASE}/registry/.well-known/keys.json` — public, no auth. Yields working key(s): `keyid`, `pub` (base64-raw 32-byte), `validFrom/validTo`, plus the root-sig block. | artifact + manifest checks |
| C | **Pinned ROOT public key** (`root-test-1`) | The **only** value not in any public endpoint — `root.key.json` is git-ignored. Get its `pub_b64raw` from whoever ran `scripts/gen-signing-key.mjs init-root` (offline). | *optional* manifest-authenticity check |
| D | **Artifact read-model** | `GET {BASE}/registry/packages/<slug>/versions/<version>` → per variant: `filename, size_bytes, sha256, uploaded_at, uploader, signature{keyid,alg,sig}`. | artifact check |
| E | **A signed artifact to test** | See Step 0 — as of this writing **none exist** (every artifact is `signature: null`). | a positive test |

### The canonical payload (what actually gets signed)

```
JCS( { "artifact": { "name","version","variant","sha256","size","uploaded_at","uploader" } } )
```

- `name` = package **slug**; `variant` from the artifact; `size` = `size_bytes` as an **integer**.
- **Gotcha:** `uploaded_at` is the **verbatim stored string** the registry returns
  (SQLite `YYYY-MM-DD HH:MM:SS`, e.g. `"2026-06-06 23:07:09"`) — **not** a reformatted ISO string.
  Reconstruct it exactly as returned or the signature won't verify.
- The signing input is the RFC-8785 bytes themselves (Ed25519 hashes internally — no pre-hash).
- The manifest signature covers `JCS( { "issuedAt", "keys" } )` (the manifest minus its `sig` block).

---

## Step 0 — make a signed artifact exist (one-time, GI-side)

As of this writing every artifact on the test registry is `signature: null` — all were published
before the working key went active (manifest `issuedAt` = 2026-07-06). So first:

1. Point the CLI at the test registry and publish (or re-publish) any package:
   ```bash
   export FGLPKG_REGISTRY=https://genero-intelligence-test.michael-folcher.workers.dev
   fglpkg publish            # from a package dir, authenticated
   ```
2. Re-fetch the version and confirm `signature` is now **non-null**:
   ```bash
   curl -fsS "$FGLPKG_REGISTRY/registry/packages/<slug>/versions/<v>" | jq '.artifacts[].signature'
   ```
   - **Non-null** → the working key is active; proceed to Step 1.
   - **Still `null`** → the working-key *secret* isn't provisioned on the worker (only the manifest
     was ingested). `signArtifact()` returns null → rows store unsigned. Fix by running the two
     `wrangler secret put` lines that `scripts/gen-signing-key.mjs new-key` prints
     (`REGISTRY_SIGNING_PRIVATE_KEY` + `REGISTRY_SIGNING_KEYID`), then re-publish.

---

## Step 1, method 1 — curl + jq + openssl (no script)

Proven that `jq -cSj` emits the exact RFC-8785 bytes the registry signed, and that openssl verifies
the registry's raw Ed25519 key.

```bash
BASE=https://genero-intelligence-test.michael-folcher.workers.dev
SLUG=qrcode; VER=1.0.0; VARIANT=genero6            # <-- the package to check

VJSON=$(curl -fsS "$BASE/registry/packages/$SLUG/versions/$VER")
KJSON=$(curl -fsS "$BASE/registry/.well-known/keys.json")

# signature + keyid for this variant
SIG_B64=$(jq -r --arg v "$VARIANT" '.artifacts[]|select(.variant==$v)|.signature.sig'   <<<"$VJSON")
KEYID=$(  jq -r --arg v "$VARIANT" '.artifacts[]|select(.variant==$v)|.signature.keyid' <<<"$VJSON")
[ "$SIG_B64" = null ] && { echo "UNSIGNED — publish while the working key is active first (Step 0)"; exit 1; }

# rebuild the EXACT canonical payload that was signed (jq -cSj == RFC 8785 for this all-ASCII shape)
jq -cSj --arg name "$SLUG" --arg v "$VARIANT" '
  .version as $ver | .artifacts[] | select(.variant==$v)
  | {artifact:{name:$name, version:$ver, variant:.variant, sha256:.sha256,
               size:.size_bytes, uploaded_at:.uploaded_at, uploader:(.uploader // "")}}' \
  <<<"$VJSON" > payload.bin

# working public key (base64-raw 32B) -> PEM openssl accepts (fixed 12-byte Ed25519 SPKI prefix)
PUB_B64=$(jq -r --arg k "$KEYID" '.keys[]|select(.keyid==$k)|.pub' <<<"$KJSON")
{ printf '302a300506032b6570032100'; printf '%s' "$PUB_B64" | base64 -d | xxd -p -c256; } | xxd -r -p | base64 \
  | { printf -- '-----BEGIN PUBLIC KEY-----\n'; cat; printf -- '-----END PUBLIC KEY-----\n'; } > working_pub.pem

# (check 2) artifact authenticity
printf '%s' "$SIG_B64" | base64 -d > sig.bin
openssl pkeyutl -verify -pubin -inkey working_pub.pem -rawin -in payload.bin -sigfile sig.bin
#   -> "Signature Verified Successfully"

# (check 3) integrity — downloaded bytes must hash to the signed sha256
DL=$(curl -fsS "$BASE/registry/packages/$SLUG/versions/$VER/artifacts/$VARIANT" | shasum -a 256 | cut -d' ' -f1)
SIGNED=$(jq -r --arg v "$VARIANT" '.artifacts[]|select(.variant==$v)|.sha256' <<<"$VJSON")
[ "$DL" = "$SIGNED" ] && echo "integrity OK ($DL)" || echo "integrity MISMATCH: $DL != $SIGNED"
```

Portability: `base64 -D` (older macOS) vs `base64 -d` (Linux); `sha256sum` on Linux instead of
`shasum -a 256`; `xxd` ships with both.

**(check 1) manifest authenticity — optional, needs the pinned root key (item C).** Proves the
working key in keys.json is genuinely the registry's, not a swapped one:

```bash
ROOT_PUB='<root-test-1 pub_b64raw>'   # from the offline root.key.json
jq -cSj '{issuedAt, keys}' <<<"$KJSON" > manifest_signed.bin
jq -r '.sig.sig' <<<"$KJSON" | base64 -d > manifest_sig.bin
{ printf '302a300506032b6570032100'; printf '%s' "$ROOT_PUB" | base64 -d | xxd -p -c256; } | xxd -r -p | base64 \
  | { printf -- '-----BEGIN PUBLIC KEY-----\n'; cat; printf -- '-----END PUBLIC KEY-----\n'; } > root_pub.pem
openssl pkeyutl -verify -pubin -inkey root_pub.pem -rawin -in manifest_signed.bin -sigfile manifest_sig.bin
```

---

## Step 1, method 2 — one-file Node verifier (no jq/openssl)

Save the script below as `verify-signing.mjs` (needs only Node 18+, zero npm dependencies) and run:

```bash
# artifact authenticity + integrity
node verify-signing.mjs --pkg qrcode --version 1.0.0 --variant genero6 --check-bytes

# also verify the manifest itself against the pinned root key (item C)
node verify-signing.mjs --pkg qrcode --version 1.0.0 --root-pub '<root-test-1 pub_b64raw>' --check-bytes
```

<details>
<summary><code>verify-signing.mjs</code></summary>

```js
#!/usr/bin/env node
// Layer 1 (registry-signed) package-signature verifier for the fglpkg / Genero
// Intelligence registry. Zero dependencies — Node 18+ only.
//
// Does, from the command line, what `fglpkg install` WILL do once internal/signing/
// ships: reconstruct the RFC 8785 (JCS) canonical payload the registry signed and
// verify the Ed25519 signature.
//
// Usage:
//   node verify-signing.mjs --pkg qrcode --version 1.0.0 [--variant genero6]
//        [--base https://genero-intelligence-test.michael-folcher.workers.dev]
//        [--root-pub <base64-raw-32-byte-root-pubkey>] [--check-bytes]

import crypto from "node:crypto";

function parseArgs(argv) {
  const o = {};
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i];
    if (a.startsWith("--")) {
      const k = a.slice(2);
      const n = argv[i + 1];
      if (n === undefined || n.startsWith("--")) o[k] = true;
      else { o[k] = n; i++; }
    }
  }
  return o;
}
const opt = parseArgs(process.argv.slice(2));
const BASE = (opt.base || "https://genero-intelligence-test.michael-folcher.workers.dev").replace(/\/$/, "");
const VARIANT = opt.variant || "genero6";
if (!opt.pkg || !opt.version) {
  console.error("usage: node verify-signing.mjs --pkg <slug> --version <v> [--variant genero6] [--base URL] [--root-pub B64] [--check-bytes]");
  process.exit(2);
}

// RFC 8785 (JCS), constrained to the payload shapes here: objects/arrays of STRINGS
// + one non-negative INTEGER (`size`). No floats, no non-ASCII — for this alphabet
// JSON.stringify() escaping and String(int) are byte-identical to a full JCS impl
// (proven byte-equal against the server's `canonicalize` package).
function jcs(v) {
  if (typeof v === "string") return JSON.stringify(v);
  if (typeof v === "number") {
    if (!Number.isInteger(v)) throw new Error("JCS subset: non-integer number");
    return String(v);
  }
  if (Array.isArray(v)) return "[" + v.map(jcs).join(",") + "]";
  if (v && typeof v === "object") {
    const keys = Object.keys(v).sort(); // UTF-16 code-unit order == JCS for ASCII keys
    return "{" + keys.map((k) => JSON.stringify(k) + ":" + jcs(v[k])).join(",") + "}";
  }
  throw new Error("JCS subset: unsupported type " + typeof v);
}

function importRawEd25519Pub(b64raw) {
  const raw = Buffer.from(b64raw, "base64");
  if (raw.length !== 32) throw new Error(`expected 32-byte raw Ed25519 pubkey, got ${raw.length}`);
  return crypto.createPublicKey({ key: { kty: "OKP", crv: "Ed25519", x: raw.toString("base64url") }, format: "jwk" });
}
function ed25519Verify(pubB64raw, dataBuf, sigB64) {
  return crypto.verify(null, dataBuf, importRawEd25519Pub(pubB64raw), Buffer.from(sigB64, "base64"));
}

async function getJSON(url) {
  const r = await fetch(url);
  if (!r.ok) throw new Error(`GET ${url} -> ${r.status}`);
  return r.json();
}

let failures = 0;
const say = (ok, msg) => { console.log(`${ok ? "✓" : "✗"} ${msg}`); if (!ok) failures++; };

// 1. keys manifest
const manifest = await getJSON(`${BASE}/registry/.well-known/keys.json`);
console.log(`keys.json: issuedAt=${manifest.issuedAt}  keys=[${manifest.keys.map((k) => k.keyid).join(", ")}]  rootKeyid=${manifest.sig?.rootKeyid}`);

// 1a. optional manifest authenticity vs pinned root key
if (opt["root-pub"]) {
  const data = Buffer.from(jcs({ issuedAt: manifest.issuedAt, keys: manifest.keys }), "utf8");
  say(ed25519Verify(opt["root-pub"], data, manifest.sig.sig), `manifest signed by root ${manifest.sig.rootKeyid}`);
} else {
  console.log("ℹ skipping manifest root-signature check (no --root-pub given)");
}

// 2. artifact record
const ver = await getJSON(`${BASE}/registry/packages/${opt.pkg}/versions/${opt.version}`);
const art = (ver.artifacts || []).find((a) => a.variant === VARIANT);
if (!art) { console.error(`no artifact variant '${VARIANT}' in ${opt.pkg}@${opt.version}`); process.exit(1); }

console.log(`artifact: ${art.filename}  size=${art.size_bytes}  sha256=${art.sha256}`);
console.log(`          uploaded_at=${JSON.stringify(art.uploaded_at)}  uploader=${JSON.stringify(art.uploader)}`);

if (!art.signature) {
  say(false, `artifact is UNSIGNED (signature: null) — published before the working key was active, or no working key is configured on this registry`);
  process.exit(failures ? 1 : 0);
}
console.log(`          signature keyid=${art.signature.keyid} alg=${art.signature.alg}`);

// working key by keyid, with validity window covering uploaded_at
const key = manifest.keys.find((k) => k.keyid === art.signature.keyid);
say(!!key, `signature keyid '${art.signature.keyid}' is present in keys.json`);
if (key) {
  const up = Date.parse(String(art.uploaded_at).replace(" ", "T") + (String(art.uploaded_at).endsWith("Z") ? "" : "Z"));
  const inWindow = up >= Date.parse(key.validFrom) && up <= Date.parse(key.validTo);
  say(inWindow, `uploaded_at within key validity window [${key.validFrom} .. ${key.validTo}]`);

  const payload = {
    artifact: {
      name: opt.pkg,
      version: ver.version,
      variant: art.variant,
      sha256: art.sha256,
      size: art.size_bytes,
      uploaded_at: art.uploaded_at,
      uploader: art.uploader ?? "",
    },
  };
  const data = Buffer.from(jcs(payload), "utf8");
  say(ed25519Verify(key.pub, data, art.signature.sig), `artifact signature verifies under working key '${key.keyid}'`);
}

// 3. optional bytes integrity
if (opt["check-bytes"]) {
  const r = await fetch(`${BASE}/registry/packages/${opt.pkg}/versions/${opt.version}/artifacts/${VARIANT}`);
  if (!r.ok) { say(false, `download artifact bytes -> ${r.status}`); }
  else {
    const buf = Buffer.from(await r.arrayBuffer());
    const h = crypto.createHash("sha256").update(buf).digest("hex");
    say(h === art.sha256, `downloaded bytes hash to the signed sha256 (${buf.length} bytes)`);
  }
}

console.log(failures ? `\nFAILED (${failures})` : `\nOK — all checks passed`);
process.exit(failures ? 1 : 0);
```

</details>

---

## What each method checks

Both methods run the same three independent checks and exit non-zero on any failure:

1. **manifest authenticity** *(needs the root key, item C)* — keys.json is signed by the offline root.
2. **artifact authenticity** — the signature verifies under the working key from keys.json; its keyid
   is present and its validity window covers `uploaded_at`.
3. **integrity** — the downloaded bytes hash to the signed `sha256`, tying the signature to the tarball.

## Negative tests (prove it catches tampering)

- **Wrong bytes:** flip the expected `sha256` you compare against — the integrity check must fail.
- **Tampered field:** change any field (e.g. bump `size` by 1) before verifying — Ed25519 must reject it.
- **Unknown / expired key:** point at an artifact whose `signature.keyid` isn't in the current
  manifest → the keyid-present / validity-window check fails (mirrors `ErrKeyUnknown` / `ErrKeyExpired`).

## Notes

- `pub` and the root key are **base64-raw 32-byte** Ed25519 keys; `signature.sig` is base64 (64 bytes).
- The Node verifier's inline JCS is **byte-identical** to the server's `canonicalize` (proven across
  artifact payloads, boundary `size` values `0`/`999999999`, key-order permutations, and the manifest
  shape), so a pass here means the future Go verifier must agree.
- This maps 1:1 onto the planned `fglpkg audit signatures`, which will walk the lockfile and
  re-verify every entry.
