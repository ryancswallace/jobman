package config

import (
	"strings"
	"testing"
	"time"
)

func TestCollectionAndPrimitiveValidationEdges(t *testing.T) {
	t.Parallel()

	if err := validateTrustedRoots([]string{"/tmp/root", "/tmp/root"}); err == nil {
		t.Fatal("validateTrustedRoots(duplicate) error = nil")
	}
	if err := validateNamedLimits(map[string]SlotLimit{"bad name": UnlimitedSlotLimit()}); err == nil {
		t.Fatal("validateNamedLimits(invalid name) error = nil")
	}
	if err := validateNamedLimits(map[string]SlotLimit{"pool": {}}); err == nil {
		t.Fatal("validateNamedLimits(unset capacity) error = nil")
	}
	if err := validateRetention(Retention{}); err == nil {
		t.Fatal("validateRetention(unset) error = nil")
	}
	tooManySecrets := make(map[string]SecretRef, maxConfiguredSecrets+1)
	for index := range maxConfiguredSecrets + 1 {
		tooManySecrets[strings.Repeat("x", index+1)] = SecretRef{provider: "env", locator: "TOKEN"}
	}
	if err := validateNamedSecrets(tooManySecrets); err == nil {
		t.Fatal("validateNamedSecrets(too many) error = nil")
	}
	if err := validateNamedSecrets(map[string]SecretRef{"bad name": {provider: "env", locator: "TOKEN"}}); err == nil {
		t.Fatal("validateNamedSecrets(invalid name) error = nil")
	}
	if err := validateNamedSecrets(map[string]SecretRef{"secret": {}}); err == nil {
		t.Fatal("validateNamedSecrets(empty reference) error = nil")
	}

	for _, condition := range []WaitCondition{
		{Type: "until"},
		{Type: "until", Until: "bad"},
		{Type: "delay"},
		{Type: "file-exists"},
		{Type: "file-exists", FileExists: &FileCondition{}},
		{Type: "file-exists", FileExists: &FileCondition{Path: "/tmp/x", Type: "unknown"}},
		{Type: "probe"},
		{Type: "unknown"},
	} {
		if err := validateWaitCondition(condition, nil); err == nil {
			t.Errorf("validateWaitCondition(%#v) error = nil", condition)
		}
	}
}

func TestProbeValidationEdges(t *testing.T) {
	t.Parallel()

	valid := ProbeCondition{
		Command: []string{"true"}, Timeout: durationMust(time.Second),
		PollInterval: durationMust(time.Second), OutputLimit: byteLimit(1),
	}
	for _, mutate := range []func(*ProbeCondition){
		func(value *ProbeCondition) { value.Command = nil },
		func(value *ProbeCondition) { value.WorkingDirectory = "relative" },
		func(value *ProbeCondition) { value.Environment.Unset = []string{"BAD=NAME"} },
		func(value *ProbeCondition) { value.Timeout = durationMust(0) },
		func(value *ProbeCondition) { value.PollInterval = durationMust(0) },
		func(value *ProbeCondition) { value.OutputLimit = UnlimitedByteLimit() },
	} {
		probe := valid
		mutate(&probe)
		if err := validateProbe(probe, nil); err == nil {
			t.Fatal("validateProbe(invalid) error = nil")
		}
	}
}

func TestNotifierValidationEdges(t *testing.T) {
	t.Parallel()

	validHTTP := func() Notifier {
		value := baseHTTPNotifier()
		value.Events = []string{"job_failed"}

		return value
	}
	for _, mutate := range []func(*Notifier){
		func(value *Notifier) { value.Timeout = Duration{} },
		func(value *Notifier) { value.Retry.MaxAttempts = 0 },
		func(value *Notifier) { value.Retry.MaxDelay = durationMust(0) },
		func(value *Notifier) { value.Events = []string{"unknown"} },
		func(value *Notifier) { value.HTTP = nil },
		func(value *Notifier) { value.Command = &CommandNotifier{} },
		func(value *Notifier) { value.Type = "unknown" },
	} {
		notifier := validHTTP()
		mutate(&notifier)
		if err := validateNotifier(notifier, nil); err == nil {
			t.Fatal("validateNotifier(invalid) error = nil")
		}
	}

	validCommand := CommandNotifier{
		Command: []string{"true"}, OutputLimit: byteLimit(1),
	}
	if err := validateCommandNotifier(validCommand, nil); err != nil {
		t.Fatalf("validateCommandNotifier(valid) error = %v", err)
	}
	validCommand.OutputLimit = UnlimitedByteLimit()
	if err := validateCommandNotifier(validCommand, nil); err == nil {
		t.Fatal("validateCommandNotifier(unlimited output) error = nil")
	}

	secret := SecretRef{provider: "env", locator: "TOKEN"}
	secrets := map[string]SecretRef{"token": secret}
	for _, mutate := range []func(*HTTPNotifier){
		func(value *HTTPNotifier) { value.Headers = map[string]string{"X-Test": "bad\x00value"} },
		func(value *HTTPNotifier) { value.SecretHeaders = map[string]string{"Bad Header": "token"} },
		func(value *HTTPNotifier) { value.SecretHeaders = map[string]string{"X-Token": "missing"} },
		func(value *HTTPNotifier) { value.SigningSecret = "missing" },
	} {
		notifier := baseHTTPNotifier().HTTP
		mutate(notifier)
		if err := validateHTTPNotifier(*notifier, secrets); err == nil {
			t.Fatal("validateHTTPNotifier(invalid) error = nil")
		}
	}

	validSMTP := SMTPNotifier{
		Address: "smtp.example.test:587", TLS: "starttls", From: "jobman@example.test",
		To: []string{"ops@example.test"},
	}
	for _, mutate := range []func(*SMTPNotifier){
		func(value *SMTPNotifier) { value.Address = "invalid" },
		func(value *SMTPNotifier) { value.Address = ":0" },
		func(value *SMTPNotifier) { value.TLS = "plain" },
		func(value *SMTPNotifier) { value.From = "invalid" },
		func(value *SMTPNotifier) { value.To = nil },
		func(value *SMTPNotifier) { value.To = []string{"invalid"} },
		func(value *SMTPNotifier) { value.PasswordSecret = "missing" },
		func(value *SMTPNotifier) { value.Username = "user" },
	} {
		notifier := validSMTP
		mutate(&notifier)
		if err := validateSMTPNotifier(notifier, secrets); err == nil {
			t.Fatal("validateSMTPNotifier(invalid) error = nil")
		}
	}
}

func TestJobPolicyValidationEdges(t *testing.T) {
	t.Parallel()

	configuration := Default()
	for _, mutate := range []func(*JobSpec){
		func(value *JobSpec) { value.Command = nil },
		func(value *JobSpec) { value.Name = "\n" },
		func(value *JobSpec) { value.Tags = []string{"tag", "tag"} },
		func(value *JobSpec) { value.Groups = []string{"bad name"} },
		func(value *JobSpec) { value.WorkingDirectory = "relative" },
		func(value *JobSpec) { value.Environment.Set = map[string]string{"BAD=NAME": "x"} },
		func(value *JobSpec) { value.Dependencies = []Dependency{{}} },
		func(value *JobSpec) { value.Stdin = "inherit" },
		func(value *JobSpec) { value.Stop.GracePeriod = Duration{} },
		func(value *JobSpec) { value.Wait.Mode = "unknown" },
		func(value *JobSpec) { value.Wait.AbortAt = "bad" },
		func(value *JobSpec) { value.Admission.Slots = 0 },
		func(value *JobSpec) { value.Completion.MaxRuns = IntegerLimit{} },
		func(value *JobSpec) { value.Delay.Strategy = "unknown" },
		func(value *JobSpec) { value.Timeouts.Run = DurationLimit{} },
		func(value *JobSpec) { value.Logging.Capture = "unknown" },
		func(value *JobSpec) { value.Notification.Events = []string{"unknown"} },
	} {
		specification := baseJobSpec()
		mutate(&specification)
		if err := validateJobSpec(specification, configuration); err == nil {
			t.Fatal("validateJobSpec(invalid) error = nil")
		}
	}
}

func TestCompletionDelayLoggingEnvironmentAndDependencyEdges(t *testing.T) {
	t.Parallel()

	baseCompletion := baseJobSpec().Completion
	for _, mutate := range []func(*CompletionPolicy){
		func(value *CompletionPolicy) { value.MaxRuns = integerLimit(0) },
		func(value *CompletionPolicy) { value.MaxFailures = integerLimit(0) },
		func(value *CompletionPolicy) { value.SuccessTarget = integerLimit(0) },
		func(value *CompletionPolicy) { value.SuccessTarget = integerLimit(2) },
		func(value *CompletionPolicy) { value.SuccessExitCodes = []int{-1} },
		func(value *CompletionPolicy) { value.RetryableExitCodes = []int{256} },
		func(value *CompletionPolicy) { value.SuccessExitCodes = []int{0, 0} },
		func(value *CompletionPolicy) { value.RetryableExitCodes = []int{1, 1} },
	} {
		value := baseCompletion
		mutate(&value)
		if err := validateCompletion(value); err == nil {
			t.Fatal("validateCompletion(invalid) error = nil")
		}
	}

	baseDelay := baseJobSpec().Delay
	for _, mutate := range []func(*DelayPolicy){
		func(value *DelayPolicy) { value.Initial = Duration{} },
		func(value *DelayPolicy) {
			value.Initial = durationMust(time.Second)
			value.MaxDelay = DurationLimit{value: 0, set: true}
		},
		func(value *DelayPolicy) { value.Base = 1.5 },
	} {
		value := baseDelay
		mutate(&value)
		if err := validateDelay(value); err == nil {
			t.Fatal("validateDelay(invalid) error = nil")
		}
	}

	baseLogging := baseJobSpec().Logging
	for _, mutate := range []func(*LoggingPolicy){
		func(value *LoggingPolicy) { value.SegmentBytes = ByteLimit{} },
		func(value *LoggingPolicy) { value.SegmentsPerRun = integerLimit(0) },
		func(value *LoggingPolicy) {
			value.SegmentBytes = byteLimit(0)
			value.SegmentsPerRun = integerLimit(1)
		},
	} {
		value := baseLogging
		mutate(&value)
		if err := validateLogging(value); err == nil {
			t.Fatal("validateLogging(invalid) error = nil")
		}
	}

	for _, environment := range []Environment{
		{Unset: []string{"BAD=NAME"}},
		{Unset: []string{"NAME", "NAME"}},
		{Unset: []string{"NAME"}, Set: map[string]string{"NAME": "x"}},
		{Set: map[string]string{"NAME": "x\x00"}},
		{Secrets: map[string]string{"BAD=NAME": "secret"}},
		{Unset: []string{"NAME"}, Secrets: map[string]string{"NAME": "secret"}},
		{Set: map[string]string{"NAME": "x"}, Secrets: map[string]string{"NAME": "secret"}},
		{Secrets: map[string]string{"NAME": "missing"}},
	} {
		if err := validateEnvironment(environment, nil); err == nil {
			t.Fatalf("validateEnvironment(%#v) error = nil", environment)
		}
	}

	for _, dependencies := range [][]Dependency{
		{{Job: " "}},
		{{Job: "job"}},
		{{Job: "job", Outcomes: []string{"unknown"}}},
		{{Job: "job", Outcomes: []string{"success", "success"}}},
	} {
		if err := validateDependencies(dependencies); err == nil {
			t.Fatalf("validateDependencies(%#v) error = nil", dependencies)
		}
	}
}

func TestProfileOverrideValidationEdges(t *testing.T) {
	t.Parallel()

	configuration := Default()
	configuration.WaitConditions["ready"] = WaitCondition{Type: "delay", Delay: durationMust(0)}
	configuration.Notifiers["hook"] = baseHTTPNotifier()
	for _, override := range []JobSpecOverride{
		{Command: []string{}},
		{Name: stringPointer("\n")},
		{Tags: []string{"tag", "tag"}},
		{Groups: []string{"bad name"}},
		{WorkingDirectory: stringPointer("relative")},
		{Environment: &Environment{Unset: []string{"BAD=NAME"}}},
		{Stdin: stringPointer("inherit")},
		{Dependencies: []Dependency{{}}},
		{Wait: &WaitPolicy{Mode: "unknown"}},
		{Wait: &WaitPolicy{Conditions: []string{"missing"}}},
		{Admission: &Admission{Pool: "missing", Slots: 1}},
		{Notification: &NotificationPolicy{Notifiers: []string{"missing"}}},
		{Notification: &NotificationPolicy{Events: []string{"unknown"}}},
	} {
		if err := validateOverride(override, configuration); err == nil {
			t.Fatalf("validateOverride(%#v) error = nil", override)
		}
	}

	validName := "job"
	validDirectory := t.TempDir()
	stdin := stdinNull
	valid := JobSpecOverride{
		Command:          []string{"true"},
		Name:             &validName,
		Tags:             []string{"tag"},
		Groups:           []string{"group"},
		WorkingDirectory: &validDirectory,
		Environment:      &Environment{},
		Stdin:            &stdin,
		Dependencies:     []Dependency{{Job: "prior", Outcomes: []string{"success"}}},
		Wait:             &WaitPolicy{Mode: waitModeAll, Conditions: []string{"ready"}},
		Admission:        &Admission{Slots: 1},
		Notification:     &NotificationPolicy{Notifiers: []string{"hook"}, Events: []string{"job_failed"}},
	}
	if err := validateOverride(valid, configuration); err != nil {
		t.Fatalf("validateOverride(valid) error = %v", err)
	}
}

func TestNormalizeAndRedactorValidationEdges(t *testing.T) {
	t.Parallel()

	configuration := Config{}
	normalize(&configuration)
	if configuration.JobSpecs == nil || configuration.WaitConditions == nil || configuration.Secrets == nil ||
		configuration.Concurrency.Pools == nil || configuration.Notifiers == nil || configuration.Profiles == nil ||
		configuration.Redaction.Names == nil || configuration.Redaction.Patterns == nil {
		t.Fatalf("normalize() left nil collections: %#v", configuration)
	}
	if _, err := NewRedactor(RedactionConfig{Names: []string{" "}}, nil); err == nil {
		t.Fatal("NewRedactor(empty name) error = nil")
	}
	if _, err := NewRedactor(RedactionConfig{Patterns: []string{""}}, nil); err == nil {
		t.Fatal("NewRedactor(empty pattern) error = nil")
	}
	tooLong := strings.Repeat("x", maxRedactionPattern+1)
	if _, err := NewRedactor(RedactionConfig{Patterns: []string{tooLong}}, nil); err == nil {
		t.Fatal("NewRedactor(long pattern) error = nil")
	}
	tooManySecrets := make(map[string]string, maxConfiguredSecrets+1)
	for index := range maxConfiguredSecrets + 1 {
		tooManySecrets[strings.Repeat("x", index+1)] = "value"
	}
	if _, err := NewRedactor(RedactionConfig{}, tooManySecrets); err == nil {
		t.Fatal("NewRedactor(too many secrets) error = nil")
	}
	if _, err := NewRedactor(RedactionConfig{}, map[string]string{"large": strings.Repeat("x", maxSecretBytes+1)}); err == nil {
		t.Fatal("NewRedactor(large secret) error = nil")
	}
}

func stringPointer(value string) *string { return &value }
