import re
import signal
from datetime import datetime, time, timedelta
from functools import wraps
from pathlib import Path
from typing import Optional, Tuple

import click

from .core.kill import kill
from .core.logs import logs
from .core.ls import ls
from .core.purge import purge
from .core.reset import reset
from .core.run import run
from .core.status import status
from .exceptions import JobmanError


def strptimedelta(td_str: str) -> timedelta:
    durations = {}
    td_str_work = td_str
    for unit in ["w", "d", "h", "m", "s"]:
        unit_v = re.findall(f"(\d+){unit}", td_str_work)
        if not unit_v:
            durations[unit] = 0
        elif len(unit_v) > 1:
            raise JobmanError(
                f"Can't convert '{td_str}' to timedelta. Got multiple values for"
                f" '{unit}'"
            )
        else:
            try:
                v = int(unit_v[0])
            except ValueError:
                raise JobmanError(
                    f"Can't convert '{td_str}' to timedelta. '{unit_v[0]}' must be an"
                    " integer."
                )
            durations[unit] = v
            td_str_work = td_str_work.replace(f"{v}{unit}", "")
    if td_str_work.strip():
        raise JobmanError(
            f"Can't convert '{td_str}' to timedelta. Got uninterpretable characters"
            f" '{td_str_work.strip()}'"
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
    """Run and monitor jobs on the command line with support for retries, timeouts,
    logging, notifications, and more.
    """


def global_options(f):
    @wraps(f)
    @click.option(
        "-q", "--quiet", is_flag=True, default=False, help="Suppress unnecessary output"
    )
    @click.option(
        "-v", "--verbose", is_flag=True, default=False, help="Show more detail"
    )
    @click.option(
        "-j",
        "--json",
        is_flag=True,
        default=False,
        help="Show output in machine-readable JSON format",
    )
    def wrapper(*args, **kwargs):
        return f(*args, **kwargs)

    return wrapper


@cli.command("run")
@click.argument("command", nargs=-1, required=True)
@click.option(
    "--wait-time",
    type=TimeOrDateTime(),
    help="Do not run the command until the specified date or time",
)
@click.option(
    "--wait-duration",
    type=TimedeltaType(),
    help="Do not run the command until the specified duration has elapsed",
)
@click.option(
    "--wait-for-file",
    type=click.Path(),
    multiple=True,
    help="Do not run the command until the specified file exists",
)
@click.option(
    "--abort-time",
    type=TimeOrDateTime(),
    help="Terminate the command if it's still running at the specified time",
)
@click.option(
    "--abort-duration",
    type=TimedeltaType(),
    help=(
        "Terminate the command if it's still running after the specified duration has"
        " elapsed"
    ),
)
@click.option(
    "--abort-for-file",
    type=click.Path(),
    multiple=True,
    help="Terminate the command if it's still running and the specified file exists",
)
@click.option(
    "--retry-attempts",
    type=click.IntRange(min=0),
    help="If the command fails, rerun the command up to the specified number",
)
@click.option(
    "--retry-delay",
    type=TimedeltaType(),
    help="Wait the specified time before starting retries",
)
@click.option(
    "-c",
    "--success-code",
    type=click.IntRange(min=0, max=255),
    multiple=True,
    help="Interpret these exit codes as a successful execution",
)
@click.option(
    "--notify-on-job-completion",
    type=str,
    multiple=True,
    help="Send a notification to this callback when the job completes",
)
@click.option(
    "--notify-on-run-completion",
    type=str,
    multiple=True,
    help="Send a notification to this callback when any run of the job completes",
)
@click.option(
    "--notify-on-job-success",
    type=str,
    multiple=True,
    help="Send a notification to this callback when the job completes successfully",
)
@click.option(
    "--notify-on-run-success",
    type=str,
    multiple=True,
    help=(
        "Send a notification to this callback when any run of the job completes"
        " successfully"
    ),
)
@click.option(
    "--notify-on-job-failure",
    type=str,
    multiple=True,
    help="Send a notification to this callback when the job fails",
)
@click.option(
    "--notify-on-run-failure",
    type=str,
    multiple=True,
    help="Send a notification to this callback when a run of the job fails",
)
@click.option(
    "-f",
    "--follow",
    is_flag=True,
    default=False,
    help="Display a running log of the command's output",
)
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
    json: bool,
):
    """Start a job in the background immune to hangups."""
    ret = run()
    click.echo(ret)


@cli.command("status")
@click.argument("job-id", nargs=-1, required=True)
@global_options
def cli_status(
    job_id: Tuple[str, ...],
    quiet: bool,
    verbose: bool,
    json: bool,
):
    """Display the status of a job."""
    ret = status()
    click.echo(ret)


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
    json: bool,
):
    """Show output from jobs."""
    ret = logs()
    click.echo(ret)


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
    json: bool,
):
    """Stop a running job."""
    ret = kill()
    click.echo(ret)


@cli.command("ls")
@click.option("-a", "--all", "all_", is_flag=True, default=False)
@global_options
def cli_ls(
    all_: bool,
    quiet: bool,
    verbose: bool,
    json: bool,
):
    """View jobs."""
    ret = ls()
    click.echo(ret)


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
    json: bool,
):
    """Delete metadata for historical jobs."""
    if not (bool(job_id) ^ _all):
        raise click.exceptions.UsageError(
            "Must supply either a job-id argument or the -a/--all flag, but not both"
        )
    click.confirm(
        "Purging will permanently delete all specified job history and logs. Continue?",
        abort=True,
    )
    ret = purge()
    click.echo(ret)


@cli.command("reset")
@click.option("-f", "--force", is_flag=True, default=False)
@global_options
def cli_reset(
    force: bool,
    quiet: bool,
    verbose: bool,
    json: bool,
):
    """Destroy and recreate full Jobman metadata database."""
    click.confirm(
        "Resetting will permanently delete all job history and logs. Continue?",
        abort=True,
    )
    ret = reset()
    click.echo(ret)


if __name__ == "__main__":
    cli()
