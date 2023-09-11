import logging
import os
from typing import Optional, Tuple

from ..config import JobmanConfig
from ..display import Displayer


def kill(
    job_id: Tuple[str, ...],
    signal: Optional[str],
    allow_retries: bool,
    config: JobmanConfig,
    displayer: Displayer,
    logger: logging.Logger,
) -> int:
    return os.EX_OK
