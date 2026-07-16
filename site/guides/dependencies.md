---
layout: default
title: Dependencies and waits
parent: User guides
nav_order: 3
permalink: /guides/dependencies/
---

# Dependencies and wait conditions

Dependencies order jobs by immutable terminal outcomes. Wait conditions gate a
job on time, files, or direct executable probes. All prerequisites are recorded
before the first run and are included in the whole-job timeout budget.

## Depend on another job

```console
$ prepare=$(jobman run --name prepare -- ./prepare-data)
$ jobman run --name analyze --after-success "$prepare" -- ./analyze
```

Available predicates include:

- `--after-success JOB`;
- `--after-finish JOB`;
- `--after-failed JOB`; and
- `--after-outcome JOB=OUTCOME[,OUTCOME...]`.

Selectors resolve to canonical job IDs during submission. Later reuse of the
same display name cannot redirect an existing dependency. Cycles and invalid
references are rejected.

If a dependency reaches a terminal outcome that can never satisfy its
predicate, the dependent job aborts rather than waiting forever.

## Time and file waits

```console
$ jobman run \
    --wait-delay 30s \
    --wait-until 2032-03-05T17:00:00Z \
    --wait-file /srv/data/ready \
    --wait-mode all \
    --wait-abort-at 2032-03-05T18:00:00Z \
    -- ./consume-data
```

`--wait-mode all` requires every condition; `any` proceeds after the first
condition succeeds. `--wait-poll` controls polling frequency. Delay waits are
measured from job acceptance, not from each process invocation.

File waits observe existence and configured file kind. They do not lock or
snapshot the file; the target remains responsible for validating data it
consumes.

## Named probes

Executable probes are configured as named wait conditions so their command,
timeout, environment, output bound, and failure behavior remain reviewable:

```yaml
wait_conditions:
  service_ready:
    type: probe
    probe:
      command: [/usr/bin/curl, --fail, --silent, https://example.com/ready]
      timeout: 10s
      poll_interval: 2s
      output_limit: 64KiB
      fatal_on_error: false
```

Use it with `jobman run --wait-condition service_ready -- ./consumer`.
Probes execute directly without an implicit shell. Their bounded diagnostic
output is state, not target log output.

## Admission follows prerequisites

Jobman evaluates dependencies and waits before acquiring concurrency slots.
Waiting work therefore does not consume execution capacity. If the whole-job
deadline expires while waiting for a prerequisite or slot, the job times out
without starting a target.
