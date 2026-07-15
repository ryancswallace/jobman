---
title: Home
nav_order: 1
---

# Jobman

Jobman is a daemonless command-line job manager approaching v1. The current
release-candidate implementation manages detached direct commands with retries,
timeouts, prerequisites, local concurrency admission, durable logs, lifecycle
controls, private local live input, and success or failure notifications
without a resident service.

{: .warning }
The planned v1 command and configuration contracts are frozen, but a build is
not a stable v1 release until its exact commit passes native CI, release
packaging, upgrade, and dogfood evidence. Keep independent backups and a direct
recovery path when evaluating prerelease builds.

## Start here

- [Project overview and installation](https://github.com/ryancswallace/jobman#readme)
- [Configuration reference](https://github.com/ryancswallace/jobman/blob/main/docs/CONFIGURATION.md)
- [Design and target behavior](https://github.com/ryancswallace/jobman/tree/main/docs/design)
- [Release verification](https://github.com/ryancswallace/jobman/blob/main/RELEASE.md)
- [Contributing](https://github.com/ryancswallace/jobman/blob/main/CONTRIBUTING.md)
- [Security policy](https://github.com/ryancswallace/jobman/blob/main/SECURITY.md)

## Current capabilities

- retry and repeated-run limits with bounded backoff and jitter;
- time, file, probe, and prior-job prerequisites;
- store-wide and named-pool concurrency slots;
- per-run and whole-job timeouts;
- raw stream capture, rotation, follow, retention, and guarded cleanup;
- best-effort pause/resume and private local live input on supported platforms;
- strict layered YAML configuration with named job specs and profiles; and
- bounded command, HTTPS webhook, and SMTP notifications.

Linux has the full assembled lifecycle and crash-boundary suite. Native macOS
and Windows CI exercises detachment, process-tree control, private live input,
permissions, and release-style builds. Stable support still requires those
native jobs and the documented dogfood runbook to pass on the exact release
commit.

## Install from source

Jobman currently requires Go 1.26.5:

```console
git clone https://github.com/ryancswallace/jobman.git
cd jobman
make install
```

Release archives, native Linux packages, signed checksums, SBOMs, provenance,
and signed container images are published through [GitHub Releases].

[GitHub Releases]: https://github.com/ryancswallace/jobman/releases
