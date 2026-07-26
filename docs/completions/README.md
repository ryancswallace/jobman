# Shell completions

`make gen-completions` generates completion scripts for Bash, Zsh, and
PowerShell beneath this directory. GoReleaser includes them in portable archives
and installs the Bash and Zsh variants, together with their shell completion
runtimes, in native Linux packages. Debian packages use Zsh's
`vendor-completions` directory; RPM and APK packages use `site-functions`.

Start a new shell after installing a native package. An already-running Bash or
Zsh process may have cached the completions that were present when it started.

The generated files are ignored by Git because they are derived from the Cobra
command tree. Do not edit them directly; update the commands or the generator in
`devel/autocomplete/` instead.
