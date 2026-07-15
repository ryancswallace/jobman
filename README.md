# Jobman

![Jobman logo](assets/logo.png)

[![Test](https://github.com/ryancswallace/jobman/actions/workflows/test.yml/badge.svg)](https://github.com/ryancswallace/jobman/actions/workflows/test.yml)
[![Codecov](https://codecov.io/gh/ryancswallace/jobman/branch/main/graph/badge.svg)](https://codecov.io/gh/ryancswallace/jobman)
[![CodeQL](https://github.com/ryancswallace/jobman/actions/workflows/codeql.yml/badge.svg)](https://github.com/ryancswallace/jobman/actions/workflows/codeql.yml)
[![OpenSSF Scorecard](https://api.scorecard.dev/projects/github.com/ryancswallace/jobman/badge)](https://scorecard.dev/viewer/?uri=github.com/ryancswallace/jobman)
[![Latest release](https://img.shields.io/github/v/release/ryancswallace/jobman?sort=semver)](https://github.com/ryancswallace/jobman/releases/latest)
[![Go version](https://img.shields.io/github/go-mod/go-version/ryancswallace/jobman)](https://github.com/ryancswallace/jobman/blob/main/go.mod)
[![Go Reference](https://pkg.go.dev/badge/github.com/ryancswallace/jobman.svg)](https://pkg.go.dev/github.com/ryancswallace/jobman)
[![Documentation](https://img.shields.io/badge/docs-GitHub_Pages-blue)](https://ryancswallace.github.io/jobman/)

Jobman is a daemonless command-line job manager. It is being designed to run
and monitor commands with retries, timeouts, durable logs, delayed execution,
and success or failure notifications without requiring a resident service.

> [!WARNING]
> Jobman remains a prerelease project. Its planned v1 public surface is frozen,
> but the release candidate is not stable until all native race, architecture,
> fuzz, performance, container, upgrade, and dogfood gates pass on the exact
> release commit. Do not use prerelease builds as the sole manager for critical
> workloads; retain independent backups and a direct recovery path.

## Design goals

- Work without a system daemon or privileged installation.
- Survive terminal disconnects and propagate signals predictably.
- Keep job state and logs inspectable from ordinary CLI commands.
- Make retry, timeout, waiting, and notification policies composable.
- Remain useful as a native binary, package-manager installation, or container.

The target command, state, and configuration model is documented in the
[design specification](docs/design/SPEC.md). Generated man pages and shell
completions are included in release archives.

## Available today

The current pre-1.0 implementation supports detached direct commands, durable
inspection, repeated-run policies, prerequisites, local concurrency limits,
timeouts, retained logs, lifecycle control, and notifications:

```console
$ jobman run --name example --retries 2 --run-timeout 1m -- \
    sh -c 'printf "hello\\n"; sleep 30'
01980f4c-7b2a-7a6f-8c10-0123456789ab
$ jobman status 01980f4c
01980f4c-7b2a-7a6f-8c10-0123456789ab  example  running
$ jobman logs --stream stdout 01980f4c
hello
$ jobman cancel 01980f4c
01980f4c-7b2a-7a6f-8c10-0123456789ab  stopping
```

The implemented commands are `run`, `list`, `status`, `show`, `logs`, `cancel`,
`pause`, `resume`, `wait`, `input`, `rerun`, `clean`, `doctor`, and `config`.
Inspection commands support versioned JSON where documented by `--help`.
Selectors accept a canonical ID, a unique ID prefix of at least eight
characters, or an unambiguous exact name. Target arguments are passed directly
to the operating system and are never joined into an implicit shell command.

`run` can combine bounded or explicitly unlimited retry/repetition rules,
constant/linear/exponential delay, per-run and whole-job timeouts, named or
direct wait conditions, immutable outcome dependencies, a store-wide slot
limit and one named pool, log capture/rotation/retention, and named notification
subscriptions. For example:

```console
$ prepare=$(jobman run --name prepare -- ./prepare-data)
$ jobman run --name analyze --after-success "$prepare" \
    --slots 2 --retry-backoff exponential \
    --retry-delay 5s --retries 3 -- ./analyze
```

Use `--wait` to block for a terminal outcome or `--foreground` to attach local
input and both output streams while the per-job supervisor remains the process
owner. A detached job submitted with `--stdin live` accepts binary standard
input from `jobman input JOB`; input bytes are not persisted or replayed. Copy
an existing effective specification with `jobman run --rerun JOB` (or the
standalone `jobman rerun JOB` command).

By default, metadata and logs live in the platform's private per-user state
directory. Use `--state-dir PATH` or `JOBMAN_STATE_DIR` to select another local
directory. SQLite metadata and raw log files are implementation compatibility
surfaces described in the [persisted-schema reference].

Configuration is strict, versioned YAML. System and per-user files are loaded
automatically, while a project `.jobman.yml` is loaded only from a root listed
in `trusted_project_roots`; `--config PATH` explicitly selects a file. Run
`jobman config paths`, `jobman config validate`, or `jobman config show` to
inspect the result. `jobman run` and `jobman rerun` synchronize durable
concurrency settings from their effective configuration before submission; use
`jobman config apply` to apply the same settings without submitting a job. The
[configuration reference] and packaged [sample configuration] document safe
defaults and reusable job, wait, pool, secret-reference, notifier, and profile
examples.

Linux has assembled-binary lifecycle and crash-boundary coverage. Native
macOS/Windows CI exercises detachment, process-tree cancellation, pause/resume,
and private live input. The [platform-capability record] describes the native
primitives and deliberate differences. Complete the [dogfood runbook] before a
stable release.

## Installation

### Release archives and native packages

Download a release from the [GitHub Releases page]. Portable archives use names
such as `jobman_<version>_linux_amd64.tar.gz` and
`jobman_<version>_windows_arm64.zip`. Linux packages use the same platform suffix
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

A detached submission cannot outlive a short-lived container: when the Jobman
PID 1 client exits, the container runtime stops its remaining supervisor and
target processes. Use `run --wait` or `run --foreground` for a one-container
job, and persist metadata and logs with a named volume:

```console
docker run --rm \
	--volume jobman-state:/home/jobman/.local/state/jobman \
  --volume "$PWD:/work" \
  ghcr.io/ryancswallace/jobman:vX.Y.Z \
  run --wait -- /work/bin/batch-job
```

The base image deliberately contains only Jobman and basic runtime utilities;
derive an image to add the actual commands your jobs execute. The full
[container contract] documents foreground use, a long-lived management
container, state ownership, derived images, and shutdown limitations.

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
- [Configuration reference](docs/CONFIGURATION.md)
- [v1 compatibility contract](docs/COMPATIBILITY.md)
- [Upgrade and restore guide](docs/UPGRADING.md)
- [Container contract](docs/CONTAINERS.md)
- [Dogfood and release-candidate runbook](docs/DOGFOOD.md)
- [Design documentation](docs/design/README.md)
- [Release and artifact verification guide](RELEASE.md)
- [Security policy](SECURITY.md)
- [Maintenance and support policy](SUPPORT.md)
- [Issue tracker](https://github.com/ryancswallace/jobman/issues)

Please use the issue templates for reproducible bugs and feature proposals.
Report suspected vulnerabilities privately as described in the security policy.

## License

Jobman is available under the [MIT License](LICENSE).

[GitHub Releases page]: https://github.com/ryancswallace/jobman/releases
[persisted-schema reference]: docs/design/PERSISTED_SCHEMA.md
[platform-capability record]: docs/design/PLATFORM_CAPABILITIES.md
[configuration reference]: docs/CONFIGURATION.md
[sample configuration]: etc/jobman/jobman.yml
[compatibility contract]: docs/COMPATIBILITY.md
[dogfood runbook]: docs/DOGFOOD.md
[container contract]: docs/CONTAINERS.md
