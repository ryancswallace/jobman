---
layout: default
title: Your first job
parent: Getting started
nav_order: 2
permalink: /getting-started/first-job/
---

# Your first job

This tutorial submits a direct command, inspects its durable state and logs,
and waits for completion. It uses a temporary state directory so the example
does not affect your normal history.

## Choose a disposable state directory

On Linux or macOS:

```console
$ export JOBMAN_STATE_DIR="$(mktemp -d)"
```

In PowerShell:

```powershell
$env:JOBMAN_STATE_DIR = Join-Path $env:TEMP ("jobman-docs-" + [guid]::NewGuid())
```

`JOBMAN_STATE_DIR` and the global `--state-dir PATH` option select the same
local store. Do not point two hosts at one state directory.

## Submit a command

On a POSIX system:

```console
$ jobman run --name hello -- sh -c 'printf "hello from Jobman\n"; sleep 2'
01980f4c-7b2a-7a6f-8c10-0123456789ab
```

On Windows:

```powershell
$job = jobman run --name hello -- powershell.exe -NoProfile -Command 'Write-Output "hello from Jobman"; Start-Sleep 2'
```

The returned UUIDv7 is the canonical job ID. Save it in automation. Humans can
also select this job using a unique prefix of at least eight characters or the
unambiguous exact name `hello`.

Everything after `--` is the target executable and its arguments. Jobman
preserves the argument boundaries and does not invoke a shell unless the shell
is the explicit executable, as it is in the POSIX example.

## Inspect state and logs

```console
$ jobman status hello
$ jobman show --json hello
$ jobman logs --stream stdout hello
hello from Jobman
```

`status` is a concise human view. `show` contains the immutable specification,
run history, runtime counters, dependencies, admission, and notification
delivery state. Use `--json` output rather than human columns in automation.

## Wait for the terminal result

```console
$ jobman wait hello
$ jobman status hello
```

`wait` attaches to the durable job lifecycle; it does not become the job's
owner. Closing the waiting terminal does not cancel the job.

You can combine submission and waiting:

```console
$ jobman run --wait --name immediate -- printf 'done\n'
```

Use `--foreground` instead when you also want target input and both output
streams attached to the current terminal.

## Try a cancellation

```console
$ jobman run --name disposable -- sleep 60
$ jobman cancel disposable
$ jobman wait disposable
```

Cancellation is durable intent. Jobman requests a graceful tree-wide stop,
then forces termination after the configured grace period when
`--force-after-grace` is enabled.

## Remove the example

The entire temporary state directory may be removed after every example job is
terminal and no Jobman process is using it. For a real store, use
[`jobman clean`]({{ site.baseurl }}/reference/commands/clean/) instead of
deleting files by hand.

Next, read [Core concepts]({{ site.baseurl }}/getting-started/concepts/) or
configure [retries and timeouts]({{ site.baseurl }}/guides/reliability/).
