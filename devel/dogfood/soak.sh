#!/bin/sh

# Run a bounded release-candidate soak using only the assembled Jobman binary.

set -eu

usage() {
	cat >&2 <<'EOF'
usage: soak.sh STATE_DIR EVIDENCE_DIR DURATION_SECONDS

Set JOBMAN_BIN to select the release-candidate binary (default: jobman).
DURATION_SECONDS must be a positive integer. This helper is for disposable
state only and intentionally does not configure external notification systems.
EOF
	exit 2
}

if [ "$#" -ne 3 ]; then
	usage
fi

state_dir=$1
evidence_dir=$2
duration=$3
jobman_bin=${JOBMAN_BIN-jobman}

case $duration in
	''|*[!0-9]*|0) usage ;;
esac

if ! command -v "$jobman_bin" >/dev/null 2>&1 && [ ! -x "$jobman_bin" ]; then
	echo "soak.sh: Jobman binary is not executable: $jobman_bin" >&2
	exit 2
fi

umask 077
mkdir -p "$state_dir" "$evidence_dir"
started=$(date +%s)
deadline=$((started + duration))
iteration=0
summary=$evidence_dir/soak-summary.tsv
diagnostics=$evidence_dir/soak-diagnostics.log
printf 'iteration\ttimestamp\tstate_kib\tjobs\n' >"$summary"
: >"$diagnostics"

jm() {
	"$jobman_bin" --state-dir "$state_dir" "$@"
}

while [ "$(date +%s)" -lt "$deadline" ]; do
	iteration=$((iteration + 1))

	# The submitted script expands in the managed target shell, not this helper.
	# shellcheck disable=SC2016
	success_id=$(jm run --name "soak-success-$iteration" \
		--log-segment-bytes 64 --log-segments 3 -- \
		sh -c 'i=0; while [ "$i" -lt 40 ]; do printf "stdout-%03d\n" "$i"; printf "stderr-%03d\n" "$i" >&2; i=$((i + 1)); done')
	retry_id=$(jm run --name "soak-retry-$iteration" --retries 2 \
		--retryable-exit-code 17 --retry-delay 10ms -- \
		sh -c 'exit 17')
	timeout_id=$(jm run --name "soak-timeout-$iteration" --run-timeout 100ms -- sleep 2)
	live_id=$(jm run --name "soak-input-$iteration" --stdin live -- sh -c 'cat >/dev/null')

	printf 'iteration %s\n' "$iteration" | jm input "$live_id" >/dev/null
	jm input --eof "$live_id" </dev/null >/dev/null

	for job_id in "$success_id" "$retry_id" "$timeout_id" "$live_id"; do
		if ! jm wait "$job_id" >>"$diagnostics" 2>&1; then
			printf 'iteration=%s job=%s wait_failed\n' "$iteration" "$job_id" >>"$diagnostics"
		fi
	done

	jm doctor --json >"$evidence_dir/doctor-latest.json"
	jm clean --older-than 0s >"$evidence_dir/clean-latest.txt"
	jobs=$(jm list --all --json | sed -n 's/.*"count":[[:space:]]*\([0-9][0-9]*\).*/\1/p')
	state_kib=$(du -sk "$state_dir" | awk '{print $1}')
	printf '%s\t%s\t%s\t%s\n' \
		"$iteration" "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" "$state_kib" "${jobs-unknown}" >>"$summary"
done

jm doctor --json >"$evidence_dir/doctor-final.json"
printf 'completed_iterations=%s\n' "$iteration" >>"$diagnostics"
printf 'Soak completed: %s iterations; evidence: %s\n' "$iteration" "$evidence_dir"
