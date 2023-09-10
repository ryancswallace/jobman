import os
from typing import Tuple

from ..display import Displayer


def status(
    job_id: Tuple[str, ...],
    displayer: Displayer,
) -> int:
    return os.EX_OK
