# sample-com-fourjs-utils-cli — standalone consumer project demonstrating
# com-fourjs-utils-cli (PACKAGE com.fourjs.utils.cli). The fglpkg registry
# name deliberately mismatches the Genero PACKAGE name here (GIS-346): the
# install directory is named after the registry slug
# ("com-fourjs-utils-cli"), while the actual PACKAGE namespace
# ("com/fourjs/utils/cli/") sits nested inside it -- so `fglpkg env` must
# add this package's own install directory individually to FGLLDPATH, not
# just a single shared `.fglpkg/packages/` parent (that shortcut only
# works when the fglpkg name and PACKAGE name are identical, which they
# deliberately aren't here).
#
# Uses the Go fglpkg BUILT IN THIS REPO (bin/fglpkg-go), not whatever
# "fglpkg" resolves on PATH -- so this always exercises the exact Go
# implementation GIS-346 was filed against, not some other environment
# install that might already differ.
#
# `fglpkg install` alone does NOT put the installed package on FGLLDPATH
# -- that's what `fglpkg env` is for. It prints shell export statements;
# `eval` them into the current shell before compiling/running.
#
#   make install   fglpkg install (fetch com-fourjs-utils-cli per fglpkg.lock)
#   make run       eval "$(fglpkg env)" then compile + run consumer.4gl
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
