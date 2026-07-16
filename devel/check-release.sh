#!/bin/sh

set -eu

die() {
	printf 'release check: %s\n' "$*" >&2
	exit 1
}

require_line() {
	file=$1
	line=$2
	grep -Fqx "$line" "$file" || die "$file is missing: $line"
}

check_metadata() {
	latest_tag=$(git describe --tags --abbrev=0 --match 'v[0-9]*' 2>/dev/null) ||
		die 'no semantic-version release tag is available'
	version=${latest_tag#v}
	printf '%s\n' "$version" |
		grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$' ||
		die "latest release tag is not vMAJOR.MINOR.PATCH: $latest_tag"
	release_date=$(git show -s --format=%cs "$latest_tag")

	require_line CITATION.cff "version: $version"
	require_line CITATION.cff "date-released: $release_date"
	require_line CHANGELOG.md "## [$version] - $release_date"
	require_line CHANGELOG.md \
		"[Unreleased]: https://github.com/ryancswallace/jobman/compare/$latest_tag...HEAD"
}

count_files() {
	directory=$1
	pattern=$2
	want=$3
	got=$(find "$directory" -maxdepth 1 -type f -name "$pattern" | wc -l | tr -d ' ')
	[ "$got" -eq "$want" ] ||
		die "$directory contains $got files matching $pattern; expected $want"
}

check_artifacts() {
	dist=${1:-dist}
	[ -d "$dist" ] || die "artifact directory does not exist: $dist"

	count_files "$dist" 'jobman_*_linux_*.tar.gz' 3
	count_files "$dist" 'jobman_*_darwin_*.tar.gz' 2
	count_files "$dist" 'jobman_*_windows_*.zip' 3
	count_files "$dist" 'jobman_*_linux_*.apk' 3
	count_files "$dist" 'jobman_*_linux_*.deb' 3
	count_files "$dist" 'jobman_*_linux_*.rpm' 3
	count_files "$dist" '*.sbom.json' 17
	count_files "$dist" 'jobman_*_checksums.txt' 1

	set -- "$dist"/jobman_*_checksums.txt
	checksum_file=$1
	(
		cd "$dist"
		sha256sum --check "$(basename "$checksum_file")"
	) >/dev/null || die 'artifact checksum verification failed'

	set -- "$dist"/jobman_*_linux_amd64.tar.gz
	archive=$1
	temporary=${TMPDIR:-/tmp}/jobman-release-check.$$
	trap 'rm -rf "$temporary"' EXIT HUP INT TERM
	mkdir -p "$temporary/extract"
	tar -tzf "$archive" >"$temporary/contents"

	for required in \
		jobman LICENSE README.md CHANGELOG.md CITATION.cff \
		etc/jobman/jobman.yml docs/manpage/jobman.1 \
		docs/completions/bash/jobman docs/completions/zsh/_jobman \
		docs/completions/powershell/jobman.ps1
	do
		require_line "$temporary/contents" "$required"
	done
	if grep -Eq '(^|/)\.gitkeep$|^docs/completions/README\.md$' "$temporary/contents"; then
		die 'portable archive contains repository-only completion scaffolding'
	fi

	tar -xzf "$archive" -C "$temporary/extract" jobman CITATION.cff
	binary_version=$(
		"$temporary/extract/jobman" --version |
			awk 'NR == 1 { print $2 }'
	)
	[ -n "$binary_version" ] || die 'could not read the packaged binary version'
	require_line "$temporary/extract/CITATION.cff" "version: $binary_version"

	rm -rf "$temporary"
	trap - EXIT HUP INT TERM
}

case ${1:-} in
	metadata)
		check_metadata
		;;
	artifacts)
		check_artifacts "${2:-dist}"
		;;
	*)
		die 'usage: devel/check-release.sh metadata | artifacts [DIST_DIR]'
		;;
esac
