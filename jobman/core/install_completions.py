"""
Convenience method for installing jobman shell completion scripts.
"""

import os
from collections import namedtuple
from pathlib import Path
from typing import Optional

Shell = namedtuple("Shell", "name config_path completion_script")

COMPLETION_FLAG = "managed by jobman install-completions"
COMPLETION_SUPPORTED_SHELLS = {
    "bash": Shell(
        name="bash",
        config_path=Path("~/.bashrc").expanduser(),
        completion_script=(
            f'eval "$(_JOBMAN_COMPLETE=bash_source jobman)"  # {COMPLETION_FLAG}'
        ),
    ),
    "zsh": Shell(
        name="zsh",
        config_path=Path("~/.zshrc").expanduser(),
        completion_script=(
            f'eval "$(_JOBMAN_COMPLETE=zsh_source jobman)"  # {COMPLETION_FLAG}'
        ),
    ),
    "fish": Shell(
        name="fish",
        config_path=Path("~/.config/fish/completions/foo-bar.fish").expanduser(),
        completion_script=(
            f"_JOBMAN_COMPLETE=fish_source jobman | source  # {COMPLETION_FLAG}"
        ),
    ),
}


def _search(flag: str, f: Path) -> bool:
    """
    Returns true iff the specified flag exists in the file f.
    """
    with open(f, "r") as fp:
        return flag in fp.read()


def _append(text: str, f: Path) -> None:
    """
    Appends the text to the file f.
    """
    f.parent.mkdir(parents=True, exist_ok=True)
    with open(f, "a+") as fp:
        fp.write(text + "\n")


def _get_shell_name() -> str:
    """
    Returns the name of the parent shell.
    """
    shell_var = os.environ.get("SHELL")
    if shell_var is None:
        raise ValueError("TODO")

    shell_path = Path(shell_var)
    shell = shell_path.name

    return shell


def install_completions(shell_name: Optional[str]) -> str:
    """
    Ensure shell completions installed for the specified shell.
    """
    shell_name = shell_name or _get_shell_name()
    shell = COMPLETION_SUPPORTED_SHELLS.get(shell_name)
    if not shell:
        raise ValueError("TODO SHELL NOT SUPPORTED")

    exists = _search(COMPLETION_FLAG, shell.config_path)
    if not exists:
        _append(shell.completion_script, shell.config_path)
        return f"Installed completions for {shell.name} shell"
    else:
        return f"Completions already installed for {shell.name} shell"
