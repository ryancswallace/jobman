# Maintenance and support policy

Jobman is maintained as a small open-source project. This policy defines the
v1 maintenance commitment without promising staffing or response times the
project cannot reliably provide.

## Release lines

- Stable releases follow semantic versioning and are cut from tested commits
  on `main` through the signed release workflow.
- Patch releases contain compatible bug, security, documentation, and
  packaging fixes.
- Minor releases may add compatible commands, flags, configuration fields, or
  JSON fields under the rules in `docs/COMPATIBILITY.md`.
- A breaking public-contract or persisted-schema change requires a new major
  version, an explicit migration path, and release notes.
- Prereleases are evaluation builds and may be superseded without a backport.

The newest stable minor line is the normal maintenance target. The previous
minor line receives only Critical and High security fixes for 90 days, as
defined in `SECURITY.md`. Routine bug fixes are not backported unless the
maintainer judges an upgrade to be unusually risky.

## Maintenance cadence

Dependabot and scheduled maintenance propose dependency, action, toolchain,
and base-image updates. Maintainers should review those changes at least
monthly and prioritize reachable vulnerabilities. There is no fixed feature
release cadence; releases should be driven by completed, reviewed behavior and
current native-platform evidence.

Issues and pull requests are handled on a best-effort basis. Security reports
use the private process in `SECURITY.md`. Ordinary bugs should include the
Jobman version, operating system and architecture, command, redacted
configuration, observable result, and relevant `doctor --json` output.

## v1 release gate

A v1 release commit must:

1. pass unit coverage, fuzz targets, assembled-binary end-to-end tests,
   performance contracts, native race jobs, architecture builds, vulnerability
   checks, documentation, container smoke tests, and release snapshot checks;
2. pass the native Linux, macOS, and Windows jobs on the exact commit;
3. complete and retain evidence from `docs/DOGFOOD.md`;
4. review `README.md`, `SECURITY.md`, `SUPPORT.md`,
   `docs/design/PLATFORM_CAPABILITIES.md`, `docs/UPGRADING.md`, and
   `CHANGELOG.md` in the release pull request; and
5. verify generated citation metadata, archives, native packages, SBOMs,
   signatures, attestations, and workload-derived container behavior.

The four fuzz jobs run on every pull request and every push to `main`, so the
release decision can use evidence from the exact candidate commit rather than
only its pre-merge pull-request head. The scheduled or manually dispatched soak
workflow supplies the longer-duration evidence; it is intentionally not hidden
inside the ordinary Test workflow.

A skipped gate needs a documented risk acceptance in the release notes. It is
not silently equivalent to a passing gate.

## End of support

End-of-support notices are published in the changelog and release notes before
the affected line expires when practical. Removed downloads are not used as a
revocation mechanism; signatures and checksums remain available so historical
artifacts can still be audited. Compromised artifacts or keys are handled as a
security incident rather than an ordinary support transition.
