# Devcontainer

This devcontainer is the reproducible contributor environment for the project.

It provides:

* Go 1.26 on Debian Bookworm.
* Common Go tooling.
* GitHub CLI for pull request and release workflows.
* Docker-outside-of-Docker for optional local container checks.
* VS Code recommendations for Go, Markdown, CSpell, GitHub Actions, containers,
    YAML, and Makefile.

Docker-outside-of-Docker exposes the host Docker socket inside the container,
which means the devcontainer should be treated as a trusted development
environment. Keep local secrets in your editor, Codespaces secrets, or ignored
shell environment files instead of adding required container run arguments.

## Local Runtime Options

Runtime settings that depend on one developer's machine should stay out of the
shared `devcontainer.json`. This section describes how to configure three common
developer-specific local patches to `devcontainer.json`:

* local secrets;
* host Git configuration;
* a relaxed seccomp profile.

### Preliminary: ignoring changes to `devcontainer.json`

Before making local-only changes, tell Git to leave your local devcontainer
edits alone:

```bash
git update-index --skip-worktree .devcontainer/devcontainer.json
```

Then, before intentionally editing the shared devcontainer config again,
re-enable Git tracking for the file:

```bash
git update-index --no-skip-worktree .devcontainer/devcontainer.json
```

### Developer-specific settings

Add your machine-specific settings to `.devcontainer/devcontainer.json`. The
following snippet mounts your host `.gitconfig`, mounts in a local environment
variable file, a Codex auth file, and sets `seccomp=unconfined` (e.g., for Codex
sandboxing).

```json
"mounts": [
    "source=${localEnv:HOME}/.gitconfig,target=/home/vscode/.gitconfig,type=bind,consistency=cached",
    "source=${localEnv:HOME}/.local/share/devcontainer-bin,target=/home/vscode/.local/share/host-bin,type=bind,consistency=cached",
    "source=${localEnv:HOME}/.codex-devcontainer,target=/home/vscode/.codex,type=bind,consistency=cached"
],
"runArgs": [
    "--env-file",
    "${localWorkspaceFolder}/.devcontainer/.env.local",
    "--security-opt",
    "seccomp=unconfined"
]
```

If you use the `--env-file` option in `runArgs`, be sure to create
`.devcontainer/.env.local` with your local-only values. For example:

```dotenv
GH_TOKEN=github_pat_example
```

The `.env.local` file is ignored by Git.
