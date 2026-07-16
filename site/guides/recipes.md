---
layout: default
title: Common recipes
parent: User guides
nav_order: 10
permalink: /guides/recipes/
---

# Common recipes

These examples show small compositions of the policies described in the
feature guides. Replace placeholder executables, paths, and release versions
with values appropriate for your host.

## Retry a flaky download

```console
$ jobman run --name download \
    --retries 4 \
    --retryable-exit-code 1 \
    --retry-backoff exponential \
    --retry-delay 2s \
    --retry-max-delay 30s \
    --run-timeout 5m \
    --job-timeout 20m \
    --pool downloads \
    -- curl --fail --location --output artifact.tar https://example.com/artifact.tar
```

## Build only after preparation succeeds

```console
$ prepare=$(jobman run --name prepare -- ./prepare)
$ build=$(jobman run --name build --after-success "$prepare" -- ./build)
$ jobman wait "$build"
```

## Capture bounded logs

```console
$ jobman run \
    --log-capture both \
    --log-segment-bytes 8388608 \
    --log-segments 4 \
    --log-retention 7d \
    -- ./verbose-task
```

## Schedule repeated successful samples

```console
$ jobman run \
    --max-runs 8 \
    --success-target 5 \
    --failure-limit 4 \
    --repeat-delay 1m \
    -- ./sample-once
```

## Submit JSON-friendly automation

Capture the canonical ID returned by `run`, then use versioned JSON for
inspection:

```console
$ job=$(jobman run --name automation -- ./task)
$ jobman status --json "$job"
$ jobman show --json "$job"
```

Do not parse human columns or depend on UUID prefixes in automation.

## Back up before an upgrade

```console
$ jobman doctor --json
$ jobman doctor --backup "$HOME/jobman-before-upgrade.db"
```

Stop concurrent Jobman invocations before replacing the binary, then follow the
[upgrade runbook]({{ site.baseurl }}/operations/upgrading/).
