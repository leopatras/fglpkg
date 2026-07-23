# sample-v6-42m-consumer — standalone consumer demonstrating
# sample-v6-42m (Genero >=6.00, shipped as compiled .42m only) across a
# real Genero-environment switch.
#
# FGLDIR selects which real Genero SDK's bin/ goes first on PATH --
# defaults to the machine's Genero 6 install; override to point at a
# different one, e.g.:
#   make FGLDIR=$$HOME/Downloads/fgl5.01.04dev update
# (expected to fail: sample-v6-42m has no Genero-5-compatible variant)
#
# Uses the Go fglpkg BUILT IN THIS REPO (bin/fglpkg-go), not whatever
# "fglpkg" resolves on PATH.
#
#   make install   fglpkg install (fetch sample-v6-42m per fglpkg.lock)
#   make update    fglpkg update (re-resolve against the active FGLDIR's
#                  Genero version -- ignores fglpkg.lock)
#   make run       eval "$(fglpkg env)" then compile + run consumer.4gl
#   make clean

REPO := $(abspath ../../..)
FGLDIR ?= $(HOME)/Downloads/fgl
FGLPKG ?= $(REPO)/bin/fglpkg-go
export PATH := $(FGLDIR)/bin:$(PATH)

.PHONY: install update run clean

install: $(FGLPKG)
	$(FGLPKG) install

update: $(FGLPKG)
	$(FGLPKG) update

run: $(FGLPKG)
	eval "$$($(FGLPKG) env)" && fglcomp -M consumer.4gl && fglrun consumer.42m

# build the Go reference binary on demand
$(FGLPKG):
	cd $(REPO) && PATH=$$HOME/sdk/go/bin:$$PATH go build -o bin/fglpkg-go ./cmd/fglpkg

clean:
	rm -f consumer.42m
