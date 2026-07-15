package policy

import (
	"strings"
	"testing"
)

func TestDefaultClassifier(t *testing.T) {
	t.Parallel()

	classifier, err := NewClassifier(ClassificationPolicy{})
	if err != nil {
		t.Fatalf("NewClassifier() error = %v", err)
	}

	tests := []struct {
		name   string
		result RunResult
		want   RunClassification
	}{
		{"zero exit succeeds", RunResult{Termination: RunTerminationExit}, RunClassificationSuccess},
		{
			"nonzero exit retries",
			RunResult{Termination: RunTerminationExit, ExitCode: 125},
			RunClassificationRetryableFailure,
		},
		{
			"signal does not retry",
			RunResult{Termination: RunTerminationSignal, Signal: "TERM"},
			RunClassificationNonRetryableFailure,
		},
		{
			"platform reason does not retry",
			RunResult{Termination: RunTerminationPlatform, PlatformReason: "killed"},
			RunClassificationNonRetryableFailure,
		},
		{"timeout does not retry", RunResult{Termination: RunTerminationTimeout}, RunClassificationNonRetryableFailure},
		{
			"start failure does not retry",
			RunResult{Termination: RunTerminationStartFailure},
			RunClassificationNonRetryableFailure,
		},
		{
			"cancellation does not retry",
			RunResult{Termination: RunTerminationCancellation},
			RunClassificationNonRetryableFailure,
		},
		{"lost never retries", RunResult{Termination: RunTerminationLost}, RunClassificationNonRetryableFailure},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, classifyErr := classifier.Classify(test.result)
			if classifyErr != nil {
				t.Fatalf("Classify() error = %v", classifyErr)
			}
			if got != test.want {
				t.Fatalf("Classify() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestConfiguredClassifier(t *testing.T) {
	t.Parallel()

	configuration := ClassificationPolicy{
		SuccessExitCodes: []int{0, 10},
		RetryableExitCodes: []ExitCodeRange{
			{First: 2, Last: 4},
			{First: 20, Last: 20},
		},
		RetryableSignals:         []string{"HUP"},
		RetryablePlatformReasons: []string{"transient"},
		RetryTimeout:             true,
		RetryStartFailure:        true,
		RetryCancellation:        true,
	}
	classifier, err := NewClassifier(configuration)
	if err != nil {
		t.Fatalf("NewClassifier() error = %v", err)
	}

	// Prove construction copied all caller-owned collections.
	configuration.SuccessExitCodes[0] = 2
	configuration.RetryableExitCodes[0] = ExitCodeRange{First: 0, Last: 100}
	configuration.RetryableSignals[0] = "TERM"
	configuration.RetryablePlatformReasons[0] = "permanent"

	tests := []struct {
		result RunResult
		want   RunClassification
	}{
		{RunResult{Termination: RunTerminationExit}, RunClassificationSuccess},
		{RunResult{Termination: RunTerminationExit, ExitCode: 3}, RunClassificationRetryableFailure},
		{RunResult{Termination: RunTerminationExit, ExitCode: 10}, RunClassificationSuccess},
		{RunResult{Termination: RunTerminationExit, ExitCode: 5}, RunClassificationNonRetryableFailure},
		{RunResult{Termination: RunTerminationSignal, Signal: "HUP"}, RunClassificationRetryableFailure},
		{RunResult{Termination: RunTerminationSignal, Signal: "TERM"}, RunClassificationNonRetryableFailure},
		{
			RunResult{Termination: RunTerminationPlatform, PlatformReason: "transient"},
			RunClassificationRetryableFailure,
		},
		{RunResult{Termination: RunTerminationTimeout}, RunClassificationRetryableFailure},
		{RunResult{Termination: RunTerminationStartFailure}, RunClassificationRetryableFailure},
		{RunResult{Termination: RunTerminationCancellation}, RunClassificationRetryableFailure},
		{RunResult{Termination: RunTerminationLost}, RunClassificationNonRetryableFailure},
	}

	for _, test := range tests {
		got, classifyErr := classifier.Classify(test.result)
		if classifyErr != nil {
			t.Fatalf("Classify(%+v) error = %v", test.result, classifyErr)
		}
		if got != test.want {
			t.Errorf("Classify(%+v) = %q, want %q", test.result, got, test.want)
		}
	}
}

func TestExplicitEmptyClassifierSets(t *testing.T) {
	t.Parallel()

	classifier, err := NewClassifier(ClassificationPolicy{
		SuccessExitCodes:   []int{},
		RetryableExitCodes: []ExitCodeRange{},
	})
	if err != nil {
		t.Fatalf("NewClassifier() error = %v", err)
	}

	for _, code := range []int{0, 1, 255} {
		classification, classifyErr := classifier.Classify(RunResult{
			Termination: RunTerminationExit,
			ExitCode:    code,
		})
		if classifyErr != nil {
			t.Fatalf("Classify(exit %d) error = %v", code, classifyErr)
		}
		if classification != RunClassificationNonRetryableFailure {
			t.Errorf("Classify(exit %d) = %q, want non-retryable failure", code, classification)
		}
	}
}

func TestNewClassifierRejectsInvalidPolicies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		policy ClassificationPolicy
		part   string
	}{
		{"negative success", ClassificationPolicy{SuccessExitCodes: []int{-1}}, "negative"},
		{"duplicate success", ClassificationPolicy{SuccessExitCodes: []int{0, 0}}, "duplicate"},
		{
			"negative range",
			ClassificationPolicy{RetryableExitCodes: []ExitCodeRange{{First: -1, Last: 1}}},
			"negative",
		},
		{
			"reversed range",
			ClassificationPolicy{RetryableExitCodes: []ExitCodeRange{{First: 4, Last: 3}}},
			"precede",
		},
		{
			"overlap",
			ClassificationPolicy{RetryableExitCodes: []ExitCodeRange{{First: 0, Last: 2}}},
			"overlap",
		},
		{"blank signal", ClassificationPolicy{RetryableSignals: []string{""}}, "nonempty"},
		{"untrimmed signal", ClassificationPolicy{RetryableSignals: []string{" HUP"}}, "trimmed"},
		{"duplicate signal", ClassificationPolicy{RetryableSignals: []string{"HUP", "HUP"}}, "duplicate"},
		{
			"blank platform reason",
			ClassificationPolicy{RetryablePlatformReasons: []string{""}},
			"nonempty",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := NewClassifier(test.policy)
			if err == nil || !strings.Contains(err.Error(), test.part) {
				t.Fatalf("NewClassifier() error = %v, want containing %q", err, test.part)
			}
		})
	}
}

func TestClassifierRejectsInvalidResults(t *testing.T) {
	t.Parallel()

	classifier, err := NewClassifier(ClassificationPolicy{})
	if err != nil {
		t.Fatalf("NewClassifier() error = %v", err)
	}

	tests := []RunResult{
		{},
		{Termination: RunTerminationExit, ExitCode: -1},
		{Termination: RunTerminationExit, Signal: "TERM"},
		{Termination: RunTerminationSignal},
		{Termination: RunTerminationSignal, Signal: "TERM", PlatformReason: "other"},
		{Termination: RunTerminationPlatform},
		{Termination: RunTerminationTimeout, Signal: "TERM"},
	}
	for _, result := range tests {
		if _, classifyErr := classifier.Classify(result); classifyErr == nil {
			t.Errorf("Classify(%+v) unexpectedly succeeded", result)
		}
	}

	if _, classifyErr := (Classifier{}).Classify(RunResult{Termination: RunTerminationExit}); classifyErr == nil {
		t.Error("zero Classifier.Classify() unexpectedly succeeded")
	}
}

func TestExitClassificationPartitionsCodeSpace(t *testing.T) {
	t.Parallel()

	classifier, err := NewClassifier(ClassificationPolicy{
		SuccessExitCodes: []int{0, 8},
		RetryableExitCodes: []ExitCodeRange{
			{First: 1, Last: 3},
			{First: 100, Last: 199},
		},
	})
	if err != nil {
		t.Fatalf("NewClassifier() error = %v", err)
	}

	for code := range 256 {
		got, classifyErr := classifier.Classify(RunResult{
			Termination: RunTerminationExit,
			ExitCode:    code,
		})
		if classifyErr != nil {
			t.Fatalf("Classify(exit %d) error = %v", code, classifyErr)
		}

		want := RunClassificationNonRetryableFailure
		switch {
		case code == 0 || code == 8:
			want = RunClassificationSuccess
		case code >= 1 && code <= 3, code >= 100 && code <= 199:
			want = RunClassificationRetryableFailure
		}
		if got != want {
			t.Errorf("Classify(exit %d) = %q, want %q", code, got, want)
		}
	}
}
