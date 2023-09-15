import logging
import multiprocessing
from datetime import datetime

from .config import JobmanConfig
from .core.purge import purge


def gc_logs(config: JobmanConfig, logger: logging.Logger) -> None:
    until = datetime.today() - config.gc_expiry
    logger.info(f"Deleting logs before {until}")
    purge_result = purge(
        job_ids=tuple(),
        _all=True,
        metadata=False,
        since=None,
        until=until,
        config=config,
        logger=logger,
    )
    logger.info(f"Purge completed: {purge_result}")


def bg_gc_logs(config: JobmanConfig, logger: logging.Logger) -> None:
    multiprocessing.Process(target=gc_logs, args=(config, logger)).start()
