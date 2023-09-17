import logging
import multiprocessing as mp
import os
import random
import shlex
import signal
import string
import subprocess
import sys
import time
from datetime import datetime, timedelta
from pathlib import Path
from types import FrameType
from typing import Dict, Optional, Tuple

from ...base_logger import make_logger
from ...config import JobmanConfig, load_config
from ...host import get_host_id
from ...models import Job, JobState, Run, RunState, init_db_models
from .abort import signal_on_abort
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
    retry_expo_backoff: bool = False,
    retry_jitter: bool = False,
    success_codes: Tuple[int] = (0,),
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

    job = Job(
        job_id=_generate_random_job_id(),
        host_id=get_host_id(),
        command=preproc_cmd(command),
        # wait for all
        wait_time=wait_time,  # wait until wait_time before starting first run
        wait_duration=wait_duration,  # wait wait_duration after command is invoked before starting first run
        wait_for_files=wait_for_files,  # wait for wait_for_files to all exist before starting first run
        # abort for any
        abort_time=abort_time,  # timeout job if isn't complete by abort_time
        abort_duration=abort_duration,  # timeout job if abort_duration passes after command is invoked
        abort_for_files=abort_for_files,  # abort job if abort_for_files all exist
        retry_attempts=retry_attempts,
        retry_delay=retry_delay,
        retry_expo_backoff=retry_expo_backoff,
        retry_jitter=retry_jitter,
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


def get_delay_secs(
    retry_delay: timedelta, attempt: int, retry_expo_backoff: bool, retry_jitter: bool
) -> float:
    base_delay_secs: float = retry_delay.total_seconds()

    if retry_expo_backoff:
        expo_factor = float(2 ** (attempt - 1))
    else:
        expo_factor = 1.0

    if retry_jitter:
        max_jitter = base_delay_secs / 10
        jitter_secs = random.uniform(-max_jitter, max_jitter)
    else:
        jitter_secs = 0.0

    return expo_factor * base_delay_secs + jitter_secs


def handle(signum: int, stack: Optional[FrameType]) -> None:
    # TODO: kill
    print("HANDLED!")
    sys.exit(1)


def run_job(job: Job, config: JobmanConfig) -> None:
    # deach from shell
    nohupify()

    # start monitoring for abort conditions
    abort_sig = signal.SIGINT
    signal.signal(abort_sig, handle)
    abort_proc = mp.Process(
        target=signal_on_abort,
        args=(
            os.getpid(),
            abort_sig,
            job.abort_time,
            job.abort_duration,
            job.abort_for_files,
        ),
    )
    abort_proc.start()

    # wait for three wait conditions
    wait(job.wait_time, job.wait_duration, job.wait_for_files)

    total_attempts = (job.retry_attempts or 0) + 1
    for attempt in range(total_attempts):
        # test if we need to bail
        if attempt != 0 and (run.exit_code in job.success_codes or run.killed):
            job.finish_time = run.finish_time
            job.state = JobState.COMPLETE.value
            job.exit_code = run.exit_code
            job.save()
            break

        if attempt != 0 and job.retry_delay:
            time.sleep(
                get_delay_secs(
                    job.retry_delay, attempt, job.retry_expo_backoff, job.retry_jitter
                )
            )

        # build and save run object
        run: Run = Run(
            job_id=job.job_id,
            attempt=attempt,
            log_path=config.stdio_path / job.job_id / str(attempt),
            state=RunState.SUBMITTED.value,
            killed=False,
        )
        run.save()

        run_run(run, job)

    # stop abort monitor
    abort_proc.kill()

    # TODO: make job notifications, if applicable


def get_job_environ(job_id: str, attempt: int) -> Dict[str, str]:
    env = dict(os.environ)
    env.update({"JOBMAN_JOB_ID": job_id, "JOBMAN_ATTEMPT_NUM": str(attempt)})

    return env


def run_run(run: Run, job: Job) -> None:
    run.log_path.mkdir(parents=True)
    out_file_path = run.log_path / "out.txt"
    err_file_path = run.log_path / "err.txt"

    with open(out_file_path, "w") as out_fp, open(err_file_path, "w") as err_fp:
        proc = subprocess.Popen(
            run.job.command,
            shell=True,
            stdout=out_fp,
            stderr=err_fp,
            env=get_job_environ(job.job_id, run.attempt),
        )

        run.pid = proc.pid
        run.start_time = datetime.now()
        run.state = RunState.RUNNING.value
        run.save()
        job.state = JobState.RUNNING.value
        job.save()

        exit_code = proc.wait()

        run.finish_time = datetime.now()
        run.state = RunState.COMPLETE.value
        run.exit_code = exit_code
        run.save()
        job.finish_time = run.finish_time
        job.state = JobState.COMPLETE.value
        job.exit_code = exit_code
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


# def procs_are_same(proc_1: psutil.Process, proc_2: psutil.Process) -> bool:
#     same_pid = proc_1.pid == proc_2.pid
#     same_create_time = proc_1.create_time() == proc_2.create_time()
#     return same_pid and same_create_time


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
