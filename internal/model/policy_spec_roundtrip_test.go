package model

import (
	"bytes"
	"testing"
	"time"

	"github.com/ryancswallace/jobman/internal/policy"
)

func TestExecutionPolicyFullCanonicalRoundTrip(t *testing.T) {
	t.Parallel()

	retryAbortAt := testTime.Add(24 * time.Hour)
	probeExecutable := testAbsolutePath("usr", "bin", "check")
	secretPath := testAbsolutePath("run", "secrets", "database-password")
	configuration := ExecutionPolicy{
		Completion: policy.CompletionPolicy{
			MaxRuns:         policy.Limit{Value: 5},
			SuccessTarget:   policy.Limit{Value: 2},
			FailureLimit:    policy.UnlimitedLimit(),
			RetryAbortAt:    retryAbortAt,
			HasRetryAbortAt: true,
		},
		Classification: policy.ClassificationPolicy{
			SuccessExitCodes:         []int{0, 2},
			RetryableExitCodes:       []policy.ExitCodeRange{{First: 3, Last: 5}},
			RetryableSignals:         []string{"terminated"},
			RetryablePlatformReasons: []string{"exit_status_unavailable"},
			RetryTimeout:             true,
			RetryStartFailure:        true,
			RetryCancellation:        true,
		},
		FailureDelay: policy.DelayPolicy{
			Base: 2 * time.Second, Backoff: policy.BackoffLinear,
			MaxDelay: time.Minute, HasMaxDelay: true, Jitter: time.Second,
		},
		SuccessDelay: policy.DelayPolicy{
			Base: time.Second, Backoff: policy.BackoffExponential, ExponentialBase: 2,
		},
		RunTimeout: 10 * time.Second,
		JobTimeout: time.Hour,
		WaitMode:   policy.WaitModeAny,
		WaitConditions: []WaitCondition{
			{Kind: WaitUntil, Until: testTime.Add(time.Hour), PollInterval: time.Second},
			{Kind: WaitDelay, Delay: 3 * time.Second, PollInterval: 2 * time.Second},
			{
				Kind: WaitFileExists, Path: testAbsolutePath("tmp", "ready"),
				FileKind: policy.FileKindRegular, PollInterval: 3 * time.Second,
				AbortAt: testTime.Add(2 * time.Hour),
			},
			{
				Kind: WaitProbe,
				Probe: policy.ProbeSpec{
					Executable: probeExecutable, Arguments: []string{"--ready"},
					Timeout: 4 * time.Second, OutputLimit: 4096, FatalOnError: true,
				},
				ProbeDirectory:        testAbsolutePath("var", "empty"),
				ProbeEnvironment:      map[string]string{"MODE": "ready"},
				ProbeUnsetEnvironment: []string{"REMOVE"},
				ProbeSecretEnv: map[string]SecretReference{
					"TOKEN": {Provider: "env", Name: "JOBMAN_PROBE_TOKEN"},
				},
				PollInterval: 4 * time.Second,
			},
		},
		Dependencies:        []DependencyRequirement{{JobID: testJobID, Predicate: "success"}},
		Concurrency:         ConcurrencyPolicy{Pool: "build", Slots: 2},
		Notifications:       []NotificationSubscription{},
		NotifierDefinitions: []NotifierDefinition{},
		Tags:                []string{"critical", "nightly"},
		Groups:              []string{"operations"},
		SecretEnv: map[string]SecretReference{
			"DATABASE_PASSWORD": {Provider: "file", Name: secretPath},
		},
		StdinPath:               testAbsolutePath("tmp", "jobman-input"),
		LogRotateSize:           1024,
		LogMaxSegmentsPerStream: 3,
		LogCapture:              "stdout",
		LogRetentionMaxAge:      48 * time.Hour,
		LogRetentionConfigured:  true,
	}
	specification, err := NewJobSpec(JobSpecInput{
		Executable: testAbsolutePath("bin", "echo"), WorkingDirectory: t.TempDir(),
		StdinPolicy: StdinFile, ExecutionPolicy: configuration,
	})
	if err != nil {
		t.Fatalf("NewJobSpec() error = %v", err)
	}
	encoded, err := specification.CanonicalJSON()
	if err != nil {
		t.Fatalf("CanonicalJSON() error = %v", err)
	}
	parsed, err := ParseJobSpecJSON(encoded)
	if err != nil {
		t.Fatalf("ParseJobSpecJSON() error = %v\nJSON: %s", err, encoded)
	}
	reencoded, err := parsed.CanonicalJSON()
	if err != nil {
		t.Fatalf("parsed CanonicalJSON() error = %v", err)
	}
	if !bytes.Equal(encoded, reencoded) {
		t.Fatalf("canonical round trip changed JSON\nfirst: %s\nnext:  %s", encoded, reencoded)
	}
	got := parsed.ExecutionPolicy()
	if got.Concurrency != configuration.Concurrency || len(got.WaitConditions) != 4 ||
		got.WaitConditions[3].Probe.Executable != probeExecutable ||
		got.SecretEnv["DATABASE_PASSWORD"] != configuration.SecretEnv["DATABASE_PASSWORD"] {
		t.Fatalf("execution policy round trip lost populated fields: %#v", got)
	}
}

func TestWaitConditionValidationVariants(t *testing.T) {
	t.Parallel()

	valid := []WaitCondition{
		{Kind: WaitUntil, Until: testTime, PollInterval: time.Second},
		{Kind: WaitDelay, PollInterval: time.Second},
		{Kind: WaitFileExists, Path: testAbsolutePath("tmp", "ready"), FileKind: policy.FileKindSymlink, PollInterval: time.Second},
		{
			Kind:         WaitProbe,
			Probe:        policy.ProbeSpec{Executable: "true", Timeout: time.Second, OutputLimit: 1},
			PollInterval: time.Second,
		},
	}
	for _, condition := range valid {
		if err := condition.Validate(); err != nil {
			t.Errorf("Validate(%s) error = %v", condition.Kind, err)
		}
	}
	for _, condition := range []WaitCondition{
		{Kind: WaitUntil, PollInterval: time.Second},
		{Kind: WaitDelay, Delay: -1, PollInterval: time.Second},
		{Kind: WaitFileExists, Path: "", PollInterval: time.Second},
		{Kind: WaitFileExists, Path: testAbsolutePath("tmp", "x"), FileKind: policy.FileKind("unknown"), PollInterval: time.Second},
		{Kind: WaitConditionKind("unknown"), PollInterval: time.Second},
		{Kind: WaitDelay, PollInterval: 0},
		{Kind: WaitDelay, PollInterval: time.Second, AbortAt: time.Unix(-1, 0)},
	} {
		if err := condition.Validate(); err == nil {
			t.Errorf("Validate(%#v) error = nil", condition)
		}
	}
}

func TestExecutionPolicyValidationEdges(t *testing.T) {
	t.Parallel()

	base := DefaultExecutionPolicy()
	validFilePolicy := func() ExecutionPolicy {
		value := base
		value.StdinPath = testAbsolutePath("tmp", "input")

		return value
	}
	tests := []struct {
		name   string
		stdin  StdinPolicy
		mutate func(*ExecutionPolicy)
	}{
		{name: "completion", mutate: func(value *ExecutionPolicy) { value.Completion.MaxRuns = policy.Limit{} }},
		{name: "classification", mutate: func(value *ExecutionPolicy) { value.Classification.SuccessExitCodes = []int{-1} }},
		{name: "failure delay", mutate: func(value *ExecutionPolicy) { value.FailureDelay.Base = -1 }},
		{name: "success delay", mutate: func(value *ExecutionPolicy) { value.SuccessDelay.Jitter = -1 }},
		{name: "timeout", mutate: func(value *ExecutionPolicy) { value.RunTimeout = -1 }},
		{name: "wait mode", mutate: func(value *ExecutionPolicy) { value.WaitMode = policy.WaitMode("unknown") }},
		{name: "wait condition", mutate: func(value *ExecutionPolicy) {
			value.WaitConditions = []WaitCondition{{Kind: WaitDelay}}
		}},
		{name: "dependency ID", mutate: func(value *ExecutionPolicy) {
			value.Dependencies = []DependencyRequirement{{JobID: "invalid", Predicate: "success"}}
		}},
		{name: "duplicate dependency", mutate: func(value *ExecutionPolicy) {
			dependency := DependencyRequirement{JobID: testJobID, Predicate: "success"}
			value.Dependencies = []DependencyRequirement{dependency, dependency}
		}},
		{name: "pool whitespace", mutate: func(value *ExecutionPolicy) { value.Concurrency.Pool = " pool " }},
		{name: "zero slots", mutate: func(value *ExecutionPolicy) { value.Concurrency.Slots = 0 }},
		{name: "foreground live", stdin: StdinLive, mutate: func(value *ExecutionPolicy) { value.Foreground = true }},
		{name: "file path missing", stdin: StdinFile},
		{name: "path for null", mutate: func(value *ExecutionPolicy) { value.StdinPath = testAbsolutePath("tmp", "input") }},
		{name: "background inherit", stdin: StdinInherit},
		{name: "negative rotation", mutate: func(value *ExecutionPolicy) { value.LogRotateSize = -1 }},
		{name: "too many segments", mutate: func(value *ExecutionPolicy) { value.LogMaxSegmentsPerStream = 1 << 16 }},
		{name: "capture", mutate: func(value *ExecutionPolicy) { value.LogCapture = "unknown" }},
		{name: "retention negative", mutate: func(value *ExecutionPolicy) { value.LogRetentionMaxAge = -1 }},
		{name: "retention ambiguous", mutate: func(value *ExecutionPolicy) { value.LogRetentionUnlimited = true }},
		{name: "invalid tag", mutate: func(value *ExecutionPolicy) { value.Tags = []string{" bad "} }},
		{name: "duplicate tag", mutate: func(value *ExecutionPolicy) { value.Tags = []string{"tag", "tag"} }},
		{name: "invalid group", mutate: func(value *ExecutionPolicy) { value.Groups = []string{""} }},
		{name: "secret environment", mutate: func(value *ExecutionPolicy) {
			value.SecretEnv = map[string]SecretReference{"BAD=NAME": {Provider: "env", Name: "TOKEN"}}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			value := base
			if test.stdin == StdinFile {
				value = validFilePolicy()
				value.StdinPath = ""
			}
			if test.mutate != nil {
				test.mutate(&value)
			}
			if err := value.Validate(test.stdin); err == nil {
				t.Fatal("Validate() error = nil")
			}
		})
	}

	foreground := base
	foreground.Foreground = true
	if err := foreground.Validate(StdinInherit); err != nil {
		t.Fatalf("Validate(foreground inherit) error = %v", err)
	}
	filePolicy := validFilePolicy()
	if err := filePolicy.Validate(StdinFile); err != nil {
		t.Fatalf("Validate(file stdin) error = %v", err)
	}
}

func TestProbeWaitValidationEdges(t *testing.T) {
	t.Parallel()

	valid := WaitCondition{
		Kind:         WaitProbe,
		Probe:        policy.ProbeSpec{Executable: "true", Timeout: time.Second, OutputLimit: 1},
		PollInterval: time.Second,
	}
	for _, mutate := range []func(*WaitCondition){
		func(value *WaitCondition) { value.Probe.Executable = "" },
		func(value *WaitCondition) { value.ProbeDirectory = "relative" },
		func(value *WaitCondition) { value.ProbeEnvironment = map[string]string{"BAD=NAME": "x"} },
		func(value *WaitCondition) { value.ProbeEnvironment = map[string]string{"OK": "x\x00"} },
		func(value *WaitCondition) { value.ProbeUnsetEnvironment = []string{"BAD=NAME"} },
		func(value *WaitCondition) {
			value.ProbeSecretEnv = map[string]SecretReference{"TOKEN": {Provider: "", Name: "TOKEN"}}
		},
	} {
		condition := valid
		mutate(&condition)
		if err := condition.Validate(); err == nil {
			t.Fatal("Validate(invalid probe condition) error = nil")
		}
	}
}

func TestExecutionPolicyWireRejectsMalformedFields(t *testing.T) {
	t.Parallel()

	specification, err := NewJobSpec(JobSpecInput{
		Executable: testAbsolutePath("bin", "true"), WorkingDirectory: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("NewJobSpec() error = %v", err)
	}
	canonical, err := specification.CanonicalJSON()
	if err != nil {
		t.Fatalf("CanonicalJSON() error = %v", err)
	}
	tests := []struct {
		name string
		old  string
		new  string
	}{
		{name: "run timeout", old: `"run_timeout":"0s"`, new: `"run_timeout":"bad"`},
		{name: "job timeout", old: `"job_timeout":"0s"`, new: `"job_timeout":"bad"`},
		{name: "retention", old: `"log_retention_max_age":"720h0m0s"`, new: `"log_retention_max_age":"bad"`},
		{name: "failure delay", old: `"failure_delay":{"base":"0s"`, new: `"failure_delay":{"base":"bad"`},
		{name: "success delay", old: `"success_delay":{"base":"0s"`, new: `"success_delay":{"base":"bad"`},
		{
			name: "failure maximum delay",
			old:  `"failure_delay":{"base":"0s","backoff":"constant","exponential_base":0,"max_delay":null`,
			new:  `"failure_delay":{"base":"0s","backoff":"constant","exponential_base":0,"max_delay":"bad"`,
		},
		{
			name: "success jitter",
			old:  `"success_delay":{"base":"0s","backoff":"constant","exponential_base":0,"max_delay":null,"jitter":"0s"`,
			new:  `"success_delay":{"base":"0s","backoff":"constant","exponential_base":0,"max_delay":null,"jitter":"bad"`,
		},
		{
			name: "retry abort timestamp",
			old:  `"failure_limit":{"value":1,"unlimited":false}`,
			new:  `"failure_limit":{"value":1,"unlimited":false},"retry_abort_at":"bad"`,
		},
		{
			name: "dependency ID",
			old:  `"dependencies":[]`,
			new:  `"dependencies":[{"job_id":"bad","predicate":"success"}]`,
		},
		{
			name: "wait delay",
			old:  `"wait_conditions":[]`,
			new:  `"wait_conditions":[{"kind":"delay","delay":"bad","poll_interval":"1s"}]`,
		},
		{
			name: "wait until",
			old:  `"wait_conditions":[]`,
			new:  `"wait_conditions":[{"kind":"until","until":"bad","poll_interval":"1s"}]`,
		},
		{
			name: "wait poll interval",
			old:  `"wait_conditions":[]`,
			new:  `"wait_conditions":[{"kind":"delay","delay":"1s","poll_interval":"bad"}]`,
		},
		{
			name: "wait abort timestamp",
			old:  `"wait_conditions":[]`,
			new:  `"wait_conditions":[{"kind":"delay","delay":"1s","poll_interval":"1s","abort_at":"bad"}]`,
		},
		{
			name: "probe timeout",
			old:  `"wait_conditions":[]`,
			new: `"wait_conditions":[{"kind":"probe","poll_interval":"1s","probe":` +
				`{"executable":"true","arguments":[],"working_directory":"","environment":{},` +
				`"unset_environment":[],"secret_environment":{},"timeout":"bad","output_limit":1,"fatal_on_error":false}}]`,
		},
		{
			name: "notifier timeout",
			old:  `"notifier_definitions":[]`,
			new:  `"notifier_definitions":[{"name":"hook","kind":"command","timeout":"bad"}]`,
		},
		{
			name: "notifier retry delay",
			old:  `"notifier_definitions":[]`,
			new: `"notifier_definitions":[{"name":"hook","kind":"command","timeout":"1s",` +
				`"retry":{"max_attempts":1,"delay":"bad","max_delay":"1s"}}]`,
		},
		{
			name: "notifier retry maximum delay",
			old:  `"notifier_definitions":[]`,
			new: `"notifier_definitions":[{"name":"hook","kind":"command","timeout":"1s",` +
				`"retry":{"max_attempts":1,"delay":"1s","max_delay":"bad"}}]`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			changed := bytes.Replace(canonical, []byte(test.old), []byte(test.new), 1)
			if bytes.Equal(changed, canonical) {
				t.Fatalf("fixture did not contain %q\n%s", test.old, canonical)
			}
			if _, err := ParseJobSpecJSON(changed); err == nil {
				t.Fatal("ParseJobSpecJSON() error = nil")
			}
		})
	}
}

func TestJobSpecStopPolicyGetter(t *testing.T) {
	t.Parallel()

	specification := validSpec(t)
	if got := specification.StopPolicy(); got.GracePeriod != 5*time.Second || !got.ForceAfterGrace {
		t.Fatalf("StopPolicy() = %#v", got)
	}
}
