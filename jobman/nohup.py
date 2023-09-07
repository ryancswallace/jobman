"""
Code to mimic the behavior of the common command line pattern
"nohup command &" to run a process in the background immune to hangups.
"""
import os

from .exceptions import JobmanForkError


def nohupify():
    """
    Double fork to detach process from the controlling terminal and run it
    in the background.
    """
    try:
        pid = os.fork()
    except OSError as e:
        raise JobmanForkError(e)

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
        raise JobmanForkError(e)

    if pid != 0:
        os._exit(0)
