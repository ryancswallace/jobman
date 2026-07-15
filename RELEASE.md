# Releasing jobman

Releases are automated from tested commits on `main`. Semantic-release chooses
the next version and creates the `vX.Y.Z` tag and GitHub release; GoReleaser then
builds, signs, and publishes the release artifacts.

## Release flow

1. Merge a pull request into `main` using Conventional Commit messages.
2. The `Test` workflow runs against the new `main` commit.
3. After that workflow succeeds, the `Release` workflow runs semantic-release.
4. If the commits since the previous tag require a release, semantic-release
   creates the next tag and GitHub release notes.
5. GoReleaser checks out that exact tag and publishes binaries, archives, native
   Linux packages, SBOMs, checksums, a checksum signature, a multi-platform GHCR
   image, and image signatures. It pushes the Homebrew Cask update to a
   version-specific branch and opens a pull request against `main`.
6. An isolated SLSA generator signs provenance for every checksummed artifact
   and uploads `jobman.intoto.jsonl` to the GitHub release.
7. If there are no releasable commits, the workflow exits successfully without
   creating a tag or publishing artifacts.

Release jobs are serialized and are never cancelled in progress. The workflow
does not run independently on tag pushes, which prevents duplicate publication
when semantic-release creates a tag.

## Version selection

Semantic-release follows Conventional Commits:

| Commit | Release effect | Example |
| --- | --- | --- |
| `fix:` or `perf:` | Patch | `fix: preserve job output on retry` |
| `feat:` | Minor | `feat: add retry backoff policies` |
| `BREAKING CHANGE:` footer or `!` | Major | `feat!: change job file format` |
| `docs:`, `test:`, `ci:`, `chore:` | No release by themselves | `docs: clarify timeout behavior` |

Use squash-merge titles that follow this convention because the commits on
`main`, not pull-request labels, determine the version.

For the first stable release, the release-triggering squash commit must carry
an explicit major-version signal, such as `feat!: freeze the v1 public
contract` or a `BREAKING CHANGE:` footer. Confirm that semantic-release selects
`v1.0.0`; do not manually replace a calculated `v0.x` tag with a v1 tag.

## Published artifacts

Each GitHub release contains:

- `.tar.gz` archives for Linux and macOS and `.zip` archives for Windows;
- `amd64`, `arm64`, and supported `386` binaries;
- `.deb`, `.rpm`, and `.apk` Linux packages;
- generated man pages, Bash/Zsh completions, the sample configuration, license,
  README, and changelog inside the portable archives;
- SPDX JSON SBOMs for archives and native packages;
- a SHA-256 checksum manifest and a keyless Sigstore bundle for that manifest;
- a keyless, verifiable SLSA provenance bundle named `jobman.intoto.jsonl` for
  every file in the checksum manifest;
- GitHub-hosted, Sigstore-signed build provenance attestations for every file
  in the checksum manifest and every published container digest;
- keyless Sigstore signatures for the multi-platform container manifest and
  its platform-specific images.

GoReleaser also publishes `linux/amd64` and `linux/arm64` images to:

```text
ghcr.io/ryancswallace/jobman:<version>
ghcr.io/ryancswallace/jobman:v<version>
ghcr.io/ryancswallace/jobman:latest
```

The `latest` image is published only for stable versions. Images run as an
unprivileged `jobman` user and include Bash, CA certificates, and timezone data.
They are CLI runtimes, not persistent daemon containers: use `run --wait` or
`run --foreground`, mount `/home/jobman/.local/state/jobman`, and derive a
workload image containing target commands as described in
[docs/CONTAINERS.md](docs/CONTAINERS.md).

The Homebrew Cask is generated into `Casks/jobman.rb` in this repository. Since
`main` requires pull requests and status checks, GoReleaser writes each update
to `automation/homebrew-<version>` and opens a pull request. Review and merge
that pull request after its required checks pass; the release artifacts remain
published even while the Cask update is awaiting review.

Because this repository does not use Homebrew's conventional
`homebrew-<name>` repository name, users must add it with the explicit remote:

```sh
brew tap ryancswallace/jobman https://github.com/ryancswallace/jobman
brew install --cask jobman
```

## Repository configuration

The workflow uses GitHub's short-lived `GITHUB_TOKEN`; no repository PAT or GPG
private key is required. Keep these workflow permissions enabled:

- `contents: write` for tags, releases, assets, and the Homebrew Cask branch;
- `pull-requests: write` for the Homebrew Cask update pull request;
- `packages: write` for GHCR;
- `id-token: write` for keyless Sigstore signing;
- `attestations: write` and `artifact-metadata: write` for provenance.

The isolated SLSA provenance job additionally receives `actions: read`,
`contents: write`, and `id-token: write`. The SLSA generator must be referenced
by a complete release tag such as `v2.1.0`: its verifier currently rejects a
commit-SHA reference. This is an intentional exception to the repository's
normal action-pinning policy. Dependabot monitors the tag for updates.

In **Settings → Actions → General → Workflow permissions**, enable **Allow
GitHub Actions to create and approve pull requests**. The workflow grants its
token only the explicit write permissions listed above. In the package settings,
ensure this repository's workflow has write access to the `jobman` GHCR package.
This is required even when the workflow has `packages: write`; an existing
package can retain access settings from the repository or token that first
created it. A `403 Forbidden` while pushing layers indicates package access,
ownership, or organization-policy configuration rather than a GoReleaser build
failure.

If tag protection rules cover `v*`, allow the GitHub Actions release identity to
create those tags.

The release job uses the repository's `main` environment. Keep that environment
restricted to deployments from `main`, and retain required reviewers when
releases need a manual approval boundary. Manual recovery runs are rejected
unless the workflow itself is dispatched from `main`.

## Local validation

Install Go 1.26.5, GoReleaser 2.17, Syft, Cosign, Docker with Buildx, and QEMU/binfmt
for multi-platform container tests. Then run:

```sh
make check
make docker-smoke
make snapshot
```

Snapshot mode writes artifacts to `dist/` and does not create a GitHub release.
The Homebrew publisher and keyless checksum and container signing are skipped
locally because they need GitHub credentials and an Actions OIDC identity.
Docker Buildx may create local platform-suffixed images during snapshot
validation.

Run every target in `.github/workflows/fuzz.yml` for 30 seconds and complete a
race-enabled `make soaktest` run as recorded in the [dogfood runbook] before
merging the v1 release commit. The scheduled workflows remain the authoritative
long-running evidence for that exact commit.

The GoReleaser pre-hook renders `.release/CITATION.cff` from the tracked
template using the resolved release version and timestamp. Confirm the copy
inside at least one archive has the release's version and date; the generated
file is ignored and must not be committed.

Before merging a release-triggering change, also confirm that generated assets
are non-empty and current:

```sh
make gen-manpage gen-completions
test -s docs/manpage/jobman.1
test -s docs/completions/bash/jobman
test -s docs/completions/zsh/_jobman
```

## Verifying a release

Download an artifact, the checksum manifest, and its `.sigstore.json` bundle
from the same GitHub release. Verify the signature first, then the checksum:

```sh
cosign verify-blob \
  --bundle jobman_<version>_checksums.txt.sigstore.json \
  --certificate-identity \
    'https://github.com/ryancswallace/jobman/.github/workflows/release.yml@refs/heads/main' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  jobman_<version>_checksums.txt

sha256sum --check jobman_<version>_checksums.txt --ignore-missing

gh attestation verify \
  --owner ryancswallace \
  jobman_<version>_linux_amd64.tar.gz

slsa-verifier verify-artifact \
  --provenance-path jobman.intoto.jsonl \
  --source-uri github.com/ryancswallace/jobman \
  jobman_<version>_linux_amd64.tar.gz
```

The GitHub attestation and downloadable SLSA bundle independently bind the
artifact name and digest to the release workflow and source commit. The SLSA
bundle covers all files listed by the release checksum manifest, so the same
bundle verifies any archive, native package, or SBOM from that release. Inspect
an SBOM before installation when provenance or dependency review is required.
For containers, pull an immutable version tag rather than `latest`:

```sh
docker pull ghcr.io/ryancswallace/jobman:<version>
cosign verify \
  --certificate-identity \
    'https://github.com/ryancswallace/jobman/.github/workflows/release.yml@refs/heads/main' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  ghcr.io/ryancswallace/jobman:<version>
```

## Recovering or retrying a release

Do not delete and recreate tags as a first response. Open the `Release` workflow,
select the `main` branch, choose **Run workflow**, and enter the existing tag
(for example, `v1.2.3`). The workflow validates the tag, checks it out, rebuilds
from that exact commit, and replaces same-named GitHub release artifacts.

Before retrying, diagnose the failed publishing stage:

- GHCR failures usually indicate missing package write access;
- Homebrew branch failures usually indicate missing `contents: write` access;
- Homebrew pull-request failures usually indicate missing `pull-requests: write`
  access or a disabled Actions pull-request setting;
- signing failures usually indicate missing `id-token: write` permission;
- a missing `jobman.intoto.jsonl` asset usually indicates that the isolated
  provenance job could not read the release or obtain its OIDC identity;
- an absent man page or completion file means the generator failed or produced
  an empty file;
- a duplicate asset error should be handled automatically by GoReleaser's
  `replace_existing_artifacts` setting.

Only remove a tag or release when it points at the wrong commit or exposed an
artifact that must be revoked. Document that incident before publishing a new
version; released version numbers should not be reused for different source.

## Manual preflight for maintainers

Before relying on the first stable release:

- confirm the latest `Test` run on `main` is green;
- check that all release-worthy commits use Conventional Commit syntax;
- run the local snapshot commands above;
- verify GitHub Actions, GHCR, and tag-protection permissions;
- verify the `main` environment's deployment protection rules;
- confirm the `jobman` GHCR package grants this repository's Actions workflow
  write access, especially if the package already exists;
- ensure the workflow identity can create the Homebrew update branch and pull
  request, and merge that pull request after its checks pass;
- review the generated GitHub release notes and all artifacts after publication;
- install at least one archive or native package and run the versioned
  container;
- review the v1 release-commit checklist in [SUPPORT.md](SUPPORT.md), including
  README warnings, security support, platform evidence, upgrades, changelog,
  every fuzz target, performance/soak results, and `make docker-smoke`.

[dogfood runbook]: docs/DOGFOOD.md
