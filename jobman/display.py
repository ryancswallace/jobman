import json
import os
import sys
from abc import ABC
from dataclasses import dataclass
from enum import Enum, auto
from typing import Any, Optional, TextIO, Union

from rich.console import Console
from rich.table import Table

from .exceptions import JobmanError
from .models import JobmanModelEncoder

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
    """A displayer renders output to stdout and stderr via its required pprint,
    print, and jprint methods."""

    def print(
        self,
        pretty_content: Optional[Union[str, Table]],
        plain_content: Optional[str],
        json_content: Optional[Any],
        stream: TextIO,
        level: Optional[DisplayLevel] = None,
        style: Optional[Union[DisplayStyle, str]] = None,
    ) -> None:
        raise NotImplementedError("Displayer class is an ABC")


class SimpleDisplayer(Displayer):
    """Displays output unformatted to stdout."""

    def print(  # type: ignore[no-untyped-def]
        self,
        pretty_content: Optional[Union[str, Table]],
        plain_content: Optional[str],
        json_content: Optional[Any],
        *args,
        **kwargs,
    ) -> None:
        print(plain_content)


class AntiDisplayer(Displayer):
    """A displayer that swallows display messages."""

    def print(  # type: ignore[no-untyped-def]
        self,
        pretty_content: Optional[Union[str, Table]],
        plain_content: Optional[str],
        json_content: Optional[Any],
        *args,
        **kwargs,
    ) -> None:
        pass


@dataclass
class RichDisplayer(Displayer):
    """Displays richly formatted output."""

    quiet: bool
    json: bool
    plain: bool

    def __post_init__(self) -> None:
        if self.json and self.plain:
            raise JobmanError(
                "Can't specify both -p/--plain and -j/--json", exit_code=os.EX_CONFIG
            )

    def _should_show(self, level: Optional[DisplayLevel]) -> bool:
        if level == DisplayLevel.ALWAYS:
            return True
        elif level in [DisplayLevel.NORMAL, DisplayLevel.DETAIL]:
            return not self.quiet
        else:
            return True

    def print(
        self,
        pretty_content: Optional[Union[str, Table]],
        plain_content: Optional[str],
        json_content: Optional[Any],
        stream: TextIO,
        level: Optional[DisplayLevel] = None,
        style: Optional[Union[DisplayStyle, str]] = None,
    ) -> None:
        # skip if display level of content is below what's configured
        if not self._should_show(level):
            return

        # dispatch to the configured printer
        if self.json and json_content is not None:
            self._json_print(json_content, stream, level)
        if self.plain and plain_content is not None:
            self._plain_print(plain_content, stream, level)
        if not (self.json or self.plain) and pretty_content is not None:
            self._pretty_print(pretty_content, stream, level, style)

    def _pretty_print(
        self,
        content: Union[str, Table],
        stream: TextIO,
        level: Optional[DisplayLevel] = DisplayLevel.NORMAL,
        style: Optional[Union[DisplayStyle, str]] = DisplayStyle.NORMAL,
    ) -> None:
        """
        Display content to the specified stream using a customized style.
        """
        if isinstance(style, str):
            rich_style = style
        elif style == DisplayStyle.SUCCESS:
            rich_style = "bold green"
        elif style == DisplayStyle.NORMAL:
            rich_style = ""
        elif style == DisplayStyle.FAILURE:
            rich_style = "bold red"
        else:
            rich_style = ""

        console = stdout if stream == sys.stdout else stderr
        console.print(content, style=rich_style)

    def _plain_print(
        self,
        content: str,
        stream: TextIO,
        level: Optional[DisplayLevel] = DisplayLevel.NORMAL,
    ) -> None:
        print(content, file=stream)

    def _json_print(
        self,
        content: Any,
        stream: TextIO,
        level: Optional[DisplayLevel] = DisplayLevel.NORMAL,
    ) -> None:
        console = stdout if stream == sys.stdout else stderr
        if isinstance(content, str):
            # assume strings are already JSON formatted
            console.print_json(content)
        else:
            # other types get serialized to JSON
            console.print_json(json.dumps(content, cls=JobmanModelEncoder))

    def print_exception(self, e: Exception) -> None:
        """
        Display an interpretable error message for the specified exception.
        """
        self.print(
            pretty_content=f"ERROR! {e}",
            plain_content=f"ERROR! {e}",
            json_content={"result": "error", "message": str(e)},
            stream=sys.stderr,
            level=DisplayLevel.ALWAYS,
            style=DisplayStyle.FAILURE,
        )
