import os
import time
from datetime import datetime, timedelta
from pathlib import Path
from signal import Signals
from typing import Optional, Tuple

POLL_SEC = 0.1


def any_file_exists(files: Optional[Tuple[Path]]) -> bool:
    if files is None:
        return False

    for f in files:
        if Path(f).exists():
            return True

    return False


def combine_aborts(
    abort_time: Optional[datetime], abort_duration: Optional[timedelta]
) -> datetime:
    if abort_duration:
        abort_duration_time = datetime.now() + abort_duration
    else:
        abort_duration_time = datetime.max

    combined_abort_time = min(abort_time or datetime.max, abort_duration_time)

    return combined_abort_time


def signal_on_abort(
    pid: int,
    sig: Signals,
    abort_time: Optional[datetime],
    abort_duration: Optional[timedelta],
    abort_for_files: Optional[Tuple[Path]],
) -> None:
    final_abort_time = combine_aborts(abort_time, abort_duration)

    while not (datetime.now() >= final_abort_time or any_file_exists(abort_for_files)):
        time.sleep(POLL_SEC)

    os.kill(pid, sig)
