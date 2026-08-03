# End-to-end tests

End-to-end tests should build and execute the real Jobman binary in isolated
temporary directories. They must not depend on developer configuration, shared
state, internet access, or timing assumptions tighter than the supported
platforms can guarantee.

The Linux suite also builds an opt-in fault-injection binary and terminates
Jobman at every durable process/log boundary. The macOS and Windows suite uses
the assembled binary for detachment, tree cancellation, pause/resume, and live
input. Hosted native execution is part of `.github/workflows/test.yml`.

`TestAssembledBinaryRunFlagContracts` is the executable contract for the
public `jobman run` surface. It combines a full persisted-policy overlay with
focused lifecycle scenarios for configured job specs and profiles, input and
attachment, retries and timeouts, waits, logs, notifications, reruns, and
invalid flag combinations. A coverage guard derives the public flags from
`jobman run --help` and fails when a flag has no assigned behavior scenario or
when a stale flag remains in the matrix.

Prioritize lifecycle transitions, terminal disconnects, signal handling,
concurrent access, retries, timeouts, log following, and interrupted-write
recovery. Run the suite with `make e2e`. Complete the manual scenarios in
[`docs/DOGFOOD.md`](../../docs/DOGFOOD.md) for a release candidate.
