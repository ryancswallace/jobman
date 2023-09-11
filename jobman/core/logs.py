import logging
import os
from datetime import datetime
from typing import Optional

from ..config import JobmanConfig
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
    config: JobmanConfig,
    displayer: Displayer,
    logger: logging.Logger,
) -> int:
    return os.EX_OK
