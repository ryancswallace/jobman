---
layout: default
title: Concurrency and pools
parent: User guides
nav_order: 4
permalink: /guides/concurrency/
---

# Concurrency and pools

Jobman uses durable local admission records to limit active work across
independent CLI invocations. There is one store-wide slot capacity and optional
named pools.

## Configure capacities

```yaml
schema_version: 1
concurrency:
  max_active_slots: 8
  pools:
    downloads: 2
    experiments: 4
```

Apply without submitting a job:

```console
$ jobman config validate
$ jobman config apply
```

`run`, `rerun`, and policy-based cleanup also synchronize effective durable
capacities. Removing a pool fails while active admission records still refer
to it.

## Request slots

```console
$ jobman run --slots 2 --pool experiments -- ./train-model
```

The request consumes two global slots and two slots from `experiments`. A
request that exceeds a finite configured capacity is rejected instead of
waiting for impossible admission.

Jobs without `--pool` consume only global slots. `--slots` defaults to one.

## Fairness and leases

Eligible jobs wait in durable admission order. A supervisor renews its
admission lease while it owns an active run. Slots are released after the run,
including on terminal failure or cancellation; stale ownership is reconciled
conservatively after its lease expires.

Admission limits active runs, not the number of submitted jobs or supervisors.
Prerequisite and retry-delay time does not consume slots.

## Local scope

Concurrency is scoped to one state store on one host. Jobman does not provide a
distributed scheduler, cross-host lease service, or network filesystem
coordination. Use an external orchestrator when capacity must be shared between
machines.

Inspect a job's current admission with `jobman show --json JOB` and store health
with `jobman doctor --json`.
