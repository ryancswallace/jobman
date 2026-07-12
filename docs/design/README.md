# Jobman

* CLI
    * `run` (default if no subcommand to `jobman`)
        * description: submit a job
        * args
            * `command` (can be omitted iff (a) provided with job spec containing a command, (b) provided with `rerun-by-name`, or (c) provided with `rerun-by-id`)
        * options
            * `name` (string) (default: "") - use `name` as the display name for job. Defaults to `command`
            * `group` (string) (default: "") - mark job as a member of job group `group`

            * `job-spec` (string) (default: "") -
            * `rerun-by-name` (string) (default: "") - rerun the job named `rerun-by-name`. If multiple jobs with this name exist, choose among them the job with the latest start datetime
            * `rerun-by-id` (string) (default: "") - rerun job with id `rerun-by-id`

            * `stop-signal`

            * `wait.datetime` (timestamp) (default: "") - do not run before `wait.datetime`
            * `wait.timedelta` (timedelta) (default: "") - do not run before `wait.timedelta` after job submitted
            * `wait.file` (string) (default: "") (nonunique) - do not run unless `wait.file` exists
            * `wait.nonopen-file` (string) (default: "") (nonunique) - do not run unless `wait.nonopen-file` exists and is not open by any process
            * `wait.script` (string) (default: "") (nonunique) - do not run unless executable at `wait.script` returns 0
            * `wait.poll-interval` (timedelta) (default: 60s) - delay `wait.poll-interval` between runs of `wait.script`
            * `wait.extra-delay` (timedelta) (default: 0) - delay `wait.extra-delay` after wait condition(s) met to start run
            * `wait.any` (boolean) (default: false) - consider wait conditions met after any individual condition is met. Default (false) requires all conditions to be met
            * `wait.abort-datetime` (datetime) (default: "") - abort job if if first run would start after `wait.abort-datetime`

            * `log.stdout` (boolean) (default: true) - persist logs of job's stdout
            * `log.stderr` (boolean) (default: true) - persist logs of job's stderr
            * `log.prefix-timestamp` (boolean) (default: true) - prefix each stdout/stderr log line with the current timestamp
            * `log.file.max-bytes` (int) (default: "") - max file size for stdout/stderr log before rotating to new file
            * `log.run.max-files` (int) (default: "") - max number of stdout/stderr logs for the run before deleting oldest files
            * `log.job.max-runs` (int) (default: "") - max number of runs to persist before deleting stdout/stderr logs of the oldest runs
            * `log.job.max-bytes` (int) (default: "") - max size of stdout/stderr log files across all jobs and all runs before deleting stdout/stderr logs of the oldest runs

            * `retries.num-runs` (int) (default: 1) - maximum number of runs before aborting. -1 indicates no limit
            * `retries.num-successes` (int) (default: -1) - maximum number of successful runs before aborting. -1 indicates no limit
            * `retries.num-failures` (int) (default: -1) - maximum number of failed runs before aborting. -1 indicates no limit
            * `retries.abort-datetime` (datetime) (default: "") - abort job if next run would start after `retries.abort-datetime`
            * `retries.success-codes` (string) (default: "0") - comma-separated list of exit codes that indicate a successful run
            * `retries.failure-codes` (string) (default: "") - comma-separated list of exit codes that indicate a failed run
            * `retries.delay` (string) (default: `0s`) - delay after finishing run to start next run
            * `retries.jitter-range` (string) (default: `0s`) - add jitter distributed Unif(-`retries.jitter-range` / 2, `retries.jitter-range` / 2) to retry delay. Actual delay is distributed max(0, Unif(`retries.delay` - (`retries.jitter-range` / 2), `retries.delay` + (`retries.jitter-range` / 2)))
            * `retries.exponential-backoff-base` (float) (default: 1) - delay (`retries.delay` * `retries.exponential-backoff-base`^[run#]), where `run#` is indexed from 0, after finishing run `run#` to start next run. Default value of 1 results in a constant delay of `retries.delay`
            * `retries.max-delay` (string) (default: -1) - delay no more than `retries.max-delay`. Useful for capping the delay when using exponential backoff (i.e., `retries.exponential-backoff-base` > 1). Default value of -1 indicates no maximum delay enforced

            * `timeout`
            * `timeout.job`
            * `timeout.run`

            * `notify` (string) (default: "") (nonunique) - callback to invoke on success, failure, or retry
            * `notify.on-success` (string) (default: "") (nonunique) - callback to invoke on success of job
            * `notify.on-retry` (string) (default: "") (nonunique) - callback to invoke on retry of job
            * `notify.on-failure` (string) (default: "") (nonunique) - callback to invoke on failure of job
        * stdout
            * JobID
        * DB
        * returns
            * 0: success
    * `list`
        * description:
        * args
            * `show-runs` (boolean) (default: false)
        * options
        * stdout
        * DB
        * returns
    * `show` - alias for `show job`
    * `show job`
        * description: Displays job status
        * args
        * options
        * stdout
        * DB
        * returns
    * `show run`
        * description: Displays run status
        * args
        * options
        * stdout
        * DB
        * returns
    * `logs`
        * description:
        * args: job name or job ID
        * options
            * `run` (string) (default -1) display logs from specified run number. Negative values index backwards from the last run. Value "all" displays all logs in sequence
            * `-follow`
            * `-lines` (-1 for full)
            * `-streams` (string) (default: "stdout,stderr")
            * `-raw` (boolean) (default: false) Remove jobman-generated line prefixes (e.g., source stream, timestamp)
        * stdout
        * DB
        * returns
    * `kill` - alias for `kill job`
    * `kill job`
        * description:
        * args
        * options
        * stdout
        * DB
        * returns
    * `kill run`
        * description:
        * args
        * options
        * stdout
        * DB
        * returns
    * `clean`
        * description: deletes records and/or stdout/stderr logs from completed jobs
        * args
        * options
            * `-dry-run`
        * stdout
        * DB
        * returns
    * `config`
        * description:
        * args
        * options
        * stdout
        * DB
        * returns
    * `help`
    * common options
        * `-log-level`
        * `-verbose`
        * `-quiet`
        * `-json`
        * `-config-file`
* config
    * how
        * command line
        * environment variables
        * file
            * format: all supported by Viper
            * paths
                * `.jobman.*`
                * `~/.jobman.*`
                * `/etc/jobman/jobman.*`
            * levels
                * job name
                * job group
                * regular expression matching command
                * global (all jobs)
    * what
        * `CLI:*:parameters`
        * `job_spec` section to define job specifications (containing arguments to `run`)
        * `sensors` section to define named sensors
        * `callbacks` section to define named callbacks
        * `reaper` section to configure job-level log reaping
            * `reaper.max_jobs` (int) (default: "") - delete oldest logs to stay under `reaper.max_jobs`
            * `reaper.max_age` (timedelta) (default: "") - delete logs older than `reaper.max_age`
* sensors (delay)
    * types
        * datetime: wait until datetime `dt`
        * timedelta: wait `s` seconds
        * file: wait `s` seconds after after file(s) to exist
        * non-open file: wait `s` seconds after after file(s) to exist and not open
        * script: wait `s` seconds after script(s) to return 0
        * [HTTP: wait `s` seconds after URL(s) to exist]
        * [S3: wait `s` seconds after after S3 bucket(s)/prefix(s)/key(s) to exist]
    * constraints
        * not after: if first run would be after datetime `dt`, abort
    * combinations
        * configurable whether sensors are `or`-ed or `and`-ed
* retries
    * how
        * rerun indefinitely
        * rerun `n` times
        * rerun until `n` failures
        * rerun until `n` failures, with max `N` reruns
        * rerun until `n` successes
        * rerun until `n` successes, with max `N` reruns
        * rerun until start time would be greater than datetime `dt`
        * rerun until start time would be greater than datetime `dt`, with max `N` reruns
    * configurations
        * success exit code(s) (default `0`)
        * error exit code(s) (default non-`0`)
        * delay between reruns
            * delay `s` seconds (default `s=0`)
            * exponential backoff (exponentially increasing delays of `s` * `b`^[rerun# - 1]) (default=false)
            * max retry delay (default=max(`retry_delay`, 600s))
* logging
    * where
        * to DB
    * jobman logs
        * logs about the operation of jobman itself
    * output logs
        * split run log into numbered files `log.<n>`
        * combined stdout and stderr, each line prefixed with source stream
        * optionally prefix line by timestamp
    * reaping
        * based on
            * size
            * age
            * number of jobs
        * level
            * job (delete oldest runs in current job)
            * run (delete start of logs in current run)
* callbacks
    * on
        * job/run success
        * retry
        * job/run failure
    * built-in
        * alerts
            * modes
                * email
                * text (SMS)
                * slack
                * write to command line
        * script
* DB
    * requirements
        * atomicity (account for user killing jobman)
        * consistency/correctness
        * isolation/concurrency (account for simultaneous requests)
    * implementations
        * file
            * default path `~/.jobman/.db/`
            * `.db/`
                * `data/`
                    * `version` (version of DB schema)
                    * `jobs/`
                        * `<UUID[:2]>/`
                            * `<UUID[2:]>/`
                                * `version` (version of job schema)
                                * `spec` (full job spec in config file format, including command)
                                * `state`: (1) `status` (pending, running, completed); (2) `result` (null, success, failure, killed); (3) `times` (start time and stop time)
                                * `jobman.log`
                                * `runs/`
                                    * `version` (version of run schema)
                                    * `<count>/`
                                        * `state`: (1) `status` (pending, running, completed); (2) `ret_code`; (3) `times` (start time and stop time)
                                        * `jobman.log`
                                        * `logs/`
                                            * `log.<n>`
        * document database (e.g., MongoDB, DynamoDB)
* implementation
    * UUID generated for job
    * heartbeat writing to latest run in DB to indicate liveness
    * `jobman run` command returns once job is `pending`
    * backgrounding
        * handle stream redirects
        * intercept signals (e.g., `HUP`)
    * shell completions
* best practices
    * updates
        * coordinate Go and jobman version number (via VERSION?) (`grep -r -I '1\.15`)
        * copyright date
            ```sh
            START_YEAR=2021
            CURRENT_YEAR=$(date '+%Y')
            grep -rl --exclude-dir=.git "© $START_YEAR-" . | xargs sed -i "s/© $START_YEAR-[0-9]\{4\}/© $START_YEAR-$CURRENT_YEAR/g"
            ```
    * files
        * readme
        * contributing
        * release
        * license
        * changelog
        * makefile
        * dockerfile
        * .github/ISSUE_TEMPLATE/
    * man pages
    * inline comments
    * webpage
        * link to download release
    * social preview image
    * testing
        * unit tests
        * end-to-end tests
        * performance tests
    * repo
        * tags
        * description
        * sponsor
        * logo
        * badges
            * https://goreportcard.com/
            * https://bestpractices.coreinfrastructure.org/
    * CI (GH actions)
        * tests
        * linting
            * go vet
            * go fmt
            * ineffassign100
            * misspell
        * how to ensure hooks/* run by CI
        * deploy docs site
        * publish to Docker Hub, GH packages
        * releases (GoReleaser)
            * assets
                * (linux|windows|darwin|freebsd).(amd64|i386).tar.gz
                * source.tar.gz
                * .rpm
                * .deb
            * Full Changelog: v4.6.1...v4.6.2
* doc site
    * installing
        * Homebrew
            ```
            brew tap ryancswallace/jobman https://github.com/ryancswallace/jobman
            brew install jobman
            ```



Conventional commit messages (CONTRIBUTING.md)
Draft release from push/merge to main
Require reviewers
Codeql
Automated method to bump jobman version
Combine or make dep between build and release actions
Twitter


* `.db/`
    * `index/`
    * `data/`
        * `job`
            * `<job-id>`
                * `spec` (full job spec in config file format, including command)
                * `state`: (1) `status` (pending, running, completed); (2) `result` (null, success, failure, neutral, killed); (3) `times` (start time and stop time)
                * `jobman.log`
                * `run`
                    * `<run-id>`
                        * `state`: (1) `status` (pending, running, completed); (2) `ret_code`; (3) `times` (start time and stop time)
                        * `jobman.log`
                        * `logs/`
                            * `log.<n>`


db.get() - [{job=1, children={spec=., state=., children=[{run=0, state=., }]}} ]



db.get(k1=v1, k2=v2, name)
db.getAll(k1=v1, k2=v2)

db.set(k1=v1, k2=v2, name, value="")

db.delete(k1=v1, k2=v2)
db.delete(all_k1=v1, k2=v2, name)

db.updateKey()
