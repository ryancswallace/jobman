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

# validate arguments
if [ "$#" -ne 1 ]; then
    echo "Must provide exactly one argument: the version number to update to."
    exit 1
fi

# check that working tree is clean
git status --porcelain=v1 2>/dev/null | grep -q '.*' > /dev/null
WORKTREE_CLEAN=$?
if [ "$WORKTREE_CLEAN" -ne 1 ]; then
    echo -e "\nUncommitted changes in the working tree! Commit or stash changes before bumping the version. Aborting."
    exit 1
fi

# calculate the version and tag
VERSION=$1
TAG="v$VERSION"
echo "Updating to version $VERSION with tag $TAG..."

# update pyproject.toml
echo "Updating version in pyproject.toml..."
sed -i 's/version = "[0-9]\+\.[0-9]\+\.[0-9]\+.*"/version = "'"$VERSION"'"/g' pyproject.toml

# check that pyproject.toml changed
git status --porcelain=v1 2>/dev/null | grep -q 'M pyproject.toml'
TOML_UNCHANGED=$?
if [ "$TOML_UNCHANGED" -ne 0 ]; then
    echo -e "\nVersion number unchanged! Aborting."
    exit 1
fi

echo -e "\nTo commit, tag, and publish the new version run the following commands:"
echo "git add pyproject.toml \\"
echo "  && git commit -m \"chore: bump to version $VERSION\" \\"
echo "  && git push origin HEAD \\"
echo "  && git tag -a \""$TAG"\" -m \"release version $VERSION\" \\"
echo "  && git push origin \""$TAG"\""