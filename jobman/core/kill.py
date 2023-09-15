import logging
import os
import sys
from signal import Signals
from typing import Dict, List, NamedTuple, Optional, Tuple, Union

from ..base_logger import make_logger
from ..config import JobmanConfig, load_config
from ..display import Displayer, DisplayLevel
from ..host import get_host_id
from ..models import Job, Run, RunState, init_db_models


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

    json_contents: Dict[str, Union[str, List[str], List[Tuple[str, int]]]] = {}
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

    if nonrunning_job_ids:
        multiple = len(nonrunning_job_ids) > 1
        displayer.print(
            pretty_content=(
                "⚠️  [bold yellow]Warning:[/ bold yellow] No active runs"
                f" for{' ' + str(len(nonrunning_job_ids)) if multiple else ''}"
                f" job{'s' if multiple else ''}:"
            ),
            plain_content=None,
            json_content=None,
            stream=sys.stderr,
            level=DisplayLevel.NORMAL,
        )
        for jid in nonrunning_job_ids:
            displayer.print(
                pretty_content=f"  {jid}",
                plain_content=f"No active run for job {jid}",
                json_content=None,
                stream=sys.stderr,
                level=DisplayLevel.NORMAL,
            )
        json_contents.update(
            {
                "result": "error",
                "nonrunning_message": (
                    f"No active runs for job{'s' if multiple else ''}"
                ),
                "nonrunning_job_ids": nonrunning_job_ids,
            }
        )

    if failed_killed_run_ids:
        multiple = len(failed_killed_run_ids) > 1
        displayer.print(
            pretty_content=(
                "⚠️  [bold yellow]Warning:[/ bold yellow] Failed to"
                f" kill{' ' + str(len(failed_killed_run_ids)) if multiple else ''} job{'s' if multiple else ''}:"
            ),
            plain_content=None,
            json_content=None,
            stream=sys.stderr,
            level=DisplayLevel.NORMAL,
        )
        for jid, attempt in failed_killed_run_ids:
            displayer.print(
                pretty_content=f"  {jid}, attempt {attempt}",
                plain_content=f"Failed to kill {jid}, attempt {attempt}",
                json_content=None,
                stream=sys.stderr,
                level=DisplayLevel.NORMAL,
            )
        json_contents.update(
            {
                "result": "error",
                "failed_message": f"Failed to kill job{'s' if multiple else ''}",
                "failed_killed_run_ids": failed_killed_run_ids,
            }
        )

    if killed_run_ids:
        multiple = len(killed_run_ids) > 1
        displayer.print(
            pretty_content=(
                f"Killed{' ' + str(len(killed_run_ids)) if multiple else ''} job{'s' if multiple else ''}:"
            ),
            plain_content=None,
            json_content=None,
            stream=sys.stderr,
            level=DisplayLevel.NORMAL,
        )
        for jid, attempt in killed_run_ids:
            displayer.print(
                pretty_content=f"  ❌ {jid}, attempt {attempt}",
                plain_content=f"{jid}, attempt {attempt}",
                json_content=None,
                stream=sys.stderr,
                level=DisplayLevel.NORMAL,
            )
        json_contents.update(
            {
                "killed_run_ids": killed_run_ids,
            }
        )

    if "result" not in json_contents:
        json_contents["result"] = "success"
    displayer.print(
        pretty_content=None,
        plain_content=None,
        json_content=json_contents,
        stream=sys.stdout,
        level=DisplayLevel.NORMAL,
    )

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


class killResult(NamedTuple):
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
    signal: Optional[str] = None,
    allow_retries: bool = False,
    config: Optional[JobmanConfig] = None,
    logger: Optional[logging.Logger] = None,
) -> killResult:
    if not config:
        config = load_config()
    if not logger:
        logger = make_logger(logging.WARN)

    init_db_models(config.db_path)
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

    return killResult(
        nonexistent_job_ids=nonexistent_job_ids,
        nonrunning_job_ids=nonrunning_job_ids,
        killed_run_ids=killed_run_ids,
        failed_killed_run_ids=failed_killed_run_ids,
    )
