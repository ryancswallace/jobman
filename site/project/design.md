---
layout: default
title: Design overview
parent: Project
nav_order: 1
permalink: /project/design/
---

# Design overview

Jobman is designed for users who need more lifecycle control than shell
backgrounding provides but do not want to install or operate a shared service.
The architecture favors local durability, explicit policy, and process-boundary
correctness over distributed scheduling.

## Product boundaries

- One user owns one local state store.
- One short-lived supervisor owns each nonterminal job.
- Target commands execute directly with the invoking user's privileges.
- Metadata is durable SQLite state; logs are raw filesystem streams with an
  ordered index.
- The CLI is the supported public API. Internal Go packages are not an SDK.
- Network filesystems and cross-host store sharing are outside v1.

## Platform and process model

Unix targets use dedicated process groups. Windows targets start suspended,
join a named Job Object, and then resume. Every lifecycle operation revalidates
PID, process creation identity, and boot identity before acting.

The portable contract is tree-wide control, not identical native signals.
Closing a terminal or SSH connection is supported; ending the entire user
session may terminate work. See the evidence-backed
[platform matrix]({{ site.baseurl }}/reference/platforms/).

## Storage model

SQLite transactions record lifecycle intent and state transitions. Raw stream
files avoid placing unbounded logs in the database. Synchronous index records
make ordered output auditable across crash boundaries. A result that cannot be
proven becomes `lost`, never guessed successful.

The [compatibility contract]({{ site.baseurl }}/reference/compatibility/)
defines the CLI, JSON, configuration, notification, immutable specification,
and forward schema surfaces supported throughout v1.

## Security model

Jobman is not a sandbox. Target commands, probes, and notifier callbacks run
with the user's authority. Security controls focus on private state, safe path
handling, direct argument boundaries, bounded work, revalidated process
identity, secret references, redacted diagnostics, and signed release
artifacts.

## Architectural decisions

The two foundational decisions are:

1. a per-job supervisor instead of a resident shared daemon; and
2. SQLite metadata with filesystem log streams.

Read the complete [specification, ADRs, and persisted schema](https://github.com/ryancswallace/jobman/tree/main/docs/design)
in the source repository. Those documents record rationale and implementation
constraints; this site documents the supported user experience.
