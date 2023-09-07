"""Defines a single instance of a logging.logger used across jobman modules.
"""
import logging
from pathlib import Path
from typing import Union


def make_logger(log_file_path: Union[str, Path], log_level: str):
    handler = logging.FileHandler(log_file_path)
    formatter = logging.Formatter(
        "%(asctime)s - %(name)s - %(levelname)s - %(message)s"
    )
    handler.setFormatter(formatter)
    logger = logging.getLogger("jobman")
    logger.setLevel(log_level)
    logger.addHandler(handler)

    return logger
