import json
from datetime import datetime, timedelta
from enum import Enum
from pathlib import Path
from typing import Any, Dict, List, Optional, Tuple, Union

from peewee import (
    BooleanField,
    DateTimeField,
    FloatField,
    ForeignKeyField,
    IntegerField,
    Model,
    TextField,
)
from playhouse.shortcuts import model_to_dict  # type: ignore
from playhouse.sqlite_ext import SqliteExtDatabase  # type: ignore
from rich.syntax import Syntax


class JobmanDatabase(SqliteExtDatabase):  # type: ignore[misc,no-any-unimported]
    pass

    def build(self) -> None:
        all_tables = [Job, Run]
        self.create_tables(all_tables)


db = JobmanDatabase(
    None,
    pragmas={
        "journal_mode": "wal",
        "cache_size": -1 * 64,  # 64KB
        "foreign_keys": 1,
        "ignore_check_constraints": 0,
    },
)


def init_db_models(db_path: Path) -> None:
    db.init(db_path)
    db.connect()
    db.build()


class TimedeltaField(FloatField):
    def db_value(self, value: Optional[timedelta]) -> Optional[str]:
        if value is None:
            return None
        if not isinstance(value, timedelta):
            raise TypeError(
                f"Received wrong type {type(value)} to serialize to TimedeltaField."
            )
        total_secs = value.total_seconds()
        total_secs_db: str = super().db_value(total_secs)  # type: ignore[no-untyped-call]
        return total_secs_db

    def python_value(self, value: Optional[str]) -> Optional[timedelta]:
        if value is None:
            return None
        total_secs = super().python_value(value)  # type: ignore[no-untyped-call]
        return timedelta(seconds=total_secs)


class TextTupleField(TextField):
    delim = "|"

    def db_value(self, value: Optional[Union[List, Tuple]]) -> Optional[str]:  # type: ignore[type-arg]
        if not value:
            return None
        if not (isinstance(value, list) or isinstance(value, tuple)):
            raise TypeError(
                f"Received wrong type {type(value)} to serialize to TextTupleField."
                " Must be a list or tuple."
            )
        try:
            value_str = [str(i) for i in value]
        except ValueError as e:
            raise ValueError(
                "All elements of a TextTupleField must support a string"
                f" representation: {e}"
            )
        for i in value_str:
            if self.delim in i:
                raise ValueError(
                    "Elements of a TextTupleField must not contain the internal"
                    f" delimiter {self.delim}. Received element {i}."
                )

        db_value: str = super().db_value(self.delim.join(value_str))  # type: ignore[no-untyped-call]
        return db_value

    def python_value(self, value: Optional[str]) -> Optional[List[str]]:
        if value is None:
            return None
        return value.split(self.delim)


class IntegerTupleField(TextTupleField):
    def python_value(self, value: Optional[str]) -> Optional[List[int]]:  # type: ignore[override]
        if value is None:
            return None
        return [int(i) for i in value.split(self.delim)]


class PathTupleField(TextTupleField):
    def python_value(self, value: Optional[str]) -> Optional[List[Path]]:  # type: ignore[override]
        if value is None:
            return None
        return [Path(i) for i in value.split(self.delim)]


class PathField(TextField):
    def python_value(self, value: Optional[str]) -> Optional[Path]:
        if value is None:
            return None
        return Path(value)


class JobmanModelEncoder(json.JSONEncoder):
    def default(self, o: Any) -> Any:
        if hasattr(o, "__dict__"):
            d = o.__dict__
            if "__data__" in d:
                return d["__data__"]
            else:
                return d
        else:
            return str(o)


class JobmanModel(Model):
    class Meta:
        database = db

    @staticmethod
    def _name_to_display_name(name: str) -> str:
        return name.replace("_", " ").title()

    @property
    def pretty(self) -> Dict[str, Tuple[str, Union[str, Syntax]]]:
        name_to_pretty = dict()
        for name in self._meta.fields:  # type: ignore[attr-defined]
            pretty_name = self._name_to_display_name(name)
            val = getattr(self, name)

            pretty_val: Union[str, Syntax] = str(val)
            if val is None:
                pretty_val = "-"
            elif name == "command":
                # fish shell has the best pygments syntax highlighting support
                # so we use fish highlighting regardless of the parent shell
                syntax = Syntax(val, "fish", background_color="default")
                pretty_val = syntax
            elif name.endswith("_time"):
                pretty_val = str(val.replace(microsecond=0))
            elif name == "state":
                pretty_val = JobState(val).name.title()
            elif name == "success_codes":
                pretty_val = ", ".join(map(str, sorted(val)))
            elif name.startswith("notify_on_"):
                pretty_val = ", ".join(sorted(val))
            elif name.endswith("_for_file"):
                pretty_val = ", ".join(str(p) for p in sorted(val))

            name_to_pretty[name] = (pretty_name, pretty_val)

        return name_to_pretty

    def __str__(self) -> str:
        args = ", ".join(
            f"{name}={getattr(self, name)}" for name in self._meta.fields.keys()  # type: ignore[attr-defined]
        )
        class_name = self.__class__.__name__
        return f"{class_name}({args})"

    def __repr__(self) -> str:
        return self.__str__()


class JobState(Enum):
    SUBMITTED = 0
    RUNNING = 1
    COMPLETE = 2


class RunState(Enum):
    SUBMITTED = 0
    RUNNING = 1
    COMPLETE = 2


class Job(JobmanModel):
    job_id: str = TextField(unique=True)  # type: ignore[assignment]
    host_id: str = TextField()  # type: ignore[assignment]
    command: str = TextField()  # type: ignore[assignment]
    wait_time: Optional[datetime] = DateTimeField(null=True)  # type: ignore[assignment]
    wait_duration: Optional[timedelta] = TimedeltaField(null=True)  # type: ignore[assignment]
    wait_for_files: Optional[Tuple[Path]] = PathTupleField(null=True)  # type: ignore[assignment]
    abort_time: Optional[datetime] = DateTimeField(null=True)  # type: ignore[assignment]
    abort_duration: Optional[timedelta] = TimedeltaField(null=True)  # type: ignore[assignment]
    abort_for_files: Optional[List[Path]] = PathTupleField(null=True)  # type: ignore[assignment]
    retry_attempts: Optional[int] = IntegerField(null=True)  # type: ignore[assignment]
    retry_delay: Optional[timedelta] = TimedeltaField(null=True)  # type: ignore[assignment]
    retry_expo_backoff: bool = BooleanField(null=True)  # type: ignore[assignment]
    retry_jitter: bool = BooleanField(null=True)  # type: ignore[assignment]
    success_codes: Tuple[int] = IntegerTupleField(null=True)  # type: ignore[assignment]
    notify_on_run_completion: Optional[Tuple[str]] = TextTupleField(null=True)  # type: ignore[assignment]
    notify_on_job_completion: Optional[Tuple[str]] = TextTupleField(null=True)  # type: ignore[assignment]
    notify_on_job_success: Optional[Tuple[str]] = TextTupleField(null=True)  # type: ignore[assignment]
    notify_on_run_success: Optional[Tuple[str]] = TextTupleField(null=True)  # type: ignore[assignment]
    notify_on_job_failure: Optional[Tuple[str]] = TextTupleField(null=True)  # type: ignore[assignment]
    notify_on_run_failure: Optional[Tuple[str]] = TextTupleField(null=True)  # type: ignore[assignment]
    follow: Optional[bool] = BooleanField(null=True)  # type: ignore[assignment]
    start_time: Optional[datetime] = DateTimeField(null=True)  # type: ignore[assignment]
    finish_time: Optional[datetime] = DateTimeField(null=True)  # type: ignore[assignment]
    state: int = IntegerField()  # type: ignore[assignment]
    exit_code: Optional[int] = IntegerField(null=True)  # type: ignore[assignment]

    def is_failed(self) -> bool:
        return self.exit_code is not None and self.exit_code not in self.success_codes

    def is_completed(self) -> bool:
        completed: bool = self.state == JobState.COMPLETE.value
        return completed


class Run(JobmanModel):
    job: Job = ForeignKeyField(Job, field="job_id", backref="runs")  # type: ignore[assignment]
    attempt: int = IntegerField()  # type: ignore[assignment]
    log_path: Path = PathField()  # type: ignore[assignment]
    pid: Optional[int] = IntegerField(null=True)  # type: ignore[assignment]
    start_time: Optional[datetime] = DateTimeField(null=True)  # type: ignore[assignment]
    finish_time: Optional[datetime] = DateTimeField(null=True)  # type: ignore[assignment]
    state: int = IntegerField()  # type: ignore[assignment]
    exit_code: Optional[int] = IntegerField(null=True)  # type: ignore[assignment]
    killed: Optional[bool] = BooleanField(null=True)  # type: ignore[assignment]

    def is_completed(self) -> bool:
        completed: bool = self.state == RunState.COMPLETE.value
        return completed
