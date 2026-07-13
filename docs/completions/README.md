# Shell completions

`make gen-completions` generates completion scripts for Bash, Zsh, and
PowerShell beneath this directory. GoReleaser includes them in portable archives
and installs the Bash and Zsh variants in native Linux packages.

The generated files are ignored by Git because they are derived from the Cobra
command tree. Do not edit them directly; update the commands or the generator in
`devel/autocomplete/` instead.
