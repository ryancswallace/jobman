import json
import sys
from abc import ABC
from dataclasses import dataclass
from enum import Enum, auto
from typing import Dict, Optional, TextIO, Union

from rich.console import Console
from rich.table import Table

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
        content: Union[str, Table],
        stream: TextIO,
        level: Optional[DisplayLevel] = None,
        style: Optional[Union[DisplayStyle, str]] = None,
    ) -> None:
        raise NotImplementedError("Displayer class is an ABC")


class SimpleDisplayer(Displayer):
    """Displays all output unformatted to stdout."""

    def display(self, content: Union[str, Table], *args, **kwargs) -> None:  # type: ignore[no-untyped-def]
        print(content)


class AntiDisplayer(Displayer):
    """A displayer that swallows display messages."""

    def display(self, content: Union[str, Table], *args, **kwargs) -> None:  # type: ignore[no-untyped-def]
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
        content: Union[str, Table],
        stream: TextIO,
        level: Optional[DisplayLevel] = DisplayLevel.NORMAL,
        style: Optional[Union[DisplayStyle, str]] = DisplayStyle.NORMAL,
    ) -> None:
        """
        Display content to the specified stream using a customized style.
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
            content_json: Dict[str, str] = {"message": str(content)}
            console.print_json(json.dumps(content_json))
        elif self.plain:
            console.print(content)
        else:
            console.print(content, style=rich_style)

    def display_exception(self, e: Exception) -> None:
        """
        Display an interpretable error message for the specified exception.
        """
        self.display(
            f"ERROR! {e}", sys.stderr, DisplayLevel.ALWAYS, DisplayStyle.FAILURE
        )
