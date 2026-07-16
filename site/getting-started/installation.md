---
layout: default
title: Installation
parent: Getting started
nav_order: 1
permalink: /getting-started/installation/
---

# Install and verify Jobman

Use an immutable release artifact for normal installation. Source builds are
most useful for development or evaluation of the current `main` branch.

{: .warning }
Jobman remains prerelease software until v1. Pin an exact version in automation
and review the release notes before upgrading a state directory.

## Release archives and Linux packages

Download the archive for your operating system and architecture from
[GitHub Releases](https://github.com/ryancswallace/jobman/releases). Portable
archives use names such as:

```text
jobman_<version>_linux_amd64.tar.gz
jobman_<version>_darwin_arm64.tar.gz
jobman_<version>_windows_amd64.zip
```

Linux releases also include `.apk`, `.deb`, and `.rpm` packages. Every release
includes a checksum manifest, a Sigstore bundle, SBOMs, and provenance.

Verify the signed checksum manifest before trusting an archive:

```console
$ cosign verify-blob \
    --bundle jobman_<version>_checksums.txt.sigstore.json \
    --certificate-identity \
      'https://github.com/ryancswallace/jobman/.github/workflows/release.yml@refs/heads/main' \
    --certificate-oidc-issuer https://token.actions.githubusercontent.com \
    jobman_<version>_checksums.txt
$ sha256sum --check jobman_<version>_checksums.txt --ignore-missing
```

On macOS, use `shasum -a 256` to compare an individual archive if GNU
`sha256sum` is unavailable. See the [release process]({{ site.baseurl }}/project/releasing/#verifying-a-release)
for attestation and SLSA verification.

## Homebrew

The generated Cask is maintained in the Jobman repository:

```console
$ brew tap ryancswallace/jobman https://github.com/ryancswallace/jobman
$ brew install --cask jobman
```

Upgrade with `brew upgrade --cask jobman` after reviewing the changelog.

## Container image

Images are published to GitHub Container Registry:

```console
$ docker pull ghcr.io/ryancswallace/jobman:vX.Y.Z
$ docker run --rm ghcr.io/ryancswallace/jobman:vX.Y.Z --version
```

The image contains Jobman and basic runtime utilities, not arbitrary target
commands. Read the [container contract]({{ site.baseurl }}/guides/containers/)
before submitting work; a detached job cannot outlive a short-lived container.

## Build from source

Install the exact Go toolchain recorded in
[`go.version`](https://github.com/ryancswallace/jobman/blob/main/go.version),
then run:

```console
$ git clone https://github.com/ryancswallace/jobman.git
$ cd jobman
$ make install
```

For the latest tagged module without cloning:

```console
$ go install github.com/ryancswallace/jobman@latest
```

## Verify the installation

```console
$ jobman --version
$ jobman doctor
$ jobman config paths
```

`doctor` opens the selected state directory and reports database, filesystem,
and lifecycle health. `config paths` shows the concrete configuration files
for the current platform without resolving secrets.

Continue with [Your first job]({{ site.baseurl }}/getting-started/first-job/).
