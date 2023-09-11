"""Defines a single instance of a logging.logger used across jobman modules.
"""
import logging


def make_logger(log_level: int) -> logging.Logger:
    handler = logging.StreamHandler()
    formatter = logging.Formatter(
        "%(asctime)s - %(name)s - %(levelname)s - %(message)s"
    )
    handler.setFormatter(formatter)
    logger = logging.getLogger("jobman")
    logger.setLevel(log_level)
    logger.addHandler(handler)

    return logger
