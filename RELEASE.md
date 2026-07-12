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
   image, and the Homebrew Cask update.
6. If there are no releasable commits, the workflow exits successfully without
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

## Published artifacts

Each GitHub release contains:

- `.tar.gz` archives for Linux and macOS and `.zip` archives for Windows;
- `amd64`, `arm64`, and supported `386` binaries;
- `.deb`, `.rpm`, and `.apk` Linux packages;
- generated man pages, Bash/Zsh completions, the sample configuration, license,
  README, and changelog inside the portable archives;
- SPDX JSON SBOMs for archives and native packages;
- a SHA-256 checksum manifest and a keyless Sigstore bundle for that manifest.

GoReleaser also publishes `linux/amd64` and `linux/arm64` images to:

```text
ghcr.io/ryancswallace/jobman:<version>
ghcr.io/ryancswallace/jobman:v<version>
ghcr.io/ryancswallace/jobman:latest
```

The `latest` image is published only for stable versions. Images run as an
unprivileged `jobman` user and include Bash, CA certificates, and timezone data.

The Homebrew Cask is generated into `Casks/jobman.rb` in this repository. The
workflow token needs permission to push that generated update to `main`; if
branch protection rejects the update, publish it through a dedicated tap or use
a narrowly scoped release-bot token instead.

## Repository configuration

The workflow uses GitHub's short-lived `GITHUB_TOKEN`; no repository PAT or GPG
private key is required. Keep these workflow permissions enabled:

- `contents: write` for tags, releases, assets, and the Homebrew Cask;
- `packages: write` for GHCR;
- `id-token: write` for keyless Sigstore signing.

In **Settings → Actions → General → Workflow permissions**, allow GitHub Actions
to create and approve the configured repository writes. In the package settings,
ensure this repository's workflow has write access to the `jobman` GHCR package.
If tag protection rules cover `v*`, allow the GitHub Actions release identity to
create those tags.

## Local validation

Install Go 1.26, GoReleaser 2.17, Syft, Cosign, Docker with Buildx, and QEMU/binfmt
for multi-platform container tests. Then run:

```sh
go test ./...
make release-check
make snapshot
```

Snapshot mode writes artifacts to `dist/` and does not create a GitHub release.
The Homebrew publisher and keyless signing are skipped locally because they need
GitHub credentials and an Actions OIDC identity. Docker Buildx may create local
platform-suffixed images during snapshot validation.

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
  --certificate-identity-regexp \
    '^https://github.com/ryancswallace/jobman/.github/workflows/release.yml@refs/.*$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  jobman_<version>_checksums.txt

sha256sum --check jobman_<version>_checksums.txt --ignore-missing
```

Inspect an SBOM before installation when provenance or dependency review is
required. For containers, pull an immutable version tag rather than `latest`:

```sh
docker pull ghcr.io/ryancswallace/jobman:<version>
```

## Recovering or retrying a release

Do not delete and recreate tags as a first response. Open the `Release` workflow,
choose **Run workflow**, and enter the existing tag (for example, `v1.2.3`). The
workflow validates the tag, checks it out, rebuilds from that exact commit, and
replaces same-named GitHub release artifacts.

Before retrying, diagnose the failed publishing stage:

- GHCR failures usually indicate missing package write access;
- Homebrew failures usually indicate branch protection or repository write
  restrictions;
- signing failures usually indicate missing `id-token: write` permission;
- an absent man page or completion file means the generator failed or produced
  an empty file;
- a duplicate asset error should be handled automatically by GoReleaser's
  `replace_existing_artifacts` setting.

Only remove a tag or release when it points at the wrong commit or exposed an
artifact that must be revoked. Document that incident before publishing a new
version; released version numbers should not be reused for different source.

## Manual preflight for maintainers

Before relying on the first automated release:

- confirm the latest `Test` run on `main` is green;
- check that all release-worthy commits use Conventional Commit syntax;
- run the local snapshot commands above;
- verify GitHub Actions, GHCR, and tag-protection permissions;
- ensure `Casks/jobman.rb` can be updated by the workflow identity;
- review the generated GitHub release notes and all artifacts after publication;
- install at least one archive or native package and run the versioned container.
