---
layout: default
title: Foreground and live input
parent: User guides
nav_order: 7
permalink: /guides/live-input/
---

# Foreground execution and live input

Standard input is explicit. Ordinary detached jobs receive null input so they
cannot unexpectedly compete for the submitting terminal.

## Foreground mode

```console
$ jobman run --foreground -- ./interactive-command
```

Foreground mode attaches the current input and both output streams while the
per-job supervisor remains the process owner. It also waits for the terminal
outcome. Terminal-specific features such as a pseudo-terminal are not implied;
the target receives ordinary pipes.

Use foreground mode for interactive evaluation and for a one-job container
whose PID 1 must remain alive.

## Detached live input

Submit a target with a private local input endpoint:

```console
$ jobman run --stdin live --name consumer -- ./consume-stream
$ printf 'one record\n' | jobman input consumer
$ jobman input --eof consumer
```

`jobman input JOB` copies stdin bytes to the active target. `--eof` closes the
target's input after any supplied bytes. The request is bound to the active run
identity so bytes cannot be redirected to a later retry accidentally.

Input bytes are not persisted, replayed, or included in logs. A partial write
uses a distinct process exit status so automation can detect that the target
received only part of the payload.

## File input

```console
$ jobman run --stdin-file /srv/input/request.json -- ./consumer
```

The supervisor opens the file for the target. Use an absolute, stable path and
retain the file until the run starts. File input and live input are mutually
exclusive.

## Platform security

Unix uses an owner-only local socket. Windows uses a named pipe protected for
the current user, SYSTEM, and administrators. The endpoint is local to the host
and is not a network API.

Treat anyone able to invoke Jobman with the same user identity as trusted to
control that user's jobs. Do not expose the state directory or input endpoint
through broad filesystem or container mounts.
