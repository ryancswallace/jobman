---
title: Introduction
layout: default
nav_order: 1
---

# Introduction

Jobman uses pyenv for Python version management and poetry for dependency management. Before working on Jobman, ensure you have `pyenv` and `poetry` installed and on your PATH.

The `Makefile` defines targets for common operations during development, including the following:
* `make setup`: set up and install the package
* `make fmt`: run the autoformatters
* `make test`: run the type tests and unit test suite
* `make build`: build the jobman wheel
