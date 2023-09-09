---
title: Home
layout: home
nav_order: 1
description: "Jobman is a command line job manager with flexible support for retries, timeouts, logging, notifications, and more."
permalink: /
---
# Jobman
{: .fs-9 }

A command line job manager with flexible support for retries, timeouts, logging, notifications, and more.
{: .fs-6 .fw-300 }

[Package on PyPI][Jobman PyPI]{: .btn .fs-5 .mb-4 .mb-md-0 .text-center }
[Code on GitHub][Jobman repo]{: .btn .fs-5 .mb-4 .mb-md-0 .text-center }

----

Jobman saves you time and frustration on the command line. Using Jobman to **run** a command, your command
* runs in the background immune to hangups
* logs stdout and stderr
* retrying commands
* aborting commands after timeout
* delaying command execution for a specified time or event
* sending notifications on command success or failure

## Requirements
Jobman runs on UNIX-like operating systems including Linux and MacOS. Windows is not supported.

Jobman requires Python3.9+.
## Installation
Install or upgrade from PyPI with
```
$ pip install -U jobman
```

## Example
The example below uses jobman to run a Python script `train.py` in the background and immune to hangups (e.g., a SIGHUP from an SSH timeout).

Jobman will ensure 60 seconds have passed *and* that the file `data.csv` exists before starting the program. If those conditions haven't been met by 5:00PM on March 5, 2032, jobman will abort the job.

Jobman will retry the program up to five times until there's one successful run, defined as an exit code of `0` or `42`, waiting ten seconds between retries.

If the job succeeds, jobman will send a notification email. If the job fails, jobman will send an SMS message.
```bash
$ jobman \
    --wait-duration 60s --wait-for-file data.csv \
    --abort-time "2032-03-05T17:00:00" \
    --retry-attempts 5 --retry-delay 10s -c 0 -c 42  \
    --notify-on-job-success my-email --notify-on-job-failure my-cell \
    train.py
12e4b604
```

After submitting the `train.py` job above, use `jobman show` to display details on job progress:
```bash
jobman show 12e4b604
```

To view a running log of the consolidated stdout and stderr streams of the latest run of the job, use `jobman logs`:
```bash
jobman logs 12e4b604 --follow
```

[Jobman repo]: https://github.com/ryancswallace/jobman
[Jobman PyPI]: https://pypi.org/project/jobman