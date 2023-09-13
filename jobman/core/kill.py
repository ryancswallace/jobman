import logging
import os
from typing import List, NamedTuple, Optional, Tuple

from ..config import JobmanConfig
from ..display import Displayer


def display_kill(
    job_id: Tuple[str, ...],
    signal: Optional[str],
    allow_retries: bool,
    config: JobmanConfig,
    displayer: Displayer,
    logger: logging.Logger,
) -> int:
    kill_results = kill(job_id, signal, allow_retries, config, logger)
    return os.EX_OK


class KillResults(NamedTuple):
    killed_job_ids: List[str]
    skipped_job_ids: List[str]


def kill(
    job_id: Tuple[str, ...],
    signal: Optional[str],
    allow_retries: bool,
    config: JobmanConfig,
    logger: logging.Logger,
) -> KillResults:
    return KillResults(killed_job_ids=[], skipped_job_ids=[])
