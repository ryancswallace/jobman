# v1 dogfood and release-candidate runbook

This runbook gathers the manual evidence required in addition to automated
tests. Run it against the exact release-candidate commit and artifacts on
dedicated Linux, macOS, and Windows test accounts. Do not use production jobs,
credentials, configuration, or a shared home directory.

## Automated baseline

Run the automated acceptance suites before beginning the manual campaign:

```console
make e2etest
make perftest
make soaktest SOAK_TIME=10m
```

On Linux, `TestAssembledBinaryDogfoodContracts` exercises the assembled binary
through preflight health and backup restore, start failures and dependency
predicates, wait-state controls, named-pool and multi-slot admission, log
following, repeated-run live input, configuration failure and redaction,
notification exhaustion, and cleanup dry-run/force behavior. The adjacent
assembled-binary lifecycle and crash-boundary tests cover terminal detachment,
process trees, retries, timeouts, rotation, stale ownership, and durable crash
points. Native macOS and Windows lifecycle tests and the performance/soak
suites provide the remaining automated platform and scale baseline.

These tests deliberately do not replace evidence that requires an independent
SSH client, real release packages, network filesystems, controlled remote
SMTP/HTTPS services, supported-prior-release artifacts, or long-running
resource observation. A release campaign must still perform every applicable
manual step below, and core live-input tests must not be skipped on release
hosts.

## Runbook conventions and evidence helpers

Commands in this guide operate only on disposable state. Run them from a source
checkout of the exact release commit, but set `JOBMAN_BIN` to the unpacked or
installed release-candidate executable. Do not substitute `go run`. On Unix,
copy and paste this block once in every new terminal or SSH session, replacing
the two marked values:

```sh
cd /path/to/jobman-release-commit
export JOBMAN_BIN=/absolute/path/to/release-candidate/jobman
export CAMPAIGN="$HOME/jobman-dogfood-$(date -u +%Y%m%dT%H%M%SZ)"
export STATE_DIR="$CAMPAIGN/state"
export EVIDENCE_DIR="$CAMPAIGN/evidence"
mkdir -p "$STATE_DIR" "$EVIDENCE_DIR"
chmod 700 "$CAMPAIGN" "$STATE_DIR" "$EVIDENCE_DIR"
export CAPTURE="$PWD/devel/dogfood/capture.sh"
export TREE_HELPER="$PWD/devel/dogfood/unix-process-tree.sh"
jm() { "$JOBMAN_BIN" --state-dir "$STATE_DIR" "$@"; }
capture() { "$CAPTURE" "$EVIDENCE_DIR" "$@"; }
printf 'CAMPAIGN=%s\nSTATE_DIR=%s\nJOBMAN_BIN=%s\n' \
  "$CAMPAIGN" "$STATE_DIR" "$JOBMAN_BIN"
```

For a second terminal, reuse the printed `CAMPAIGN`, `STATE_DIR`, and
`JOBMAN_BIN` values instead of creating a new campaign. `capture LABEL COMMAND
...` writes the exact argument vector, separate stdout/stderr files, UTC times,
and exit status under `EVIDENCE_DIR`. It refuses to overwrite an existing
label. Never pass real secrets through it, and review all evidence before
sharing it.

On native Windows PowerShell, initialize a campaign with:

```powershell
Set-Location C:\path\to\jobman-release-commit
$JobmanBin = 'C:\absolute\path\to\jobman.exe'
$Campaign = Join-Path $HOME ('jobman-dogfood-' + (Get-Date).ToUniversalTime().ToString('yyyyMMddTHHmmssZ'))
$StateDir = Join-Path $Campaign 'state'
$EvidenceDir = Join-Path $Campaign 'evidence'
New-Item -ItemType Directory -Force $StateDir, $EvidenceDir | Out-Null
function jm { & $JobmanBin --state-dir $StateDir @args }
Write-Host "Campaign: $Campaign"
```

PowerShell examples use `Tee-Object` for evidence. Run the Windows campaign in
PowerShell, not WSL, MSYS2, or a Linux container, because those environments do
not exercise native process, named-pipe, path, and ACL behavior.

## 1. Prepare the test campaign

For each operating system, record:

- OS name, version, architecture, filesystem type, shell/terminal, and whether
  the session is local or SSH;
- release commit, archive/package name, checksum verification result, and
  `jobman --version`;
- test start/end time and operator; and
- a fresh absolute state directory and configuration file used only for this
  campaign.

Verify the release commit first:

```sh
capture source-check make check
capture source-snapshot make snapshot
```

On the test host, install from the release-candidate artifact rather than
`go run`. Set `STATE_DIR` to a new local-disk directory. Run:

```sh
capture binary-version "$JOBMAN_BIN" --version
capture config-validate "$JOBMAN_BIN" --state-dir "$STATE_DIR" config validate
capture config-origins "$JOBMAN_BIN" --state-dir "$STATE_DIR" config show --origins
capture doctor-initial "$JOBMAN_BIN" --state-dir "$STATE_DIR" doctor --json

uname -a >"$EVIDENCE_DIR/platform.txt"
uname -m >>"$EVIDENCE_DIR/platform.txt"
id >>"$EVIDENCE_DIR/platform.txt"
df -T "$STATE_DIR" >>"$EVIDENCE_DIR/platform.txt" 2>/dev/null || \
  df "$STATE_DIR" >>"$EVIDENCE_DIR/platform.txt"
git rev-parse HEAD >"$EVIDENCE_DIR/release-commit.txt"
```

Expected: configuration is valid, the store is healthy, the current schema is
supported, and foreign-key violations are zero. Repeat `doctor` with a path on
a known network filesystem and record the expected rejection; never continue
testing on that state root.

On macOS, add this platform detail:

```sh
sw_vers >>"$EVIDENCE_DIR/platform.txt"
diskutil info "$(df "$STATE_DIR" | awk 'END {print $1}')" \
  >>"$EVIDENCE_DIR/platform.txt"
```

On Windows, capture the corresponding evidence:

```powershell
& $JobmanBin --version | Tee-Object (Join-Path $EvidenceDir 'binary-version.txt')
jm config validate | Tee-Object (Join-Path $EvidenceDir 'config-validate.txt')
jm config show --origins | Tee-Object (Join-Path $EvidenceDir 'config-origins.json')
jm doctor --json | Tee-Object (Join-Path $EvidenceDir 'doctor-initial.json')
Get-ComputerInfo | Out-File (Join-Path $EvidenceDir 'platform.txt')
Get-Volume | Format-List | Out-File (Join-Path $EvidenceDir 'volumes.txt')
```

Mount or map a disposable network filesystem, set `NETWORK_STATE_DIR`, then
confirm that Jobman rejects it. A zero exit status is a release blocker:

```sh
export NETWORK_STATE_DIR=/mounted/network/path/jobman-dogfood-state
if "$JOBMAN_BIN" --state-dir "$NETWORK_STATE_DIR" doctor --json \
    >"$EVIDENCE_DIR/network-state.stdout" \
    2>"$EVIDENCE_DIR/network-state.stderr"; then
  echo 'FAIL: Jobman accepted a network state directory' >&2
  exit 1
fi
```

## 2. Basic lifecycle and terminal disconnect

Submit a gated command that writes distinct stdout/stderr markers, waits for a
file, and then exits with zero. Record the job ID. In another terminal confirm
`status`, `show --json`, and growing `logs` work. Close the submitting terminal
entirely, create the gate file from the second terminal, and verify terminal
outcome `success` and exact raw stream bytes.

Repeat from an SSH session:

1. Connect to the same disposable account and submit a job waiting on a gate.
2. Record the ID in a second independent session.
3. Terminate the SSH client process or network connection without logging out
   the whole OS user session.
4. Release the gate from the second session.
5. Verify the target completed and no Jobman process retained the first PTY or
   terminal handles.

Also run commands that exit 0, exit nonzero, receive cancellation, exceed a run
timeout, fail to start, and produce an unterminated final output fragment.
Confirm each documented outcome and exit fact in `show --json`.

### Unix terminal-disconnect recipe

In terminal A, submit a job which cannot finish until terminal B creates its
gate. The command prints one line to each stream plus an unterminated fragment:

```sh
export GATE="$CAMPAIGN/release-basic"
rm -f "$GATE"
BASIC_ID=$(jm run --name dogfood-disconnect -- sh -c '
  printf "stdout-line\nstdout-fragment"
  printf "stderr-line\nstderr-fragment" >&2
  while [ ! -e "$1" ]; do sleep 1; done
' jobman-dogfood "$GATE")
printf 'BASIC_ID=%s\n' "$BASIC_ID"
jm status "$BASIC_ID"
exit
```

Close terminal A completely. In terminal B, initialize `jm` with the same
paths, replace `BASIC_ID` with the printed value, and run:

```sh
export BASIC_ID=replace-with-recorded-job-id
export GATE="$CAMPAIGN/release-basic"
capture basic-running "$JOBMAN_BIN" --state-dir "$STATE_DIR" show --json "$BASIC_ID"
touch "$GATE"
capture basic-wait "$JOBMAN_BIN" --state-dir "$STATE_DIR" wait "$BASIC_ID"
capture basic-final "$JOBMAN_BIN" --state-dir "$STATE_DIR" show --json "$BASIC_ID"
capture basic-stdout "$JOBMAN_BIN" --state-dir "$STATE_DIR" logs \
  --stream stdout --raw "$BASIC_ID"
capture basic-stderr "$JOBMAN_BIN" --state-dir "$STATE_DIR" logs \
  --stream stderr --raw "$BASIC_ID"
printf 'stdout-line\nstdout-fragment' >"$CAMPAIGN/expected.stdout"
printf 'stderr-line\nstderr-fragment' >"$CAMPAIGN/expected.stderr"
cmp "$CAMPAIGN/expected.stdout" "$EVIDENCE_DIR/basic-stdout.stdout"
cmp "$CAMPAIGN/expected.stderr" "$EVIDENCE_DIR/basic-stderr.stdout"
```

For the SSH test, run the terminal-A block inside SSH, then terminate the local
SSH client without typing `exit`—for example, close the terminal emulator or
disconnect the test network. Do not kill the remote user session. Run the
terminal-B block from an independently established SSH connection. On Unix,
record the absence of retained terminal descriptors after completion:

```sh
if command -v lsof >/dev/null 2>&1; then
  lsof -n +D "$STATE_DIR" >"$EVIDENCE_DIR/open-state-files.txt" 2>&1 || :
  lsof -n | grep "$BASIC_ID" >"$EVIDENCE_DIR/open-job-files.txt" 2>&1 || :
fi
```

Exercise the remaining terminal outcomes with these direct recipes:

```sh
OK_ID=$(jm run -- sh -c 'exit 0')
FAIL_ID=$(jm run -- sh -c 'exit 23')
CANCEL_ID=$(jm run -- sleep 300)
TIMEOUT_ID=$(jm run --run-timeout 1s -- sleep 300)
START_ID=$(jm run -- /definitely/missing/jobman-dogfood-command)
FRAGMENT_ID=$(jm run -- sh -c 'printf final-fragment')
jm cancel "$CANCEL_ID"
for id in "$OK_ID" "$FAIL_ID" "$CANCEL_ID" "$TIMEOUT_ID" "$START_ID" "$FRAGMENT_ID"; do
  jm wait "$id" || :
  jm show --json "$id" >"$EVIDENCE_DIR/lifecycle-$id.json"
done
jm logs --stream stdout --raw "$FRAGMENT_ID" \
  >"$EVIDENCE_DIR/unterminated-fragment.bin"
test "$(wc -c <"$EVIDENCE_DIR/unterminated-fragment.bin")" -eq 14
```

On Windows, use `Start-Sleep` as the gated workload, close the first PowerShell
window after submission, and complete the job from a second window:

```powershell
$Gate = Join-Path $Campaign 'release-windows'
$WindowsId = jm run --wait-file $Gate -- powershell.exe -NoProfile -Command `
  '[Console]::Out.Write("windows-stdout"); [Console]::Error.Write("windows-stderr")'
Write-Host "WINDOWS_ID=$WindowsId"
# Close this entire window now. In a second native PowerShell window:
$WindowsId = 'replace-with-recorded-job-id'
$Gate = Join-Path $Campaign 'release-windows'
New-Item -ItemType File -Force $Gate | Out-Null
jm wait $WindowsId
jm show --json $WindowsId | Tee-Object (Join-Path $EvidenceDir 'windows-disconnect.json')
```

## 3. Process-tree control

Use a helper that starts a child and grandchild and records all PIDs. Exercise:

- graceful cancellation where every process handles the graceful request;
- forced escalation where one descendant ignores the graceful request;
- pause while active, proof that progress stops, resume, and proof that
  progress restarts; and
- cancellation while paused.

After every case, independently verify that no recorded PID remains and that a
new unrelated process cannot be affected by a stale Jobman identity. On
Windows, confirm descendants are Job Object members and the forced phase ends
the whole tree. Record whether best-effort console break was observed; failure
of that advisory phase is acceptable only when forced Job Object termination
succeeds after the configured grace period.

### Unix process-tree recipe

The checked-in helper creates a real parent, child, and grandchild, records
their PIDs, and appends progress once per second. It has a graceful mode and a
stubborn mode whose descendants ignore the initial termination request. Invoke
it by absolute path as shown here:

```sh
export TREE_CASE="$CAMPAIGN/tree-graceful"
mkdir -p "$TREE_CASE"
GRACEFUL_ID=$(jm run --name dogfood-tree-graceful --stop-grace 5s -- \
  "$TREE_HELPER" graceful "$TREE_CASE/pids" "$TREE_CASE/progress")
sleep 3
cat "$TREE_CASE/pids"
test "$(wc -l <"$TREE_CASE/pids")" -eq 3

jm pause "$GRACEFUL_ID"
sleep 2
LINES_PAUSED=$(wc -l <"$TREE_CASE/progress")
sleep 3
test "$(wc -l <"$TREE_CASE/progress")" -eq "$LINES_PAUSED"
jm resume "$GRACEFUL_ID"
sleep 3
test "$(wc -l <"$TREE_CASE/progress")" -gt "$LINES_PAUSED"
jm cancel "$GRACEFUL_ID"
jm wait "$GRACEFUL_ID"

while read -r role pid; do
  if kill -0 "$pid" 2>/dev/null; then
    echo "FAIL: $role PID $pid survived graceful cancellation" >&2
    exit 1
  fi
done <"$TREE_CASE/pids"
```

Repeat with descendants that require forced escalation, then cancel once more
while the job is paused:

```sh
export TREE_CASE="$CAMPAIGN/tree-stubborn"
mkdir -p "$TREE_CASE"
STUBBORN_ID=$(jm run --name dogfood-tree-stubborn --stop-grace 2s -- \
  "$TREE_HELPER" stubborn "$TREE_CASE/pids" "$TREE_CASE/progress")
sleep 3
jm cancel "$STUBBORN_ID"
jm wait "$STUBBORN_ID"
while read -r role pid; do
  if kill -0 "$pid" 2>/dev/null; then
    echo "FAIL: $role PID $pid survived forced cancellation" >&2
    exit 1
  fi
done <"$TREE_CASE/pids"

export TREE_CASE="$CAMPAIGN/tree-cancel-paused"
mkdir -p "$TREE_CASE"
PAUSED_ID=$(jm run --stop-grace 2s -- \
  "$TREE_HELPER" stubborn "$TREE_CASE/pids" "$TREE_CASE/progress")
sleep 3
jm pause "$PAUSED_ID"
jm cancel "$PAUSED_ID"
jm wait "$PAUSED_ID"
```

Inspect each final job with `jm show --json JOB_ID`. Because PIDs can be reused,
`kill -0` is only the immediate leak check; the recorded Jobman process
identity and start facts in `show --json` are the evidence that a later,
unrelated process cannot be signalled.

On native Windows, record target Job Object membership and handle counts with
Sysinternals Process Explorer or an equivalent trusted inspection tool. Use a
PowerShell workload that creates a descendant, then run `pause`, `resume`, and
`cancel` from a second console. Confirm the descendant disappears after the
configured grace period:

```powershell
$TreeId = jm run --stop-grace 2s -- powershell.exe -NoProfile -Command `
  '$p = Start-Process powershell.exe -ArgumentList "-NoProfile","-Command","Start-Sleep 300" -PassThru; Write-Output $PID; Write-Output $p.Id; Wait-Process $p.Id'
Start-Sleep 2
jm logs --stream stdout --raw $TreeId | Tee-Object (Join-Path $EvidenceDir 'windows-tree-pids.txt')
jm pause $TreeId
Start-Sleep 3
jm resume $TreeId
jm cancel $TreeId
jm wait $TreeId
Get-Content (Join-Path $EvidenceDir 'windows-tree-pids.txt') | ForEach-Object {
  if (Get-Process -Id ([int]$_) -ErrorAction SilentlyContinue) { throw "PID $_ survived cancellation" }
}
```

## 4. Scheduling and policy matrix

Use short deterministic commands and verify:

- retries for retryable exit, start failure, signal/platform reason, and
  timeout, plus a nonretryable failure;
- constant, linear, and exponential delays, maximum delay, jitter bounds, run
  limit, success target, and failure limit;
- `--after-success`, `--after-finish`, `--after-failed`, each terminal-outcome
  predicate, an impossible dependency, and multiple prerequisites;
- until, delay, file, and executable-probe waits, including abort deadlines;
- global and named-pool concurrency limits, multi-slot jobs, FIFO/bounded
  bypass behavior, `config apply`, and removal of an unused pool; and
- policy-only pause/resume while waiting, queued, and backing off.

Run concurrent `list`, `status`, `show`, `logs`, `cancel`, and `doctor` clients
during these cases. Any partial JSON, impossible transition, database busy loop,
or reader-induced mutation is a release blocker.

Use a fresh subdirectory for this matrix and preserve every final JSON record.
These commands cover the high-risk interactions; repeat the retry command with
`constant`, `linear`, and `exponential`, and compare recorded run timestamps to
the configured bounds:

```sh
RETRY_ID=$(jm run --retries 3 --retryable-exit-code 17 \
  --retry-delay 250ms --retry-backoff exponential --retry-max-delay 1s -- \
  sh -c 'exit 17')
jm wait "$RETRY_ID" || :
jm show --json "$RETRY_ID" >"$EVIDENCE_DIR/policy-retry.json"

TIMEOUT_RETRY_ID=$(jm run --retries 1 --retry-timeouts \
  --run-timeout 250ms --retry-delay 100ms -- sleep 5)
START_RETRY_ID=$(jm run --retries 1 --retry-start-failures \
  --retry-delay 100ms -- /definitely/missing/jobman-dogfood-command)
jm wait "$TIMEOUT_RETRY_ID" || :
jm wait "$START_RETRY_ID" || :

SUCCESS_ID=$(jm run -- sh -c 'exit 0')
FAILED_ID=$(jm run -- sh -c 'exit 9')
jm wait "$SUCCESS_ID"
jm wait "$FAILED_ID" || :
AFTER_SUCCESS_ID=$(jm run --after-success "$SUCCESS_ID" -- sh -c 'exit 0')
AFTER_FAILED_ID=$(jm run --after-failed "$FAILED_ID" -- sh -c 'exit 0')
AFTER_FINISH_ID=$(jm run --after-finish "$FAILED_ID" -- sh -c 'exit 0')
IMPOSSIBLE_ID=$(jm run --after-success "$FAILED_ID" -- sh -c 'exit 0')
for id in "$AFTER_SUCCESS_ID" "$AFTER_FAILED_ID" "$AFTER_FINISH_ID" "$IMPOSSIBLE_ID"; do
  jm wait "$id" || :
  jm show --json "$id" >"$EVIDENCE_DIR/dependency-$id.json"
done

WAIT_GATE="$CAMPAIGN/policy-wait-gate"
rm -f "$WAIT_GATE"
WAIT_ID=$(jm run --wait-file "$WAIT_GATE" --wait-delay 1s --wait-mode all -- \
  sh -c 'exit 0')
jm pause "$WAIT_ID"
jm show --json "$WAIT_ID" >"$EVIDENCE_DIR/wait-paused.json"
touch "$WAIT_GATE"
jm resume "$WAIT_ID"
jm wait "$WAIT_ID"
```

Exercise durable global and named-pool limits with an explicit disposable
configuration. Keep this file outside the state root so a restore or cleanup
cannot alter it:

```sh
export POLICY_CONFIG="$CAMPAIGN/policy.yml"
cat >"$POLICY_CONFIG" <<'YAML'
schema_version: 1
concurrency:
  max_active_slots: 3
  pools:
    dogfood: 2
YAML
chmod 600 "$POLICY_CONFIG"
"$JOBMAN_BIN" --state-dir "$STATE_DIR" --config "$POLICY_CONFIG" config validate
"$JOBMAN_BIN" --state-dir "$STATE_DIR" --config "$POLICY_CONFIG" config apply

POOL_A=$("$JOBMAN_BIN" --state-dir "$STATE_DIR" --config "$POLICY_CONFIG" \
  run --pool dogfood --slots 2 -- sleep 10)
POOL_B=$("$JOBMAN_BIN" --state-dir "$STATE_DIR" --config "$POLICY_CONFIG" \
  run --pool dogfood --slots 1 -- sleep 10)
sleep 2
jm show --json "$POOL_A" >"$EVIDENCE_DIR/pool-a-active.json"
jm show --json "$POOL_B" >"$EVIDENCE_DIR/pool-b-queued.json"
jm cancel "$POOL_A"
jm wait "$POOL_B"
```

While a long job is active, run independent readers and a canceller at the same
instant. Every background client must exit cleanly and every `.json` file must
parse:

```sh
RACE_ID=$(jm run -- sleep 300)
sleep 2
jm list --json >"$EVIDENCE_DIR/concurrent-list.json" & p1=$!
jm status --json "$RACE_ID" >"$EVIDENCE_DIR/concurrent-status.json" & p2=$!
jm show --json "$RACE_ID" >"$EVIDENCE_DIR/concurrent-show.json" & p3=$!
jm logs --stream stdout "$RACE_ID" >"$EVIDENCE_DIR/concurrent-logs.txt" & p4=$!
jm doctor --json >"$EVIDENCE_DIR/concurrent-doctor.json" & p5=$!
jm cancel "$RACE_ID" >"$EVIDENCE_DIR/concurrent-cancel.txt" & p6=$!
wait "$p1" "$p2" "$p3" "$p4" "$p5" "$p6"
for file in "$EVIDENCE_DIR"/concurrent-*.json; do
  jq -e . "$file" >/dev/null
done
```

The final loop requires `jq`; install it only as an operator-side inspection
tool. It is not a Jobman runtime dependency. In PowerShell, use `Get-Content
-Raw FILE | ConvertFrom-Json` for the same syntax check.

## 5. Logs, input, configuration, and redaction

Generate binary data, large lines, interleaved stdout/stderr, and enough output
to rotate every configured segment. Verify raw streams byte-for-byte, combined
ordering status, follow-to-completion, active reads, and the behavior after
`clean` pruning.

For live input, send binary chunks from multiple clients, exercise backpressure,
send EOF, retry EOF, and confirm a later run gets a fresh endpoint. Verify no
TCP listener exists. On Unix inspect socket ownership/mode; on Windows inspect
the named-pipe DACL.

Create configuration layers at every precedence level. Confirm origins,
trusted-project enforcement, unknown-key rejection, and that malformed config
does not prevent `list`, `status`, `show`, `logs`, `cancel`, or `doctor`. Apply
store-wide concurrency with `config apply`, `run`, `rerun`, and policy-based
`clean`; confirm explicit `clean --older-than` does not become an authority
operation.

Put unique canary values in fields named `token`, `password`, and a configured
redaction pattern. Confirm canaries do not appear in human diagnostics, JSON,
notification diagnostics, or error output. It is expected that a target which
prints a canary places it in raw target logs; document this boundary.

Generate deterministic binary input once, submit it as target output, and
compare the retrieved raw stream byte for byte. This rotates many segments
without pruning any of them:

```sh
dd if=/dev/urandom of="$CAMPAIGN/expected-binary" bs=1024 count=16
LOG_ID=$(jm run --log-segment-bytes 1024 --log-segments 32 -- \
  cat "$CAMPAIGN/expected-binary")
jm wait "$LOG_ID"
jm logs --stream stdout --raw "$LOG_ID" >"$CAMPAIGN/actual-binary"
cmp "$CAMPAIGN/expected-binary" "$CAMPAIGN/actual-binary"
jm show --json "$LOG_ID" >"$EVIDENCE_DIR/log-rotation.json"

FOLLOW_ID=$(jm run -- sh -c 'i=1; while [ "$i" -le 5 ]; do echo "line-$i"; i=$((i + 1)); sleep 1; done')
jm logs --follow --stream stdout --raw "$FOLLOW_ID" \
  >"$EVIDENCE_DIR/follow.stdout"
jm show --json "$FOLLOW_ID" >"$EVIDENCE_DIR/follow-final.json"
```

For live input, keep the target running while two independent clients send
binary chunks. Inspect local IPC before EOF, then verify concatenated output.
Client ordering is deliberately nondeterministic, so compare sorted digests or
accept either complete-chunk order; bytes within each chunk must not interleave:

```sh
dd if=/dev/urandom of="$CAMPAIGN/chunk-a" bs=4096 count=8
dd if=/dev/urandom of="$CAMPAIGN/chunk-b" bs=4096 count=8
INPUT_ID=$(jm run --stdin live -- cat)
jm input "$INPUT_ID" <"$CAMPAIGN/chunk-a" & input_a=$!
jm input "$INPUT_ID" <"$CAMPAIGN/chunk-b" & input_b=$!
wait "$input_a" "$input_b"

find "$STATE_DIR" -type s -ls >"$EVIDENCE_DIR/live-input-sockets.txt" 2>&1 || :
if command -v lsof >/dev/null 2>&1; then
  lsof -n -P -iTCP >"$EVIDENCE_DIR/tcp-listeners.txt"
fi
jm input --eof "$INPUT_ID" </dev/null
if jm input --eof "$INPUT_ID" </dev/null \
    >"$EVIDENCE_DIR/repeated-eof.stdout" \
    2>"$EVIDENCE_DIR/repeated-eof.stderr"; then
  echo 'FAIL: repeated EOF unexpectedly succeeded' >&2
  exit 1
fi
jm wait "$INPUT_ID"
jm logs --stream stdout --raw "$INPUT_ID" >"$CAMPAIGN/live-output"
cat "$CAMPAIGN/chunk-a" "$CAMPAIGN/chunk-b" >"$CAMPAIGN/order-ab"
cat "$CAMPAIGN/chunk-b" "$CAMPAIGN/chunk-a" >"$CAMPAIGN/order-ba"
cmp "$CAMPAIGN/live-output" "$CAMPAIGN/order-ab" || \
  cmp "$CAMPAIGN/live-output" "$CAMPAIGN/order-ba"

INPUT_RERUN_ID=$(jm rerun "$INPUT_ID")
jm show --json "$INPUT_RERUN_ID" >"$EVIDENCE_DIR/input-rerun.json"
printf 'fresh endpoint\n' | jm input "$INPUT_RERUN_ID"
jm input --eof "$INPUT_RERUN_ID" </dev/null
jm wait "$INPUT_RERUN_ID"
```

On Unix, socket files must be owned by the test user and have no group/other
permissions. Check the paths recorded above with `stat`. On Windows, locate the
active endpoint in `show --json`, inspect its named-pipe ACL with an
administrator-approved native tool, and use `Get-NetTCPConnection -State
Listen` before and during input to prove Jobman did not open a TCP listener.

Malformed configuration must not disable emergency commands. This block first
proves strict validation fails, then runs every emergency operation against the
same malformed explicit file:

```sh
cat >"$CAMPAIGN/malformed.yml" <<'YAML'
schema_version: 1
unknown_v1_key: must-fail
YAML
if "$JOBMAN_BIN" --state-dir "$STATE_DIR" --config "$CAMPAIGN/malformed.yml" \
    config validate >"$EVIDENCE_DIR/malformed.stdout" \
    2>"$EVIDENCE_DIR/malformed.stderr"; then
  echo 'FAIL: malformed configuration was accepted' >&2
  exit 1
fi
for command in list status show logs cancel doctor; do
  case $command in
    list|doctor)
      "$JOBMAN_BIN" --state-dir "$STATE_DIR" --config "$CAMPAIGN/malformed.yml" \
        "$command" >"$EVIDENCE_DIR/emergency-$command.stdout" \
        2>"$EVIDENCE_DIR/emergency-$command.stderr"
      ;;
    cancel)
      EMERGENCY_ID=$(jm run -- sleep 300)
      "$JOBMAN_BIN" --state-dir "$STATE_DIR" --config "$CAMPAIGN/malformed.yml" \
        cancel "$EMERGENCY_ID" >"$EVIDENCE_DIR/emergency-cancel.stdout" \
        2>"$EVIDENCE_DIR/emergency-cancel.stderr"
      ;;
    *)
      "$JOBMAN_BIN" --state-dir "$STATE_DIR" --config "$CAMPAIGN/malformed.yml" \
        "$command" "$OK_ID" >"$EVIDENCE_DIR/emergency-$command.stdout" \
        2>"$EVIDENCE_DIR/emergency-$command.stderr"
      ;;
  esac
done
```

Use fresh, obviously fake canaries for the redaction test. This configuration
exercises named `token`/`password` secret references and a diagnostic pattern;
the raw target log is then checked separately as the documented exception:

```sh
export JOBMAN_DOGFOOD_TOKEN='dogfood-token-7f8f6018'
export JOBMAN_DOGFOOD_PASSWORD='dogfood-password-c24ac7b1'
export JOBMAN_DOGFOOD_PATTERN='private-8675309'
cat >"$CAMPAIGN/redaction.yml" <<'YAML'
schema_version: 1
secrets:
  token: env:JOBMAN_DOGFOOD_TOKEN
  password: env:JOBMAN_DOGFOOD_PASSWORD
redaction:
  names: [token, password]
  patterns: ['private-[0-9]+']
YAML
REDACT_ID=$("$JOBMAN_BIN" --state-dir "$STATE_DIR" --config "$CAMPAIGN/redaction.yml" \
  run --name "$JOBMAN_DOGFOOD_PATTERN" --secret-env TOKEN=token \
  --secret-env PASSWORD=password -- printf %s "$JOBMAN_DOGFOOD_PATTERN")
jm wait "$REDACT_ID"
"$JOBMAN_BIN" --state-dir "$STATE_DIR" --config "$CAMPAIGN/redaction.yml" \
  list --json >"$EVIDENCE_DIR/redaction-list.json"
"$JOBMAN_BIN" --state-dir "$STATE_DIR" --config "$CAMPAIGN/redaction.yml" \
  show --json "$REDACT_ID" >"$EVIDENCE_DIR/redaction-show.json"
grep -F '[REDACTED]' "$EVIDENCE_DIR/redaction-list.json" >/dev/null
grep -F '[REDACTED]' "$EVIDENCE_DIR/redaction-show.json" >/dev/null
"$JOBMAN_BIN" --state-dir "$STATE_DIR" --config "$CAMPAIGN/redaction.yml" \
  logs --stream stdout --raw "$REDACT_ID" >"$CAMPAIGN/redaction-raw.log"
test "$(cat "$CAMPAIGN/redaction-raw.log")" = "$JOBMAN_DOGFOOD_PATTERN"
grep -R -n -F "$JOBMAN_DOGFOOD_TOKEN" "$EVIDENCE_DIR" && \
  echo 'FAIL: token canary found in evidence' >&2 && exit 1
grep -R -n -F "$JOBMAN_DOGFOOD_PASSWORD" "$EVIDENCE_DIR" && \
  echo 'FAIL: password canary found in evidence' >&2 && exit 1
grep -R -n -F "$JOBMAN_DOGFOOD_PATTERN" "$EVIDENCE_DIR" && \
  echo 'FAIL: pattern canary found in evidence' >&2 && exit 1
```

Do not search raw target logs in this last check; raw logs are intentionally
outside Jobman's diagnostic-redaction boundary.

## 6. Notifications and recovery

Use disposable local command notifiers and controlled SMTP/HTTPS endpoints.
Never use production credentials. Verify event payload schema, authentication,
timeouts, response truncation, retry timing, maximum attempts, and that delivery
failure does not alter the job outcome.

Terminate a supervisor during a notification claim, wait for claim expiry, and
run `doctor --repair`. Confirm the due delivery is recovered once, attempts are
durable, and idempotency keys prevent an unnoticed duplicate side effect.

Start with the checked-in command notifier so the payload and retry behavior
are local and inspectable. YAML paths below must be absolute and must not
contain test credentials:

```sh
export NOTIFIER_HELPER="$PWD/devel/dogfood/command-notifier.sh"
export NOTIFIER_EVENTS="$CAMPAIGN/notifier-events"
mkdir -p "$NOTIFIER_EVENTS"
cat >"$CAMPAIGN/notifiers.yml" <<YAML
schema_version: 1
notifiers:
  audit:
    type: command
    events: [job_succeeded]
    timeout: 5s
    retry: {max_attempts: 1, delay: 10ms, max_delay: 10ms}
    command:
      command: ["$NOTIFIER_HELPER", success, "$NOTIFIER_EVENTS/audit"]
      output_limit: 4KiB
  failing:
    type: command
    events: [job_succeeded]
    timeout: 1s
    retry: {max_attempts: 2, delay: 100ms, max_delay: 100ms}
    command:
      command: ["$NOTIFIER_HELPER", failure, "$NOTIFIER_EVENTS/failing"]
      output_limit: 32B
YAML
chmod 600 "$CAMPAIGN/notifiers.yml"
"$JOBMAN_BIN" --state-dir "$STATE_DIR" --config "$CAMPAIGN/notifiers.yml" config validate

AUDIT_ID=$("$JOBMAN_BIN" --state-dir "$STATE_DIR" --config "$CAMPAIGN/notifiers.yml" \
  run --notify audit -- sh -c 'exit 0')
FAIL_NOTIFY_ID=$("$JOBMAN_BIN" --state-dir "$STATE_DIR" --config "$CAMPAIGN/notifiers.yml" \
  run --notify failing -- sh -c 'exit 0')
jm wait "$AUDIT_ID"
jm wait "$FAIL_NOTIFY_ID"
sleep 3
jm show --json "$AUDIT_ID" >"$EVIDENCE_DIR/notifier-audit.json"
jm show --json "$FAIL_NOTIFY_ID" >"$EVIDENCE_DIR/notifier-failure.json"
for payload in "$NOTIFIER_EVENTS"/*/*.event.json; do jq -e . "$payload" >/dev/null; done
```

Expected: `audit` has one successful attempt; `failing` has two bounded failed
attempts; both jobs retain outcome `success`; captured payloads have schema
version 1, unique event IDs, and the expected job IDs. Verify the configured
32-byte output bound in the failure diagnostics.

For recovery, use a notifier that blocks only its first invocation. When its
`.claimed` marker appears, kill the recorded supervisor and the abandoned
notifier child, wait longer than the 15-second lease, and invoke repair:

```sh
export RECOVERY_EVENTS="$CAMPAIGN/notifier-recovery"
mkdir -p "$RECOVERY_EVENTS"
cat >"$CAMPAIGN/notifier-recovery.yml" <<YAML
schema_version: 1
notifiers:
  recovery:
    type: command
    events: [job_succeeded]
    timeout: 5m
    retry: {max_attempts: 2, delay: 100ms, max_delay: 100ms}
    command:
      command: ["$NOTIFIER_HELPER", slow-once, "$RECOVERY_EVENTS"]
      output_limit: 4KiB
YAML
RECOVERY_ID=$("$JOBMAN_BIN" --state-dir "$STATE_DIR" \
  --config "$CAMPAIGN/notifier-recovery.yml" run --notify recovery -- sh -c 'exit 0')
while ! find "$RECOVERY_EVENTS" -name '*.claimed' -print -quit | grep -q .; do sleep 1; done
SUPERVISOR_PID=$(cat "$RECOVERY_EVENTS"/*.supervisor-pid | head -1)
NOTIFIER_PID=$(find "$RECOVERY_EVENTS" -name '*.claimed' -print -quit | \
  sed 's/.*-\([0-9][0-9]*\)\.claimed/\1/')
kill -KILL "$SUPERVISOR_PID"
kill -KILL "$NOTIFIER_PID" 2>/dev/null || :
sleep 20
jm doctor --repair
sleep 3
jm show --json "$RECOVERY_ID" >"$EVIDENCE_DIR/notifier-recovered.json"
find "$RECOVERY_EVENTS" -type f -print \
  >"$EVIDENCE_DIR/notifier-recovery-files.txt"
```

The recovered record must show one completed external attempt, not two. The
two captured payload files demonstrate why an external notifier must dedupe by
the stable event/idempotency key: the first process received the event before
its supervisor died, so exactly-once external side effects cannot be inferred
from a local attempt record.

Next repeat the successful and failing cases against disposable HTTPS and SMTP
capture services controlled by the test operator. Use a unique event ID as the
remote service's idempotency key. Record server timestamps, headers with secret
values removed, response status, truncated response diagnostics, and attempt
count. Test one connection timeout and one non-success response. Do not point
the release candidate at a production endpoint, and do not place the service
credential in the YAML or evidence wrapper.

## 7. Retention, upgrade, backup, and restore

Create old completed jobs with dependency and notification history. Exercise
policy dry-run and forced cleanup. Confirm finite log age is honored, metadata
is deleted only after logs are pruned, and unresolved dependencies, pending
notifications, or active admissions block deletion.

For every supported prior schema fixture and the most recent released binary:

1. Copy the fixture to a fresh state root.
2. Record a pre-upgrade `doctor --json` where the old binary supports it.
3. Open it with the release candidate.
4. Confirm an automatic file appeared under `backups/` before schema change.
5. Run `doctor`, inspect all representative jobs/logs, and compare counts.
6. Follow [UPGRADING.md](UPGRADING.md) to restore the backup into a different
   state root and verify it with the appropriate binary.
7. Simulate unwritable backup storage and confirm migration aborts without
   changing the original schema.

First prove cleanup is a dry run by default and that forced pruning leaves a
durable log tombstone without changing the job result:

```sh
CLEAN_ID=$(jm run -- sh -c 'printf retained-dogfood-log')
jm wait "$CLEAN_ID"
jm clean "$CLEAN_ID" --older-than 0s \
  >"$EVIDENCE_DIR/clean-dry-run.txt"
jm logs --stream stdout --raw "$CLEAN_ID" \
  >"$CAMPAIGN/clean-before"
test "$(cat "$CAMPAIGN/clean-before")" = retained-dogfood-log
jm clean "$CLEAN_ID" --older-than 0s --dry-run=false --force \
  >"$EVIDENCE_DIR/clean-forced.txt"
if jm logs --stream stdout --raw "$CLEAN_ID" \
    >"$EVIDENCE_DIR/clean-pruned.stdout" \
    2>"$EVIDENCE_DIR/clean-pruned.stderr"; then
  echo 'FAIL: pruned logs remain readable' >&2
  exit 1
fi
jm show --json "$CLEAN_ID" >"$EVIDENCE_DIR/clean-tombstone.json"
```

Create and validate an operator-selected backup before every upgrade rehearsal:

```sh
export BACKUP_DB="$CAMPAIGN/pre-upgrade.db"
rm -f "$BACKUP_DB"
jm doctor --json >"$EVIDENCE_DIR/pre-upgrade-doctor.json"
jm doctor --backup "$BACKUP_DB"
chmod 600 "$BACKUP_DB"
if command -v sha256sum >/dev/null 2>&1; then
  sha256sum "$BACKUP_DB" >"$EVIDENCE_DIR/pre-upgrade.sha256"
else
  shasum -a 256 "$BACKUP_DB" >"$EVIDENCE_DIR/pre-upgrade.sha256"
fi
```

For a prior release, use separate explicit binaries and state roots. Replace
the marked values, create representative completed and active history with the
old binary, then open that exact root with the release candidate:

```sh
export OLD_JOBMAN=/absolute/path/to/most-recent-released/jobman
export UPGRADE_STATE="$CAMPAIGN/upgrade-state"
mkdir -m 700 "$UPGRADE_STATE"
OLD_ID=$("$OLD_JOBMAN" --state-dir "$UPGRADE_STATE" run -- sh -c 'printf old-release-log')
"$OLD_JOBMAN" --state-dir "$UPGRADE_STATE" wait "$OLD_ID"
"$OLD_JOBMAN" --state-dir "$UPGRADE_STATE" doctor --json \
  >"$EVIDENCE_DIR/upgrade-before.json"
find "$UPGRADE_STATE/backups" -type f -print 2>/dev/null | sort \
  >"$CAMPAIGN/backups-before.txt"

"$JOBMAN_BIN" --state-dir "$UPGRADE_STATE" doctor --json \
  >"$EVIDENCE_DIR/upgrade-after.json"
"$JOBMAN_BIN" --state-dir "$UPGRADE_STATE" show --json "$OLD_ID" \
  >"$EVIDENCE_DIR/upgrade-old-job.json"
"$JOBMAN_BIN" --state-dir "$UPGRADE_STATE" logs --stream stdout --raw "$OLD_ID" \
  >"$EVIDENCE_DIR/upgrade-old-log.bin"
test "$(cat "$EVIDENCE_DIR/upgrade-old-log.bin")" = old-release-log
find "$UPGRADE_STATE/backups" -type f -print | sort \
  >"$EVIDENCE_DIR/backups-after.txt"
diff -u "$CAMPAIGN/backups-before.txt" "$EVIDENCE_DIR/backups-after.txt"
```

The last `diff` is expected to report at least one added automatic backup when
a schema migration occurs; no difference is expected if both binaries use the
same schema. Follow [UPGRADING.md](UPGRADING.md) to restore the new backup into
a third state root. Never restore over the original. On Unix, simulate backup
failure on a copy by pre-creating its `backups` directory without owner write
permission, then compare database digests before and after the rejected open:

```sh
export FAILURE_STATE="$CAMPAIGN/upgrade-backup-failure"
cp -R "$UPGRADE_STATE" "$FAILURE_STATE"
chmod 500 "$FAILURE_STATE/backups"
sha256sum "$FAILURE_STATE/jobman.db" >"$CAMPAIGN/failure-before.sha256"
if "$JOBMAN_BIN" --state-dir "$FAILURE_STATE" doctor --json \
    >"$EVIDENCE_DIR/upgrade-failure.stdout" \
    2>"$EVIDENCE_DIR/upgrade-failure.stderr"; then
  echo 'FAIL: migration proceeded without writable backup storage' >&2
  chmod 700 "$FAILURE_STATE/backups"
  exit 1
fi
sha256sum "$FAILURE_STATE/jobman.db" >"$CAMPAIGN/failure-after.sha256"
diff -u "$CAMPAIGN/failure-before.sha256" "$CAMPAIGN/failure-after.sha256"
chmod 700 "$FAILURE_STATE/backups"
```

Run this failure recipe only when the copied database actually requires a
migration; otherwise opening it successfully is correct. Use `shasum -a 256`
instead of `sha256sum` on macOS.

## 8. Packaging and endurance

Install and uninstall every applicable archive, native package, Homebrew Cask,
and container image. Verify man pages, completions, sample configuration,
unprivileged container identity, signal handling, checksums, signatures, SBOMs,
and that packaged defaults point to writable per-user state.

Run at least one 24-hour soak on each operating system with concurrent short
jobs, periodic retries/timeouts, log rotation, live input, notifications,
cleanup dry-runs, and `doctor`. Record peak database/WAL/log sizes, process and
handle counts, CPU/memory, command latency percentiles, and every nonzero
diagnostic. Growth without a configured bound or leaked processes/handles is a
release blocker.

Download the checksum, Sigstore bundle, provenance, and selected artifact into
one empty directory. Replace `<version>` and run the complete published
verification chain before installing anything:

```sh
cd /path/to/empty/release-download
cosign verify-blob \
  --bundle "jobman_<version>_checksums.txt.sigstore.json" \
  --certificate-identity \
    'https://github.com/ryancswallace/jobman/.github/workflows/release.yml@refs/heads/main' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  "jobman_<version>_checksums.txt"
sha256sum --check "jobman_<version>_checksums.txt" --ignore-missing
gh attestation verify --owner ryancswallace \
  "jobman_<version>_linux_amd64.tar.gz"
slsa-verifier verify-artifact --provenance-path jobman.intoto.jsonl \
  --source-uri github.com/ryancswallace/jobman \
  "jobman_<version>_linux_amd64.tar.gz"
```

Use `shasum -a 256 -c` for the downloaded artifact on macOS if GNU
`sha256sum` is unavailable. After extracting an archive, verify its executable,
release-specific citation, generated help assets, and sample configuration:

```sh
mkdir -p "$CAMPAIGN/archive"
tar -xzf "jobman_<version>_linux_amd64.tar.gz" -C "$CAMPAIGN/archive"
"$CAMPAIGN/archive/jobman" --version
grep -F 'version: <version>' "$CAMPAIGN/archive/CITATION.cff"
test -s "$CAMPAIGN/archive/docs/manpage/jobman.1"
test -s "$CAMPAIGN/archive/docs/completions/bash/jobman"
"$CAMPAIGN/archive/jobman" config validate \
  "$CAMPAIGN/archive/etc/jobman/jobman.yml"
```

Install each native package in a disposable VM, not over the operator's normal
installation. Use the package manager both to install and remove it:

```sh
sudo dpkg -i ./jobman_<version>_linux_amd64.deb
jobman --version && man jobman
sudo dpkg --remove jobman

sudo rpm -i ./jobman_<version>_linux_amd64.rpm
jobman --version && man jobman
sudo rpm -e jobman

sudo apk add --allow-untrusted ./jobman_<version>_linux_amd64.apk
jobman --version && man jobman
sudo apk del jobman
```

Run only the block for the VM's native package manager. Confirm package removal
does not delete user state and that `/etc/jobman/jobman.yml` follows the package
manager's configuration-preservation semantics. On macOS, test the Cask after
its generated pull request is merged:

```sh
brew tap ryancswallace/jobman https://github.com/ryancswallace/jobman
brew install --cask jobman
jobman --version
man jobman
brew uninstall --cask jobman
```

On Windows, expand the ZIP with `Expand-Archive`, run `jobman.exe --version`,
dot-source the packaged completion in a disposable PowerShell process, validate
the sample configuration, and delete the extracted directory. Record the
absence of installer-created machine state because the v1 Windows artifact is
portable rather than an MSI.

Verify the signed runtime image by immutable version and run a foreground job
with persistent state. The derived image proves users can add their own target
commands:

```sh
docker pull "ghcr.io/ryancswallace/jobman:<version>"
cosign verify \
  --certificate-identity \
    'https://github.com/ryancswallace/jobman/.github/workflows/release.yml@refs/heads/main' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  "ghcr.io/ryancswallace/jobman:<version>"
docker volume create jobman-dogfood-state
docker run --rm --init -v jobman-dogfood-state:/home/jobman/.local/state/jobman \
  "ghcr.io/ryancswallace/jobman:<version>" \
  run --foreground -- bash -c 'printf container-dogfood'
cat >"$CAMPAIGN/Dockerfile.dogfood" <<'DOCKERFILE'
ARG JOBMAN_IMAGE
FROM ${JOBMAN_IMAGE}
USER root
RUN printf '#!/bin/sh\nprintf derived-image-workload\n' >/usr/local/bin/dogfood-target \
    && chmod 0755 /usr/local/bin/dogfood-target
USER jobman
DOCKERFILE
docker build --build-arg JOBMAN_IMAGE="ghcr.io/ryancswallace/jobman:<version>" \
  -f "$CAMPAIGN/Dockerfile.dogfood" -t jobman-dogfood-derived "$CAMPAIGN"
docker run --rm --init -v jobman-dogfood-state:/home/jobman/.local/state/jobman \
  jobman-dogfood-derived run --wait -- /usr/local/bin/dogfood-target
docker volume rm jobman-dogfood-state
```

Run the bounded Unix soak helper for 24 hours (86,400 seconds). It repeatedly
exercises concurrent submission, retries, timeouts, rotation, live input,
cleanup dry-runs, and `doctor`, while retaining size and diagnostic samples:

```sh
JOBMAN_BIN="$JOBMAN_BIN" ./devel/dogfood/soak.sh \
  "$CAMPAIGN/soak-state" "$EVIDENCE_DIR/soak" 86400
```

Run the native Windows equivalent from PowerShell:

```powershell
.\devel\dogfood\soak.ps1 -JobmanBin $JobmanBin `
  -StateDir (Join-Path $Campaign 'soak-state') `
  -EvidenceDir (Join-Path $EvidenceDir 'soak') `
  -DurationSeconds 86400
```

These drivers intentionally omit real SMTP/HTTPS services and human resource
observation. During each soak, sample the OS process tree, CPU, resident memory,
file descriptors or handles, and disk usage with native monitoring tools at a
fixed interval. Run the controlled notifier cases periodically in a second
terminal. Preserve raw samples and calculate latency percentiles after the run;
do not infer percentiles from averages.

## 9. Evidence and exit decision

For each scenario retain the commands (with secrets removed), job IDs, relevant
JSON, expected versus actual result, timestamps, artifact version, and issue
link for deviations. Do not attach raw state or logs until reviewed for secrets.

At the end of each host campaign, capture final health, inventory the evidence,
and create a private archive for review:

```sh
capture doctor-final "$JOBMAN_BIN" --state-dir "$STATE_DIR" doctor --json
find "$EVIDENCE_DIR" -type f -print | sort >"$EVIDENCE_DIR/manifest.txt"
grep -R -n -E '(token|password|authorization|secret)' "$EVIDENCE_DIR" \
  >"$CAMPAIGN/evidence-secret-review.txt" || :
tar -czf "$CAMPAIGN/evidence-review-required.tar.gz" -C "$CAMPAIGN" evidence
printf 'Review %s before copying or attaching it.\n' \
  "$CAMPAIGN/evidence-review-required.tar.gz"
```

The grep output is a mandatory human-review queue, not proof that evidence is
safe. Do not archive the state directory by default: immutable job
specifications and raw logs can contain sensitive target data.
