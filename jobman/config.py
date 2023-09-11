import os
from dataclasses import field
from datetime import timedelta
from pathlib import Path
from typing import Any, Dict, List

import ruamel.yaml
from pydantic import BaseModel, ConfigDict, ValidationError

from .exceptions import JobmanError

CONFIG_HOME = Path(
    os.environ.get("JOBMAN_CONFIG_HOME", "~/.config/jobman/")
).expanduser()


class JobmanConfig(BaseModel):
    storage_path: Path = Path("~/.local/share/jobman")
    gc_expiry_days: timedelta = timedelta(days=7)
    notification_sinks: List[Dict[str, str]] = field(default_factory=lambda: [])
    db_path: Path = Path()
    stdio_path: Path = Path()

    model_config = ConfigDict(extra="forbid", frozen=False)

    def model_post_init(self, __config: Any) -> None:
        self.storage_path = self.storage_path.expanduser()
        self.db_path = self.storage_path / "db"
        self.stdio_path = self.storage_path / "stdio"


def _load_config_file(config_file_path: Path) -> Dict[str, Any]:
    """
    Read the configuration file into a dictionary.
    """
    if not config_file_path.is_file():
        empty_config: Dict[str, Any] = dict()
        return empty_config

    yaml = ruamel.yaml.YAML(typ="safe")
    with open(config_file_path, "r") as f:
        config: Dict[str, Any] = yaml.load(f)

    return config


def load_config() -> JobmanConfig:
    """
    Read the configuration file and parse it into a Configuration object.
    """
    config_path = CONFIG_HOME / "config.yml"
    try:
        config_dict = _load_config_file(config_path)
    except (
        IOError,
        OSError,
        ruamel.yaml.parser.ParserError,
        ruamel.yaml.scanner.ScannerError,
    ):
        raise JobmanError(
            f"Failed to parse config file {config_path}", exit_code=os.EX_CONFIG
        )

    try:
        jobman_config = JobmanConfig.model_validate(config_dict)
    except ValidationError:
        raise JobmanError(
            f"Invalid config file at {config_path}", exit_code=os.EX_CONFIG
        )

    return jobman_config
