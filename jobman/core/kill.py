import os
from typing import Optional, Tuple

from ..display import Displayer


def kill(
    job_id: Tuple[str, ...],
    signal: Optional[str],
    allow_retries: bool,
    displayer: Displayer,
) -> int:
    return os.EX_OK
