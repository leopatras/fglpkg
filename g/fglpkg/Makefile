#gwa style Makefile for the fglpkg 4GL implementation
export FGLLDPATH=$(CURDIR)/..

FGLFLAGS?=
FGLCOMP=fglcomp -M -Wall $(FGLFLAGS)

MODS=$(patsubst %.4gl,%.42m,$(wildcard *.4gl))

all: $(MODS)

%.42m: %.4gl
	$(FGLCOMP) $<

#inter-module dependencies (IMPORT FGL graph)
semver.42m: fglpkgutils.42m
glob.42m: fglpkgutils.42m
ignore.42m: fglpkgutils.42m glob.42m
genero.42m: fglpkgutils.42m semver.42m
checksum.42m: fglpkgutils.42m
manifest.42m: fglpkgutils.42m semver.42m glob.42m
credentials.42m: fglpkgutils.42m
registry.42m: fglpkgutils.42m manifest.42m semver.42m
resolver.42m: fglpkgutils.42m manifest.42m semver.42m registry.42m genero.42m \
  workspace.42m
lockfile.42m: fglpkgutils.42m glob.42m manifest.42m resolver.42m
outdated.42m: fglpkgutils.42m semver.42m manifest.42m lockfile.42m registry.42m
oauth.42m: fglpkgutils.42m credentials.42m registry.42m
publish.42m: fglpkgutils.42m manifest.42m genero.42m pack.42m registry.42m \
  credentials.42m hooks.42m
hooks.42m: fglpkgutils.42m manifest.42m glob.42m
installer.42m: fglpkgutils.42m manifest.42m lockfile.42m resolver.42m \
  registry.42m checksum.42m genero.42m hooks.42m
env.42m: fglpkgutils.42m glob.42m workspace.42m
pack.42m: fglpkgutils.42m manifest.42m glob.42m ignore.42m genero.42m checksum.42m
templates.42m: fglpkgutils.42m
commands.42m: fglpkgutils.42m
completion.42m: fglpkgutils.42m glob.42m commands.42m
runner.42m: fglpkgutils.42m glob.42m manifest.42m genero.42m env.42m
workspace.42m: fglpkgutils.42m glob.42m manifest.42m
sbom.42m: fglpkgutils.42m manifest.42m lockfile.42m commands.42m
audit.42m: fglpkgutils.42m manifest.42m lockfile.42m sbom.42m
cli.42m: fglpkgutils.42m commands.42m manifest.42m semver.42m genero.42m \
  lockfile.42m registry.42m resolver.42m installer.42m env.42m pack.42m \
  templates.42m credentials.42m oauth.42m publish.42m outdated.42m \
  completion.42m runner.42m workspace.42m sbom.42m audit.42m
main.42m: cli.42m

fglpkgutils.42m: myassert.inc

test: all
	$(MAKE) -C test run

clean:
	rm -f *.42m err
	-$(MAKE) -C test clean

# --- fglpkg packaging (publishing this 4GL port as a source package) --
# Publishes this directory as the "fglpkg" package to the fglpkg
# registry, under a SINGLE variant rather than one per Genero major.
# fglpkg-genero (the published bin script) always recompiles from
# source via `make` on every invocation, so the same package works
# across Genero majors without shipping .42m bytecode -- verified
# against a real Genero 4.01.10 toolchain, not just assumed. See
# .claude/memory/registry-publishing-and-variants.md for the full
# variant-resolution background (why a single variant is enough, and
# why this relies on an undocumented fglpkg resolver fallback).
#
# The installed directory name must be exactly "fglpkg" for
# IMPORT FGL fglpkg.X to resolve -- this is why the package can't be
# published under any other name.

FGLPKG_REGISTRY ?= https://genero-intelligence-test.michael-folcher.workers.dev
export FGLPKG_REGISTRY
FGLPKG_GENERO_TARGET ?= 4.01.10
BUMP ?= patch

.PHONY: fglpkg-pack-list fglpkg-publish fglpkg-bump-publish

# preview the zip contents without publishing
fglpkg-pack-list:
	fglpkg pack --list

# publish the CURRENT version in fglpkg.json (fails if that
# version+variant is already published -- bump first, or use
# fglpkg-bump-publish below)
fglpkg-publish:
	FGLPKG_GENERO_VERSION=$(FGLPKG_GENERO_TARGET) fglpkg publish

# bump to the next version and publish it in one step
#   make fglpkg-bump-publish             # patch bump (default)
#   make fglpkg-bump-publish BUMP=minor  # or major | prerelease | <semver>
fglpkg-bump-publish:
	fglpkg version $(BUMP)
	FGLPKG_GENERO_VERSION=$(FGLPKG_GENERO_TARGET) fglpkg publish

.PHONY: all test clean
