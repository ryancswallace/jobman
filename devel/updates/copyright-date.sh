#!/bin/sh

# Update the ending year of the project's copyright ranges in tracked files.

set -eu

for command in cat date git mktemp rm sed
do
    if ! command -v "$command" >/dev/null 2>&1
    then
        echo "error: required command not found: $command" >&2
        exit 127
    fi
done

temporary=
trap 'rm -f "${temporary:-}"' EXIT HUP INT TERM

START_YEAR=2021
CURRENT_YEAR=$(date -u '+%Y')

pattern="© $START_YEAR-[0-9][0-9][0-9][0-9]"
if git grep -Iq "$pattern" -- .
then
    git grep -Il "$pattern" -- . |
        while IFS= read -r file
        do
            temporary=$(mktemp "${file}.tmp.XXXXXXXXXX") || {
                echo "error: could not create a temporary file for $file" >&2
                exit 1
            }
            sed "s/© $START_YEAR-[0-9]\{4\}/© $START_YEAR-$CURRENT_YEAR/g" \
                "$file" > "$temporary"
            cat "$temporary" > "$file"
            rm -f "$temporary"
            temporary=
        done
else
    status=$?
    if [ "$status" -ne 1 ]
    then
        exit "$status"
    fi
fi

trap - EXIT HUP INT TERM
