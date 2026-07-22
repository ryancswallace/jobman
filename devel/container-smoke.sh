#!/bin/sh

# Exercise the published container contract against either a local tag or an
# immutable registry digest. Keep this helper POSIX-sh compatible so the same
# checks can run from the Makefile and GitHub-hosted Linux runners.
set -eu

usage() {
	printf '%s\n' \
		'usage: devel/container-smoke.sh IMAGE EXPECTED_VERSION EXPECTED_COMMIT' >&2
	exit 2
}

[ "$#" -eq 3 ] || usage

image=$1
expected_version=$2
expected_commit=$3
docker=${DOCKER:-docker}
docker_progress=${DOCKER_PROGRESS:-plain}

[ -n "$image" ] || usage
[ -n "$expected_version" ] || usage
if ! printf '%s\n' "$expected_commit" \
	| grep -Eq '^[0-9a-f]{40}([0-9a-f]{24})?$'; then
	printf 'container smoke: invalid expected commit: %s\n' "$expected_commit" >&2
	exit 2
fi

script_dir=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
repository_root=$(dirname -- "$script_dir")
volume="jobman-container-smoke-$$"
derived="jobman-container-smoke-$$:local"

cleanup() {
	"$docker" volume rm -f "$volume" >/dev/null 2>&1 || true
	"$docker" image rm -f "$derived" >/dev/null 2>&1 || true
}
trap cleanup 0 1 2 15

short_commit=$(printf '%.12s' "$expected_commit")
expected_display="jobman $expected_version ($short_commit)"
actual_display=$("$docker" run --rm "$image" --version)
if [ "$actual_display" != "$expected_display" ]; then
	printf 'container smoke: version = %s, want %s\n' \
		"$actual_display" "$expected_display" >&2
	exit 1
fi

# The expansions below intentionally occur in the image's shell.
# shellcheck disable=SC2016
"$docker" run --rm --entrypoint /bin/sh "$image" -ec '
	test "$(id -u)" = 10001
	test "$(id -g)" = 10001
	test "$(stat -c %a /home/jobman/.config/jobman)" = 700
	test "$(stat -c %a /home/jobman/.local/state/jobman)" = 700
	test -s /usr/share/licenses/jobman/LICENSE
	test -s /usr/share/licenses/jobman/THIRD_PARTY_NOTICES.md
'

"$docker" volume create "$volume" >/dev/null
"$docker" build --progress="$docker_progress" \
	--build-arg "BASE_IMAGE=$image" \
	--tag "$derived" \
	"$repository_root/tests/container"

"$docker" run --rm \
	--volume "$volume:/home/jobman/.local/state/jobman" \
	"$derived" run --wait -- /opt/jobman/bin/container-target

inspection=$("$docker" run --rm \
	--volume "$volume:/home/jobman/.local/state/jobman" \
	"$derived" list --completed --outcome success --limit 1 --json)
printf '%s\n' "$inspection" | grep -Fq '"phase":"completed"'
printf '%s\n' "$inspection" | grep -Fq '"outcome":"success"'

printf 'container smoke: %s passed\n' "$image"
