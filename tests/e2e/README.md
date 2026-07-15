# End-to-end tests

End-to-end tests should build and execute the real Jobman binary in isolated
temporary directories. They must not depend on developer configuration, shared
state, internet access, or timing assumptions tighter than the supported
platforms can guarantee.

The Linux suite also builds an opt-in fault-injection binary and terminates
Jobman at every durable process/log boundary. The macOS and Windows suite uses
the assembled binary for detachment, tree cancellation, pause/resume, and live
input. Hosted native execution is part of `.github/workflows/test.yml`.

Prioritize lifecycle transitions, terminal disconnects, signal handling,
concurrent access, retries, timeouts, log following, and interrupted-write
recovery. Run the suite with `make e2e`. Complete the manual scenarios in
[`docs/DOGFOOD.md`](../../docs/DOGFOOD.md) for a release candidate.
