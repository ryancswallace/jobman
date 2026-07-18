#!/bin/sh

# Disposable target workload for native Unix process-tree lifecycle tests.

set -eu

usage() {
	echo 'usage: unix-process-tree.sh graceful|stubborn PID_FILE PROGRESS_FILE' >&2
	exit 2
}

if [ "$#" -ne 3 ]; then
	usage
fi

mode=$1
pid_file=$2
progress_file=$3

case $mode in
	graceful|stubborn) ;;
	*) usage ;;
esac

case $0 in
	/*) script=$0 ;;
	*)
		echo 'unix-process-tree.sh: invoke this helper by absolute path' >&2
		exit 2
		;;
esac

umask 077
mkdir -p "$(dirname "$pid_file")" "$(dirname "$progress_file")"
: >"$pid_file"
: >"$progress_file"

run_progress_loop() {
	role=$1
	while :; do
		printf '%s %s\n' "$role" "$(date -u '+%Y-%m-%dT%H:%M:%SZ')" >>"$progress_file"
		sleep 1
	done
}

case ${JOBMAN_DOGFOOD_ROLE-} in
	grandchild)
		printf 'grandchild %s\n' "$$" >>"$pid_file"
		if [ "$mode" = graceful ]; then
			trap 'exit 0' TERM INT
		else
			trap '' TERM INT
		fi
		run_progress_loop grandchild
		;;
	child)
		printf 'child %s\n' "$$" >>"$pid_file"
		JOBMAN_DOGFOOD_ROLE=grandchild "$script" "$mode" "$pid_file" "$progress_file" &
		grandchild=$!
		if [ "$mode" = graceful ]; then
			trap 'kill -TERM "$grandchild" 2>/dev/null || :; wait "$grandchild" 2>/dev/null || :; exit 0' TERM INT
		else
			trap '' TERM INT
		fi
		run_progress_loop child
		;;
esac

printf 'parent %s\n' "$$" >>"$pid_file"
JOBMAN_DOGFOOD_ROLE=child "$script" "$mode" "$pid_file" "$progress_file" &
child=$!

if [ "$mode" = graceful ]; then
	trap 'kill -TERM "$child" 2>/dev/null || :; wait "$child" 2>/dev/null || :; exit 0' TERM INT
else
	trap '' TERM INT
fi

wait "$child"
