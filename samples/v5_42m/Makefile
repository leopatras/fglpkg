# sample-v5-42m — requires Genero >= 5.00.03 (base.Channel.getExitStatus),
# shipped as compiled .42m ONLY -- fglpkg.json's "files" lists just
# "*.42m", so v5_42m.4gl is never included in the published zip. Because
# there is no source to self-compile from at install time, a real .42m
# must be compiled AND PUBLISHED SEPARATELY under each Genero major this
# package supports (there is no single-artifact fallback like the
# source-only sample-v5).
#
#   make compile       fglcomp v5_42m.4gl (with whichever Genero is
#                      currently active on PATH)
#   make pack-list     preview the zip contents without publishing
#                      (should show only v5_42m.42m + fglpkg.json)
#   make publish       publish the CURRENT version in fglpkg.json, for
#                      the Genero major of whichever fglcomp compiled
#                      v5_42m.42m
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
	fglcomp -M v5_42m.4gl

pack-list:
	$(FGLPKG) pack --list

# fails if this version+variant is already published -- bump first, or
# use bump-publish below
publish:
	$(FGLPKG) publish

bump-publish:
	$(FGLPKG) version $(BUMP)
	$(FGLPKG) publish
