# sample-e — standalone consumer project demonstrating the sample-img
# webcomponent package.
#
# `fglpkg install` alone does NOT put the installed webcomponent on
# FGLIMAGEPATH -- that's what `fglpkg env` is for. It prints shell
# export statements (FGLLDPATH, and CLASSPATH/FGLIMAGEPATH when
# applicable); `eval` them into the current shell before compiling.
#
#   make install   fglpkg install (fetch sample-img per fglpkg.lock)
#   make build     eval "$(fglpkg env)" then compile the form + program
#                  (not run -- WEBCOMPONENT widgets need a browser-
#                  capable front end (GBC/GWC), see samples/Makefile)
#   make clean

FGLPKG ?= fglpkg

.PHONY: install build clean

install:
	$(FGLPKG) install

build:
	eval "$$($(FGLPKG) env)" && fglform -M imgdemo.per && fglcomp -M imgdemo.4gl

run: build
	eval "$$($(FGLPKG) env)" && fglrun imgdemo

clean:
	rm -f imgdemo.42m imgdemo.42f
