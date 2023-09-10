"""
Convenience method for installing jobman shell completion scripts.
"""

import os
import sys
from pathlib import Path
from typing import Dict, NamedTuple, Optional

from ..display import Displayer, DisplayStyle
from ..exceptions import JobmanError


class Shell(NamedTuple):
    name: str
    config_path: Path
    completion_script: str


COMPLETION_FLAG = "managed by jobman install-completions"
COMPLETION_SUPPORTED_SHELLS: Dict[str, Shell] = {
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
        raise JobmanError(
            f"Can't infer parent shell. Specify the shell explicitly.",
            exit_code=os.EX_NOTFOUND,
        )

    shell_path = Path(shell_var)
    shell = shell_path.name

    return shell


def install_completions(shell_name: Optional[str], displayer: Displayer) -> int:
    """
    Ensure shell completions installed for the specified shell.
    """
    shell_name = shell_name or _get_shell_name()
    shell = COMPLETION_SUPPORTED_SHELLS.get(shell_name)
    if not shell:
        raise JobmanError(
            f"Completions are not supported for {shell_name} shell.",
            exit_code=os.EX_UNAVAILABLE,
        )

    exists = _search(COMPLETION_FLAG, shell.config_path)
    if not exists:
        _append(shell.completion_script, shell.config_path)
        displayer.display(
            f"Installed completions for {shell.name} shell",
            stream=sys.stderr,
            style=DisplayStyle.SUCCESS,
        )
    else:
        displayer.display(
            f"Completions already installed for {shell.name} shell",
            stream=sys.stderr,
            style=DisplayStyle.NORMAL,
        )

    return os.EX_OK
