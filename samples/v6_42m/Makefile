# sample-v6-42m — requires Genero >= 6.00 (prometheus package), shipped
# as compiled .42m ONLY -- fglpkg.json's "files" lists just "*.42m", so
# v6_42m.4gl is never included in the published zip.
#
#   make compile       fglcomp v6_42m.4gl (with whichever Genero is
#                      currently active on PATH)
#   make pack-list     preview the zip contents without publishing
#                      (should show only v6_42m.42m + fglpkg.json)
#   make publish       publish the CURRENT version in fglpkg.json
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
	fglcomp -M v6_42m.4gl

pack-list:
	$(FGLPKG) pack --list

# fails if this version+variant is already published -- bump first, or
# use bump-publish below
publish:
	$(FGLPKG) publish

bump-publish:
	$(FGLPKG) version $(BUMP)
	$(FGLPKG) publish
