package jobman

import (
	"errors"
	"testing"
	"time"
)

func TestScalarFlagTypes(t *testing.T) {
	t.Parallel()

	if got := (*durationFlagValue)(nil).Type(); got != "duration" {
		t.Fatalf("duration flag type = %q", got)
	}
	if got := (*byteSizeFlagValue)(nil).Type(); got != "byte-size" {
		t.Fatalf("byte-size flag type = %q", got)
	}
}

func TestRunAcceptsExtendedScalarFlags(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend(t)
	_, err := executeCommand(t, dependenciesFor(backend), []string{
		"run",
		"--stop-grace", "1d",
		"--retry-delay", "1w",
		"--repeat-delay", "2d",
		"--retry-jitter", "12h",
		"--retry-max-delay", "2w",
		"--run-timeout", "3d",
		"--job-timeout", "4d",
		"--wait-delay", "5d",
		"--wait-poll", "6d",
		"--log-segment-bytes", "16MiB",
		"--log-retention", "7d",
		"--", "true",
	})
	if err != nil {
		t.Fatalf("extended scalar run flags error = %v", err)
	}
	request := backend.submitRequest
	if request == nil {
		t.Fatal("Submit() was not called")
	}
	if !request.StopPolicySet || request.StopPolicy.GracePeriod != 24*time.Hour {
		t.Fatalf("stop policy = %+v (set=%t)", request.StopPolicy, request.StopPolicySet)
	}
	policy := request.ExecutionPolicy
	if policy.FailureDelay.Base != 7*24*time.Hour ||
		policy.SuccessDelay.Base != 2*24*time.Hour ||
		policy.FailureDelay.Jitter != 12*time.Hour ||
		policy.FailureDelay.MaxDelay != 14*24*time.Hour ||
		policy.RunTimeout != 3*24*time.Hour ||
		policy.JobTimeout != 4*24*time.Hour ||
		policy.LogRotateSize != 16<<20 ||
		policy.LogRetentionMaxAge != 7*24*time.Hour {
		t.Fatalf("extended scalar policy = %+v", policy)
	}
	if len(policy.WaitConditions) != 1 ||
		policy.WaitConditions[0].Delay != 5*24*time.Hour ||
		policy.WaitConditions[0].PollInterval != 6*24*time.Hour {
		t.Fatalf("extended wait policy = %+v", policy.WaitConditions)
	}
}

func TestCleanAcceptsExtendedDurationFlag(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend(t)
	if _, err := executeCommand(t, dependenciesFor(backend), []string{
		"clean", "--older-than", "2w",
	}); err != nil {
		t.Fatalf("clean --older-than 2w error = %v", err)
	}
	if backend.cleanRequest == nil || backend.cleanRequest.OlderThan != 14*24*time.Hour {
		t.Fatalf("clean request = %+v", backend.cleanRequest)
	}
}

func TestCLIRejectsInvalidExtendedScalars(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		flag  string
		value string
	}{
		{name: "negative duration", flag: "--stop-grace", value: "-1s"},
		{name: "calendar duration", flag: "--run-timeout", value: "1mo"},
		{name: "duration overflow", flag: "--job-timeout", value: "999999999999999999999999d"},
		{name: "negative bytes", flag: "--log-segment-bytes", value: "-1"},
		{name: "decimal byte overflow", flag: "--log-segment-bytes", value: "18446744073709551616"},
		{name: "IEC byte overflow", flag: "--log-segment-bytes", value: "16EiB"},
		{name: "model byte overflow", flag: "--log-segment-bytes", value: "8EiB"},
		{name: "decimal byte fraction", flag: "--log-segment-bytes", value: "1.5MiB"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			backend := newFakeBackend(t)
			_, err := executeCommand(t, dependenciesFor(backend), []string{
				"run", test.flag, test.value, "--", "true",
			})
			if !errors.Is(err, errUsage) || ExitCode(err) != 2 {
				t.Fatalf("invalid scalar error/code = %v/%d, want usage/2", err, ExitCode(err))
			}
			if backend.submitRequest != nil {
				t.Fatal("invalid scalar reached Submit()")
			}
		})
	}
}

func TestRunRejectsInvalidDeferredDurationScalars(t *testing.T) {
	t.Parallel()

	for _, arguments := range [][]string{
		{"run", "--wait-delay", "1mo", "--", "true"},
		{"run", "--log-retention", "-1d", "--", "true"},
	} {
		backend := newFakeBackend(t)
		_, err := executeCommand(t, dependenciesFor(backend), arguments)
		if !errors.Is(err, errUsage) || ExitCode(err) != 2 {
			t.Fatalf("invalid scalar %v error/code = %v/%d, want usage/2", arguments, err, ExitCode(err))
		}
		if backend.submitRequest != nil {
			t.Fatalf("invalid scalar %v reached Submit()", arguments)
		}
	}
}
