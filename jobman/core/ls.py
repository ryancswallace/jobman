import logging
import os
import sys
from typing import Dict, List, NamedTuple, Optional, Union

from rich import box
from rich.table import Table

from ..base_logger import make_logger
from ..config import JobmanConfig, load_config
from ..display import Displayer, DisplayLevel
from ..host import get_host_id
from ..models import Job, JobState, Run, init_db_models


def display_ls(
    all_: bool,
    show_runs: bool,
    config: JobmanConfig,
    displayer: Displayer,
    logger: logging.Logger,
) -> int:
    jobs, runs = ls(
        all_=all_,
        show_runs=show_runs,
        config=config,
        logger=logger,
    )

    # print warning but exit with 0 if there are no jobs
    if not jobs:
        displayer.print(
            pretty_content="🔎  No jobs found",
            plain_content="No jobs found",
            json_content={"result": "success", "message": "No jobs found"},
            stream=sys.stderr,
            level=DisplayLevel.NORMAL,
        )
        return os.EX_OK

    # sort output with most recent jobs first
    jobs.sort(key=lambda j: (j.start_time is None, j.start_time), reverse=True)

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
    table.add_column("ID", justify="right")
    for name in col_names[1:]:
        table.add_column(Job._name_to_display_name(name))

    for job in jobs:
        field_to_val = dict()
        for name in col_names:
            field_to_val[name] = job.pretty[name][1]

        field_to_val["job_id"] = "[bold blue]" + str(field_to_val["job_id"])
        # make completed rows dim and colorize exit codes
        field_to_val["job_id"] = (
            "[dim]" + str(field_to_val["job_id"])
            if job.is_completed()
            else field_to_val["job_id"]
        )
        if "exit_code" in field_to_val:
            exit_code_color = (
                ""
                if not job.is_completed()
                else ("[red]" if job.is_failed() else "[green]")
            )
            field_to_val["exit_code"] = exit_code_color + str(field_to_val["exit_code"])

        table.add_row(*field_to_val.values())

        if show_runs and runs:
            job_runs = [r for r in runs if r.job_id.job_id == job.job_id]
            job_runs.sort(
                key=lambda r: (r.attempt, r.start_time is None, r.start_time),
                reverse=True,
            )
            for run in job_runs:
                run_col_names = [
                    "attempt",
                    "start_time",
                    "finish_time",
                    "state",
                    "exit_code",
                ]
                field_to_val = dict()
                for name in run_col_names:
                    field_to_val[name] = run.pretty[name][1]

                # field_to_val["attempt"] = "attempt " + field_to_val["attempt"]
                # make completed rows dim and colorize exit codes
                field_to_val["attempt"] = (
                    "[dim]" + str(field_to_val["attempt"])
                    if run.is_completed()
                    else field_to_val["attempt"]
                )
                if "exit_code" in field_to_val:
                    exit_code_color = (
                        ""
                        if not run.is_completed()
                        else (
                            "[red]" if run.exit_code in job.success_codes else "[green]"
                        )
                    )
                    field_to_val["exit_code"] = exit_code_color + str(
                        field_to_val["exit_code"]
                    )

                vals = list(field_to_val.values())
                vals.insert(1, "")
                table.add_row(*vals)

    json_content: Dict[str, Union[List[Job], List[Run]]] = {"jobs": jobs}
    plain_content = "\n".join(str(j.job_id) for j in jobs)
    if show_runs:
        if not runs:
            r: List[Run] = []
            json_content["runs"] = r
        else:
            json_content["runs"] = runs
        plain_content = "\n".join(
            f"{j.job_id}:"
            f" {len([r for r in (runs or []) if r.job_id.job_id == j.job_id])} runs"
            for j in jobs
        )

    displayer.print(
        pretty_content=table,
        plain_content=plain_content,
        json_content=json_content,
        stream=sys.stdout,
        level=DisplayLevel.NORMAL,
    )

    return os.EX_OK


class LsResult(NamedTuple):
    jobs: List[Job]
    runs: Optional[List[Run]]


def ls(
    all_: bool = False,
    show_runs: bool = False,
    config: Optional[JobmanConfig] = None,
    logger: Optional[logging.Logger] = None,
) -> LsResult:
    if not config:
        config = load_config()
    if not logger:
        logger = make_logger()

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

    if show_runs:
        runs_q = Run.select().join(Job).where(Job.job_id << [j.job_id for j in jobs])  # type: ignore[no-untyped-call]
        runs = list(runs_q)
        logger.info(f"Found {len(runs)} run(s)")
    else:
        runs = None

    return LsResult(jobs=jobs, runs=runs)
