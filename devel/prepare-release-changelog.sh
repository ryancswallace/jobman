#!/bin/sh

# Render a release-local changelog after semantic-release has created the tag.
# The tracked source stays unchanged; GoReleaser packages this generated copy.

set -eu

if [ "$#" -ne 1 ] || [ -z "$1" ]; then
	printf 'usage: %s OUTPUT\n' "$0" >&2
	exit 2
fi

output=$1
case $output in
	CHANGELOG.md | ./CHANGELOG.md)
		echo 'error: release changelog output must not replace the tracked source' >&2
		exit 2
		;;
esac

for command in cp dirname mkdir; do
	if ! command -v "$command" >/dev/null 2>&1; then
		echo "error: required command not found: $command" >&2
		exit 127
	fi
done

mkdir -p "$(dirname "$output")"
cp CHANGELOG.md "$output"
CHANGELOG_FILE=$output ./devel/updates/release-metadata.sh
