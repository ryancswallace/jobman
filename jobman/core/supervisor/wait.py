import time
from datetime import datetime, timedelta
from pathlib import Path
from typing import Optional, Tuple

POLL_SEC = 0.1


def files_exist(files: Optional[Tuple[Path]]) -> bool:
    if files is None:
        return True

    for f in files:
        if not Path(f).exists():
            return False

    return True


def combine_waits(
    wait_time: Optional[datetime], wait_duration: Optional[timedelta]
) -> datetime:
    if wait_duration:
        wait_duration_time = datetime.now() + wait_duration
    else:
        wait_duration_time = datetime.now()

    return max(wait_time or datetime.now(), wait_duration_time)


def wait(
    wait_time: Optional[datetime] = None,
    wait_duration: Optional[timedelta] = None,
    wait_for_files: Optional[Tuple[Path]] = None,
) -> None:
    final_wait_time = combine_waits(wait_time, wait_duration)
    while not (datetime.now() >= final_wait_time and files_exist(wait_for_files)):
        time.sleep(POLL_SEC)
