# fglpkg samples — five BDL packages (A, B, C, v5, v6), one webcomponent
# package (WC), and two consumers (D, E).
#
#   sample-a   (A/)   no dependencies
#   sample-b   (B/)   depends on sample-a and sample-c
#   sample-c   (C/)   depends on sample-a and sample-b   (B <-> C is a cycle)
#   sample-v5  (v5/)  requires Genero >= 5.00.03 (base.Channel.getExitStatus)
#   sample-v6  (v6/)  requires Genero >= 6.00    (prometheus package)
#   sample-img (WC/)  pure webcomponent package (no BDL content) — an
#                     image-display widget, COMPONENTTYPE "img"
#   sample-d   (D/)   app project consuming a/b/c via `fglpkg install`
#   sample-e   (E/)   app project consuming sample-img via `fglpkg install`
#
# Targets:
#   make demo-4gl   end-to-end with the Genero 4GL fglpkg (g/fglpkg)
#   make demo-go    end-to-end with the Go fglpkg (bin/fglpkg-go)
#   make modules    only precompile the package modules
#   make clean
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
# + run D, and install + build E; the registry only lives for the
# duration of the recipe
define demo_flow
	set -e; \
	rm -rf .registry-state .fglpkg-home D/.fglpkg D/fglpkg.lock D/d.42m E/.fglpkg E/fglpkg.lock E/imgdemo.42m E/imgdemo.42f; \
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
	echo "== install dependencies in D"; \
	(cd D && $(1) install); \
	echo "== compile and run D"; \
	(cd D && eval "$$($(1) env)" && fglcomp -M d.4gl && fglrun d.42m); \
	echo "== install dependencies in E"; \
	(cd E && $(1) install); \
	echo "== compile E (webcomponent form — not run, see header comment)"; \
	(cd E && eval "$$($(1) env)" && fglform -M imgdemo.per && fglcomp -M imgdemo.4gl)
endef

demo-4gl: modules
	$(call demo_flow,$(FGLPKG_4GL))

demo-go: modules $(FGLPKG_GO)
	$(call demo_flow,$(FGLPKG_GO))

# B and C import each other, so neither can be compiled first inside its
# own directory (fglcomp resolves IMPORT FGL from FGLLDPATH only as
# compiled .42m). Compile all modules together in a flat staging dir —
# there fglcomp reads the imported interface straight from the source —
# and copy the .42m back so the published zips ship them prebuilt.
modules:
	rm -rf .build && mkdir .build
	cp A/a.4gl B/b.4gl C/c.4gl .build/
	cd .build && fglcomp -M a.4gl && fglcomp -M b.4gl && fglcomp -M c.4gl
	cp .build/a.42m A/ && cp .build/b.42m B/ && cp .build/c.42m C/
	rm -rf .build
	cd v5 && fglcomp -M v5.4gl
	cd v6 && fglcomp -M v6.4gl

# build the Go reference binary on demand
$(FGLPKG_GO):
	cd $(REPO) && PATH=$$HOME/sdk/go/bin:$$PATH go build -o bin/fglpkg-go ./cmd/fglpkg

clean:
	rm -rf .build .registry-state .fglpkg-home
	rm -f A/*.42m B/*.42m C/*.42m D/*.42m v5/*.42m v6/*.42m E/*.42m E/*.42f
	rm -rf D/.fglpkg E/.fglpkg
	rm -f D/fglpkg.lock E/fglpkg.lock
