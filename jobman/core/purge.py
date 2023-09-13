import logging
import os
import shutil
import sys
from datetime import datetime
from typing import List, NamedTuple, Optional, Tuple

import click

from ..config import JobmanConfig
from ..display import Displayer
from ..host import get_host_id
from ..models import Job, JobState, Run, get_or_create_db


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
    purged_job_ids, skipped_job_ids = purge(
        job_id, _all, metadata, since, until, config, logger
    )

    if since and until and since > until:
        displayer.display(
            "⚠️  [bold yellow]Warning:[/ bold yellow] -s/--since date is after"
            " -u/--until date",
            stream=sys.stderr,
        )

    if not purged_job_ids:
        displayer.display(
            "No matching jobs found!", stream=sys.stdout, style="bold blue"
        )
    else:
        multiple = len(purged_job_ids) > 1
        header = (
            "Deleted stdout/stderr logs and metadata for"
            f" {len(purged_job_ids)} job{'s' if multiple else ''}:"
            if metadata
            else (
                "Deleted stdout/stderr logs for"
                f" {len(purged_job_ids)} job{'s' if multiple else ''}:"
            )
        )
        displayer.display(header, stream=sys.stderr)
        for jid in purged_job_ids:
            displayer.display(f"  ❌ {jid}", stream=sys.stderr, style="bold red")

    if skipped_job_ids:
        multiple = len(skipped_job_ids) > 1
        displayer.display(
            "⚠️  [bold yellow]Warning:[/ bold yellow] Skipped running"
            f" job{'s' if multiple else ''}:",
            stream=sys.stderr,
        )
        for jid in skipped_job_ids:
            displayer.display(f"  🏃 {jid}", stream=sys.stderr)

    return os.EX_DATAERR if not _all and skipped_job_ids else os.EX_OK


class PurgeResults(NamedTuple):
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
) -> PurgeResults:
    if not (bool(job_id) ^ _all):
        raise click.exceptions.UsageError(
            "Must supply either a job-id argument or the -a/--all flag, but not both"
        )

    get_or_create_db(config.db_path)
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

    return PurgeResults(purged_job_ids=purged_job_ids, skipped_job_ids=running_job_ids)
