# System configuration assets

This directory contains files installed by native packages. The sample
`jobman/jobman.yml` is installed as `/etc/jobman/jobman.yml` with
configuration-preserving package semantics.

The installed file activates only the current `schema_version`; its commented
sections are a validated guide to concurrency, retention, secret references,
wait conditions, notifiers, named jobs, and profiles. Site-specific settings
should be uncommented deliberately and checked with `jobman config validate`.

Package assets must contain safe defaults, no host-specific paths, and no
credentials. Changes here should be validated with `make snapshot` and an
installation test for at least one supported package format.
