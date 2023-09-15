import logging
import os
import random
import shlex
import string
import sys
from datetime import datetime, timedelta
from pathlib import Path
from typing import Optional, Tuple

from ..config import JobmanConfig
from ..display import Displayer, DisplayLevel, DisplayStyle
from ..host import get_host_id
from ..models import Job, JobState, Run, RunState, init_db_models


def display_run(
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
    job_id = run(
        command=command,
        wait_time=wait_time,
        wait_duration=wait_duration,
        wait_for_file=wait_for_file,
        abort_time=abort_time,
        abort_duration=abort_duration,
        abort_for_file=abort_for_file,
        retry_attempts=retry_attempts,
        retry_delay=retry_delay,
        success_code=success_code,
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
    logger: logging.Logger,
) -> str:
    init_db_models(config.db_path)
    logger.info(f"Successfully connected to database in {config.storage_path}")

    job = Job(
        job_id=_generate_random_job_id(),
        host_id=get_host_id(),
        command=shlex.join(command),
        wait_time=wait_time,
        wait_duration=wait_duration,
        wait_for_file=wait_for_file,
        abort_time=abort_time,
        abort_duration=abort_duration,
        abort_for_file=abort_for_file,
        retry_attempts=retry_attempts,
        retry_delay=retry_delay,
        success_code=success_code,
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

    attempt = 0
    run = Run(
        job_id=job.job_id,
        attempt=attempt,
        log_path=config.stdio_path / job.job_id / str(attempt),
        start_time=datetime.now(),
        state=RunState.SUBMITTED.value,
    )
    run.save()

    # TODO REMOVE
    # job.exit_code = "2"
    # job.wait_duration = timedelta(days=2, minutes=5)
    # job.retry_attempts = 10
    # job.state = JobState.COMPLETE.value
    # job.save()

    # run.state = RunState.RUNNING.value
    # run.pid = "2953125"
    # run.start_time = datetime(2022, 3, 5)
    # run.save()

    # run2 = Run(
    #     job_id=job.job_id,
    #     attempt=attempt,
    #     log_path=config.stdio_path / job.job_id / str(attempt),
    #     start_time=datetime.now(),
    #     state=RunState.SUBMITTED.value,
    # )
    # run2.attempt = 1
    # run2.state = RunState.COMPLETE.value
    # run2.pid = "12345"
    # run2.start_time = datetime(2022, 3, 5)
    # run2.finish_time = datetime(2022, 3, 7)
    # run2.exit_code = "149"
    # run2.save()

    # run3 = Run(
    #     job_id=job.job_id,
    #     attempt=attempt,
    #     log_path=config.stdio_path / job.job_id / str(attempt),
    #     start_time=datetime.now(),
    #     state=RunState.SUBMITTED.value,
    # )
    # run3.attempt = 2
    # run3.state = RunState.COMPLETE.value
    # run3.pid = "123"
    # run3.start_time = datetime(2022, 3, 7)
    # run3.finish_time = datetime(2022, 3, 8)
    # run3.exit_code = "2"
    # run3.save()

    # END TODO REMOVE

    return str(job.job_id)
