# Repository update scripts

The `make update` target runs executable `*.sh` files in this directory in
lexical order. It exports `GO_VERS` from the canonical `go.version` file.

Scripts must be POSIX `sh`, deterministic, non-interactive, and safe to run
multiple times. A missing required tool or malformed input must produce a
nonzero exit status. The scheduled repository-maintenance workflow runs the
scripts, validates their result, and opens a pull request only when files
change.

Repository settings must allow GitHub Actions to create pull requests with the
workflow token. Without that setting, local updates still work but scheduled
maintenance cannot publish its branch.

- `copyright-date.sh` updates copyright ranges in tracked files.
- `go-vers.sh` synchronizes Go version declarations across tooling, containers,
  workflows, and documentation.
- `release-metadata.sh` synchronizes the tracked changelog section with the
  newest reachable semantic-version tag.

GoReleaser archives the stable, project-level `CITATION.cff` directly. Release
versions and dates are intentionally omitted so post-release maintenance cannot
leave the citation stale. After a successful Release workflow, the
repository-maintenance workflow runs `make update-all` and opens a reviewed pull
request that advances the changelog records. This post-release pull request is
necessary because the release tag must remain attached to the exact commit that
passed the test workflow.

Container base images are pinned by both tag and digest. When changing a base
image tag manually, update its digest in the same change; an old digest paired
with a new tag intentionally fails closed. Dependabot covers the root and
devcontainer Dockerfiles to automate routine tag and digest updates.
