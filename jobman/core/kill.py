import logging
import os
import sys
from signal import Signals
from typing import List, NamedTuple, Optional, Tuple

from ..config import JobmanConfig
from ..display import Displayer
from ..host import get_host_id
from ..models import Job, Run, RunState, get_or_create_db


def display_kill(
    job_id: Tuple[str, ...],
    signal: Optional[str],
    allow_retries: bool,
    config: JobmanConfig,
    displayer: Displayer,
    logger: logging.Logger,
) -> int:
    (
        nonexistent_job_ids,
        nonrunning_job_ids,
        killed_run_ids,
        failed_killed_run_ids,
    ) = kill(job_id, signal, allow_retries, config, logger)

    if nonexistent_job_ids:
        multiple = len(nonexistent_job_ids) > 1
        displayer.display(
            "⚠️  [bold yellow]Warning:[/ bold yellow] No such"
            f" job{'s' if multiple else ''}:",
            stream=sys.stderr,
        )
        for jid in nonexistent_job_ids:
            displayer.display(f"  {jid}", stream=sys.stderr)

    if nonrunning_job_ids:
        multiple = len(nonrunning_job_ids) > 1
        displayer.display(
            "⚠️  [bold yellow]Warning:[/ bold yellow] No active runs for"
            f" job{'s' if multiple else ''}:",
            stream=sys.stderr,
        )
        for jid in nonrunning_job_ids:
            displayer.display(f"  {jid}", stream=sys.stderr)

    if failed_killed_run_ids:
        multiple = len(failed_killed_run_ids) > 1
        displayer.display(
            f"⚠️  [bold yellow]Warning:[/ bold yellow] Failed to kill:",
            stream=sys.stderr,
        )
        for jid, attempt in failed_killed_run_ids:
            displayer.display(f"  {jid}, attempt {attempt}", stream=sys.stderr)

    if killed_run_ids:
        multiple = len(killed_run_ids) > 1
        displayer.display(f"Killed:", stream=sys.stderr)
        for jid, attempt in killed_run_ids:
            displayer.display(f"  ❌ {jid}, attempt {attempt}", stream=sys.stderr)

    failed = nonexistent_job_ids or nonrunning_job_ids or failed_killed_run_ids
    return os.EX_DATAERR if failed else os.EX_OK


def get_signal_num(signal: Optional[str]) -> int:
    # default to SIGINT (2) if no signal specified
    if signal is None:
        return Signals["SIGINT"].value

    try:
        # first check if the signal is a number
        signal_num = int(signal)
    except ValueError:
        # if it's not a number, it must be a name
        signal_num = Signals[signal].value

    return signal_num


class KillResults(NamedTuple):
    # jobs specified that don't exist
    nonexistent_job_ids: List[str]

    # jobs specified that do exist but don't have an active run
    nonrunning_job_ids: List[str]

    # killed runs as a list of (job_id, attempt) pairs
    killed_run_ids: List[Tuple[str, int]]

    # runs that we tried to kill but failed in (job_id, attempt) pairs
    failed_killed_run_ids: List[Tuple[str, int]]


def kill(
    job_id: Tuple[str, ...],
    signal: Optional[str],
    allow_retries: bool,
    config: JobmanConfig,
    logger: logging.Logger,
) -> KillResults:
    get_or_create_db(config.db_path)
    logger.info(f"Successfully connected to database in {config.storage_path}")

    # find active runs
    jobs_q = Job.select().where(  # type: ignore[no-untyped-call]
        (Job.host_id == get_host_id()) & (Job.job_id << job_id)
    )
    existent_job_ids = [j.job_id for j in jobs_q]
    nonexistent_job_ids = [jid for jid in job_id if jid not in existent_job_ids]

    runs = list(
        Run.select()  # type: ignore[no-untyped-call]
        .join(Job)
        .where(
            (Job.job_id << existent_job_ids)
            & (Run.state == RunState.RUNNING.value)
            & (~Run.pid.is_null())
        )
    )
    running_job_ids = list(set(run.job_id.job_id for run in runs))
    nonrunning_job_ids = [jid for jid in existent_job_ids if jid not in running_job_ids]

    if not allow_retries:
        # mark runs killed so they can't be restarted
        for run in runs:
            run.killed = True
            run.save()
            logger.info(f"Marked run {run.job_id.job_id} attempt {run.attempt} killed")

    # kill runs with specified signal
    signal_num = get_signal_num(signal)
    killed_run_ids, failed_killed_run_ids = [], []
    for run in runs:
        try:
            os.kill(run.pid, signal_num)
        except (ProcessLookupError, SystemError, OSError) as e:
            logger.error(
                f"Error occurred trying to kill run {run.job_id.job_id} attempt"
                f" {run.attempt} with PID {run.pid} with signal {signal_num}: {e}"
            )
            failed_killed_run_ids.append((run.job_id.job_id, run.attempt))
            continue

        killed_run_ids.append((run.job_id.job_id, run.attempt))
        logger.info(
            f"Killed run {run.job_id.job_id} attempt {run.attempt} with PID"
            f" {run.pid} with signal {signal_num}"
        )

    return KillResults(
        nonexistent_job_ids=nonexistent_job_ids,
        nonrunning_job_ids=nonrunning_job_ids,
        killed_run_ids=killed_run_ids,
        failed_killed_run_ids=failed_killed_run_ids,
    )
