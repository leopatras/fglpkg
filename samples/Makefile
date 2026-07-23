# fglpkg samples — five BDL packages (A, B, C, v5, v6), one webcomponent
# package (WC), and two consumers (consumers/D, consumers/E).
#
#   sample-a   (A/)             PACKAGE a, no dependencies
#   sample-b   (B/)             PACKAGE b, depends on sample-a and sample-c
#   sample-c   (C/)             PACKAGE c, depends on sample-a and sample-b (B <-> C cycle)
#   sample-v5  (v5/)            requires Genero >= 5.00.03 (base.Channel.getExitStatus)
#   sample-v6  (v6/)            requires Genero >= 6.00    (prometheus package)
#   sample-img (WC/)            pure webcomponent package (no BDL content) — an
#                               image-display widget, COMPONENTTYPE "img"
#   sample-d   (consumers/D/)   app project consuming a/b/c via `fglpkg install`
#   sample-e   (consumers/E/)   app project consuming sample-img via `fglpkg install`
#
# A/B/C each declare PACKAGE <name> and store their one module nested
# under <name>/ (e.g. A/a/Core.4gl, PACKAGE a) -- `buildFGLLDPATH` (see
# internal/env/env.go, specs/import-root.md) adds each installed
# package's own directory to FGLLDPATH verbatim, so a package's
# namespace must sit directly at that root: the on-disk path relative
# to the project dir (here "a/Core.4gl") is exactly what a consumer's
# `IMPORT FGL a.Core` expects to find under the installed package dir.
# The module is deliberately named Core, not "a" -- fglcomp fails to
# resolve a PUBLIC function from another package when a single-segment
# PACKAGE and its own module share the identical name (confirmed: only
# renaming the module made cross-package resolution work), so keep
# PascalCase module names distinct from the lowercase package path.
#
# Targets:
#   make demo-4gl   end-to-end with the Genero 4GL fglpkg (g/fglpkg)
#   make demo-go    end-to-end with the Go fglpkg (bin/fglpkg-go)
#   make modules    only precompile the package modules
#   make clean
#
# Each package (A, B, C, v5, v6, WC, and hello/) also has its own
# Makefile for real, standalone compile/publish/re-publish (as opposed
# to the throwaway mock-registry publish the demo targets do above) --
# `make compile`, `make pack-list`, `make publish`, `make bump-publish`
# (BUMP=patch|minor|major|prerelease). consumers/D/ and consumers/E/ have
# their own Makefiles too, for `make install` + `make run`/`make build`
# (the latter wired through `eval "$(fglpkg env)"`, since `fglpkg install`
# alone does not put installed packages on FGLLDPATH).
#
# Each demo target starts a private mock registry
# (g/fglpkg/test/mock_registry.py — its package index lives in memory),
# publishes A, C, B, v5, v6, WC, then installs/compiles/runs consumer D
# and installs/compiles consumer E against it, and stops the registry
# again. E's webcomponent form is only compiled (fglform -M), not run
# interactively — WEBCOMPONENT widgets need a browser-capable front end
# (GBC/GWC), which this headless FGLGUI=0/TERM=xterm demo doesn't have;
# compiling the form + program is enough to prove the package installed
# into .fglpkg/webcomponents/img/ and is discoverable via FGLIMAGEPATH.

SHELL := /bin/bash
REPO := $(abspath ..)
REGISTRY_PORT ?= 18930

export FGLPKG_REGISTRY := http://127.0.0.1:$(REGISTRY_PORT)
export FGLPKG_TOKEN := gpr_e2e_pat
export FGLPKG_HOME := $(abspath .fglpkg-home)
export FGLGUI := 0
TERM ?= xterm
export TERM

FGLPKG_4GL := $(REPO)/g/fglpkg/fglpkg
FGLPKG_GO := $(REPO)/bin/fglpkg-go

.PHONY: all demo-4gl demo-go modules clean

all: demo-4gl

# publish A/C/B/v5/v6/WC to a fresh mock registry, then install + build
# + run consumers/D, and install + build consumers/E; the registry only
# lives for the duration of the recipe
define demo_flow
	set -e; \
	rm -rf .registry-state .fglpkg-home consumers/D/.fglpkg consumers/D/fglpkg.lock consumers/D/d.42m consumers/E/.fglpkg consumers/E/fglpkg.lock consumers/E/imgdemo.42m consumers/E/imgdemo.42f; \
	lsof -ti tcp:$(REGISTRY_PORT) | xargs kill 2>/dev/null || true; \
	python3 $(REPO)/g/fglpkg/test/mock_registry.py $(REGISTRY_PORT) .registry-state & REG=$$!; \
	trap 'kill $$REG 2>/dev/null || true' EXIT; \
	sleep 1; \
	for p in A C B v5 v6 WC; do \
	  echo "== publish $$p"; \
	  (cd $$p && $(1) publish --ci); \
	done; \
	echo "== publish v5 genero5 variant"; \
	(cd v5 && FGLPKG_GENERO_VERSION=5.00.05 $(1) publish --ci); \
	echo "== install dependencies in consumers/D"; \
	(cd consumers/D && $(1) install); \
	echo "== compile and run consumers/D"; \
	(cd consumers/D && eval "$$($(1) env)" && fglcomp -M d.4gl && fglrun d.42m); \
	echo "== install dependencies in consumers/E"; \
	(cd consumers/E && $(1) install); \
	echo "== compile consumers/E (webcomponent form — not run, see header comment)"; \
	(cd consumers/E && eval "$$($(1) env)" && fglform -M imgdemo.per && fglcomp -M imgdemo.4gl)
endef

demo-4gl: modules
	$(call demo_flow,$(FGLPKG_4GL))

demo-go: modules $(FGLPKG_GO)
	$(call demo_flow,$(FGLPKG_GO))

# B and C import each other, so neither can be compiled first inside its
# own directory (fglcomp resolves IMPORT FGL from FGLLDPATH only as
# compiled .42m). Compile all three packages together in a staging dir
# that mirrors their nested a/, b/, c/ package layout, with FGLLDPATH
# pointing at that dir's root — fglcomp reads each other's interface
# straight from source across the a/b/c cycle — then copy the .42m back
# so the published zips ship them prebuilt.
modules:
	rm -rf .build && mkdir -p .build/a .build/b .build/c
	cp A/a/Core.4gl .build/a/ && cp B/b/Core.4gl .build/b/ && cp C/c/Core.4gl .build/c/
	cd .build && FGLLDPATH=. fglcomp -M a/Core.4gl b/Core.4gl c/Core.4gl
	cp .build/a/Core.42m A/a/ && cp .build/b/Core.42m B/b/ && cp .build/c/Core.42m C/c/
	rm -rf .build
	cd v5 && fglcomp -M v5.4gl
	cd v6 && fglcomp -M v6.4gl

# build the Go reference binary on demand
$(FGLPKG_GO):
	cd $(REPO) && PATH=$$HOME/sdk/go/bin:$$PATH go build -o bin/fglpkg-go ./cmd/fglpkg

clean:
	rm -rf .build .registry-state .fglpkg-home
	rm -f A/a/*.42m B/b/*.42m C/c/*.42m consumers/D/*.42m v5/*.42m v6/*.42m consumers/E/*.42m consumers/E/*.42f
	rm -rf consumers/D/.fglpkg consumers/E/.fglpkg
	rm -f consumers/D/fglpkg.lock consumers/E/fglpkg.lock
