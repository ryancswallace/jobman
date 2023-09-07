![jobman](https://github.com/ryancswallace/jobman/raw/main/assets/logo.png?raw=true)

Jobman automates the process of running and monitoring jobs on the command line. Jobman supports
* running commands in the background immune to hangups
* logging command output
* retrying commands
* aborting commands after timeout
* delaying command execution for a specified time or event
* sending notifications on command success or failure

![Build Status](https://github.com/ryancswallace/jobman/actions/workflows/test.yml/badge.svg)
[![codecov](https://codecov.io/gh/ryancswallace/jobman/branch/main/graph/badge.svg)](https://codecov.io/gh/ryancswallace/jobman)
[![Docs site](https://img.shields.io/badge/docs-GitHub_Pages-blue)](https://ryancswallace.github.io/jobman/)

# Documentation
**Visit the :book: [jobman documentation site](https://ryancswallace.github.io/jobman/) :book: for complete information on using jobman.**

# Requirements
Jobman runs on UNIX-like operating systems including Linux and MacOS. Windows is not supported.

Jobman requires Python3.9+.
# Installation
Install or upgrade from PyPI with
```
$ pip install jobman
```

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
jobman logs train.py --follow
```

# Alternatives
Jobman aims to be reliable and fully-featured. It operates *without* requiring a system service/daemon for orchestration.

Alternative tools for similar use cases include:
* **cron**: for scheduling repeated executions of a job
* **Airflow**, **Prefect**, and **Dagster**: for managing dependencies between multiple jobs
* **supervisord**: for daemon-based job management

# Developing
Jobman uses pyenv for Python version management and poetry for builds. Before working on Jobman, ensure you have `pyenv` and `poetry` installed.

The `Makefile` defines targets for common operations during development, including setting up and installing the package (`make setup`), running the autoformatters (`make fmt`), running the type tests and unit test suite (`make test`), and building the jobman wheel (`make build`).

# Contributing
Feature requests, bug reports, and pull requests are welcome! See [CONTRIBUTING.md](https://github.com/ryancswallace/jobman/blob/main/CONTRIBUTING.md) for details on how to contribute to jobman.