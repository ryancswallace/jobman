---
title: Home
nav_order: 1
---

# Jobman

Jobman is a daemonless command-line job manager under active development. It is
being designed for retries, timeouts, durable logs, delayed execution, and
success or failure notifications without a resident service.

{: .warning }
The command surface and configuration format are not yet stable. Evaluate the
current release before using it for important workloads.

## Start here

- [Project overview and installation](https://github.com/ryancswallace/jobman#readme)
- [Design and target behavior](https://github.com/ryancswallace/jobman/tree/main/docs/design)
- [Release verification](https://github.com/ryancswallace/jobman/blob/main/RELEASE.md)
- [Contributing](https://github.com/ryancswallace/jobman/blob/main/CONTRIBUTING.md)
- [Security policy](https://github.com/ryancswallace/jobman/blob/main/SECURITY.md)

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
