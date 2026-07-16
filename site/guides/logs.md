---
layout: default
title: Logs and retention
parent: User guides
nav_order: 5
permalink: /guides/logs/
---

# Logs, rotation, and retention

Jobman can capture stdout, stderr, both streams, or neither. Captured bytes are
stored raw; an index records their observed ordering for combined output.

## Read and follow output

```console
$ jobman logs JOB
$ jobman logs --stream stderr JOB
$ jobman logs --follow JOB
$ jobman logs --lines 50 JOB
$ jobman logs --run -1 JOB
$ jobman logs --all JOB
```

`--run` accepts a positive run number or a negative index, where `-1` is the
latest retained run. `--all` reads every retained run and cannot be combined
with `--follow`. Use `--raw` to omit presentation headers when reading several
runs.

## Select capture and rotation

```console
$ jobman run \
    --log-capture both \
    --log-segment-bytes 16777216 \
    --log-segments 8 \
    --log-retention 14d \
    -- ./verbose-task
```

Rotation bounds each stream using segment size and segment count. When a cap
removes an older segment, Jobman preserves explicit metadata rather than
presenting the remaining bytes as a complete log.

`--log-capture none` does not redirect target output into Jobman storage. With
`--foreground`, current output is attached to the terminal while configured
capture remains independently available for later inspection.

## Clean safely

Cleanup is dry-run by default:

```console
$ jobman clean
$ jobman clean --older-than 30d
$ jobman clean --dry-run=false --force
```

Policy cleanup evaluates configured age, per-job run and byte limits, store
job limits, and total log bytes. It prunes completed-run logs before deleting
eligible metadata and never removes active state. Metadata remains while an
unresolved dependency, notification, or admission needs it.

Explicit `--older-than` cleanup does not require valid configuration. Do not
delete files below the state root directly.

## Integrity and secrets

`show --json` reports log availability, sizes, index version, integrity,
recording health, diagnostics, and pruning details. An unindexed tail after a
crash is not treated as valid ordered output.

Captured stdout and stderr are intentionally raw. Configuration redaction
protects Jobman diagnostics and structured output, not bytes written by the
target. Keep secrets out of target logs.
