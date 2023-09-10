import json
import sys
from abc import ABC
from dataclasses import dataclass
from enum import Enum, auto
from typing import Optional, TextIO, Union

from rich.console import Console

stdout = Console()
stderr = Console(file=sys.stderr)


class DisplayLevel(Enum):
    """Importance of a display message, akin to log level."""

    ALWAYS = 0
    NORMAL = 1
    DETAIL = 2


class DisplayStyle(Enum):
    """Sytle in which to render a display message."""

    SUCCESS = auto()
    NORMAL = auto()
    FAILURE = auto()


class Displayer(ABC):
    """A displayer renders output to stdout and stderr via its required display method."""

    def display(
        self,
        text: str,
        stream: TextIO,
        level: Optional[DisplayLevel] = None,
        style: Optional[DisplayStyle] = None,
    ) -> None:
        raise NotImplementedError("Displayer class is an ABC")


class SimpleDisplayer(Displayer):
    """Displays all output unformatted to stdout."""

    def display(self, text: str, *args, **kwargs) -> None:  # type: ignore[no-untyped-def]
        print(text)


class AntiDisplayer(Displayer):
    """A displayer that swallows display messages."""

    def display(self, text: str, *args, **kwargs) -> None:  # type: ignore[no-untyped-def]
        pass


@dataclass
class RichDisplayer(Displayer):
    """Displays richly formatted output."""

    quiet: bool
    verbose: bool
    json: bool
    plain: bool

    def display(
        self,
        text: str,
        stream: TextIO,
        level: Optional[DisplayLevel] = DisplayLevel.NORMAL,
        style: Optional[Union[DisplayStyle, str]] = DisplayStyle.NORMAL,
    ) -> None:
        """
        Display text to the specified stream using a customized style.
        """
        if level == DisplayLevel.ALWAYS:
            show = True
        elif level == DisplayLevel.NORMAL:
            show = not self.quiet
        elif level == DisplayLevel.DETAIL:
            show = self.verbose

        if isinstance(style, str):
            rich_style = style
        elif style == DisplayStyle.SUCCESS:
            rich_style = "bold green"
        elif style == DisplayStyle.NORMAL:
            rich_style = ""
        elif style == DisplayStyle.FAILURE:
            rich_style = "bold red"

        if not show:
            return

        console = stdout if stream == sys.stdout else stderr
        if self.json:
            console.print_json(json.dumps({"message": text}))
        else:
            console.print(text, style=rich_style)

    def display_exception(self, e: Exception) -> None:
        """
        Display an interpretable error message for the specified exception.
        """
        self.display(
            f"ERROR! {e}", sys.stderr, DisplayLevel.ALWAYS, DisplayStyle.FAILURE
        )
