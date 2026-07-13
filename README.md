# Jobman

![Jobman logo](assets/logo.png)

[![Test](https://github.com/ryancswallace/jobman/actions/workflows/test.yml/badge.svg)](https://github.com/ryancswallace/jobman/actions/workflows/test.yml)
[![CodeQL](https://github.com/ryancswallace/jobman/actions/workflows/codeql.yml/badge.svg)](https://github.com/ryancswallace/jobman/actions/workflows/codeql.yml)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/ryancswallace/jobman/badge)](https://scorecard.dev/viewer/?uri=github.com/ryancswallace/jobman)
[![Codecov](https://codecov.io/gh/ryancswallace/jobman/branch/main/graph/badge.svg)](https://codecov.io/gh/ryancswallace/jobman)
[![Go Report Card](https://goreportcard.com/badge/github.com/ryancswallace/jobman)](https://goreportcard.com/report/github.com/ryancswallace/jobman)
[![Go Reference](https://pkg.go.dev/badge/github.com/ryancswallace/jobman.svg)](https://pkg.go.dev/github.com/ryancswallace/jobman)
[![Documentation](https://img.shields.io/badge/docs-GitHub_Pages-blue)](https://ryancswallace.github.io/jobman/)

Jobman is a daemonless command-line job manager. It is being designed to run
and monitor commands with retries, timeouts, durable logs, delayed execution,
and success or failure notifications without requiring a resident service.

> [!WARNING]
> Jobman is under active development. The command surface and configuration
> format are not yet stable, and the current implementation does not provide
> every capability described in the design documentation. Evaluate it before
> using it for important workloads.

## Design goals

- Work without a system daemon or privileged installation.
- Survive terminal disconnects and propagate signals predictably.
- Keep job state and logs inspectable from ordinary CLI commands.
- Make retry, timeout, waiting, and notification policies composable.
- Remain useful as a native binary, package-manager installation, or container.

The target command and configuration model is documented in
[docs/design](docs/design/README.md). Generated man pages and shell completions
are included in release archives.

## Installation

### Release archives and native packages

Download a release from the [GitHub Releases page]. Portable archives use names
such as `jobman_0.1.0_linux_amd64.tar.gz` and
`jobman_0.1.0_windows_arm64.zip`. Linux packages use the same platform suffix
with `.apk`, `.deb`, or `.rpm` extensions.

Verify downloaded artifacts using the checksum and Sigstore instructions in
[RELEASE.md](RELEASE.md#verifying-a-release).

### Homebrew

The generated Cask lives in this repository, so add it as an explicit custom
tap before installation:

```console
brew tap ryancswallace/jobman https://github.com/ryancswallace/jobman
brew install --cask jobman
```

### Container image

Versioned Linux images are published to GitHub Container Registry:

```console
docker pull ghcr.io/ryancswallace/jobman:vX.Y.Z
docker run --rm ghcr.io/ryancswallace/jobman:vX.Y.Z --help
```

The image runs as an unprivileged user, uses `/work` as its working directory,
and includes Bash, CA roots, timezone data, and Tini. Mount a working directory
when a managed command needs access to host files:

```console
docker run --rm \
  --volume "$PWD:/work" \
  ghcr.io/ryancswallace/jobman:vX.Y.Z --help
```

Pin a release tag in automation. The `latest` tag is updated only for stable
releases and is intended for interactive evaluation.

### Build from source

Building Jobman requires [Go](https://go.dev/doc/install) 1.26.5.

```console
git clone https://github.com/ryancswallace/jobman.git
cd jobman
make install
```

To install the latest tagged version without cloning the repository:

```console
go install github.com/ryancswallace/jobman@latest
```

Run `make help` to see all development, validation, packaging, and container
targets.

## Development

The fastest contributor setup is the included devcontainer. For a local Go
installation:

```console
make setup
make format
make check
```

`make check` verifies modules, formatting, lint, workflows, shell scripts,
known vulnerabilities, tests, generated documentation, spelling, the local
binary, the GoReleaser configuration, and every declared release build target.
See [CONTRIBUTING.md](CONTRIBUTING.md) for the development and pull-request
conventions.

## Documentation and support

- [Documentation site](https://ryancswallace.github.io/jobman/)
- [Design documentation](docs/design/README.md)
- [Release and artifact verification guide](RELEASE.md)
- [Security policy](SECURITY.md)
- [Issue tracker](https://github.com/ryancswallace/jobman/issues)

Please use the issue templates for reproducible bugs and feature proposals.
Report suspected vulnerabilities privately as described in the security policy.

## License

Jobman is available under the [MIT License](LICENSE).

[GitHub Releases page]: https://github.com/ryancswallace/jobman/releases
