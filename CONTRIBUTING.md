# Contributing to benchmatrix

Thanks for considering a contribution.

## Before you start

For substantial changes, open an issue first so the problem and proposed
direction can be discussed. Small fixes and documentation improvements can go
directly to a pull request.

By participating, you agree to follow the [code of conduct](CODE_OF_CONDUCT.md).
Please report security vulnerabilities through [SECURITY.md](SECURITY.md), not a
public issue.

## Local checks

The normal pre-submission loop is:

```bash
make format
make check
```

Documentation changes should pass `make docs`, and public API behavior changes
should update tests, docs, and [CHANGELOG.md](CHANGELOG.md) when users will
notice the change.

## Pull requests

Keep each pull request focused on one coherent change. In the description,
explain the problem, the chosen approach, compatibility impact, and verification
performed.

Contributions are accepted under the project's [MIT License](LICENSE).
