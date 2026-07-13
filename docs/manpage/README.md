# Manual pages

`make gen-manpage` generates section 1 manual pages from the Cobra command tree.
GoReleaser includes the generated pages in archives and native packages.

Generated pages are ignored by Git. Update command help text or the generator in
`devel/manpages/`, then run `make docs` to validate the result.
