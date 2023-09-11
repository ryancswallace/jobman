import logging
import os

from ..config import JobmanConfig
from ..display import Displayer


def reset(config: JobmanConfig, displayer: Displayer, logger: logging.Logger) -> int:
    return os.EX_OK
