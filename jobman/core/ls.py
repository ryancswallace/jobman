import logging
import os
import sys
from typing import List

from rich import box
from rich.table import Table

from ..config import JobmanConfig
from ..display import Displayer, DisplayLevel
from ..host import get_host_id
from ..models import Job, JobState, init_db_models


def display_ls(
    all_: bool, config: JobmanConfig, displayer: Displayer, logger: logging.Logger
) -> int:
    jobs = ls(
        all_=all_,
        config=config,
        logger=logger,
    )

    # print warning but exit with 0 if there are no jobs
    if not jobs:
        displayer.print(
            pretty_content="🔎  No jobs found",
            plain_content="No jobs found",
            json_content={"result": "error", "message": "No jobs found"},
            stream=sys.stderr,
            level=DisplayLevel.NORMAL,
        )
        return os.EX_OK

    # print found jobs
    table = Table()
    table.title = f"[bold blue]⚡ {'All' if all_ else 'Running'} Jobman Jobs ⚡"
    table.border_style = "blue"
    table.box = box.SIMPLE_HEAD

    col_names = [
        "job_id",
        "command",
        "start_time",
        "finish_time",
    ]
    if all_:
        col_names += [
            "state",
            "exit_code",
        ]
    for name in col_names:
        table.add_column(Job._name_to_display_name(name))

    jobs.sort(key=lambda j: (j.start_time is None, j.start_time), reverse=True)
    for job in jobs:
        col_to_val = dict()
        for name in col_names:
            col_to_val[name] = job.pretty[name][1]

        # make completed rows dim and colorize exit codes
        exit_code_color = ""
        if job.is_completed():
            col_to_val["job_id"] = "[dim]" + str(col_to_val["job_id"])
            exit_code_color = "[red]" if job.is_failed() else "[green]"
        if "exit_code" in col_to_val:
            col_to_val["exit_code"] = exit_code_color + str(col_to_val["exit_code"])

        table.add_row(*col_to_val.values())

    displayer.print(
        pretty_content=table,
        plain_content="\n".join(str(j.job_id) for j in jobs),
        json_content=jobs,
        stream=sys.stdout,
        level=DisplayLevel.NORMAL,
    )

    return os.EX_OK


def ls(all_: bool, config: JobmanConfig, logger: logging.Logger) -> List[Job]:
    init_db_models(config.db_path)
    logger.info(f"Successfully connected to database in {config.storage_path}")

    jobs_q = (
        Job.select().where(Job.host_id == get_host_id())  # type: ignore[no-untyped-call]
        if all_
        else Job.select().where(  # type: ignore[no-untyped-call]
            (Job.host_id == get_host_id())
            & (Job.state << [JobState.SUBMITTED.value, JobState.RUNNING.value])
        )
    )

    jobs = list(jobs_q)
    logger.info(f"Found {len(jobs)} job(s)")

    return jobs
