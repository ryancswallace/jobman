import os
from datetime import datetime
from typing import Optional

from ..display import Displayer


def logs(
    job_id: str,
    hide_stdout: bool,
    hide_stderr: bool,
    follow: bool,
    no_log_prefix: bool,
    tail: Optional[int],
    since: Optional[datetime],
    until: Optional[datetime],
    displayer: Displayer,
) -> int:
    return os.EX_OK
