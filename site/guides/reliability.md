---
layout: default
title: Retries and timeouts
parent: User guides
nav_order: 2
permalink: /guides/reliability/
---

# Retries, repetition, and timeouts

Jobman separates completion limits, result classification, retry eligibility,
delay, and time budgets. This makes policies expressive, but every unbounded
choice should be deliberate.

## A bounded retry policy

```console
$ jobman run \
    --retries 3 \
    --retryable-exit-code 1 \
    --retryable-exit-code 2 \
    --retry-timeouts \
    --retry-backoff exponential \
    --retry-delay 5s \
    --retry-max-delay 1m \
    --retry-jitter 1s \
    --run-timeout 10m \
    --job-timeout 45m \
    -- ./import-data
```

`--retries 3` permits four total attempts: the initial run and three retries.
Only classified retryable failures and, here, run timeouts schedule another
attempt. Jitter is bounded around the computed delay and never bypasses the
maximum delay or whole-job deadline.

## Completion limits

The general policy uses three independent limits:

- `--max-runs`: maximum attempts of any outcome;
- `--success-target`: required number of successful runs; and
- `--failure-limit`: failures allowed before the job must terminate.

The shorthand `--retries N` configures the common “one success with N retries”
case. Use the general flags for repeated successful work:

```console
$ jobman run \
    --max-runs 8 \
    --success-target 5 \
    --failure-limit 4 \
    --repeat-delay 30s \
    -- ./sample-once
```

At least five successes are required, no more than eight total runs may start,
and the fourth failure ends the job. Limits accept `unlimited` where the
command reference permits it, but a policy with no achievable terminal bound
should also have a whole-job timeout or explicit operator cancellation plan.

## Exit-code classification

Exit code `0` is successful by default. Repeat `--success-exit-code` to define
another success set and `--retryable-exit-code` to distinguish failures that
may retry. A nonretryable failure does not schedule another run even when run
capacity remains.

Signals and platform reasons are recorded factually. Cross-platform policy
should prefer exit codes because native termination details differ.

## Two timeout budgets

- `--run-timeout` bounds one target execution and can be retryable with
  `--retry-timeouts`.
- `--job-timeout` bounds the whole job, including prerequisites, admission,
  delays, runs, and notification retry waits.

Pause time does not consume active run or job timeout budget. A whole-job
timeout is terminal; it does not start another run.

## Stop escalation and retry cutoffs

`--stop-grace` controls the delay between graceful and forced tree-wide
termination. Keep `--force-after-grace` enabled unless leaving an unresponsive
target alive is explicitly preferable.

Use `--retry-abort-at RFC3339` when no new retry may start after an absolute
operational cutoff. This differs from `--job-timeout`, which is measured from
the accepted job lifecycle.

Inspect the effective immutable policy with `jobman show --json JOB` before
using a complex policy in production.
