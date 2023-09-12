import logging
import os
from typing import Tuple

from ..config import JobmanConfig
from ..display import Displayer


def display_status(
    job_id: Tuple[str, ...],
    config: JobmanConfig,
    displayer: Displayer,
    logger: logging.Logger,
) -> int:
    return os.EX_OK
