// Package policy implements deterministic run classification, completion,
// delay, timeout, and prerequisite policy decisions.
package policy

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// RunTermination identifies the factual reason a run stopped.
type RunTermination string

// Run termination reasons understood by the v1 policy engine.
const (
	RunTerminationExit         RunTermination = "exit"
	RunTerminationSignal       RunTermination = "signal"
	RunTerminationPlatform     RunTermination = "platform"
	RunTerminationTimeout      RunTermination = "timeout"
	RunTerminationStartFailure RunTermination = "start_failure"
	RunTerminationCancellation RunTermination = "cancellation"
	RunTerminationLost         RunTermination = "lost"
)

// RunResult contains the factual result used to classify one completed run.
// Exactly one termination-specific value is permitted.
type RunResult struct {
	Termination    RunTermination
	ExitCode       int
	Signal         string
	PlatformReason string
}

// Validate checks that the run result is unambiguous and complete.
func (result RunResult) Validate() error {
	switch result.Termination {
	case RunTerminationExit:
		return result.validateExit()
	case RunTerminationSignal:
		return result.validateSignal()
	case RunTerminationPlatform:
		return result.validatePlatformReason()
	case RunTerminationTimeout, RunTerminationStartFailure,
		RunTerminationCancellation, RunTerminationLost:
		if result.ExitCode != 0 || result.Signal != "" || result.PlatformReason != "" {
			return fmt.Errorf("validate run result: %s result must not include another termination reason", result.Termination)
		}

		return nil
	default:
		return fmt.Errorf("validate run result: unknown termination %q", result.Termination)
	}
}

func (result RunResult) validateExit() error {
	if result.ExitCode < 0 {
		return errors.New("validate run result: exit code must not be negative")
	}
	if result.Signal != "" || result.PlatformReason != "" {
		return errors.New("validate run result: exit result must not include another termination reason")
	}

	return nil
}

func (result RunResult) validateSignal() error {
	if result.Signal == "" {
		return errors.New("validate run result: signal must not be empty")
	}
	if result.ExitCode != 0 || result.PlatformReason != "" {
		return errors.New("validate run result: signal result must not include another termination reason")
	}

	return nil
}

func (result RunResult) validatePlatformReason() error {
	if result.PlatformReason == "" {
		return errors.New("validate run result: platform reason must not be empty")
	}
	if result.ExitCode != 0 || result.Signal != "" {
		return errors.New("validate run result: platform result must not include another termination reason")
	}

	return nil
}

// ExitCodeRange is an inclusive range of process exit codes.
type ExitCodeRange struct {
	First int
	Last  int
}

// Validate checks that the inclusive range is ordered and nonnegative.
func (codeRange ExitCodeRange) Validate() error {
	if codeRange.First < 0 {
		return errors.New("validate exit code range: first code must not be negative")
	}
	if codeRange.Last < codeRange.First {
		return errors.New("validate exit code range: last code must not precede first code")
	}

	return nil
}

func (codeRange ExitCodeRange) contains(code int) bool {
	return code >= codeRange.First && code <= codeRange.Last
}

// ClassificationPolicy configures run-result classification. A nil
// SuccessExitCodes slice defaults to {0}; a non-nil empty slice means that no
// exit code is successful. A nil RetryableExitCodes slice makes every unlisted
// nonzero exit retryable, while a non-nil slice is an explicit allowlist.
type ClassificationPolicy struct {
	SuccessExitCodes         []int
	RetryableExitCodes       []ExitCodeRange
	RetryableSignals         []string
	RetryablePlatformReasons []string
	RetryTimeout             bool
	RetryStartFailure        bool
	RetryCancellation        bool
}

// RunClassification is the policy meaning assigned to one factual run result.
type RunClassification string

// Run classifications produced by Classifier.
const (
	RunClassificationSuccess             RunClassification = "success"
	RunClassificationRetryableFailure    RunClassification = "retryable_failure"
	RunClassificationNonRetryableFailure RunClassification = "non_retryable_failure"
)

// Classifier is an immutable validated run-result classifier.
type Classifier struct {
	successCodes      []int
	retryableCodes    []ExitCodeRange
	retryableSignals  []string
	retryableReasons  []string
	explicitCodes     bool
	retryTimeout      bool
	retryStartFailure bool
	retryCancellation bool
	initialized       bool
}

// NewClassifier validates and defensively copies a classification policy.
func NewClassifier(configuration ClassificationPolicy) (Classifier, error) {
	successCodes := slices.Clone(configuration.SuccessExitCodes)
	if successCodes == nil {
		successCodes = []int{0}
	}

	classifier := Classifier{
		successCodes:      successCodes,
		retryableCodes:    slices.Clone(configuration.RetryableExitCodes),
		retryableSignals:  slices.Clone(configuration.RetryableSignals),
		retryableReasons:  slices.Clone(configuration.RetryablePlatformReasons),
		explicitCodes:     configuration.RetryableExitCodes != nil,
		retryTimeout:      configuration.RetryTimeout,
		retryStartFailure: configuration.RetryStartFailure,
		retryCancellation: configuration.RetryCancellation,
		initialized:       true,
	}
	if err := classifier.validate(); err != nil {
		return Classifier{}, err
	}

	return classifier, nil
}

func (classifier Classifier) validate() error {
	seenSuccess := make(map[int]struct{}, len(classifier.successCodes))
	for _, code := range classifier.successCodes {
		if code < 0 {
			return errors.New("validate classification policy: success exit code must not be negative")
		}
		if _, exists := seenSuccess[code]; exists {
			return fmt.Errorf("validate classification policy: duplicate success exit code %d", code)
		}
		seenSuccess[code] = struct{}{}
	}

	for _, codeRange := range classifier.retryableCodes {
		if err := codeRange.Validate(); err != nil {
			return fmt.Errorf("validate classification policy: %w", err)
		}
		for successCode := range seenSuccess {
			if codeRange.contains(successCode) {
				return fmt.Errorf(
					"validate classification policy: success and retryable exit codes overlap at %d",
					successCode,
				)
			}
		}
	}

	if err := validateNames("retryable signal", classifier.retryableSignals); err != nil {
		return err
	}
	if err := validateNames("retryable platform reason", classifier.retryableReasons); err != nil {
		return err
	}

	return nil
}

func validateNames(field string, names []string) error {
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if name == "" || strings.TrimSpace(name) != name {
			return fmt.Errorf("validate classification policy: %s must be nonempty and trimmed", field)
		}
		if _, exists := seen[name]; exists {
			return fmt.Errorf("validate classification policy: duplicate %s %q", field, name)
		}
		seen[name] = struct{}{}
	}

	return nil
}

// Classify assigns the configured policy meaning to one factual result.
func (classifier Classifier) Classify(result RunResult) (RunClassification, error) {
	if !classifier.initialized {
		return "", errors.New("classify run result: classifier is not initialized")
	}
	if err := result.Validate(); err != nil {
		return "", err
	}

	switch result.Termination {
	case RunTerminationExit:
		return classifier.classifyExit(result.ExitCode), nil
	case RunTerminationSignal:
		return retryClassification(slices.Contains(classifier.retryableSignals, result.Signal)), nil
	case RunTerminationPlatform:
		return retryClassification(slices.Contains(classifier.retryableReasons, result.PlatformReason)), nil
	case RunTerminationTimeout:
		return retryClassification(classifier.retryTimeout), nil
	case RunTerminationStartFailure:
		return retryClassification(classifier.retryStartFailure), nil
	case RunTerminationCancellation:
		return retryClassification(classifier.retryCancellation), nil
	case RunTerminationLost:
		return RunClassificationNonRetryableFailure, nil
	default:
		return "", fmt.Errorf("classify run result: unknown termination %q", result.Termination)
	}
}

func (classifier Classifier) classifyExit(code int) RunClassification {
	if slices.Contains(classifier.successCodes, code) {
		return RunClassificationSuccess
	}

	if classifier.explicitCodes {
		for _, codeRange := range classifier.retryableCodes {
			if codeRange.contains(code) {
				return RunClassificationRetryableFailure
			}
		}

		return RunClassificationNonRetryableFailure
	}

	return retryClassification(code != 0)
}

func retryClassification(retryable bool) RunClassification {
	if retryable {
		return RunClassificationRetryableFailure
	}

	return RunClassificationNonRetryableFailure
}
