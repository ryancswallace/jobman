Subcommands
# TODO
* help

* move all IO to cli.py (e.g., print job id)

* GLOBAL OPTIONS
    quiet: bool = False
    verbose: bool = False
    no-color: bool = False
    json: bool = False

* run (implied)
    command: str

    wait_time: str = ""
    wait_duration: str = ""
    wait_for_file: List[str] = field(default_factory=lambda: [])

    abort_time: str = ""
    abort_duration: str = ""
    abort_for_file: List[str] = field(default_factory=lambda: [])

    retry_attempts: int = 0
    retry_delay: str = ""
    retry_success_codes: List[int] = field(default_factory=lambda: [0])

    notify_on_run_completion: List[str] = field(default_factory=lambda: [])
    notify_on_job_completion: List[str] = field(default_factory=lambda: [])
    notify_on_job_success: List[str] = field(default_factory=lambda: [])
    notify_on_run_success: List[str] = field(default_factory=lambda: [])
    notify_on_job_failure: List[str] = field(default_factory=lambda: [])
    notify_on_run_failure: List[str] = field(default_factory=lambda: [])

    follow_logs: bool = False

* status
    job_id: str

* logs
    job_id: str

    stdout: bool = True
    stderr: bool = True
    follow: bool = False
    no_log_prefix: bool = False
    tail (-n): int = None
    since: datetime = None
    until: datetime = None

* kill
    job_id: str

    signal: Union[str, int] = SIGTERM
    allow_retries: bool = False

    force: bool = False

* ls
    all: bool = False

* purge
    job_id: str = None
    all: bool = False

    logs: bool = True
    metadata: bool = False

    since: datetime = None
    until: datetime = None

    force: bool = False

* reset
    force: bool = False