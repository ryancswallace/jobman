#!/bin/sh

# Disposable command notifier for release-candidate delivery and recovery tests.

set -eu

usage() {
	echo 'usage: command-notifier.sh success|failure|slow|slow-once OUTPUT_DIR' >&2
	exit 2
}

if [ "$#" -ne 2 ]; then
	usage
fi

mode=$1
output_dir=$2
case $mode in
	success|failure|slow|slow-once) ;;
	*) usage ;;
esac

umask 077
mkdir -p "$output_dir"
stamp=$(date -u '+%Y%m%dT%H%M%SZ')
prefix=$output_dir/$stamp-$$
printf '%s\n' "$PPID" >"$prefix.supervisor-pid"
cat >"$prefix.event.json"

case $mode in
	success)
		exit 0
		;;
	failure)
		printf 'dogfood command notifier intentional failure\n' >&2
		exit 19
		;;
	slow)
		touch "$prefix.claimed"
		sleep 300
		;;
	slow-once)
		if [ -e "$output_dir/slow-once.used" ]; then
			exit 0
		fi
		touch "$output_dir/slow-once.used" "$prefix.claimed"
		sleep 300
		;;
esac
