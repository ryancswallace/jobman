## Development Prerequisites
* Install `pyenv`
* Install `poetry`

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
Use `docker run` to pull and run the latest jobman Docker image from Docker Hub:
```bash
docker run -it jobman
```

To build the Docker image locally instead of pulling from Docker Hub, use the `docker-image` make target:
```bash
make docker-image
```

### Build from source
Building jobman from source code requires [Go](https://golang.org/doc/install) version 1.15 or greater.

Start by cloning the repository:
```bash
git clone https://github.com/ryancswallace/jobman.git
cd jobman
```

Then build and install the jobman binary under your `GOPATH` using make:
```bash
make install
```

The Makefile provides several other targets for convenience while developing, including:
* *format*: formats the source code
* *test*: runs the jobman test suite, including unit tests, end-to-end tests, performance tests, and linters
* *build*: builds the `jobman` binary for the current platform

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