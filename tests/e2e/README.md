# End-to-end tests

End-to-end tests should build and execute the real Jobman binary in isolated
temporary directories. They must not depend on developer configuration, shared
state, internet access, or timing assumptions tighter than the supported
platforms can guarantee.

Prioritize lifecycle transitions, terminal disconnects, signal handling,
concurrent access, retries, timeouts, log following, and interrupted-write
recovery. Run the suite with `make e2e`.
