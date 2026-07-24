# sample-shadow-v5-consumer — installs both sample-v5 and
# sample-shadow-v5 (they both ship a module named v5.4gl/v5.42m) to
# demonstrate the FGLLDPATH module-shadowing gap in GIS-346: no warning
# is raised, and whichever package's directory comes first on FGLLDPATH
# silently wins `IMPORT FGL v5`.
#
# Uses the Go fglpkg BUILT IN THIS REPO (bin/fglpkg-go), not whatever
# "fglpkg" resolves on PATH.
#
#   make install   fglpkg install (fetch both dependencies)
#   make run       eval "$(fglpkg env)" then compile + run consumer.4gl
#                   -- watch which "Hello ..." message wins
#   make clean

REPO := $(abspath ../../..)
FGLPKG ?= $(REPO)/bin/fglpkg-go

.PHONY: install run clean

install: $(FGLPKG)
	$(FGLPKG) install

run: $(FGLPKG)
	eval "$$($(FGLPKG) env)" && fglcomp -M consumer.4gl && fglrun consumer.42m

# build the Go reference binary on demand
$(FGLPKG):
	cd $(REPO) && PATH=$$HOME/sdk/go/bin:$$PATH go build -o bin/fglpkg-go ./cmd/fglpkg

clean:
	rm -f consumer.42m
