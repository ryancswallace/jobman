---
layout: default
title: Lifecycle controls
parent: User guides
nav_order: 6
permalink: /guides/lifecycle/
---

# Lifecycle controls

Lifecycle commands operate through durable state and revalidated native
process identity. They never target a PID solely because it was recorded in the
past.

## Inspect before changing

```console
$ jobman status JOB
$ jobman show JOB
$ jobman list --active
```

Use JSON output when a script must decide which operation is valid. Repeating a
completed or conflicting lifecycle operation returns a stable conflict error
rather than silently changing unrelated state.

## Cancel

```console
$ jobman cancel JOB
$ jobman cancel job JOB
$ jobman cancel run JOB -1
```

`cancel JOB` and `cancel job JOB` are equivalent. Cancellation prevents future
runs. In v1, canceling the selected active run also cancels its owning job, so a
retry does not follow.

The supervisor records cancellation intent, requests a graceful tree-wide
stop, waits for the job's grace period, and optionally forces termination.
Native signal details differ; see [Platform support]({{ site.baseurl }}/reference/platforms/).

## Pause and resume

```console
$ jobman pause JOB
$ jobman resume JOB
```

Pause/resume is a best-effort platform feature implemented for the managed
process tree. Pause duration does not consume run or whole-job timeout budget.
The target might still interact with external systems immediately before the
native suspension takes effect, so pause is not a transactional application
checkpoint.

Queued or prerequisite-waiting work can also be paused. Resume returns it to
its prior phase.

## Wait without taking ownership

```console
$ jobman wait JOB
```

`wait` blocks the client until the durable job is terminal. It does not own the
target and closing the waiting client does not cancel the job. The command's
result reflects the managed job outcome according to the CLI contract.

## Rerun immutably

```console
$ jobman rerun JOB
$ jobman rerun --name replacement JOB
$ jobman run --rerun JOB --wait
```

Rerun submits a new job using the prior effective immutable specification. It
does not mutate or reopen the original history. Policy flags are rejected when
cloning through `run --rerun`; only documented submission controls such as the
new name and waiting behavior may differ.

## Recover uncertain state

If a supervisor or host stopped unexpectedly, inspect first:

```console
$ jobman doctor --json
$ jobman show --json JOB
```

Use `doctor --repair` only after reviewing the report. Recovery checkpoints the
WAL, reconciles provably stale ownership, and wakes due notification work; it
does not invent a successful target result.
