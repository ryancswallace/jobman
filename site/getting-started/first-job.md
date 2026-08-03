---
layout: default
title: Your first job
parent: Getting started
nav_order: 2
permalink: /getting-started/first-job/
---

# Your first job

If Jobman is installed and available on `PATH`, you can submit a managed
command, inspect its durable result, and read its captured output in about a
minute.

## Run your first job

These commands are the same in a POSIX shell and PowerShell:

```text
jobman run --wait --name first-job -- jobman --version
jobman status first-job
jobman logs --stream stdout first-job
```

The first command submits `jobman --version` as the target, prints the new
job's stable ID, and waits for it to finish. `status` reads the recorded
lifecycle result, and `logs` reads the target's captured standard output even
though the process has already exited.

The example adds one completed job to your normal per-user history. If
`first-job` is already an ambiguous name in your store, use a different name
or the ID printed by `run`.

Everything after `--` is the target executable and its arguments. Jobman
preserves those argument boundaries and does not invoke a shell unless the
shell is the explicit executable.

## Keep the remaining examples separate

The rest of this tutorial can use a temporary state directory so it does not
add more jobs to your normal history. Setting this variable starts a separate,
empty Jobman store for the current shell.

On Linux or macOS:

```console
export JOBMAN_STATE_DIR="$(mktemp -d)"
```

In PowerShell:

```powershell
$env:JOBMAN_STATE_DIR = Join-Path $env:TEMP ("jobman-docs-" + [guid]::NewGuid())
```

`JOBMAN_STATE_DIR` and the global `--state-dir PATH` option select the same
local store. Do not point two hosts at one state directory.

## Try a longer command

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

The POSIX example requests a shell explicitly because it uses a command list.
The Windows example invokes PowerShell explicitly for the same reason.

## Inspect the job in more detail

```console
$ jobman status hello
$ jobman show --json hello
$ jobman logs --stream stdout hello
hello from Jobman
```

`status` is a concise human view. `show` contains the immutable specification,
run history, runtime counters, dependencies, admission, and notification
delivery state. Use `--json` output rather than human columns in automation.

## Wait without owning the job

```console
jobman wait hello
jobman status hello
```

`wait` attaches to the durable job lifecycle; it does not become the job's
owner. Closing the waiting terminal does not cancel the job.

The opening example combines submission and waiting with `run --wait`. You can
also attach the current terminal's input and output to a new job:

```console
jobman run --foreground -- ./interactive-command
```

Foreground mode still leaves the per-job supervisor as the process owner. It
uses ordinary pipes rather than a pseudo-terminal.

## Try a cancellation

On Linux or macOS:

```console
jobman run --name disposable -- sleep 60
jobman cancel disposable
jobman wait disposable
```

In PowerShell:

```powershell
jobman run --name disposable -- powershell.exe -NoProfile -Command 'Start-Sleep 60'
jobman cancel disposable
jobman wait disposable
```

Cancellation is durable intent. Jobman requests a graceful tree-wide stop,
then forces termination after the configured grace period when
`--force-after-grace` is enabled.

## Clean up

The opening example is a completed job in your normal store. Preview and then
remove it with:

```console
jobman clean first-job
jobman clean first-job --force
```

Omit the second command if you want to keep the record.

The temporary state directory may be removed after every tutorial job is
terminal and no Jobman process is using it. For any other real store, use
[`jobman clean`]({{ site.baseurl }}/reference/commands/clean/) instead of
deleting files by hand.

Next, read [Core concepts]({{ site.baseurl }}/getting-started/concepts/) or
configure [retries and timeouts]({{ site.baseurl }}/guides/reliability/).
