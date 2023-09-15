import logging
import os
import sys
from typing import Dict, List, NamedTuple, Optional, Tuple, Union

from rich import box
from rich.table import Table

from ..base_logger import make_logger
from ..config import JobmanConfig, load_config
from ..display import Displayer, DisplayLevel, DisplayStyle
from ..host import get_host_id
from ..models import Job, Run, init_db_models


def display_status(
    job_id: Tuple[str, ...],
    no_runs: bool,
    all_: bool,
    config: JobmanConfig,
    displayer: Displayer,
    logger: logging.Logger,
) -> int:
    jobs, runs = status(job_id, no_runs, config, logger)

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
        multiple = len(not_found_job_ids) > 1
        displayer.print(
            pretty_content=(
                "⚠️  [bold yellow]Warning: [/ bold yellow]No"
                f" such{' ' + str(len(not_found_job_ids)) if multiple else ''} job{'s' if multiple else ''}:"
            ),
            plain_content=None,
            json_content=None,
            stream=sys.stderr,
            level=DisplayLevel.NORMAL,
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
    jobs_sorted = sorted(jobs, key=lambda r: r.start_time, reverse=True)
    for idx, job in enumerate(jobs_sorted):
        job_table = Table(title_justify="left", show_header=False)
        job_table.title = (
            f"[not italic]Job [underline][bold blue]{job.job_id}[/ underline][/ bold"
            " blue]:"
        )
        job_table.min_width = len(f"Job {job.job_id}:") + 1
        job_table.box = None

        status_fields = [
            "command",
            "state",
            "start_time",
            "finish_time",
            "exit_code",
        ]
        spec_fields = [
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
        fields = status_fields + spec_fields if all_ else status_fields
        null_display_fields = []
        for name in fields:
            display_name, display_val = job.pretty[name]
            if getattr(job, name) is not None:
                if name == "exit_code":
                    color = "[green]" if job.exit_code in job.success_code else "[red]"
                    display_val = color + str(display_val)
                job_table.add_row(display_name, display_val)
            else:
                null_display_fields.append(display_name)
        if all_ and null_display_fields:
            job_table.add_row(
                "[dim]Null fields", "[dim]" + ", ".join(null_display_fields)
            )

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
            pretty_content=job_table,
            plain_content=f"{job.job_id}: {job.pretty['state'][1]}",
            json_content=None,
            stream=sys.stdout,
        )

        # display runs for this job
        job_runs = [r for r in runs or list() if r.job_id.job_id == job.job_id]
        if not no_runs and job_runs:
            run_table = Table(show_header=True)
            run_table.title = f"[bold blue][not italic]Runs"
            run_table.border_style = "blue"
            run_table.box = box.SIMPLE_HEAD
            fields = [
                "attempt",
                "pid",
                "state",
                "start_time",
                "finish_time",
                "exit_code",
            ]
            for field in fields:
                run_table.add_column(Run._name_to_display_name(field))

            job_runs_sorted = sorted(job_runs, key=lambda r: r.attempt, reverse=True)
            for run in job_runs_sorted:
                field_to_val = dict()
                for field in fields:
                    field_to_val[field] = run.pretty[field][1]

                # make completed rows dim and colorize exit codes
                if run.is_completed():
                    run_failed = run.exit_code not in job.success_code
                    exit_code_color = "[red]" if run_failed else "[green]"
                    field_to_val["exit_code"] = exit_code_color + str(
                        field_to_val["exit_code"]
                    )
                    field_to_val["attempt"] = "[dim]" + str(field_to_val["attempt"])

                run_table.add_row(*field_to_val.values())

            displayer.print(
                pretty_content=run_table,
                plain_content="\n".join(
                    f"  attempt {r.attempt}: {r.pretty['state'][1]}" for r in job_runs
                ),
                json_content=None,
                stream=sys.stdout,
            )

    # display all results as JSON together
    json_content: Dict[str, Optional[Union[str, List[str], List[Job], List[Run]]]] = {
        "jobs": jobs
    }
    if not no_runs:
        json_content["runs"] = runs
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


class StatusResult(NamedTuple):
    jobs: List[Job]
    runs: Optional[List[Run]]


def status(
    job_id: Tuple[str, ...],
    no_runs: bool = False,
    config: Optional[JobmanConfig] = None,
    logger: Optional[logging.Logger] = None,
) -> StatusResult:
    if not config:
        config = load_config()
    if not logger:
        logger = make_logger(logging.WARN)

    init_db_models(config.db_path)
    logger.info(f"Successfully connected to database in {config.storage_path}")

    jobs_q = Job.select().where(  # type: ignore[no-untyped-call]
        (Job.host_id == get_host_id()) & (Job.job_id << job_id)
    )
    jobs = list(jobs_q)

    if not no_runs:
        runs_q = Run.select().join(Job).where(Job.job_id << job_id)  # type: ignore[no-untyped-call]
        runs = list(runs_q)
    else:
        runs = None

    return StatusResult(jobs=jobs, runs=runs)
