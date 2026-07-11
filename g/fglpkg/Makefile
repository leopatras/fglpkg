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

.PHONY: all test clean
