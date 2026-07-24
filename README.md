<!-- markdownlint-disable MD041 -->
<!-- markdownlint-disable MD033 -->
<picture>
  <source media="(prefers-color-scheme: dark)" srcset="assets/logo-dark-transparent.svg">
  <img alt="Jobman" src="assets/logo.svg" width="420">
</picture>
<!-- markdownlint-enable MD033 -->

[![Test](https://github.com/ryancswallace/jobman/actions/workflows/test.yml/badge.svg)](https://github.com/ryancswallace/jobman/actions/workflows/test.yml)
[![Codecov](https://codecov.io/gh/ryancswallace/jobman/branch/main/graph/badge.svg)](https://codecov.io/gh/ryancswallace/jobman)
[![CodeQL](https://github.com/ryancswallace/jobman/actions/workflows/codeql.yml/badge.svg)](https://github.com/ryancswallace/jobman/actions/workflows/codeql.yml)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/ryancswallace/jobman/badge)](https://scorecard.dev/viewer/?uri=github.com/ryancswallace/jobman)
[![Latest release](https://img.shields.io/github/v/release/ryancswallace/jobman?sort=semver)](https://github.com/ryancswallace/jobman/releases/latest)
[![Go version](https://img.shields.io/github/go-mod/go-version/ryancswallace/jobman)](https://github.com/ryancswallace/jobman/blob/main/go.mod)
[![Go Reference](https://pkg.go.dev/badge/github.com/ryancswallace/jobman/jobman.svg)](https://pkg.go.dev/github.com/ryancswallace/jobman/jobman)
[![Documentation](https://img.shields.io/badge/docs-GitHub_Pages-blue)](https://ryancswallace.github.io/jobman/)

**Jobman** is a daemonless CLI for running and managing local background jobs. It
adds retries, timeouts, durable logs, dependencies, concurrency controls, and
notifications without a resident service.

## Basic demo

<!-- markdownlint-disable MD033 -->
<p align="center">
  <img src="docs/screencaps/gifs/basic-demo.gif"
       alt="A beginner Jobman terminal demo"
       width="900">
</p>
<!-- markdownlint-enable MD033 -->

Follow the [first-job guide] for a copy-paste introduction.

## Features

| Capability | Jobman provides... |
| --- | --- |
| Daemonless execution | A per-job supervisor that requires no shared service or privileged installation |
| Durable state | Inspectable job metadata and logs stored in a private per-user directory |
| Execution policies | Retries, repeated runs, backoff, delayed starts, and per-run or whole-job timeouts |
| Coordination | Dependencies, wait conditions, store-wide limits, and named concurrency pools |
| Lifecycle control | Status, wait, pause, resume, cancel, rerun, and live input |
| Logging | Separate stdout/stderr capture, following, rotation, and retention |
| Notifications | Named notifiers and event subscriptions with bounded delivery retries |
| Automation | Stable selectors, versioned JSON output, strict YAML configuration, and shell completions |

> [!NOTE]
> Jobman is a local, per-user process manager—not a distributed scheduler or
> backup system. Jobs can survive a closed terminal or SSH connection, but may
> end with the operating-system user session. Back up important state and logs
> independently.

## Quick start

Submit a detached command, inspect it, and read its output:

```console
$ job_id=$(jobman run --name example -- sh -c 'printf "hello\n"; sleep 30')
$ jobman status "$job_id"
01980f4c-7b2a-7a6f-8c10-0123456789ab  example  running
$ jobman logs --stream stdout "$job_id"
hello
$ jobman cancel "$job_id"
01980f4c-7b2a-7a6f-8c10-0123456789ab  stopping
```

Combine dependencies, retries, backoff, and concurrency controls:

```console
$ prepare=$(jobman run --name prepare -- ./prepare-data)
$ jobman run --name analyze --after-success "$prepare" \
    --slots 2 --retry-backoff exponential \
    --retry-delay 5s --retries 3 -- ./analyze
```

Target arguments are passed directly to the operating system; Jobman does not
join them into an implicit shell command. Use an explicit shell, as in the
first example, only when shell syntax is required.

## Command overview

| Task | Commands |
| --- | --- |
| Submit work | `run`, `rerun` |
| Inspect work | `list`, `status`, `show`, `logs`, `wait` |
| Control work | `pause`, `resume`, `cancel`, `input` |
| Maintain Jobman | `clean`, `doctor`, `config` |

Job selectors accept a full ID, a unique ID prefix of at least eight
characters, or an unambiguous exact name. Run `jobman COMMAND --help` for all
options, or browse the [command reference].

### Configuration and state

- Configuration is strict, versioned YAML.
- Sources merge from built-in defaults through system, user, trusted project,
  explicit-file, environment, and command-line layers.
- Project `.jobman.yml` files load only from configured trusted roots.
- `jobman config paths`, `config validate`, and `config show` explain the
  effective configuration.
- `--state-dir PATH` or `JOBMAN_STATE_DIR` selects a different local state
  directory.

See the [configuration reference], [sample configuration], and
[persisted-schema reference] for the complete contracts.

## Installation

| Method | Starting point |
| --- | --- |
| Release archive or Linux package | Download and verify an artifact from [GitHub Releases] |
| Go toolchain | `go install github.com/ryancswallace/jobman@latest` |
| Source checkout | Clone the repository, then run `make install` |
| Container | Pull `ghcr.io/ryancswallace/jobman:vX.Y.Z` |

Release artifacts include signed checksums, provenance, SBOMs, man pages, and
shell completions. Follow the [installation guide] for copy-paste installation
and verification commands.

Supported operating systems, architectures, lifecycle primitives, and known
platform differences are documented in the [platform support reference].

### Containers

Run a single managed job with a persistent state volume:

```console
docker run --rm \
  --volume jobman-state:/home/jobman/.local/state/jobman \
  --volume "$PWD:/work" \
  ghcr.io/ryancswallace/jobman:vX.Y.Z \
  run --wait -- /work/bin/batch-job
```

The base image contains Jobman and basic runtime utilities, not arbitrary
target commands. Read the [container guide] before deriving an image or
running detached work.

## Documentation

| Topic | Resource |
| --- | --- |
| Start here | [Getting started] and the [first-job guide] |
| Common workflows | [User guides] |
| CLI | [Command reference][command reference] |
| Configuration | [Configuration reference][configuration reference] |
| Platform support | [Platform support reference][platform support reference] |
| Compatibility | [Compatibility contract] |
| Containers | [Container guide][container guide] |
| Troubleshooting and recovery | [Operations guides] |
| Architecture | [Design documentation] |
| Releases | [Release and artifact verification guide] |

Use the [issue tracker] for reproducible bugs and feature proposals. Report
suspected vulnerabilities privately according to the [security policy].

## Development

Use the included devcontainer or a local Go installation:

```console
make setup
make quick-check
make check
```

`make help` lists development, test, documentation, packaging, and container
targets. See [CONTRIBUTING.md](CONTRIBUTING.md) for contribution requirements.

## License

Jobman is available under the [MIT License](LICENSE). Release binaries also
include the components and terms in
[THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).

[command reference]: https://ryancswallace.github.io/jobman/reference/commands/
[compatibility contract]: https://ryancswallace.github.io/jobman/reference/compatibility/
[configuration reference]: https://ryancswallace.github.io/jobman/reference/configuration/
[container guide]: https://ryancswallace.github.io/jobman/guides/containers/
[design documentation]: docs/design/README.md
[first-job guide]: https://ryancswallace.github.io/jobman/getting-started/first-job/
[getting started]: https://ryancswallace.github.io/jobman/getting-started/
[GitHub Releases]: https://github.com/ryancswallace/jobman/releases
[installation guide]: https://ryancswallace.github.io/jobman/getting-started/installation/
[issue tracker]: https://github.com/ryancswallace/jobman/issues
[operations guides]: https://ryancswallace.github.io/jobman/operations/
[persisted-schema reference]: docs/design/PERSISTED_SCHEMA.md
[platform support reference]: https://ryancswallace.github.io/jobman/reference/platforms/
[release and artifact verification guide]: RELEASE.md
[sample configuration]: etc/jobman/jobman.yml
[security policy]: SECURITY.md
[user guides]: https://ryancswallace.github.io/jobman/guides/
