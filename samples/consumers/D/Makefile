# sample-d — standalone consumer project demonstrating sample-a/b/c
# (each declares PACKAGE a/b/c, cross-imported as a.Core/b.Core/c.Core).
#
# `fglpkg install` alone does NOT put the installed packages on
# FGLLDPATH -- that's what `fglpkg env` is for. It prints shell export
# statements (FGLLDPATH, and CLASSPATH/FGLIMAGEPATH if applicable);
# `eval` them into the current shell before compiling/running.
#
#   make install   fglpkg install (fetch a/b/c per fglpkg.lock)
#   make run       eval "$(fglpkg env)" then compile + run d.4gl
#   make clean

FGLPKG ?= fglpkg

.PHONY: install run clean

install:
	$(FGLPKG) install

run:
	eval "$$($(FGLPKG) env)" && fglcomp -M d.4gl && fglrun d.42m

clean:
	rm -f d.42m
