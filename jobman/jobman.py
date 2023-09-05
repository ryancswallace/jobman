import subprocess
import uuid
from pathlib import Path
import time
from dataclasses import dataclass, field
import platform
import hashlib
from typing import Optional, List, Union
import psutil


def get_host_id():
    system_info = platform.uname()
    system_info_str = ";".join([
        system_info.node,
        system_info.system,
        system_info.release,
        system_info.version,
        system_info.machine,
        system_info.processor,
    ])
    host_id = hashlib.sha256(system_info_str.encode()).hexdigest()
    return host_id


@dataclass
class NotificationSink:
    pass


@dataclass
class JobmanConfig:
    db_path: Union[str, Path] = "~/.jobman_db"
    log_path: Union[str, Path] = "~/.jobman_log"
    notification_sinks: List[NotificationSink] = field(default_factory=lambda: [])

    def __post_init__(self):
        self.db_path = Path(self.db_path).expanduser()
        self.log_path = Path(self.log_path).expanduser()

jobman_config = JobmanConfig()
print(jobman_config)

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
    retry_successes: int = 0
    retry_failures: int = 0
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

    def start(self) -> None:
        print("running", self)


def procs_are_same(proc_1: psutil.Process, proc_2: psutil.Process):
    same_pid = proc_1.pid == proc_2.pid
    same_create_time = proc_1.create_time() == proc_2.create_time()
    return same_pid and same_create_time


class JobRun:
    def __init__(self, command: str, proc: Optional[psutil.Process] = None, pid: Optional[int] = None):
        self.command = command
        self.pid = pid
        self.proc = proc
        
    def start(self):
        if self.proc is not None or self.pid is not None:
            raise RuntimeError("Job has already been run")
        
        popen_ret = subprocess.Popen(self.command, shell=True)
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
        if self.pid is None:
            return False

        try:
            proc = psutil.Process(self.pid)
        except (psutil.NoSuchProcess, psutil.AccessDenied):
            return False
        
        if proc.status() == psutil.STATUS_ZOMBIE:
            return False

        return procs_are_same(self.proc, proc)


job_run = JobRun("true")
job_run.start()
while True:
    print(f"{job_run.is_running()}")
    import time; time.sleep(1)

import sys; sys.exit()

log_root_path = Path("./logs")

job_id = uuid.uuid4()

job_log_path = log_root_path / str(job_id)


for run in range(3):
    run_log_path = job_log_path / str(run)
    run_log_path.mkdir(parents=True)

    out_file_path = run_log_path / "out.txt"
    err_file_path = run_log_path / "err.txt"

    with open(out_file_path, "w") as out_fp, open(err_file_path, "w") as err_fp:
        print(f"{run=}")
        run_proc = subprocess.Popen("sleep 10", shell=True, stdout=out_fp, stderr=err_fp)
        proc_rc = None
        while proc_rc is None:
            proc_rc = run_proc.poll()
            time.sleep(1)