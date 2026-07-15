# Container contract

Jobman's container image is a CLI runtime, not a daemon image. A container is
also a process-lifecycle boundary: when PID 1 exits, the container runtime
stops the remaining supervisor and target processes. Detaching a Jobman job
inside a short-lived `docker run` therefore does not make that job durable
after the container exits.

## Image contract

The release image:

- runs as unprivileged UID/GID `10001`;
- uses `/work` as its working directory;
- stores configuration below `/home/jobman/.config/jobman`;
- stores metadata and logs below `/home/jobman/.local/state/jobman`;
- uses Tini as PID 1 for signal forwarding and child reaping; and
- includes Bash, BusyBox utilities, CA roots, and timezone data, but does not
  include arbitrary user target commands.

The image entry point is `jobman`. Arguments after the image name are Jobman
arguments unless the entry point is explicitly overridden.

## One container per job

Use `run --wait` when output may remain in Jobman's logs and only the final
outcome is needed. Mount a named volume if the state and logs must remain
available after the container is removed:

```console
docker volume create jobman-state
docker run --rm \
  --volume jobman-state:/home/jobman/.local/state/jobman \
  --volume "$PWD:/work" \
  ghcr.io/ryancswallace/jobman:vX.Y.Z \
  run --wait -- /work/bin/batch-job --input /work/input.json
```

Use `run --foreground` and an interactive standard-input attachment when the
target should receive input and stream output directly:

```console
docker run --rm -i \
  --volume jobman-state:/home/jobman/.local/state/jobman \
  ghcr.io/ryancswallace/jobman:vX.Y.Z \
  run --foreground -- /opt/workload/bin/interactive-job
```

Both modes keep the Jobman client—and therefore container PID 1—alive until
the managed job reaches a terminal state. Stopping or killing the container is
not equivalent to `jobman cancel`; it may prevent the supervisor from
persisting a final transition. Use the CLI to cancel important jobs before
stopping their container.

## Long-lived management container

To submit several detached jobs into one container, keep an inert PID 1 alive
and invoke Jobman with `docker exec`. The container remains daemonless with
respect to Jobman; `tail` only preserves the shared process namespace:

```console
docker run --detach --name jobman-runner \
  --volume jobman-state:/home/jobman/.local/state/jobman \
  --volume "$PWD:/work" \
  --entrypoint /sbin/tini \
  ghcr.io/ryancswallace/jobman:vX.Y.Z -- tail -f /dev/null

docker exec jobman-runner jobman run -- /work/bin/batch-job
docker exec jobman-runner jobman list --active
docker exec jobman-runner jobman logs --follow JOB
```

All management commands for those jobs must execute in the same container.
Container restart does not recreate live supervisors or target processes; use
`jobman doctor` to inspect durable state after an unexpected restart.

## Images containing target commands

Derive a workload-specific image from an immutable Jobman release. Copy or
install targets as root, then restore the unprivileged runtime user:

```dockerfile
FROM ghcr.io/ryancswallace/jobman:vX.Y.Z

USER root
COPY --chown=10001:10001 --chmod=0555 ./bin/batch-job /opt/workload/bin/batch-job
# Install only the runtime libraries the target actually requires.
USER 10001:10001
```

Do not bake secrets into the derived image. Supply secret references through
mounted owner-readable configuration or runtime environment injection, and
avoid mounting the Docker socket or granting privileged capabilities. The
target must be executable by UID `10001` and all writable target paths must be
owned by that user.

Run `make docker-smoke` to build a representative derived image, execute its
target with `run --wait`, and inspect the completed job from a second container
using the same state volume.
