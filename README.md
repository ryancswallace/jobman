![jobman](https://github.com/ryancswallace/jobman/raw/main/assets/logo.png?raw=true)

Jobman automates the process of running and monitoring jobs on the command line. Jobman supports
* running commands in the background immune to hangups
* retrying commands
* aborting commands after a timeout period
* logging command output
* sending notifications on command success or failure
* delaying command execution for a specified time or event

![Build Status](https://github.com/ryancswallace/jobman/actions/workflows/test.yml/badge.svg)
[![codecov](https://codecov.io/gh/ryancswallace/jobman/branch/main/graph/badge.svg)](https://codecov.io/gh/ryancswallace/jobman)
[![Go Report Card](https://goreportcard.com/badge/github.com/ryancswallace/jobman)](https://goreportcard.com/report/github.com/ryancswallace/jobman)
[![Docs site](https://img.shields.io/badge/docs-GitHub_Pages-blue)](https://ryancswallace.github.io/jobman/)
[![GoDoc](https://godoc.org/gotest.tools?status.svg)](https://pkg.go.dev/github.com/ryancswallace/jobman)

# Documentation
**Visit the :book: [jobman documentation site](https://ryancswallace.github.io/jobman/) :book: for complete information on using jobman.**

For package implementation details, see the [jobman page](https://pkg.go.dev/github.com/ryancswallace/jobman) in the Go reference.

# Example
The example below uses jobman to run a Python script `train.py` in the background and immune to hangups (e.g., a SIGHUP from an SHH timeout).

Jobman will ensure 60 seconds have passed *and* that the file `data.csv` exists before starting the program. If those conditions haven't been met by 5:00PM on March 5, 2032, jobman will abort the job.

Jobman will retry the program up to five times until there's one successful run, defined as an exit code of `0` or `42`, waiting ten seconds between retries.

If the job succeeds, jobman will send a notification email. If the job fails, jobman will send an SMS message.
```bash
jobman \
    -wait.timedelta 60s -wait.file data.csv -wait.abort-datetime "2032-03-05T17:00:00" \
    -retries.num-successes 1 -retries.num-runs 5 -retries.success-codes 0,42 -retries.delay 10s \
    -notify.on-success my-email -notify.on-failure my-cell \
    train.py
```

After submitting the `train.py` job above, use `jobman show` to display details on job progress:
```bash
jobman show train.py
```

To view a running log of the consolidated stdout and stderr streams of the latest run of the `train.py` job, use `jobman logs`:
```bash
jobman logs train.py -follow
```

# Installation
There are multiple ways to install jobman. The recommended method is to use the RPM, deb, or apk package, if applicable, or a precompiled binary otherwise.

### Package manager packages
Jobman is available via RPM, deb, and apk packages as `jobman_<version>-_linux_(amd64|386).(rpm|deb|apk)`. Download packages for the latest jobman version from the [latest releases page](https://github.com/ryancswallace/jobman/releases/latest) on the GitHub repository.

### Precompiled binaries
Precompiled binaries are available for Linux, MacOS, and Windows as `jobman_(Linux|Darwin|Windows)_<(x86_64|i386)>.tar.gz`. Download binaries for the latest jobman version from the [latest releases page](https://github.com/ryancswallace/jobman/releases/latest) on the GitHub repository.

### Docker image
Use a versioned image from GitHub Container Registry for reproducible runs:

```shell
docker run --rm \
  --user "$(id -u):$(id -g)" \
  --env HOME=/tmp \
  --env XDG_CONFIG_HOME=/tmp/.config \
  --volume "$PWD:/work" \
  ghcr.io/ryancswallace/jobman:latest --help
```

The image runs as an unprivileged user with `/work` as its working directory.
It includes Bash, CA certificates, timezone data, and Tini so child jobs receive
signals correctly. Override the command after the image name to run a jobman
subcommand. Pin a release tag in automation; `latest` is intended for interactive
evaluation only.

To build the image locally instead, use the `docker-image` make target:

```shell
make docker-image
docker run --rm \
  --user "$(id -u):$(id -g)" \
  --env HOME=/tmp \
  --env XDG_CONFIG_HOME=/tmp/.config \
  --volume "$PWD:/work" \
  jobman --help
```

### Build from source
Building jobman from source requires [Go](https://go.dev/doc/install) 1.26.

Start by cloning the repository:
```bash
git clone https://github.com/ryancswallace/jobman.git
cd jobman
```

Then build and install the jobman binary under your `GOPATH` using make:
```bash
make install
```

The Makefile automates the common development workflows. Start with `make help`
for the complete target list. The primary targets are:

- `make setup`: installs pinned tools and downloads dependencies;
- `make format`: formats Go source with the configured formatters;
- `make check`: runs module, format, lint, test, documentation, build, and
  release-configuration checks;
- `make test`: runs unit, end-to-end, and performance suites;
- `make docs`: generates and validates man pages and shell completions;
- `make build`: builds `bin/jobman` for the current platform;
- `make docker-image`: builds the local container image;
- `make snapshot`: builds release artifacts locally without publishing them.

# Alternatives
Jobman aims to be reliable and fully-featured. It operates *without* requiring a system service/daemon for orchestration.

cron
Airflow/prefect/dagster
supervisord
https://github.com/kadwanev/retry
https://github.com/linyows/go-retry
https://github.com/martinezdelariva/retry

# Contributing
Feature requests, bug reports, and pull requests are welcome! See [CONTRIBUTING.md](https://github.com/ryancswallace/jobman/blob/main/CONTRIBUTING.md) for details on how to contribute to jobman.
