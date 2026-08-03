---
layout: default
title: Why Jobman
nav_order: 2
permalink: /why-jobman/
---

# Reliable background jobs without a shared daemon

Jobman turns an ordinary command into a durable, inspectable local job. It
keeps the direct-command simplicity of a CLI while adding the lifecycle policy
that long-running work usually accumulates in shell scripts: retries, timeouts,
dependencies, concurrency limits, retained logs, and notifications.

Use it when a command should keep running after you close the terminal or lose
an SSH connection—and when you need to know what happened afterward.

[Install Jobman]({{ site.baseurl }}/getting-started/installation/){: .btn .btn-primary }
[Compare the alternatives]({{ site.baseurl }}/guides/comparison/){: .btn }

## The gap Jobman fills

A background process is easy to start. Making it reliable is the harder part.
A typical script soon needs to answer questions such as:

- Did the command start, fail, time out, or lose its supervisor?
- Which attempt produced this output?
- Should a particular failure retry, and how long should the next attempt wait?
- How do several independent commands share a limited local resource?
- Can another job wait for this one without polling a PID or parsing a log?
- How does an operator inspect, cancel, rerun, or clean up the work later?

Jobman records those answers in private per-user state instead of leaving each
script to invent its own PID files, retry loops, log naming, and cleanup rules.

## From a command to a managed job

This example gives an import up to three retries, exponential backoff, and a
20-minute limit for each attempt:

```console
$ jobman run \
    --name import-data \
    --retries 3 \
    --retryable-exit-code 1 \
    --retry-backoff exponential \
    --retry-delay 5s \
    --run-timeout 20m \
    -- ./import-data
```

The command returns a stable job ID. The name is also usable while it remains
unambiguous:

```console
$ jobman status import-data
$ jobman logs --follow import-data
$ jobman wait import-data
```

Jobman executes the target directly and preserves its argument boundaries. It
does not silently place the command inside a shell; request a shell explicitly
when you need shell expansion or pipelines.

## What Jobman adds

### Durable answers

Each job has immutable execution policy, explicit lifecycle transitions, and
one or more recorded runs. Metadata remains inspectable after the target exits,
and captured stdout and stderr are retained separately with rotation and
retention controls.

### Bounded reliability policy

Classify successful and retryable exit codes, bound the total attempts, choose
constant, linear, or exponential backoff, add jitter, and set per-run or
whole-job timeouts. Delays and limits are recorded with the job rather than
hidden in a wrapper script.

### Local coordination

Jobs can wait for a time, a file, an executable probe, or another job. A local
state store can enforce a global active-work limit and named concurrency pools
across otherwise independent Jobman invocations.

### Operator controls

Inspect status, follow logs, wait for completion, cancel, rerun, and use
best-effort pause and resume where the platform supports them. Detached jobs
can opt into a private local input endpoint without exposing a network API.

### Automation-friendly output

Stable selectors, versioned JSON, strict layered YAML, predictable exit
statuses, and shell completions make the same workflow usable by a person or a
script.

## Where Jobman fits well

- long-running commands launched over SSH;
- local data imports, model runs, builds, downloads, and maintenance tasks;
- developer and workstation automation that needs history and logs;
- several local jobs that must respect dependencies or shared capacity; and
- portable workflows that should behave consistently on Linux, macOS, and
  Windows.

## Where another tool fits better

Jobman is deliberately local and per-user. It is not a cluster scheduler,
distributed queue, backup system, boot-time service manager, or interactive
terminal multiplexer. It does not perform CPU or GPU discovery, cross-host
resource placement, preemption, or fair-share scheduling.

Jobs can survive a closed terminal or SSH connection, but ending the entire
operating-system user session may terminate them. Important metadata and logs
still need an independent backup policy.

Read the [comparison guide]({{ site.baseurl }}/guides/comparison/) for a
task-based choice among Jobman, `nohup`, tmux, and `systemd-run`.

## No shared Jobman service to operate

Submission starts a small supervisor dedicated to that job. It owns the target
process tree, updates durable state, applies policy, and exits when the job is
terminal. There is no continuously running shared Jobman daemon, privileged
installation requirement, or remote-control listener.

Start with [Your first job]({{ site.baseurl }}/getting-started/first-job/),
browse the [source on GitHub](https://github.com/ryancswallace/jobman), or read
the [contribution guide]({{ site.baseurl }}/project/contributing/).
