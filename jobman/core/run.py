import logging
import os
import random
import shlex
import string
import sys
from datetime import datetime, timedelta
from pathlib import Path
from typing import Optional, Tuple

from ..base_logger import make_logger
from ..config import JobmanConfig, load_config
from ..display import Displayer, DisplayLevel, DisplayStyle
from ..host import get_host_id
from ..models import Job, JobState, init_db_models


def preproc_cmd(command: Tuple[str, ...]) -> str:
    if len(command) == 1:
        # covers the case of a single-word command, plus the case
        # of a command enclosed in quotes (double or single)
        return command[0]
    else:
        return shlex.join(command)


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
    job_id = run(
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
        pretty_content=f"🏃  Submitted job [bold blue]{job_id}",
        plain_content=job_id,
        json_content={"result": "success", "message": "Job sumitted", "job_id": job_id},
        stream=sys.stdout,
        level=DisplayLevel.NORMAL,
        style=DisplayStyle.SUCCESS,
    )
    return os.EX_OK


def _generate_random_job_id() -> str:
    id_len = 8
    return "".join(random.choices(string.hexdigits, k=id_len)).lower()


def run(
    command: Tuple[str, ...],
    wait_time: Optional[datetime] = None,
    wait_duration: Optional[timedelta] = None,
    wait_for_files: Optional[Tuple[Path]] = None,
    abort_time: Optional[datetime] = None,
    abort_duration: Optional[timedelta] = None,
    abort_for_files: Optional[Tuple[Path]] = None,
    retry_attempts: Optional[int] = None,
    retry_delay: Optional[timedelta] = None,
    success_codes: Optional[Tuple[int]] = None,
    notify_on_run_completion: Optional[Tuple[str]] = None,
    notify_on_job_completion: Optional[Tuple[str]] = None,
    notify_on_job_success: Optional[Tuple[str]] = None,
    notify_on_run_success: Optional[Tuple[str]] = None,
    notify_on_job_failure: Optional[Tuple[str]] = None,
    notify_on_run_failure: Optional[Tuple[str]] = None,
    follow: bool = False,
    config: Optional[JobmanConfig] = None,
    logger: Optional[logging.Logger] = None,
) -> str:
    if not config:
        config = load_config()
    if not logger:
        logger = make_logger()

    init_db_models(config.db_path)
    logger.info(f"Successfully connected to database in {config.storage_path}")

    if success_codes is None:
        success_codes = (0,)

    job = Job(
        job_id=_generate_random_job_id(),
        host_id=get_host_id(),
        command=preproc_cmd(command),
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
        start_time=datetime.now(),
        state=JobState.SUBMITTED.value,
    )
    job.save()

    # TODO: REMOVE
    # job.exit_code = "2"
    # job.wait_duration = timedelta(days=2, minutes=5)
    # job.retry_attempts = 10
    # job.state = JobState.COMPLETE.value
    # job.save()

    # attempt = 0
    # run = Run(
    #     job_id=job.job_id,
    #     attempt=attempt,
    #     log_path=config.stdio_path / job.job_id / str(attempt),
    #     start_time=datetime.now(),
    #     state=RunState.SUBMITTED.value,
    # )
    # run.save()

    # attempt = 1
    # run = Run(
    #     job_id=job.job_id,
    #     attempt=attempt,
    #     log_path=config.stdio_path / job.job_id / str(attempt),
    #     start_time=datetime.now(),
    #     pid=1234,
    #     state=RunState.RUNNING.value,
    # )
    # run.save()

    # attempt = 2
    # run = Run(
    #     job_id=job.job_id,
    #     attempt=attempt,
    #     log_path=config.stdio_path / job.job_id / str(attempt),
    #     pid=314,
    #     state=RunState.COMPLETE.value,
    #     exit_code=149,
    #     start_time=datetime(2022, 3, 5),
    #     finish_time=datetime(2022, 3, 7),
    # )
    # run.save()

    # attempt = 3
    # run = Run(
    #     job_id=job.job_id,
    #     attempt=attempt,
    #     log_path=config.stdio_path / job.job_id / str(attempt),
    #     pid=31459,
    #     state=RunState.COMPLETE.value,
    #     exit_code=0,
    #     start_time=datetime(2022, 3, 6),
    #     finish_time=datetime(2022, 3, 8),
    # )
    # run.save()
    # END TODO: REMOVE

    return str(job.job_id)
