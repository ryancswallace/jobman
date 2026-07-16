# Documentation site

This directory contains authored, task-oriented pages for the Jobman website.
`make gen-site` stages these pages in `site-build/`, copies selected canonical
repository documents with site navigation metadata, publishes the commented
sample configuration, and generates command reference pages from the Cobra
tree. Do not edit `site-build/`.

The production workflow is `.github/workflows/pages.yml`. Run `make docs` to
validate generation, spelling, internal links, and the production-equivalent
Jekyll build. `.github/workflows/docs-links.yml` checks published HTTPS links
on relevant changes and weekly; deliberate example endpoints are excluded.
Do not commit `_site/`, `site/_site/`, `site-build/`, or other generated output.
