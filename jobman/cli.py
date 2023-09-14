import logging
import os
import re
import sys
from datetime import datetime, time, timedelta
from functools import wraps
from pathlib import Path
from signal import Signals
from typing import Callable, Dict, List, Optional, Tuple, TypeVar, Union

import click

from .base_logger import make_logger
from .config import load_config
from .core.install_completions import display_install_completions
from .core.kill import display_kill
from .core.logs import display_logs
from .core.ls import display_ls, ls
from .core.purge import display_purge
from .core.reset import display_reset
from .core.run import display_run
from .core.status import display_status
from .display import RichDisplayer
from .exceptions import JobmanError
from .gc import bg_gc_logs


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
                f" '{unit}'",
                exit_code=os.EX_USAGE,
            )
        else:
            try:
                v = int(unit_v[0])
            except ValueError:
                raise JobmanError(
                    f"Can't convert '{td_str}' to timedelta. '{unit_v[0]}' must be an"
                    " integer.",
                    exit_code=os.EX_USAGE,
                )
            durations[unit] = v
            td_str_work = td_str_work.replace(f"{v}{unit}", "")
    if td_str_work.strip():
        raise JobmanError(
            f"Can't convert '{td_str}' to timedelta. Got uninterpretable characters"
            f" '{td_str_work.strip()}'",
            exit_code=os.EX_USAGE,
        )
    return timedelta(
        weeks=durations["w"],
        days=durations["d"],
        hours=durations["h"],
        minutes=durations["m"],
        seconds=durations["s"],
    )


def complete_job_id(
    ctx: click.Context, param: Optional[click.Parameter], incomplete: str
) -> List[str]:
    try:
        config = load_config()
        logger = make_logger(DEBUG_TO_LEVEL[False])
    except JobmanError:
        # disable autocompletion of job-id if we can't build a
        # config or logger
        return []

    jobs = ls(all_=True, config=config, logger=logger)
    command_name = ctx.command.name
    if command_name in ["status", "logs"]:
        # all jobs on the host
        return [str(j.job_id) for j in jobs if str(j.job_id).startswith(incomplete)]
    elif command_name == "kill":
        # all running jobs on the host
        return [
            str(j.job_id)
            for j in jobs
            if not j.is_completed() and str(j.job_id).startswith(incomplete)
        ]
    elif command_name == "purge":
        # all non-running jobs on the host
        return [
            str(j.job_id)
            for j in jobs
            if j.is_completed() and str(j.job_id).startswith(incomplete)
        ]
    else:
        return []


def cli_exec(  # type: ignore[no-untyped-def]
    fn: Callable[..., int],
    quiet: bool,
    json: bool,
    plain: bool,
    debug: bool,
    *args,
) -> None:
    try:
        displayer = RichDisplayer(quiet, json, plain)
    except JobmanError as e:
        # can't use the displayer to render the error message if we can't
        # initialize the displayer itself
        print(f"ERROR! {e}", file=sys.stderr)
        sys.exit(e.exit_code)
    try:
        config = load_config()
        logger = make_logger(DEBUG_TO_LEVEL[debug])
        if fn in [display_logs, display_ls, display_run, display_status]:
            bg_gc_logs(config, logger)
        sys.exit(fn(*args, config, displayer, logger))
    except JobmanError as e:
        displayer.print_exception(e)
        sys.exit(e.exit_code)


class TimedeltaType(click.ParamType):
    name = "timedelta"

    def convert(
        self,
        value: Union[str, timedelta],
        param: Optional[click.Parameter],
        ctx: Optional[click.Context],
    ) -> timedelta:
        if isinstance(value, timedelta):
            return value

        try:
            return strptimedelta(value)
        except JobmanError as e:
            self.fail(str(e))


class TimeOrDateTime(click.DateTime):
    def convert(
        self, value: str, param: Optional[click.Parameter], ctx: Optional[click.Context]
    ) -> datetime:
        today = datetime.today()
        try:
            tm = time.fromisoformat(value)
        except ValueError:
            dt: datetime = super().convert(value, param, ctx)
            return dt

        return today.replace(
            hour=tm.hour, minute=tm.minute, second=tm.second, microsecond=0
        )


class JobmanGroup(click.Group):
    """
    Typical click Group class, but displays the usage epilog without an indent.
    """

    def format_epilog(
        self, ctx: Optional[click.Context], formatter: click.HelpFormatter
    ) -> None:
        if self.epilog:
            formatter.write_paragraph()
            for line in self.epilog.split("\n"):
                formatter.write_text(line)


DEBUG_TO_LEVEL: Dict[bool, int] = {
    True: logging.DEBUG,
    False: logging.CRITICAL,
}
EXAMPLES = """\
Examples:
  Run an echo command with a delay and retry attempts.
    $ jobman run --wait-duration 60s --retry-attempts 5 echo hi
  Wrap a command containing special shell characters in single quotes.
    $ jobman run 'myutil < file.txt | grep 123'
  Show the status of a job.
    $ jobman status abcdef12
  View and follow the stderr output only for a job.
    $ jobman logs --follow --hide-stdout 123456ab
  List all active jobs.
    $ jobman list
"""
CONTEXT_SETTINGS = {"help_option_names": ["-h", "--help"]}


@click.group(cls=JobmanGroup, context_settings=CONTEXT_SETTINGS, epilog=EXAMPLES)
@click.version_option(None, "--version", "-V")
def cli() -> None:
    """Run and monitor jobs on the command line with support for retries, timeouts,
    logging, notifications, and more.
    """


T = TypeVar("T")
R = TypeVar("R")


def global_options(f: Callable[..., R]) -> Callable[..., Callable[..., R]]:
    @wraps(f)
    @click.option(
        "-q", "--quiet", is_flag=True, default=False, help="Suppress unnecessary output"
    )
    @click.option(
        "-j",
        "--json",
        is_flag=True,
        default=False,
        help=(
            "Show output in machine-readable JSON format. Mutually exclusive with"
            " -p/--plain"
        ),
    )
    @click.option(
        "-p",
        "--plain",
        is_flag=True,
        default=False,
        help=(
            "Show output in plain machine-readable format. Mutually exclusive with"
            " -j/--json"
        ),
    )
    @click.option(
        "-d",
        "--debug",
        is_flag=True,
        default=False,
        help="Show detailed debugging logs",
    )
    def wrapper(*args, **kwargs):  # type: ignore[no-untyped-def]
        return f(*args, **kwargs)

    return wrapper


@cli.command("run", context_settings=CONTEXT_SETTINGS)
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
    json: bool,
    plain: bool,
    debug: bool,
) -> None:
    """Start a job in the background immune to hangups."""
    cli_exec(
        display_run,
        quiet,
        json,
        plain,
        debug,
        command,
        wait_time,
        wait_duration,
        wait_for_file,
        abort_time,
        abort_duration,
        abort_for_file,
        retry_attempts,
        retry_delay,
        success_code,
        notify_on_run_completion,
        notify_on_job_completion,
        notify_on_job_success,
        notify_on_run_success,
        notify_on_job_failure,
        notify_on_run_failure,
        follow,
    )


@cli.command("status", context_settings=CONTEXT_SETTINGS)
@click.argument("job-id", nargs=-1, required=True, shell_complete=complete_job_id)
@global_options
def cli_status(
    job_id: Tuple[str, ...],
    quiet: bool,
    json: bool,
    plain: bool,
    debug: bool,
) -> None:
    """Display the status of a job(s) JOB_ID."""
    cli_exec(display_status, quiet, json, plain, debug, job_id)


@cli.command("logs", context_settings=CONTEXT_SETTINGS)
@click.argument("job-id", nargs=1, shell_complete=complete_job_id)
@click.option(
    "-o",
    "--hide-stdout",
    is_flag=True,
    default=False,
    help="Don't display job's stdout",
)
@click.option(
    "-e",
    "--hide-stderr",
    is_flag=True,
    default=False,
    help="Don't display job's stderr",
)
@click.option(
    "-f",
    "--follow",
    is_flag=True,
    default=False,
    help="Display running log messages as output",
)
@click.option(
    "-x",
    "--no-log-prefix",
    is_flag=True,
    default=False,
    help="Don't display leading log timestamp info",
)
@click.option(
    "-n",
    "--tail",
    type=click.IntRange(min=0),
    help="Show only the last n lines of log output",
)
@click.option(
    "-s", "--since", type=TimeOrDateTime(), help="Don't show logs before this datetime"
)
@click.option(
    "-u", "--until", type=TimeOrDateTime(), help="Don't show logs after this datetime"
)
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
    json: bool,
    plain: bool,
    debug: bool,
) -> None:
    """Show output from job(s) JOB_ID."""
    cli_exec(
        display_logs,
        quiet,
        json,
        plain,
        debug,
        job_id,
        hide_stdout,
        hide_stderr,
        follow,
        no_log_prefix,
        tail,
        since,
        until,
    )


SIGNALS = [s.name for s in Signals] + [str(s.value) for s in Signals]


@cli.command("kill", context_settings=CONTEXT_SETTINGS)
@click.argument("job-id", nargs=-1, required=True, shell_complete=complete_job_id)
@click.option(
    "-s",
    "--signal",
    type=click.Choice(SIGNALS),
    help=(
        "Name (e.g., SIGINT) or integer number (e.g., 2) of signal to send to job"
        " process"
    ),
)
@click.option(
    "-r",
    "--allow-retries",
    is_flag=True,
    default=False,
    help="Don't stop future retries from running if retries remain for the job",
)
@click.option(
    "-f", "--force", is_flag=True, default=False, help="Don't prompt for confirmation"
)
@global_options
def cli_kill(
    job_id: Tuple[str, ...],
    signal: Optional[str],
    allow_retries: bool,
    force: bool,
    quiet: bool,
    json: bool,
    plain: bool,
    debug: bool,
) -> None:
    """Stop running job JOB_ID."""
    multiple = len(job_id) > 1
    if not force:
        click.confirm(
            "⚠️  Are you sure you want to stop"
            f" job{'s' if multiple else ''} {', '.join(job_id)}?",
            abort=True,
        )
    cli_exec(display_kill, quiet, json, plain, debug, job_id, signal, allow_retries)


@cli.command("ls", context_settings=CONTEXT_SETTINGS)
@click.option(
    "-a", "--all", "all_", is_flag=True, default=False, help="Include finished jobs"
)
@global_options
def cli_ls(
    all_: bool,
    quiet: bool,
    json: bool,
    plain: bool,
    debug: bool,
) -> None:
    """View jobs."""
    cli_exec(display_ls, quiet, json, plain, debug, all_)


@cli.command("purge", context_settings=CONTEXT_SETTINGS)
@click.argument("job-id", nargs=-1, required=False, shell_complete=complete_job_id)
@click.option(
    "-a",
    "--all",
    "_all",
    is_flag=True,
    default=False,
    help="Delete all jobs. Mutually exclusive with job-id",
)
@click.option(
    "-m",
    "--metadata",
    is_flag=True,
    default=False,
    help="Delete job metadata in addition to logs",
)
@click.option(
    "-s",
    "--since",
    type=TimeOrDateTime(),
    help="When using -a/--all, don't delete jobs before this datetime",
)
@click.option(
    "-u",
    "--until",
    type=TimeOrDateTime(),
    help="When using -a/--all, don't delete jobs after this datetime",
)
@click.option(
    "-f", "--force", is_flag=True, default=False, help="Don't prompt for confirmation"
)
@global_options
def cli_purge(
    job_id: Tuple[str, ...],
    _all: bool,
    metadata: bool,
    since: Optional[datetime],
    until: Optional[datetime],
    force: bool,
    quiet: bool,
    json: bool,
    plain: bool,
    debug: bool,
) -> None:
    """Delete metadata for historical job(s) JOB_ID."""

    if not force:
        click.confirm(
            "⚠️  Purging will permanently delete all specified job history and logs."
            " Continue?",
            abort=True,
        )
    cli_exec(
        display_purge,
        quiet,
        json,
        plain,
        debug,
        job_id,
        _all,
        metadata,
        since,
        until,
    )


@cli.command("reset", context_settings=CONTEXT_SETTINGS)
@click.option(
    "-f", "--force", is_flag=True, default=False, help="Don't prompt for confirmation"
)
@global_options
def cli_reset(
    force: bool,
    quiet: bool,
    json: bool,
    plain: bool,
    debug: bool,
) -> None:
    """Destroy and recreate Jobman metadata database. Delete all job logs."""
    if not force:
        click.confirm(
            "⚠️  Resetting will permanently delete all job history and logs. Continue?",
            abort=True,
        )
    cli_exec(display_reset, quiet, json, plain, debug)


@cli.command("install-completions", context_settings=CONTEXT_SETTINGS)
@click.argument(
    "shell",
    nargs=1,
    required=False,
    default=None,
    shell_complete=lambda *_: ["bash", "zsh", "fish"],
)
@global_options
def cli_install_completions(
    shell: Optional[str],
    quiet: bool,
    json: bool,
    plain: bool,
    debug: bool,
) -> None:
    """Configure shell for command, argument, and option completions."""
    cli_exec(display_install_completions, quiet, json, plain, debug, shell)


if __name__ == "__main__":
    cli()
