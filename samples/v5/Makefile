# sample-v5 — requires Genero >= 5.00.03 (base.Channel.getExitStatus)
#
#   make compile       fglcomp v5.4gl
#   make pack-list     preview the zip contents without publishing
#   make publish       publish the CURRENT version in fglpkg.json, for
#                      the Genero major of whichever fglcomp is on PATH
#                      (override: make publish FGLPKG_GENERO_VERSION=5.00.05)
#   make bump-publish  bump (default: patch) then publish
#                      make bump-publish BUMP=minor
#
# Requires FGLPKG_REGISTRY (and auth: `fglpkg login` or FGLPKG_TOKEN) set
# in the environment -- not defaulted here on purpose, since publish is a
# real, hard-to-reverse action against a shared registry (no `unpublish`
# exists -- see .claude/memory/registry-publishing-and-variants.md).

FGLPKG ?= fglpkg
BUMP ?= patch

.PHONY: compile pack-list publish bump-publish

compile:
	fglcomp -M v5.4gl

pack-list:
	$(FGLPKG) pack --list

# fails if this version+variant is already published -- bump first, or
# use bump-publish below
publish:
	$(FGLPKG) publish

bump-publish:
	$(FGLPKG) version $(BUMP)
	$(FGLPKG) publish
