"""
Code to mimic the behavior of the common command line pattern
"nohup command &" to run a process in the background immune to hangups.
"""
import os
import sys

from ...exceptions import JobmanError


def nohupify() -> None:
    """
    Double fork to detach process from the controlling terminal and run it
    in the background.
    """
    sys.stdout = open(os.devnull, "w")
    sys.stderr = open(os.devnull, "w")
    sys.stdin = open(os.devnull, "r")

    try:
        pid = os.fork()
    except OSError as e:
        raise JobmanError(str(e), exit_code=os.EX_OSERR)

    if pid != 0:
        # os._exit is preferred over os.exit since _exit doesn't invoke the
        # registered signal handlers that exit does, which could result in
        # stdio streams being flushed twice
        os._exit(0)

    # become session leader and ensure no controlling terminal
    os.setsid()

    # for again and exit immediately to prevent zombies
    try:
        pid = os.fork()
    except OSError as e:
        raise JobmanError(str(e), exit_code=os.EX_OSERR)

    if pid != 0:
        os._exit(0)
