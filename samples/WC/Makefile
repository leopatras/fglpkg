# sample-img — pure webcomponent package (no BDL content), an
# image-display widget, COMPONENTTYPE "img". No compile step: there is
# no .4gl/.42m to build, just the webcomponents/img/ tree.
#
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

.PHONY: pack-list publish bump-publish

pack-list:
	$(FGLPKG) pack --list

# fails if this version+variant is already published -- bump first, or
# use bump-publish below
publish:
	$(FGLPKG) publish

bump-publish:
	$(FGLPKG) version $(BUMP)
	$(FGLPKG) publish
