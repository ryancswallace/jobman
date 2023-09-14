import logging
import os
import sys
from typing import List, Tuple

from rich.table import Table

from ..config import JobmanConfig
from ..display import Displayer, DisplayLevel, DisplayStyle
from ..host import get_host_id
from ..models import Job, JobState, init_db_models


def display_status(
    job_id: Tuple[str, ...],
    config: JobmanConfig,
    displayer: Displayer,
    logger: logging.Logger,
) -> int:
    jobs = status(job_id, config, logger)

    # check that all jobs requested were found
    jobs_not_found = set()
    for requested_job_id in job_id:
        found = False
        for found_job in jobs:
            if found_job.job_id == requested_job_id:
                found = True
                break
        if not found:
            jobs_not_found.add(requested_job_id)

    # display message about any jobs not found
    if jobs_not_found:
        displayer.print(
            pretty_content="No such jobs:",
            plain_content="No such jobs:",
            json_content=None,
            stream=sys.stderr,
            level=DisplayLevel.NORMAL,
            style=DisplayStyle.FAILURE,
        )
        for jid in jobs_not_found:
            displayer.print(
                pretty_content=f"  ❌ {jid}",
                plain_content=jid,
                json_content=None,
                stream=sys.stderr,
                level=DisplayLevel.NORMAL,
                style=DisplayStyle.FAILURE,
            )

    # separate not found and found section with empty line
    if jobs_not_found and jobs:
        displayer.print(
            pretty_content="", plain_content="", json_content=None, stream=sys.stderr
        )

    # display found jobs
    for idx, job in enumerate(jobs):
        table = Table(title_justify="left", show_header=False)
        table.title = (
            f"[not italic]Job [underline][bold blue]{job.job_id}[/ underline][/ bold"
            " blue]:"
        )
        table.min_width = len(f"Job {job.job_id}:") + 1
        table.box = None

        names = [
            "command",
            "start_time",
            "finish_time",
            "state",
            "exit_code",
            "wait_time",
            "wait_duration",
            "wait_for_file",
            "abort_time",
            "abort_duration",
            "abort_for_file",
            "retry_attempts",
            "retry_delay",
            "success_code",
            "notify_on_run_completion",
            "notify_on_job_completion",
            "notify_on_job_success",
            "notify_on_run_success",
            "notify_on_job_failure",
            "notify_on_run_failure",
        ]
        for name in names:
            display_name, display_val = job.pretty[name]
            if getattr(job, name) is None:
                display_name, display_val = (
                    "[dim]" + display_name,
                    "[dim]" + display_val,
                )
            table.add_row(display_name, display_val)

        # for pretty printed tables, add separating line before printing the
        # next table unless this is the first table
        if idx != 0:
            displayer.print(
                pretty_content="",
                plain_content=None,
                json_content=None,
                stream=sys.stderr,
            )
        displayer.print(
            pretty_content=table,
            plain_content=f"{job.job_id}: {JobState(job.state).name}",
            json_content=None,
            stream=sys.stderr,
        )

    # display all results as JSON together
    displayer.print(
        pretty_content=None,
        plain_content=None,
        json_content={
            "result": "error",
            "message": "No such jobs",
            "missing_job_ids": list(jobs_not_found),
            "jobs": jobs,
        },
        stream=sys.stderr,
    )

    return os.EX_UNAVAILABLE if jobs_not_found else os.EX_OK


def status(
    job_id: Tuple[str, ...],
    config: JobmanConfig,
    logger: logging.Logger,
) -> List[Job]:
    init_db_models(config.db_path)
    logger.info(f"Successfully connected to database in {config.storage_path}")

    jobs_q = Job.select().where(  # type: ignore[no-untyped-call]
        (Job.host_id == get_host_id()) & (Job.job_id << job_id)
    )
    return list(jobs_q)
