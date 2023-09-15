import logging
import os
from datetime import datetime
from typing import Optional

from ..base_logger import make_logger
from ..config import JobmanConfig, load_config
from ..display import Displayer


def display_logs(
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


def logs(
    job_id: str,
    hide_stdout: bool = False,
    hide_stderr: bool = False,
    follow: bool = False,
    no_log_prefix: bool = False,
    tail: Optional[int] = None,
    since: Optional[datetime] = None,
    until: Optional[datetime] = None,
    config: Optional[JobmanConfig] = None,
    logger: Optional[logging.Logger] = None,
) -> None:
    if not config:
        config = load_config()
    if not logger:
        logger = make_logger(logging.WARN)
