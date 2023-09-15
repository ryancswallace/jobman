import logging
import os
import sys
from typing import Dict, List, Tuple, Union

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
    not_found_job_ids = set()
    for requested_job_id in job_id:
        found = False
        for found_job in jobs:
            if found_job.job_id == requested_job_id:
                found = True
                break
        if not found:
            not_found_job_ids.add(requested_job_id)

    # display message about any jobs not found
    if not_found_job_ids:
        displayer.print(
            pretty_content="No such jobs:",
            plain_content=None,
            json_content=None,
            stream=sys.stderr,
            level=DisplayLevel.NORMAL,
            style=DisplayStyle.FAILURE,
        )
        for jid in not_found_job_ids:
            displayer.print(
                pretty_content=f"  ❌ {jid}",
                plain_content=f"No such job: {jid}",
                json_content=None,
                stream=sys.stderr,
                level=DisplayLevel.NORMAL,
                style=DisplayStyle.FAILURE,
            )

    # separate not found and found section with empty line
    if not_found_job_ids and jobs:
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
                stream=sys.stdout,
            )
        displayer.print(
            pretty_content=table,
            plain_content=f"{job.job_id}: {JobState(job.state).name}",
            json_content=None,
            stream=sys.stdout,
        )

    # display all results as JSON together
    json_content: Dict[str, Union[str, List[str], List[Job]]] = {"jobs": jobs}
    if not_found_job_ids:
        json_content.update(
            {
                "result": "error",
                "message": "No such jobs",
                "missing_job_ids": list(not_found_job_ids),
            }
        )
    else:
        json_content.update({"result": "success"})
    displayer.print(
        pretty_content=None,
        plain_content=None,
        json_content=json_content,
        stream=sys.stdout,
    )

    return os.EX_UNAVAILABLE if not_found_job_ids else os.EX_OK


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
    jobs = list(jobs_q)

    return jobs
