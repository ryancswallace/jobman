#!/bin/sh

###
### Use this script to commit and tag a new version of the package.
###
### For example, to bump the version to 1.2.3, with a clean working
### tree, run the following command:
### $ ./bumpver.sh 1.2.3
###
### The script will do the following:
### 0. Check that the working tree is clean
### 1. Update the version number in pyproject.toml
### 2. Commit and tag the result
### 3. Print out the git push command to run to publish the change to GitHub (and, by extension, PyPI)
###

if [ "$#" -ne 1 ]; then
    echo "Must provide exactly one argument: the version number to update to."
    exit 1
fi

# check that working tree is clean
git status --porcelain=v1 2>/dev/null | grep -q '.*' > /dev/null
WORKTREE_CLEAN=$?
if [ "$WORKTREE_CLEAN" -ne 1 ]; then
    echo "Uncommitted changes in the working tree. Commit or stash changes before bumping the version."
    exit 1
fi


VERSION=$1
TAG="v$VERSION"

echo "Updating to version $VERSION with tag $TAG"


# sed -i 's/version = "[0-9]\+\.[0-9]\+\.[0-9]\+.*"/version = "'"$VERSION"'"/g' pyproject.toml

# git add pyproject.toml
# git commit -m "chore: bump to version $VERSION"
# git tag -a "$TAG" -m "release version $VERSION"
# git push origin "$TAG"