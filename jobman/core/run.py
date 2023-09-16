import logging
import os
import sys
from datetime import datetime, timedelta
from pathlib import Path
from typing import Optional, Tuple

from ..config import JobmanConfig
from ..display import Displayer, DisplayLevel, DisplayStyle
from .supervisor.run import build_job, run_job


def display_run(
    command: Tuple[str, ...],
    wait_time: Optional[datetime],
    wait_duration: Optional[timedelta],
    wait_for_files: Optional[Tuple[Path]],
    abort_time: Optional[datetime],
    abort_duration: Optional[timedelta],
    abort_for_files: Optional[Tuple[Path]],
    retry_attempts: Optional[int],
    retry_delay: Optional[timedelta],
    success_codes: Optional[Tuple[int]],
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
    job = build_job(
        command=command,
        wait_time=wait_time,
        wait_duration=wait_duration,
        wait_for_files=wait_for_files,
        abort_time=abort_time,
        abort_duration=abort_duration,
        abort_for_files=abort_for_files,
        retry_attempts=retry_attempts,
        retry_delay=retry_delay,
        success_codes=success_codes,
        notify_on_run_completion=notify_on_run_completion,
        notify_on_job_completion=notify_on_job_completion,
        notify_on_job_success=notify_on_job_success,
        notify_on_run_success=notify_on_run_success,
        notify_on_job_failure=notify_on_job_failure,
        notify_on_run_failure=notify_on_run_failure,
        follow=follow,
        config=config,
        logger=logger,
    )

    displayer.print(
        pretty_content=f"🏃  Submitted job [bold blue]{job.job_id}",
        plain_content=str(job.job_id),
        json_content={
            "result": "success",
            "message": "Job sumitted",
            "job_id": job.job_id,
        },
        stream=sys.stdout,
        level=DisplayLevel.NORMAL,
        style=DisplayStyle.SUCCESS,
    )

    run_job(job)

    return os.EX_OK
