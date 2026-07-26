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

The production site uses the `jobman.tech` apex domain configured in
`_config.yml`. Because Pages is deployed by a custom GitHub Actions workflow,
configure the custom domain in the repository's Pages settings and configure
the apex and `www` DNS records at the DNS provider; a repository `CNAME` file
is ignored by this deployment mode.
