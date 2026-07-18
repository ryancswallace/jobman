# Development utilities

This directory contains repository-maintenance programs that are not part of
the shipped Jobman binary.

- `autocomplete/` generates Bash, Zsh, and PowerShell completion files.
- `dogfood/` contains disposable evidence, process-tree, notifier, and bounded
  soak helpers used by the release-candidate runbook in `docs/DOGFOOD.md`.
- `manpages/` generates manual pages from the Cobra command tree.
- `sitedocs/` stages the published manual, imports canonical contracts, checks
  internal links, and generates the web command reference from Cobra.
- `check-release.sh` verifies tracked release records and the contents,
  checksums, SBOM inventory, and embedded metadata of a built release snapshot.
- `updates/` contains deterministic repository-maintenance scripts.

Run the utilities through the Makefile so paths and validation stay consistent:

```console
make gen-all
make release-check
make snapshot
make update
```

Generators must be deterministic and must fail when an output cannot be
created. Add focused tests beside a generator when its behavior changes.
