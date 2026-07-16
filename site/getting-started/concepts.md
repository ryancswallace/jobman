---
layout: default
title: Core concepts
parent: Getting started
nav_order: 3
permalink: /getting-started/concepts/
---

# Core concepts

Jobman is local, per-user, and daemonless. Understanding its ownership model
makes retries, process control, containers, and recovery much easier to reason
about.

## Job, run, and target

A **job** is an immutable command specification plus durable lifecycle and
policy state. A job can contain several **runs** when retry or repetition
policy permits it. Each run starts one **target** process tree.

- A retry follows a failed, timed-out, or optionally start-failed run.
- A repetition follows a successful run until its success target or run limit
  is reached.
- A job receives one terminal outcome after its policy can no longer schedule
  another run.

The immutable specification records arguments, environment changes, waits,
exit-code policy, retry limits, timeouts, log policy, dependencies, admission,
and notification subscriptions. Resolved secret values are not persisted in
that specification.

## One supervisor per job

Submission starts a small supervisor dedicated to one job. The supervisor
claims durable ownership, evaluates prerequisites, acquires concurrency slots,
starts and monitors targets, records transitions, and delivers notifications.
It exits when the job is terminal.

There is no shared resident Jobman daemon. Closing the submitting terminal or
SSH connection does not stop a correctly detached supervisor, but ending the
entire operating-system user session may do so. Container boundaries impose
additional limits described in the [container guide]({{ site.baseurl }}/guides/containers/).

## Durable state and raw logs

Metadata is stored in a private SQLite database. Captured stdout and stderr are
private filesystem files with an ordered index. The state root is selected in
this order:

1. `--state-dir PATH`;
2. `JOBMAN_STATE_DIR`; and
3. the platform's per-user state location.

State must be on a local filesystem with working SQLite WAL locks. Jobman
rejects known remote or distributed filesystems. Do not edit the database,
move active state, or delete log files by hand.

## Explicit state transitions

Commands record intent before producing destructive side effects. Repeated
cancel, cleanup, and recovery operations are designed to converge without
inventing success. If Jobman cannot prove what happened across a crash or
process boundary, it records a `lost` outcome rather than guessing.

Use [`jobman doctor`]({{ site.baseurl }}/reference/commands/doctor/) to inspect
store integrity and `doctor --repair` for the deliberately conservative repair
set.

## Direct execution, not an implicit shell

This command executes `printf` directly:

```console
$ jobman run -- printf '%s\n' 'literal $HOME'
```

The `$HOME` text remains literal. Shell expansion occurs only when you request
a shell explicitly:

```console
$ jobman run -- sh -c 'printf "%s\n" "$HOME"'
```

This boundary prevents quoting ambiguities and accidental command injection.

## Configuration and authority

Configuration is layered and strict, but not every command is allowed to
change durable scheduler settings. `run`, `rerun`, `config apply`, and
policy-based `clean` are configuration-authority paths. Inspection,
lifecycle-emergency commands, `doctor`, and explicit age-based cleanup remain
usable even when configuration is malformed.

Read the [configuration guide]({{ site.baseurl }}/guides/configuration/) before
introducing named job specs, profiles, pools, secrets, or notifiers.
