#!/bin/sh

# Capture one non-interactive dogfood command without changing its exit status.

set -u

usage() {
	cat >&2 <<'EOF'
usage: capture.sh EVIDENCE_DIR LABEL COMMAND [ARG ...]

LABEL may contain letters, numbers, dots, underscores, and hyphens. The command
and its output may contain sensitive data; use this only with disposable test
values and review the evidence before sharing it.
EOF
	exit 2
}

if [ "$#" -lt 3 ]; then
	usage
fi

evidence_dir=$1
label=$2
shift 2

case $label in
	''|*[!A-Za-z0-9._-]*)
		echo "capture.sh: invalid label: $label" >&2
		exit 2
		;;
esac

umask 077
mkdir -p "$evidence_dir" || exit 1
prefix=$evidence_dir/$label

for suffix in command.txt metadata.txt stdout stderr; do
	if [ -e "$prefix.$suffix" ]; then
		echo "capture.sh: evidence already exists: $prefix.$suffix" >&2
		exit 2
	fi
done

quote_argument() {
	printf "'"
	printf '%s' "$1" | sed "s/'/'\\\\''/g"
	printf "'"
}

{
	first=true
	for argument do
		if [ "$first" = true ]; then
			first=false
		else
			printf ' '
		fi
		quote_argument "$argument"
	done
	printf '\n'
} >"$prefix.command.txt"

started_at=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
set +e
"$@" >"$prefix.stdout" 2>"$prefix.stderr"
status=$?
set -e
finished_at=$(date -u '+%Y-%m-%dT%H:%M:%SZ')

{
	printf 'started_at=%s\n' "$started_at"
	printf 'finished_at=%s\n' "$finished_at"
	printf 'exit_status=%s\n' "$status"
} >"$prefix.metadata.txt"

if [ -s "$prefix.stdout" ]; then
	cat "$prefix.stdout"
fi
if [ -s "$prefix.stderr" ]; then
	cat "$prefix.stderr" >&2
fi

exit "$status"
