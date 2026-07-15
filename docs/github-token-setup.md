# GitHub token setup — retired

> **This document is obsolete.** fglpkg no longer stores package artifacts as
> GitHub Release assets, and **no GitHub token is involved** in publishing or
> installing. The registry stores artifacts itself (R2-backed object storage).

## What to use instead

Authentication is now a registry OAuth login (or a Personal Access Token for CI):

```bash
fglpkg login                 # browser OAuth (authorization code + PKCE)
fglpkg login --token <PAT>   # non-interactive / CI
# or, in CI, set the env var directly:
export FGLPKG_TOKEN=<PAT>
```

- Installing **public** packages needs no token at all.
- Publishing needs a registry account; a freshly published version is *pending*
  until a registry administrator approves it.

See the [User Guide → Publishing](user-guide.md#publishing) and
[User Guide → Registry Authentication](user-guide.md#registry-authentication),
or the [README](../README.md#authentication), for the current flow. For a
secondary JFrog Artifactory repository, see
[Secondary Repositories](user-guide.md#secondary-repositories-jfrog-artifactory).
