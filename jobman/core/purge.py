import logging
import os
import shutil
import sys
from datetime import datetime
from typing import Dict, List, NamedTuple, Optional, Tuple, Union

import click

from ..config import JobmanConfig
from ..display import Displayer, DisplayLevel, DisplayStyle
from ..host import get_host_id
from ..models import Job, JobState, Run, init_db_models


def _delete_job(job: Job, metadata: bool, logger: logging.Logger) -> None:
    # first delete stdout/stderr logs
    runs = Run.select().join(Job).where(Job.job_id == job.job_id)  # type: ignore[no-untyped-call]
    # assumes that logs for all runs are stored under
    # the same parent folder
    if runs:
        job_log_path = runs[0].log_path.parent
        try:
            shutil.rmtree(job_log_path)
        except FileNotFoundError:
            logger.warn(f"Stdout/stderr log folder {job_log_path} doesn't exist")

    # then delete job record and dependent runs from db
    if metadata:
        job.delete_instance(recursive=True)


def display_purge(
    job_id: Tuple[str, ...],
    _all: bool,
    metadata: bool,
    since: Optional[datetime],
    until: Optional[datetime],
    config: JobmanConfig,
    displayer: Displayer,
    logger: logging.Logger,
) -> int:
    nonexistent_job_ids, purged_job_ids, skipped_job_ids = purge(
        job_id, _all, metadata, since, until, config, logger
    )

    json_contents: Dict[str, Union[str, List[str]]] = {}
    if since and until and since > until:
        displayer.print(
            pretty_content=(
                "⚠️  [bold yellow]Warning:[/ bold yellow] -s/--since date is after"
                " -u/--until date"
            ),
            plain_content="Warning: -s/--since date is after -u/--until date",
            json_content=None,
            stream=sys.stderr,
            level=DisplayLevel.NORMAL,
        )
        json_contents.update(
            {
                "result": "error",
                "args_message": "-s/--since date is after -u/--until date",
            }
        )
        ""

    if nonexistent_job_ids:
        multiple = len(nonexistent_job_ids) > 1
        displayer.print(
            pretty_content=(
                "⚠️  [bold yellow]Warning: [/ bold yellow]No"
                f" such{' ' + str(len(nonexistent_job_ids)) if multiple else ''}"
                f" job{'s' if multiple else ''}:"
            ),
            plain_content=None,
            json_content=None,
            stream=sys.stderr,
            level=DisplayLevel.NORMAL,
        )
        for jid in nonexistent_job_ids:
            displayer.print(
                pretty_content=f"  {jid}",
                plain_content=f"No such job {jid}",
                json_content=None,
                stream=sys.stderr,
                level=DisplayLevel.NORMAL,
            )
        json_contents.update(
            {
                "result": "error",
                "nonexistent_message": f"No such job{'s' if multiple else ''}",
                "nonexistent_job_ids": nonexistent_job_ids,
            }
        )

    if skipped_job_ids:
        multiple = len(skipped_job_ids) > 1
        displayer.print(
            pretty_content=(
                "⚠️  [bold yellow]Warning:[/ bold yellow]"
                f" Skipped{' ' + str(len(skipped_job_ids)) if multiple else ''} running"
                f" job{'s' if multiple else ''}:"
            ),
            plain_content=None,
            json_content=None,
            stream=sys.stderr,
            level=DisplayLevel.NORMAL,
        )
        for jid in skipped_job_ids:
            displayer.print(
                pretty_content=f"  🏃 {jid}",
                plain_content=f"Skipped purging running job {jid}",
                json_content=None,
                stream=sys.stderr,
                level=DisplayLevel.NORMAL,
            )
        json_contents.update(
            {
                "skipped_message": (
                    f"Skipped {len(skipped_job_ids)} running"
                    f" job{'s' if multiple else ''}"
                ),
                "skipped_job_ids": skipped_job_ids,
            }
        )

    if not purged_job_ids:
        displayer.print(
            pretty_content="No matching jobs",
            plain_content=None,
            json_content=None,
            stream=sys.stderr,
            level=DisplayLevel.NORMAL,
            style=DisplayStyle.FAILURE if job_id else DisplayStyle.NORMAL,
        )
        json_contents.update({"message": "No matching jobs found"})
    else:
        multiple = len(purged_job_ids) > 1
        header = (
            "Deleted stdout/stderr logs and metadata for"
            f"{' ' + str(len(purged_job_ids)) if multiple else ''} job{'s' if multiple else ''}:"
            if metadata
            else (
                "Deleted stdout/stderr logs for"
                f"{' ' + str(len(purged_job_ids)) if multiple else ''} job{'s' if multiple else ''}:"
            )
        )
        displayer.print(
            pretty_content=header,
            plain_content=None,
            json_content=None,
            stream=sys.stdout,
            level=DisplayLevel.NORMAL,
        )
        for jid in purged_job_ids:
            displayer.print(
                pretty_content=f"  🧹 {jid}",
                plain_content=jid,
                json_content=None,
                stream=sys.stdout,
                level=DisplayLevel.NORMAL,
            )
        json_contents.update({"purged_job_ids": purged_job_ids})
        if "result" not in json_contents:
            json_contents["result"] = "success"

    # having collected all the JSON output in json_contents, render the JSON
    # in one call to print
    displayer.print(
        pretty_content=None,
        plain_content=None,
        json_content=json_contents,
        stream=sys.stdout,
        level=DisplayLevel.NORMAL,
    )

    return os.EX_DATAERR if not _all and skipped_job_ids else os.EX_OK


class PurgeResult(NamedTuple):
    nonexistent_job_ids: List[str]
    purged_job_ids: List[str]
    skipped_job_ids: List[str]


def purge(
    job_id: Tuple[str, ...],
    _all: bool,
    metadata: bool,
    since: Optional[datetime],
    until: Optional[datetime],
    config: JobmanConfig,
    logger: logging.Logger,
) -> PurgeResult:
    if not (bool(job_id) ^ _all):
        raise click.exceptions.UsageError(
            "Must supply either a job-id argument or the -a/--all flag, but not both"
        )

    init_db_models(config.db_path)
    logger.info(f"Successfully connected to database in {config.storage_path}")

    jobs_q = Job.select().where(Job.host_id == get_host_id())  # type: ignore[no-untyped-call]
    if job_id:
        jobs_q = jobs_q.where(Job.job_id << job_id)
    if since:
        jobs_q = jobs_q.where(Job.start_time >= since)
    if until:
        jobs_q = jobs_q.where(Job.start_time <= until)

    running_jobs = jobs_q.where(Job.state != JobState.COMPLETE.value)
    running_job_ids = [j.job_id for j in running_jobs]

    jobs = list(jobs_q)
    purged_job_ids = []
    for job in jobs:
        if job.job_id in running_job_ids:
            logger.warn(f"Job {job.job_id} is not complete. Skipping.")
            continue

        logger.warn(f"Deleting job {job.job_id}")
        _delete_job(job, metadata, logger)
        purged_job_ids.append(job.job_id)

    nonexistent_job_ids = [
        jid for jid in job_id if jid not in purged_job_ids + running_job_ids
    ]
    return PurgeResult(
        nonexistent_job_ids=nonexistent_job_ids,
        purged_job_ids=purged_job_ids,
        skipped_job_ids=running_job_ids,
    )
