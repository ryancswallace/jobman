---
title: Home
nav_order: 1
---

# Jobman

Jobman is a daemonless command-line job manager under active development. The
current pre-1.0 implementation manages detached direct commands with retries,
timeouts, prerequisites, local concurrency admission, durable logs, lifecycle
controls, Unix private live input, and success or failure notifications without
a resident service.

{: .warning }
The command surface and configuration format are not yet stable. Evaluate the
current release before using it for important workloads.

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
- pause/resume on supported Unix-like systems and Unix private live input;
- strict layered YAML configuration with named job specs and profiles; and
- bounded command, HTTPS webhook, and SMTP notifications.

The command and configuration formats remain pre-1.0. Linux has native core
lifecycle evidence; macOS and Windows builds are maintained, but documented
platform and fault-injection acceptance work remains before stable support.

## Install from source

Jobman currently requires Go 1.26.5:

```console
git clone https://github.com/ryancswallace/jobman.git
cd jobman
make install
```

Release archives, native Linux packages, signed checksums, SBOMs, and signed
container images are published through [GitHub Releases] as the release
pipeline becomes available.

[GitHub Releases]: https://github.com/ryancswallace/jobman/releases
