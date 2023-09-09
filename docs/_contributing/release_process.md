---
title: Release Process
layout: default
nav_order: 3
---

# Release Process

We use the [SemVer](http://semver.org/) versioning scheme. To release a new version of the package, select the new version number and use the `bumpver.sh` script. For example, to update to version 1.2.3, run `./bumpver.sh 1.2.3`.

The script will do the following:
0. Check that the working tree is clean
1. Update the version number in pyproject.toml
3. Print out the git commit, tag, and push commands to publish the change to GitHub (and, by extension, PyPI)
