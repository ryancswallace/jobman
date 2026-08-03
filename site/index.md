---
layout: default
title: Home
nav_order: 1
permalink: /
---

# Jobman

Jobman runs and manages durable command-line jobs without a continuously
running shared daemon. It combines detached execution, retries, timeouts,
dependencies, concurrency limits, retained logs, lifecycle controls, live
input, and notifications in one local CLI.

## Get productive quickly

- Read [Why Jobman]({{ site.baseurl }}/why-jobman/) and the
  [comparison guide]({{ site.baseurl }}/guides/comparison/) to decide whether
  it fits your workflow.
- [Install Jobman]({{ site.baseurl }}/getting-started/installation/) using the
  recommended package or archive for your operating system. The guide also
  covers Go, source, and container-based installation.
- Follow [Your first job]({{ site.baseurl }}/getting-started/first-job/) to
  submit a command and inspect its result.
- Read [Core concepts]({{ site.baseurl }}/getting-started/concepts/) to
  understand jobs, runs, supervisors, and durable state.
- Browse the generated [`jobman` command reference]({{ site.baseurl }}/reference/commands/).
- Use the [configuration guide]({{ site.baseurl }}/guides/configuration/) and
  [schema reference]({{ site.baseurl }}/reference/configuration/) for reusable
  policies.

## What Jobman manages

- **Execution:** detached or foreground direct commands with preserved argument
  boundaries and explicit standard-input policy.
- **Reliability:** bounded retries, repeated successful runs, backoff, jitter,
  per-run timeouts, and whole-job deadlines.
- **Scheduling:** time, file, executable-probe, and prior-job dependencies plus
  store-wide and named-pool concurrency limits.
- **Observability:** durable state, raw stdout/stderr capture, log following,
  rotation, retention, versioned JSON, and health checks.
- **Control:** wait, cancel, rerun, best-effort pause/resume, and private live
  input using the native mechanism documented for each platform.
- **Integration:** strict layered YAML, secret references, command callbacks,
  HTTPS webhooks, and SMTP notifications.

## Basic examples

The following recordings walk through common Jobman workflows, from running a
single command to coordinating dependencies, sending input, and handling failures.

### Run and inspect a job

Submit a named job, list it, follow its logs, and inspect its recorded state.
Job _selectors_ accept a complete ID, a unique ID prefix of at least eight
characters, or an unambiguous exact name.

<video controls muted playsinline autoplay loop preload="metadata" width="100%">
  <source src="{{ site.baseurl }}/assets/videos/basic.webm" type="video/webm">
  <a href="{{ site.baseurl }}/assets/videos/basic.webm">Watch the basic job demo.</a>
</video>

### Coordinate dependent jobs

Make one job wait for another to succeed, then pause and resume the prerequisite
while following both jobs' progress.

<video controls muted playsinline autoplay loop preload="metadata" width="100%">
  <source src="{{ site.baseurl }}/assets/videos/dependencies.webm" type="video/webm">
  <a href="{{ site.baseurl }}/assets/videos/dependencies.webm">Watch the job dependencies demo.</a>
</video>

### Send live input

Start a job with live standard input, send it a line, close the input stream,
and review the resulting job state and logs.

<video controls muted playsinline autoplay loop preload="metadata" width="100%">
  <source src="{{ site.baseurl }}/assets/videos/input.webm" type="video/webm">
  <a href="{{ site.baseurl }}/assets/videos/input.webm">Watch the live input demo.</a>
</video>

### Retry failures

Watch Jobman run a failing command with two configured retries, wait for it to
finish, and report the final result.

<video controls muted playsinline autoplay loop preload="metadata" width="100%">
  <source src="{{ site.baseurl }}/assets/videos/retries.webm" type="video/webm">
  <a href="{{ site.baseurl }}/assets/videos/retries.webm">Watch the retries demo.</a>
</video>

### Enforce a run timeout

Apply a five-second timeout to a long-running command, wait for the timeout,
and inspect the completed job.

<video controls muted playsinline autoplay loop preload="metadata" width="100%">
  <source src="{{ site.baseurl }}/assets/videos/timeouts.webm" type="video/webm">
  <a href="{{ site.baseurl }}/assets/videos/timeouts.webm">Watch the run timeout demo.</a>
</video>

## License

Jobman is available under the [MIT License](https://github.com/ryancswallace/jobman/blob/main/LICENSE).
Release binaries also include the applicable
[third-party notices](https://github.com/ryancswallace/jobman/blob/main/THIRD_PARTY_NOTICES.md).
