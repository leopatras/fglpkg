---
name: fgl-module-resolution-and-shadowing
description: How FGLLDPATH resolves .42m modules, how to diagnose which one wins, and the module-shadowing gap this exposes (GIS-346/GIS-376)
metadata:
  type: project
---

# FGLLDPATH module resolution, diagnosis, and the shadowing gap

## Resolution order (documented, `c_fgl_EnvVariables_FGLLDPATH.html`)

At both compile time (`IMPORT FGL`) and run time, modules are searched in
this order, first match wins:

1. The current working directory.
2. The directory where the program file resides (the `.42m` containing
   `MAIN`, or the `.42r`).
3. Each path in `FGLLDPATH`, in order (`:`-separated on Unix, `;` on
   Windows).
4. `$FGLDIR/lib`.

For packages, an FGLLDPATH entry is a **topdir**: a package module is
found as `topdir/package-path/module-name[.4gl|.42m]` — see
[[bdl-porting-traps]] and `g/PORTING.md` for the fglpkg-specific
consequence (why `buildFGLLDPATH` adds each installed package's own
directory as its own topdir, and why a package's module can't share its
own package-path segment as its filename).

## How to tell which `.42m` actually gets resolved

No single "verbose module load" flag exists for `fglrun` itself, but
three real, documented tools answer this in practice (asked and
verified this session, analogous to the same question for Java's
classpath — see below):

- **`fglcomp -M --verbose <program>.4gl`** — the direct equivalent of
  Java's `-verbose:class`. Prints `[parsing <full-resolved-path>]` for
  every module it needs, using the exact same FGLLDPATH rules the
  runtime uses. Best answer for "which file would this IMPORT FGL
  resolve to."
- **`fglrun -r <module.42m>`** — dumps a full disassembly whose header
  includes `buildSource=<original .4gl absolute path>` and
  `buildNo=<Genero version>`. Best answer for "what exactly is this
  specific `.42m` I already have in hand" (e.g. one sitting in an
  installed package directory) — equivalent to Java's
  `Class.forName(...).getProtectionDomain().getCodeSource().getLocation()`.
  Used repeatedly this session to prove a `.42m` was genuinely compiled
  by the SDK claimed (see [[bdl-porting-traps]]'s Go `exec` PATH-lookup
  gotcha, which this technique caught).
- **Manual FGLLDPATH walk** — since entries are plain directories (not
  archives like a JAR), no need to peek inside anything: iterate the
  documented search order looking for the first hit. Easier than the
  Java case.

## The module-shadowing gap (GIS-346 §3, GIS-376)

FGLLDPATH's first-match-wins, flat/ordered design is structurally the
same shape as a Java classpath (or Python `sys.path`) — and exposed to
the exact same failure mode: **if two independently-installed packages
both ship a module with the identical name, whichever's directory
sorts first in FGLLDPATH silently wins; the other becomes unreachable,
with zero warning or error anywhere in the flow.**

Reproduced concretely this session:

- `samples/shadow-v5/` (`sample-shadow-v5`) ships a bare module also
  named `v5.4gl`/`v5.42m` — the same name `sample-v5` ships.
- `samples/consumers/shadow-v5-consumer/` depends on both.
- `fglpkg install` reports "Resolved 2 package(s)" with **no
  indication of any conflict**. `sample-shadow-v5` sorts alphabetically
  before `sample-v5`, so its FGLLDPATH entry comes first, and
  `IMPORT FGL v5` silently resolves to the wrong package's module
  (confirmed by each module printing a distinguishable message).

None of npm, pip, or Maven warn about the analogous case by default
either (npm's nested `node_modules` avoids the shape entirely; pip and
Maven both have the same flat-namespace exposure and no default
warning — Maven has an opt-in `maven-enforcer-plugin` rule; Java 9+
JPMS hard-fails at launch instead of silently picking one, but most of
the ecosystem still doesn't use it).

**Important complication for any fix**: FGLLDPATH order-sensitivity is
not purely an accident to eliminate — Four Js's own docs describe
"[Module overriding](https://4js.com/online_documentation/fjs-fgl-manual-html/fgl-topics/c_fgl_programs_module_overriding.html)"
as a **supported, intentional** pattern: shipping a customized module
earlier on FGLLDPATH than a default implementation, specifically to
override plugin/hook functions. A naive "always warn/error on duplicate
module name" fix would have false positives against this legitimate,
documented use case — any real fix needs to distinguish deliberate
override (one module, one FGLLDPATH entry ahead of another **by the
same author/project's own design**) from accidental collision between
two unrelated, independently-published packages.

Filed as **GIS-376** ("Raise warnings/errors for modules/packages with
the same name"), building on **GIS-346**'s third paragraph (which
first raised this exact concern in the context of PACKAGE-name
clashes). Scope per GIS-376: publish-time warning for a colliding bare
module name, hard error for a colliding PACKAGE-declared name;
install-time warn/fail symmetrically, since a consumer pulling from
multiple registries can only fully detect a clash client-side.

## Reference

FGLLDPATH doc: `c_fgl_EnvVariables_FGLLDPATH.html`. Module overriding
doc: `c_fgl_programs_module_overriding.html`. Package/FGLLDPATH doc:
`c_fgl_programs_IMPORT_FGL_packages.html`. fglrun diagnostic options
(`-r`, `-b`, `--print-imports`, `--print-missing-imports`):
`c_fgl_tools_fglrun.html`.
