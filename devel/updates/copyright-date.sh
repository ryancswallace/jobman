#!/usr/bin/sh

# Update the ending year of the project's copyright ranges in tracked files.

set -eu

for command in date git sed xargs
do
    if ! command -v "$command" >/dev/null 2>&1
    then
        echo "error: required command not found: $command" >&2
        exit 127
    fi
done

START_YEAR=2021
CURRENT_YEAR=$(date -u '+%Y')

pattern="© $START_YEAR-[0-9][0-9][0-9][0-9]"
if git grep -Iq "$pattern" -- .
then
    git grep -Ilz "$pattern" -- . \
        | xargs -0 -r sed -i \
            "s/© $START_YEAR-[0-9]\{4\}/© $START_YEAR-$CURRENT_YEAR/g"
else
    status=$?
    if [ "$status" -ne 1 ]
    then
        exit "$status"
    fi
fi
