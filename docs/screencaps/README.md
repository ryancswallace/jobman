# Terminal screencasts

Jobman's terminal demos are defined as
[VHS](https://github.com/charmbracelet/vhs) tapes under `tape/`. Each tape
produces a GIF for Markdown and a WebM video for browsers that support it.
Generated media is tracked so GitHub and the documentation site do not need VHS
at page-build time.

## Prerequisites

The Makefile installs the pinned VHS CLI into `bin/`. Rendering also requires
`zsh`, `ttyd`, and `ffmpeg` on `PATH`; VHS uses the latter two to provide and
encode the virtual terminal. Install those runtime dependencies with your
operating system's package manager.

The supported devcontainer installs all three native dependencies. Rebuild the
container after pulling changes to `.devcontainer/Dockerfile`, then verify the
environment with:

```console
make screencast-deps-check
```

Build Jobman and render every tape:

```console
make screencasts
```

Render one tape by its basename or path:

```console
make screencast TAPE=basic
make screencast TAPE=docs/screencaps/tape/retries.tape
```

Validate the one-to-one tape/GIF/WebM inventory without installing or running
VHS:

```console
make screencast-check
```

The render targets always build `bin/jobman` first and run VHS from this
directory, where tape output paths are rooted. Every tape uses a private
temporary state directory and must remove it during hidden cleanup. Review the
resulting media before committing it: timing, UUIDs, timestamps, and process
scheduling make terminal recordings intentionally nondeterministic.

When adding a tape named `example.tape`, declare both outputs at its top:

```text
Output gif/example.gif
Output webm/example.webm
```
