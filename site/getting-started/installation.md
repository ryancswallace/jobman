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

Pin an exact version in automation and review the release notes and
[upgrade guide]({{ site.baseurl }}/operations/upgrading/) before opening an
existing state directory with a newer binary.

## Supported systems

Jobman v1 adopts the
[Go 1.26 minimum operating-system requirements](https://go.dev/wiki/MinimumRequirements):
Linux kernel 3.2 or later, macOS 12 Monterey or later, and Windows 10 or
Windows Server 2016 or later.

| Operating system | Release architectures | Distribution |
| --- | --- | --- |
| Linux | `amd64`, `arm64`, `386` | `.tar.gz`, `.deb`, `.rpm`, `.apk`, container (`amd64`/`arm64`) |
| macOS | `amd64`, `arm64` | portable `.tar.gz` |
| Windows | `amd64`, `arm64`, `386` | portable `.zip` |

Every listed target receives a release-style compile check. Lifecycle and race
tests run natively on the current GitHub-hosted runner for each operating
system; cross-compiled architectures do not receive identical native evidence.
The state directory must be on a local filesystem with reliable SQLite WAL
locking. Jobs are scoped to the current OS user session even though they can
survive closing the submitting terminal or SSH connection.

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

### Install a Linux or macOS archive

After verification, set `VERSION`, `OS`, and `ARCH` to the downloaded artifact.
Use `OS=linux` or `OS=darwin`; use `ARCH=amd64`, `arm64`, or, on Linux only,
`386`:

```sh
VERSION=1.0.0
OS=linux
ARCH=amd64
ARCHIVE="jobman_${VERSION}_${OS}_${ARCH}.tar.gz"
EXTRACT_DIR="$(mktemp -d)"
tar -xzf "$ARCHIVE" -C "$EXTRACT_DIR"
install -d -m 0755 "$HOME/.local/bin"
install -m 0755 "$EXTRACT_DIR/jobman" "$HOME/.local/bin/jobman"
"$HOME/.local/bin/jobman" --version
```

Ensure the per-user binary directory is on `PATH`. Add this line to the startup
file for your shell (for example, `~/.profile`), then start a new shell:

```sh
export PATH="$HOME/.local/bin:$PATH"
```

For a system-wide installation, replace the two `install` commands with:

```sh
sudo install -m 0755 "$EXTRACT_DIR/jobman" /usr/local/bin/jobman
```

The archive also contains the man page, shell completions, sample
configuration, changelog, project license, third-party notices, and citation
file. Copy those optional files to the locations used by your operating system
or shell.

On macOS, Gatekeeper may block the first launch because the v1 executable is
not notarized. Only after verifying the signed checksum above, attempt the
launch once, open **System Settings → Privacy & Security**, select **Open
Anyway** for Jobman, authenticate, and confirm the next launch. This is Apple's
per-application approval path; it does not disable Gatekeeper globally. If the
control is unavailable or organization policy forbids an exception, do not
bypass that policy—build from reviewed source or wait for a notarized release.
See [Apple's guidance for safely opening an unnotarized app](https://support.apple.com/en-us/102445).

### Install a Windows archive

Download and verify the ZIP, then run native PowerShell. Set the version and
architecture to the downloaded artifact:

```powershell
$Version = '1.0.0'
$Arch = 'amd64'
$Archive = "jobman_${Version}_windows_${Arch}.zip"
$Manifest = "jobman_${Version}_checksums.txt"
$Bundle = "${Manifest}.sigstore.json"
$InstallDir = Join-Path $env:LOCALAPPDATA 'Programs\Jobman'

cosign verify-blob `
    --bundle $Bundle `
    --certificate-identity `
      'https://github.com/ryancswallace/jobman/.github/workflows/release.yml@refs/heads/main' `
    --certificate-oidc-issuer 'https://token.actions.githubusercontent.com' `
    $Manifest
if ($LASTEXITCODE -ne 0) { throw 'checksum signature verification failed' }

$ChecksumLine = @(Get-Content $Manifest | Where-Object {
    ($_ -split '\s+', 2)[1] -eq $Archive
})
if ($ChecksumLine.Count -ne 1) { throw 'archive is absent or duplicated in checksum manifest' }
$ExpectedHash = ($ChecksumLine[0] -split '\s+', 2)[0].ToLowerInvariant()
$ActualHash = (Get-FileHash -LiteralPath $Archive -Algorithm SHA256).Hash.ToLowerInvariant()
if ($ActualHash -ne $ExpectedHash) { throw 'archive checksum verification failed' }

New-Item -ItemType Directory -Force $InstallDir | Out-Null
Expand-Archive -Path $Archive -DestinationPath $InstallDir -Force

$UserPath = [Environment]::GetEnvironmentVariable('Path', 'User')
$PathEntries = @($UserPath -split ';' | Where-Object { $_ })
if ($PathEntries -notcontains $InstallDir) {
    [Environment]::SetEnvironmentVariable(
        'Path', (($PathEntries + $InstallDir) -join ';'), 'User'
    )
}
$env:Path = "$InstallDir;$env:Path"
& (Join-Path $InstallDir 'jobman.exe') --version
```

The `PATH` update applies automatically to new terminals; the final assignment
makes the binary available in the current PowerShell session. The ZIP is
portable and does not create a Windows service or machine-wide state. The
packaged PowerShell completion is at
`docs\completions\powershell\jobman.ps1` inside the installation directory.

The v1 Windows executable is not Authenticode signed. Its signed checksum and
provenance establish the downloaded bytes and Jobman release workflow, but do
not display a verified Windows publisher. Microsoft Defender SmartScreen may
therefore show **Windows protected your PC**, and Windows 11 Smart App Control
or enterprise policy may block the executable entirely. Proceed only after the
verification steps above and only where local policy permits; do not weaken a
managed device's security controls. See [Microsoft's SmartScreen reputation
guidance](https://learn.microsoft.com/en-us/windows/apps/package-and-deploy/smartscreen-reputation).

### Install a Linux package

Use the command matching the target system after verifying the package. The
package installs the binary, man page, Bash/Zsh completions, project and
third-party license notices, and a preserved sample configuration at
`/etc/jobman/jobman.yml`:

```sh
sudo apt install ./jobman_1.0.0_linux_amd64.deb
sudo dnf install ./jobman_1.0.0_linux_amd64.rpm
sudo apk add --allow-untrusted ./jobman_1.0.0_linux_amd64.apk
```

Run only one of these commands. The packaged system configuration contains safe
defaults; a user's configuration overrides it, and runtime state remains
per-user by default.

## macOS signing status

Jobman v1 does not publish a Homebrew Cask. The release archives have signed
checksums and provenance, but the macOS executable is not yet Apple Developer
ID signed and notarized. Use the verified `darwin` archive instructions above;
the manual Gatekeeper approval may therefore be required. The project
deliberately does not install an `xattr` hook that bypasses quarantine. A future
package-manager channel must add native signing and notarization plus
installation tests first.

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

Source builds require Go 1.26.5. Reproducible development and release checks
use the exact toolchain recorded in
[`go.version`](https://github.com/ryancswallace/jobman/blob/main/go.version).
Then run:

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
