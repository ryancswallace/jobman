import logging
import os
import shutil
import sys

from ..config import JobmanConfig
from ..display import Displayer
from ..models import get_or_create_db


def display_reset(
    config: JobmanConfig, displayer: Displayer, logger: logging.Logger
) -> int:
    reset(config, logger)
    displayer.display("✨🧹✨  Reset database to factory settings.", stream=sys.stderr)

    return os.EX_OK


def reset(config: JobmanConfig, logger: logging.Logger) -> None:
    config.db_path.unlink(missing_ok=True)
    logger.warn(f"Ensured old database at {config.db_path} deleted")

    config.stdio_path.mkdir(parents=True, exist_ok=True)
    shutil.rmtree(config.stdio_path)
    config.stdio_path.mkdir()
    logger.warn(f"Deleted all stdout/stderr logs from {config.stdio_path}")

    get_or_create_db(config.db_path)
    logger.info(f"Created new database at {config.db_path}")
