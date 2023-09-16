import logging
import random
import shlex
import string
from datetime import datetime, timedelta
from pathlib import Path
from typing import Optional, Tuple

from ...base_logger import make_logger
from ...config import JobmanConfig, load_config
from ...host import get_host_id
from ...models import Job, JobState, init_db_models
from .nohup import nohupify
from .wait import wait


def preproc_cmd(command: Tuple[str, ...]) -> str:
    if len(command) == 1:
        # covers the case of a single-word command, plus the case
        # of a command enclosed in quotes (double or single)
        return command[0]
    else:
        return shlex.join(command)


def _generate_random_job_id() -> str:
    id_len = 8
    return "".join(random.choices(string.hexdigits, k=id_len)).lower()


def build_job(
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
) -> Job:
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
        wait_time=wait_time,  # wait until wait_time before starting first run
        wait_duration=wait_duration,  # wait wait_duration after command is invoked before starting first run
        wait_for_files=wait_for_files,  # wait for wait_for_files to all exist before starting first run
        abort_time=abort_time,  # timeout job if isn't complete by abort_time
        abort_duration=abort_duration,  # timeout job if abort_duration passes after command is invoked
        abort_for_files=abort_for_files,  # abort job if abort_for_files all exist
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

    return job


def run_job(job: Job) -> None:
    # detach from shell
    nohupify()

    # start monitoring for abort conditions
    # convert abort_time to duration, then abort at min(abort_duration, duration_to(abort_time))

    # wait for three wait conditions
    wait(job.wait_time, job.wait_duration, job.wait_for_files)

    # (0) build and save run object
    # start run
    # when run finishes, inspect result
    # make run notifications, if applicable
    # go to (0) after waiting if applicable
    # make job notifications, if applicable


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


# def procs_are_same(proc_1: psutil.Process, proc_2: psutil.Process) -> bool:
#     same_pid = proc_1.pid == proc_2.pid
#     same_create_time = proc_1.create_time() == proc_2.create_time()
#     return same_pid and same_create_time


# def start(self) -> None:
#     print(self.job_id)

#     nohupify()

#     run_id = 0
#     run_stdio_path = self.job_stdio_path / str(run_id)
#     run_stdio_path.mkdir(parents=True)
#     out_file_path = run_stdio_path / "out.txt"
#     err_file_path = run_stdio_path / "err.txt"

#     job_run = JobRun(
#         self.command,
#         out_file_path,
#         err_file_path,
#         self.logger,
#     )
#     job_run.start()
#     if job_run.proc:
#         job_run.proc.wait()


# def is_running(self) -> bool:
#     """
#     Return whether the process for this JobRun:
#     1) has been started, AND
#     2) still exists in the process table, AND
#     3) is not a zombie
#     """
#     if self.proc is None:
#         return False

#     try:
#         proc = psutil.Process(self.pid)
#     except (psutil.NoSuchProcess, psutil.AccessDenied):
#         return False

#     if proc.status() == psutil.STATUS_ZOMBIE:
#         return False

#     return procs_are_same(self.proc, proc)
