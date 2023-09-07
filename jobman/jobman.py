import hashlib
import logging
import platform
import random
import string
import subprocess
from dataclasses import dataclass, field
from pathlib import Path
from typing import List, Optional, Union

import psutil

from . import base_logger
from .nohup import nohupify
from .rotating_stdio import RotatingIOWrapper


def get_host_id():
    system_info = platform.uname()
    system_info_str = ";".join(
        [
            system_info.node,
            system_info.system,
            system_info.release,
            system_info.version,
            system_info.machine,
            system_info.processor,
        ]
    )
    host_id = hashlib.sha256(system_info_str.encode()).hexdigest()[:12]
    return host_id


@dataclass
class NotificationSink:
    pass


@dataclass
class JobmanConfig:
    storage_path: Union[str, Path] = "~/.jobman"
    notification_sinks: List[NotificationSink] = field(default_factory=lambda: [])

    # TOD0: gc config
    def __post_init__(self):
        self.storage_path = Path(self.storage_path).expanduser()
        self.db_path = self.storage_path / "db"
        self.stdio_path = self.storage_path / "stdio"
        self.log_path = self.storage_path / "log"


@dataclass
class Job:
    command: str

    wait_time: str = ""
    wait_duration: str = ""
    wait_for_file: List[str] = field(default_factory=lambda: [])

    abort_time: str = ""
    abort_duration: str = ""
    abort_for_file: List[str] = field(default_factory=lambda: [])

    retry_attempts: int = 0
    retry_delay: str = ""

    notify_on_run_completion: List[str] = field(default_factory=lambda: [])
    notify_on_job_completion: List[str] = field(default_factory=lambda: [])
    notify_on_job_success: List[str] = field(default_factory=lambda: [])
    notify_on_run_success: List[str] = field(default_factory=lambda: [])
    notify_on_job_failure: List[str] = field(default_factory=lambda: [])
    notify_on_run_failure: List[str] = field(default_factory=lambda: [])

    success_codes: List[int] = field(default_factory=lambda: [0])
    follow_logs: bool = False
    quiet: bool = False
    verbose: bool = False

    @staticmethod
    def _generate_random_id():
        id_len = 8
        return "".join(random.choices(string.hexdigits, k=id_len)).lower()

    def __post_init__(self):
        self.jobman_config = jobman_config
        self.host_id = get_host_id()
        self.job_id = self._generate_random_id()

        self.job_stdio_path = self.jobman_config.stdio_path / self.host_id / self.job_id

        host_log_path = self.jobman_config.log_path / self.host_id
        host_log_path.mkdir(parents=True, exist_ok=True)
        self.logger = base_logger.make_logger(host_log_path / self.job_id, "INFO")

    def start(self) -> None:
        print(self.job_id)

        nohupify()

        run_id = 0
        run_stdio_path = self.job_stdio_path / str(run_id)
        run_stdio_path.mkdir(parents=True)
        out_file_path = run_stdio_path / "out.txt"
        err_file_path = run_stdio_path / "err.txt"

        job_run = JobRun(
            self.command,
            out_file_path,
            err_file_path,
            self.logger,
        )
        job_run.start()
        if job_run.proc:
            job_run.proc.wait()


def procs_are_same(proc_1: psutil.Process, proc_2: psutil.Process):
    same_pid = proc_1.pid == proc_2.pid
    same_create_time = proc_1.create_time() == proc_2.create_time()
    return same_pid and same_create_time


class JobRun:
    def __init__(
        self,
        command: str,
        out_file_path: Path,
        err_file_path: Path,
        logger: logging.Logger,
        proc: Optional[psutil.Process] = None,
        pid: Optional[int] = None,
    ):
        self.command = command
        self.out_file_path = out_file_path
        self.err_file_path = err_file_path
        self.logger = logger
        self.pid = pid
        self.proc = proc

    def start(self):
        if self.proc is not None or self.pid is not None:
            raise RuntimeError("Job has already been run")

        out_fp = RotatingIOWrapper(self.out_file_path)
        err_fp = RotatingIOWrapper(self.err_file_path)

        out_fp.write("TEST123")

        popen_ret = subprocess.Popen(
            self.command, shell=True, stdout=out_fp, stderr=err_fp
        )

        self.pid = popen_ret.pid
        try:
            self.proc = psutil.Process(popen_ret.pid)
        except psutil.NoSuchProcess:
            print("Terminated before getting proc")

    def is_running(self):
        """
        Return whether the process for this JobRun:
        1) has been started, AND
        2) still exists in the process table, AND
        3) is not a zombie
        """
        if self.proc is None:
            return False

        try:
            proc = psutil.Process(self.pid)
        except (psutil.NoSuchProcess, psutil.AccessDenied):
            return False

        if proc.status() == psutil.STATUS_ZOMBIE:
            return False

        return procs_are_same(self.proc, proc)


jobman_config = JobmanConfig()

job = Job("sleep 5; echo hi; sleep 5")
job.start()
