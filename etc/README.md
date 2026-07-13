# System configuration assets

This directory contains files installed by native packages. The sample
`jobman/jobman.yml` is installed as `/etc/jobman/jobman.yml` with
configuration-preserving package semantics.

Package assets must contain safe defaults, no host-specific paths, and no
credentials. Changes here should be validated with `make snapshot` and an
installation test for at least one supported package format.
