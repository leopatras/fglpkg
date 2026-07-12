# Java Class-File Versioning vs FGL P-Code Versioning

**Date:** 2026-07-12
**Audience:** FGL runtime developers and package-ecosystem designers
**Scope:** how the JVM versions its compiled artifacts, what its
compatibility contract enables at ecosystem scale, and how FGL's p-code
versioning compares — background for FGL-6664 ("Keep FGL pcode
compatible between V7/V6/V5/V4")
**Related:** [version-switching-and-constraints.md](version-switching-and-constraints.md)

---

## How Java versions its bytecode

Every `.class` file carries a version number in its header (bytes 4–7:
minor, major). The major version is bumped with **every JDK release**:
Java 1.1 → 45.3, 1.2 → 46, +1 per release — Java 8 → 52, 11 → 55,
17 → 61, 21 → 65, 25 → 69.

The class loader **hard-rejects any class file with a higher major
version than it supports**, eagerly at load time:

```
java.lang.UnsupportedClassVersionError: Foo has been compiled by a more
recent version of the Java Runtime (class file version 61.0), this
version of the Java Runtime only recognizes class file versions up to 52.0
```

Key properties of the contract:

1. **No forward compatibility, ever.** Older JVMs reject newer bytecode
   categorically — even when it uses no new instruction. The gate is
   deliberate policy, not a per-feature check. (Genuinely new opcodes
   are rare: `invokedynamic` in Java 7 was the first since 1.0.)
2. **Backward compatibility is the strong guarantee.** Current JVMs
   still load class files from the 1990s. Erosion is rare and
   *version-conditional*: e.g. the `jsr`/`ret` opcodes are rejected only
   in class files *declaring* version ≥ 51 — old class files keep
   working. The loader branches on the artifact's declared version
   instead of raising the floor.
3. **Preview features** use minor version 65535, loadable only by the
   exact same JDK with `--enable-preview` — an "experimental p-code"
   marker.
4. **Escape hatches** for the version gap:
   - `javac --release N` (JDK 9+): a newer compiler emits older
     bytecode *and* checks against the historic API surface — one
     toolchain, any supported target.
   - **Multi-Release JARs** (JEP 238): one artifact with base bytecode
     plus `META-INF/versions/9/`, `/11/`… overrides; the runtime picks
     the newest applicable.

## What that contract buys the ecosystem

Maven Central serves **one artifact per library release**. A jar built
with `--release 8` runs unchanged on every JVM from 8 to 25 — seventeen
major runtime releases. Library maintainers build once, publish once;
consumers on any runtime version share the same binary, the same
checksums, the same CVE story. None of Maven, Gradle, or the registry
needs a per-JVM-major artifact axis.

## The FGL comparison

`.42m` p-code behaves like the class file: version stamped at compile
time (`TgMajor`), eager version-gated load, no forward compatibility.
The differences before FGL-6664:

- FGL had **no declared backward-compatibility contract** — V4–V6
  compatibility existed only because `PC_MAJOR` happened to stay 31
  from 4.00.02 through 6.0x. The loader demanded exact equality, so the
  V7 policy bump to 32 (without any encoding change) rejected every
  module compiled since 4.00.02.
- FGL has **no `--release` equivalent**: fglcomp cannot emit
  older-major p-code, so cross-targeting is not available as an escape
  hatch. That makes a loader-side compatibility range the only
  practical mechanism.

FGL-6664 adopts the Java model: the loader accepts
`[PC_MAJOR_MIN, PC_MAJOR]` (31..32 today), rejects newer p-code with a
directional error (-6224, the `UnsupportedClassVersionError` analogue,
telling the user to *upgrade the runtime*), keeps -6201 for
below-the-floor versions ("recompile"), and records each module's
version (`Module.pcodeVersion`) so future format changes can be handled
per-version — exactly Java's `jsr`/`ret` pattern — instead of breaking
the floor.

## Why compatible p-code from V4 matters for packages

With fglpkg, packages ship compiled `.42m` (BDL has no
install-from-source convention like npm). Whether p-code is
backward-compatible decides the entire shape of the package ecosystem:

**With the compatibility range (one artifact per release):**
- A package built with a 4.01 toolchain runs on V4, V5, V6 and V7
  runtimes — one zip, one checksum, one entry per version in the
  registry.
- Publishers maintain **one** build pipeline; a bug fix is one release,
  not one per supported major.
- Consumers on mixed-version estates (typical during migrations) share
  one artifact; a `fglpkg.lock` stays valid across a runtime upgrade.
- The registry's genero-major *variant* axis remains available for
  packages that genuinely need per-major builds (C extensions,
  version-specific APIs) but stops being mandatory for plain BDL code.

**With per-major incompatible p-code (the counterfactual):**
- Every package needs a `genero4` + `genero5` + `genero6` + `genero7`
  artifact for the *same* source — 4× the builds, uploads, checksums
  and registry storage, growing by one axis entry every major release.
- Publishers must keep N toolchains installed and re-publish their
  whole catalogue after every major FGL release before consumers can
  upgrade — the classic ecosystem-lag problem (compare Python 2→3 or
  the pre-MRJAR Java 9 module rollout pain, vs. the non-event that
  every Java release since has been for library consumers).
- A runtime upgrade invalidates every installed package and every
  lockfile simultaneously; upgrades of runtime and dependencies can no
  longer be decoupled.
- Old package releases whose publishers have moved on become unusable
  on new runtimes even though their code would run fine — dead weight
  in the registry.

Java demonstrates the end state of the compatible choice: a
twenty-year-deep binary ecosystem where the runtime version is largely
a consumer-side decision. FGL-6664 gives BDL packages the same
foundation from V4 onward.
