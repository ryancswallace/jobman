from typing import Tuple, Optional, List
from functools import wraps
from datetime import datetime, time, timedelta
import signal
from pathlib import Path
import re

import click

from exceptions import JobmanError


def strptimedelta(td_str: str) -> timedelta:
    durations = {}
    td_str_work = td_str
    for unit in ["w", "d", "h", "m", "s"]:
        unit_v = re.findall(f"(\d+){unit}", td_str_work)
        if not unit_v:
            durations[unit] = 0
        elif len(unit_v) > 1:
            raise JobmanError(
                f"Can't convert '{td_str}' to timedelta. Got multiple values for '{unit}'"
            )
        else:
            try:
                v = int(unit_v[0])
            except ValueError:
                raise JobmanError(
                    f"Can't convert '{td_str}' to timedelta. '{unit_v[0]}' must be an integer."
                )
            durations[unit] = v
            td_str_work = td_str_work.replace(f"{v}{unit}", "")
    if td_str_work.strip():
        raise JobmanError(
            f"Can't convert '{td_str}' to timedelta. Got uninterpretable characters '{td_str_work.strip()}'"
        )
    return timedelta(
        weeks=durations["w"],
        days=durations["d"],
        hours=durations["h"],
        minutes=durations["m"],
        seconds=durations["s"],
    )


class TimedeltaType(click.ParamType):
    name = "timedelta"

    def convert(self, value, param, ctx):
        if isinstance(value, timedelta):
            return value

        try:
            return strptimedelta(value)
        except JobmanError as e:
            self.fail(str(e))


class TimeOrDateTime(click.DateTime):
    def convert(self, value, param, ctx):
        today = datetime.today()
        try:
            tm = time.fromisoformat(value)
        except ValueError:
            return super().convert(value, param, ctx)

        return today.replace(
            hour=tm.hour, minute=tm.minute, second=tm.second, microsecond=0
        )


@click.group()
def cli():
    pass


def global_options(f):
    @wraps(f)
    @click.option("-q", "--quiet", is_flag=True, default=False)
    @click.option("-v", "--verbose", is_flag=True, default=False)
    @click.option("-m", "--no-color", is_flag=True, default=False)
    @click.option("-j", "--json", is_flag=True, default=False)
    def wrapper(*args, **kwargs):
        return f(*args, **kwargs)

    return wrapper


@cli.command("run")
@click.argument("command", nargs=-1, required=True)
@click.option("--wait-time", type=TimeOrDateTime())
@click.option("--wait-duration", type=TimedeltaType())
@click.option("--wait-for-file", type=click.Path(), multiple=True)
@click.option("--abort-time", type=TimeOrDateTime())
@click.option("--abort-duration", type=TimedeltaType())
@click.option("--abort-for-file", type=click.Path(), multiple=True)
@click.option("--retry-attempts", type=click.IntRange(min=0))
@click.option("--retry-delay", type=TimedeltaType())
@click.option(
    "-c", "--success-code", type=click.IntRange(min=0, max=255), multiple=True
)
@click.option("--notify-on-job-completion", type=str, multiple=True)
@click.option("--notify-on-run-completion", type=str, multiple=True)
@click.option("--notify-on-job-success", type=str, multiple=True)
@click.option("--notify-on-run-success", type=str, multiple=True)
@click.option("--notify-on-job-failure", type=str, multiple=True)
@click.option("--notify-on-run-failure", type=str, multiple=True)
@click.option("-f", "--follow", is_flag=True, default=False)
@global_options
def cli_run(
    command: Tuple[str, ...],
    wait_time: Optional[datetime],
    wait_duration: Optional[timedelta],
    wait_for_file: Optional[Tuple[Path]],
    abort_time: Optional[datetime],
    abort_duration: Optional[timedelta],
    abort_for_file: Optional[Tuple[Path]],
    retry_attempts: Optional[int],
    retry_delay: Optional[timedelta],
    success_code: Optional[Tuple[str]],
    notify_on_run_completion: Optional[Tuple[str]],
    notify_on_job_completion: Optional[Tuple[str]],
    notify_on_job_success: Optional[Tuple[str]],
    notify_on_run_success: Optional[Tuple[str]],
    notify_on_job_failure: Optional[Tuple[str]],
    notify_on_run_failure: Optional[Tuple[str]],
    follow: bool,
    quiet: bool,
    verbose: bool,
    no_color: bool,
    json: bool,
):
    print(f"run")
    print(f"{notify_on_run_completion=}")
    print(f"{command=}")
    print(f"{success_code=}")
    print(f"{wait_duration=}")
    print(f"{wait_time=}")


@cli.command("status")
@click.argument("job-id", nargs=-1, required=True)
@global_options
def cli_status(
    job_id: Tuple[str, ...],
    quiet: bool,
    verbose: bool,
    no_color: bool,
    json: bool,
):
    print(f"status")


@cli.command("logs")
@click.argument("job-id", nargs=1)
@click.option("-o", "--hide-stdout", is_flag=True, default=False)
@click.option("-e", "--hide-stderr", is_flag=True, default=False)
@click.option("-f", "--follow", is_flag=True, default=False)
@click.option("-p", "--no-log-prefix", is_flag=True, default=False)
@click.option("-n", "--tail", type=click.IntRange(min=0))
@click.option("-s", "--since", type=TimeOrDateTime())
@click.option("-u", "--until", type=TimeOrDateTime())
@global_options
def cli_logs(
    job_id: str,
    hide_stdout: bool,
    hide_stderr: bool,
    follow: bool,
    no_log_prefix: bool,
    tail: Optional[int],
    since: Optional[datetime],
    until: Optional[datetime],
    quiet: bool,
    verbose: bool,
    no_color: bool,
    json: bool,
):
    print(f"logs")


SIGNALS = [s.name for s in signal.Signals] + [str(s.value) for s in signal.Signals]


@cli.command("kill")
@click.argument("job-id", nargs=-1, required=True)
@click.option("-s", "--signal", type=click.Choice(SIGNALS))
@click.option("-r", "--allow-retries", is_flag=True, default=False)
@click.option("-f", "--force", is_flag=True, default=False)
@global_options
def cli_kill(
    job_id: Tuple[str, ...],
    signal: Optional[str],
    allow_retries: bool,
    force: bool,
    quiet: bool,
    verbose: bool,
    no_color: bool,
    json: bool,
):
    print(f"kill")
    print(f"{signal=}")


@cli.command("ls")
@click.option("-a", "--all", "_all", is_flag=True, default=False)
@global_options
def cli_ls(
    _all: bool,
    quiet: bool,
    verbose: bool,
    no_color: bool,
    json: bool,
):
    print(f"ls")


@cli.command("purge")
@click.argument("job-id", nargs=-1, required=False)
@click.option("-a", "--all", "_all", is_flag=True, default=False)
@click.option("-m", "--metadata", is_flag=True, default=False)
@click.option("-s", "--since", type=TimeOrDateTime())
@click.option("-u", "--until", type=TimeOrDateTime())
@click.option("-f", "--force", is_flag=True, default=False)
@global_options
def cli_purge(
    job_id: Tuple[str, ...],
    _all: bool,
    metadata: bool,
    since: Optional[datetime],
    until: Optional[datetime],
    force: bool,
    quiet: bool,
    verbose: bool,
    no_color: bool,
    json: bool,
):
    print(f"purge")
    if not (bool(job_id) ^ _all):
        raise click.exceptions.UsageError(
            "Must supply either a job-id argument or the -a/--all flag, but not both"
        )


@cli.command("reset")
@click.option("-f", "--force", is_flag=True, default=False)
@global_options
def cli_reset(
    force: bool,
    quiet: bool,
    verbose: bool,
    no_color: bool,
    json: bool,
):
    print(f"reset")


if __name__ == "__main__":
    cli()
