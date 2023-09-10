import os
from datetime import datetime
from typing import Optional, Tuple

import click

from ..display import Displayer


def purge(
    job_id: Tuple[str, ...],
    _all: bool,
    metadata: bool,
    since: Optional[datetime],
    until: Optional[datetime],
    displayer: Displayer,
) -> int:
    if not (bool(job_id) ^ _all):
        raise click.exceptions.UsageError(
            "Must supply either a job-id argument or the -a/--all flag, but not both"
        )
    return os.EX_OK
