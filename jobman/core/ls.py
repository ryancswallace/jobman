import logging
import os
import sys
from typing import List

from rich import box
from rich.table import Table

from ..config import JobmanConfig
from ..display import Displayer
from ..models import Job, JobState, get_or_create_db


def display_ls(
    all_: bool, config: JobmanConfig, displayer: Displayer, logger: logging.Logger
) -> int:
    ls_out = ls(
        all_=all_,
        config=config,
        logger=logger,
    )
    if not ls_out:
        displayer.display("No jobs found!", stream=sys.stdout, style="bold blue")
        return os.EX_OK

    table = Table()
    table.title = (
        "[bold blue][not italic]:high_voltage:[/]"
        f" {'All' if all_ else 'Running'} Jobman Jobs [not italic]:high_voltage:[/]"
    )
    table.border_style = "bright_yellow"
    table.box = box.SIMPLE_HEAD

    if all_:
        col_names = {
            "job_id": "ID",
            "command": "Command",
            "start_time": "Start Time",
            "finish_time": "Finish Time",
            "state": "State",
            "exit_code": "Exit Code",
        }
    else:
        col_names = {
            "job_id": "ID",
            "command": "Command",
            "start_time": "Start Time",
            "state": "State",
        }

    for name in col_names.values():
        table.add_column(name)

    ls_out.sort(key=lambda j: (j.start_time is None, j.start_time))
    for job in ls_out:
        col_to_val = {c: getattr(job, c) for c in col_names}

        col_to_val["state"] = JobState(col_to_val["state"]).name.title()

        if col_to_val["start_time"] is not None:
            col_to_val["start_time"] = col_to_val["start_time"].replace(microsecond=0)
        if "finish_time" in col_to_val and col_to_val["finish_time"] is not None:
            col_to_val["finish_time"] = col_to_val["finish_time"].replace(microsecond=0)

        for col, val in col_to_val.items():
            col_to_val[col] = str(val) if val is not None else "-"

        if job.is_completed():
            color = "[dim]"
            exit_code_color = "[red]" if job.is_failed() else "[green]"
        else:
            color = ""
            exit_code_color = ""
        for col, val in col_to_val.items():
            col_to_val[col] = color + val
        if "exit_code" in col_to_val:
            col_to_val["exit_code"] = exit_code_color + col_to_val["exit_code"]
        table.add_row(*col_to_val.values())

    displayer.display(table, stream=sys.stdout)

    return os.EX_OK


def ls(all_: bool, config: JobmanConfig, logger: logging.Logger) -> List[Job]:
    get_or_create_db(config.db_path)
    logger.info(f"Successfully connected to database in {config.storage_path}")

    jobs_q = (
        Job.select()  # type: ignore[no-untyped-call]
        if all_
        else Job.select().where(  # type: ignore[no-untyped-call]
            Job.state << [JobState.SUBMITTED.value, JobState.RUNNING.value]
        )
    )

    jobs = list(jobs_q)
    logger.info(f"Found {len(jobs)} job(s)")

    return jobs
