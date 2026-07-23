# com.fourjs.utils.cli — PACKAGE com.fourjs.utils.cli (multi-segment path,
# nested under com/fourjs/utils/cli/), one PUBLIC FUNCTION myErr(), no
# dependencies. Source-only (no shipped .42m), like fglwebrun/sample-a/b/c.
#
#   make compile       fglcomp the module
#   make pack-list     preview the zip contents without publishing
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
	fglcomp -M com/fourjs/utils/cli/Errors.4gl

pack-list:
	$(FGLPKG) pack --list

# fails if this version+variant is already published -- bump first, or
# use bump-publish below
publish:
	$(FGLPKG) publish

bump-publish:
	$(FGLPKG) version $(BUMP)
	$(FGLPKG) publish
