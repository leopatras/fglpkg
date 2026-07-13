export FGLLDPATH=$(CURDIR)/../..
#the fglpkg launcher forces CHAR semantics (see ../fglpkg); match that
#here so tests exercise the same ORD()/getCharAt behavior as production
export FGL_LENGTH_SEMANTICS=CHAR

FGLFLAGS?=
FGLCOMP=fglcomp -M -Wall $(FGLFLAGS)

TESTS=$(patsubst %.4gl,%.42m,$(wildcard test*.4gl))

all: $(TESTS)

%.42m: %.4gl
	$(FGLCOMP) $<

$(filter-out testutils.42m,$(TESTS)): testutils.42m testassert.inc

run: all
	@set -e; for t in $(filter-out testutils.42m,$(TESTS)); do \
	  echo "== $$t"; FGLGUI=0 fglrun $$t; \
	done

clean:
	rm -f *.42m err

.PHONY: all run clean
