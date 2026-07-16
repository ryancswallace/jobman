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

{: .warning }
These pages document the current `main` branch and latest prerelease. Jobman is
not a stable v1 release yet. Keep independent backups and a direct recovery
path when evaluating it with important workloads.

## Get productive quickly

- [Install Jobman]({{ site.baseurl }}/getting-started/installation/) from a
  release, Homebrew, a container image, or source.
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
- **Control:** wait, cancel, rerun, and best-effort pause/resume and live input
  where the platform can implement them safely.
- **Integration:** strict layered YAML, secret references, command callbacks,
  HTTPS webhooks, and SMTP notifications.

## A small example

```console
$ jobman run --name report --retries 2 --run-timeout 5m -- ./build-report quarterly.csv
01980f4c-7b2a-7a6f-8c10-0123456789ab
$ jobman status 01980f4c
01980f4c-7b2a-7a6f-8c10-0123456789ab  report  running
$ jobman logs --follow 01980f4c
```

Job selectors accept a complete ID, a unique ID prefix of at least eight
characters, or an unambiguous exact name. Target arguments after `--` are
passed directly to the operating system; Jobman does not construct an implicit
shell command.

## Choose the next topic

| Goal | Documentation |
| --- | --- |
| Make execution resilient | [Retries, repetition, and timeouts]({{ site.baseurl }}/guides/reliability/) |
| Order related jobs | [Dependencies and wait conditions]({{ site.baseurl }}/guides/dependencies/) |
| Bound local resource use | [Concurrency and pools]({{ site.baseurl }}/guides/concurrency/) |
| Inspect or prune output | [Logs and retention]({{ site.baseurl }}/guides/logs/) |
| Operate an active job | [Lifecycle controls]({{ site.baseurl }}/guides/lifecycle/) |
| Diagnose state | [Troubleshooting]({{ site.baseurl }}/operations/troubleshooting/) |
| Upgrade safely | [Upgrading and restoring]({{ site.baseurl }}/operations/upgrading/) |
| Check platform behavior | [Platform support]({{ site.baseurl }}/reference/platforms/) |

Jobman is available under the [MIT License](https://github.com/ryancswallace/jobman/blob/main/LICENSE).
