#!/bin/sh

# Synchronize the tracked changelog with every reachable stable semantic tag.

set -eu

for command in awk cat git grep mktemp rm
do
    if ! command -v "$command" >/dev/null 2>&1
    then
        echo "error: required command not found: $command" >&2
        exit 127
    fi
done

changelog_file=${CHANGELOG_FILE:-CHANGELOG.md}
changelog_tmp=$(mktemp "${changelog_file}.tmp.XXXXXXXXXX") || {
    echo "error: could not create a temporary changelog file" >&2
    exit 1
}
trap 'rm -f "$changelog_tmp"' EXIT HUP INT TERM

stable_tags=$(git tag --merged HEAD --list 'v*' --sort=v:refname |
    grep -E '^v([1-9][0-9]*\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)|0\.[1-9][0-9]*\.(0|[1-9][0-9]*))$' || true)
if [ -z "$stable_tags" ]
then
    echo 'error: no stable semantic-version release tag is available' >&2
    exit 1
fi

previous_tag=
latest_tag=
for release_tag in $stable_tags
do
    version=${release_tag#v}
    release_date=$(git show -s --format=%cs "${release_tag}^{commit}")
    if ! printf '%s\n' "$release_date" |
        grep -Eq '^[0-9]{4}-[0-9]{2}-[0-9]{2}$'
    then
        echo "error: could not determine release date for $release_tag" >&2
        exit 1
    fi

    if [ -n "$previous_tag" ]
    then
        version_url="https://github.com/ryancswallace/jobman/compare/${previous_tag}...${release_tag}"
    else
        version_url="https://github.com/ryancswallace/jobman/releases/tag/${release_tag}"
    fi

    if grep -F "## [$version] - " "$changelog_file" |
        grep -Eq '^## \[[0-9]+\.[0-9]+\.[0-9]+\] - [0-9]{4}-[0-9]{2}-[0-9]{2}$'
    then
        if ! grep -Fqx "[$version]: $version_url" "$changelog_file"
        then
            echo "error: $changelog_file has a $version heading but no matching comparison link" >&2
            exit 1
        fi
    else
        awk \
            -v version="$version" \
            -v release_date="$release_date" \
            -v release_tag="$release_tag" \
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
                print "[Unreleased]: https://github.com/ryancswallace/jobman/compare/" release_tag "...HEAD"
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
        ' "$changelog_file" >"$changelog_tmp" || {
            echo "error: $changelog_file must contain one Unreleased heading and comparison link" >&2
            exit 1
        }

        cat "$changelog_tmp" > "$changelog_file"
    fi

    previous_tag=$release_tag
    latest_tag=$release_tag
done

if ! grep -Fqx \
    "[Unreleased]: https://github.com/ryancswallace/jobman/compare/$latest_tag...HEAD" \
    "$changelog_file"
then
    echo "error: $changelog_file has a stale Unreleased comparison link" >&2
    exit 1
fi

rm -f "$changelog_tmp"
trap - EXIT HUP INT TERM
