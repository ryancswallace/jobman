import logging
import os
from datetime import datetime, timedelta
from pathlib import Path
from typing import Optional, Tuple

from ..config import JobmanConfig
from ..display import Displayer


def run(
    command: Tuple[str, ...],
    wait_time: Optional[datetime],
    wait_duration: Optional[timedelta],
    wait_for_file: Optional[Tuple[Path]],
    abort_time: Optional[datetime],
    abort_duration: Optional[timedelta],
    abort_for_file: Optional[Tuple[Path]],
    retry_attempts: Optional[int],
    retry_delay: Optional[timedelta],
    success_code: Optional[Tuple[str]],
    notify_on_run_completion: Optional[Tuple[str]],
    notify_on_job_completion: Optional[Tuple[str]],
    notify_on_job_success: Optional[Tuple[str]],
    notify_on_run_success: Optional[Tuple[str]],
    notify_on_job_failure: Optional[Tuple[str]],
    notify_on_run_failure: Optional[Tuple[str]],
    follow: bool,
    config: JobmanConfig,
    displayer: Displayer,
    logger: logging.Logger,
) -> int:
    return os.EX_OK
