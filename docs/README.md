# Documentation

The published documentation is available on the [Jobman documentation site].

- `design/` records the target product model and architectural constraints.
- `manpage/` contains release-generated manual pages.
- `completions/` contains release-generated shell completions.

Run `make docs` to regenerate and validate documentation. Generated man pages
and completions are intentionally ignored by Git and are built from the tagged
source during a release.

[Jobman documentation site]: https://ryancswallace.github.io/jobman/
