#!/usr/bin/sh

# Synchronize the tracked changelog with the newest reachable semantic tag.

set -eu

for command in awk git grep mv
do
    if ! command -v "$command" >/dev/null 2>&1
    then
        echo "error: required command not found: $command" >&2
        exit 127
    fi
done

latest_tag=$(git describe --tags --abbrev=0 --match 'v[0-9]*' 2>/dev/null) || {
    echo 'error: no semantic-version release tag is available' >&2
    exit 1
}
version=${latest_tag#v}
if ! printf '%s\n' "$version" |
    grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$'
then
    echo "error: latest release tag is not vMAJOR.MINOR.PATCH: $latest_tag" >&2
    exit 1
fi

release_date=$(git show -s --format=%cs "${latest_tag}^{commit}")
if ! printf '%s\n' "$release_date" |
    grep -Eq '^[0-9]{4}-[0-9]{2}-[0-9]{2}$'
then
    echo "error: could not determine release date for $latest_tag" >&2
    exit 1
fi

previous_tag=$(git describe --tags --abbrev=0 --match 'v[0-9]*' "${latest_tag}^" 2>/dev/null || true)
if [ -n "$previous_tag" ]
then
    version_url="https://github.com/ryancswallace/jobman/compare/${previous_tag}...${latest_tag}"
else
    version_url="https://github.com/ryancswallace/jobman/releases/tag/${latest_tag}"
fi

changelog_tmp=.CHANGELOG.md.tmp.$$
trap 'rm -f "$changelog_tmp"' EXIT HUP INT TERM

if grep -Fqx "## [$version] - $release_date" CHANGELOG.md
then
    if ! grep -Fqx "[$version]: $version_url" CHANGELOG.md
    then
        echo "error: CHANGELOG.md has a $version heading but no matching comparison link" >&2
        exit 1
    fi
    awk '{ print }' CHANGELOG.md >"$changelog_tmp"
else
    awk \
        -v version="$version" \
        -v release_date="$release_date" \
        -v latest_tag="$latest_tag" \
        -v version_url="$version_url" '
        BEGIN {
            headings = 0
            links = 0
        }
        $0 == "## [Unreleased]" {
            print
            print ""
            print "## [" version "] - " release_date
            headings++
            next
        }
        /^\[Unreleased\]: / {
            print "[Unreleased]: https://github.com/ryancswallace/jobman/compare/" latest_tag "...HEAD"
            print "[" version "]: " version_url
            links++
            next
        }
        { print }
        END {
            if (headings != 1 || links != 1) {
                exit 1
            }
        }
    ' CHANGELOG.md >"$changelog_tmp" || {
        echo 'error: CHANGELOG.md must contain one Unreleased heading and comparison link' >&2
        exit 1
    }
fi

mv "$changelog_tmp" CHANGELOG.md
trap - EXIT HUP INT TERM
