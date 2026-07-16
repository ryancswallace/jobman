---
layout: default
title: Notifications and secrets
parent: User guides
nav_order: 8
permalink: /guides/notifications/
---

# Notifications and secrets

Jobman supports bounded command callbacks, HTTPS webhooks, and SMTP delivery.
Configurations persist secret references; supervisors resolve values only when
a target, probe, or notifier needs them.

## Define secret references

```yaml
schema_version: 1
secrets:
  webhook_token: env:JOBMAN_WEBHOOK_TOKEN
  smtp_password: file:/home/example/.config/jobman/smtp-password
```

An environment reference is resolved from the supervisor environment. A file
reference must be a clean absolute path; on Unix the file must be regular,
non-symlinked, and inaccessible to group and other users.

Do not put literal credentials in ordinary environment maps, HTTP headers, or
the configuration file.

## Configure a webhook

```yaml
notifiers:
  operations:
    type: http
    events: [job_succeeded, job_failed, job_timed_out]
    timeout: 10s
    retry:
      max_attempts: 3
      delay: 1s
      max_delay: 1m
    http:
      url: https://hooks.example.com/jobman
      secret_headers:
        Authorization: webhook_token
      signing_secret: webhook_token
      allow_http: false
      allow_private_hosts: false
      follow_redirects: false
```

Subscribe at submission:

```console
$ jobman run --notify operations --notify-on job_failed -- ./batch
```

Notifier defaults and named job specs can also define subscriptions. HTTP is
HTTPS-only by default and rejects local/private destinations unless explicitly
allowed. Sensitive headers must use a secret reference.

## Delivery semantics

Lifecycle events and pending deliveries are committed in the same SQLite
transaction as their state transition. Attempts are leased, bounded, and
recorded for inspection. A notification failure never changes the target job
outcome.

Retry waits consume the remaining whole-job timeout budget. Because there is no
resident notification daemon, abandoned due work is recovered when another
supervisor runs or when an operator invokes:

```console
$ jobman doctor --repair
```

Inspect delivery and attempt records with `jobman show --json JOB`.

## Redaction boundary

Configured sensitive names and bounded RE2 patterns redact Jobman diagnostics
and structured output. They do not rewrite raw target stdout or stderr. Avoid
printing secrets in targets, probes, or callback diagnostics.

The [configuration schema]({{ site.baseurl }}/reference/configuration/#secrets-and-notifiers)
documents command, HTTP, SMTP, event, and retry constraints.
