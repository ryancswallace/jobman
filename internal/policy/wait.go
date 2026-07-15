package policy

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"time"
)

// WaitMode identifies how multiple prerequisite conditions are combined.
type WaitMode string

// Wait modes supported by the v1 specification.
const (
	WaitModeAll WaitMode = "all"
	WaitModeAny WaitMode = "any"
)

// ConditionResult records one deterministic prerequisite evaluation.
type ConditionResult struct {
	Satisfied bool
	Fatal     bool
	Err       error
}

// WaitDecision is the combined result of all current condition observations.
// Err retains nonfatal errors for durable diagnostics even when any mode is
// otherwise satisfied.
type WaitDecision struct {
	Satisfied bool
	Fatal     bool
	Err       error
}

// CombineWaitConditions combines observations without short-circuiting away
// errors. Zero conditions are satisfied because there is no prerequisite.
func CombineWaitConditions(mode WaitMode, results []ConditionResult) (WaitDecision, error) {
	if mode == "" {
		mode = WaitModeAll
	}
	if mode != WaitModeAll && mode != WaitModeAny {
		return WaitDecision{}, fmt.Errorf("combine wait conditions: unknown mode %q", mode)
	}

	decision := WaitDecision{Satisfied: mode == WaitModeAll || len(results) == 0}
	conditionErrors := make([]error, 0, len(results))
	for index, result := range results {
		if err := validateConditionResult(index, result); err != nil {
			return WaitDecision{}, err
		}

		decision.Fatal = decision.Fatal || result.Fatal
		if result.Err != nil {
			conditionErrors = append(conditionErrors, result.Err)
		}
		if mode == WaitModeAll {
			decision.Satisfied = decision.Satisfied && result.Satisfied
		} else {
			decision.Satisfied = decision.Satisfied || result.Satisfied
		}
	}

	decision.Err = errors.Join(conditionErrors...)
	if decision.Fatal {
		decision.Satisfied = false
	}

	return decision, nil
}

func validateConditionResult(index int, result ConditionResult) error {
	if result.Satisfied && result.Err != nil {
		return fmt.Errorf(
			"combine wait conditions: condition %d is both satisfied and erroneous",
			index,
		)
	}
	if result.Fatal && result.Err == nil {
		return fmt.Errorf(
			"combine wait conditions: condition %d is fatal without an error",
			index,
		)
	}

	return nil
}

// EvaluateUntil reports satisfaction at or after an absolute timestamp.
func EvaluateUntil(now, until time.Time) (ConditionResult, error) {
	if now.IsZero() {
		return ConditionResult{}, errors.New("evaluate until condition: current time must not be zero")
	}
	if until.IsZero() {
		return ConditionResult{}, errors.New("evaluate until condition: target time must not be zero")
	}

	return ConditionResult{Satisfied: !now.Before(until)}, nil
}

// EvaluateDelay reports satisfaction once the configured duration has elapsed
// since supervisor acceptance.
func EvaluateDelay(now, acceptedAt time.Time, delay time.Duration) (ConditionResult, error) {
	if now.IsZero() || acceptedAt.IsZero() {
		return ConditionResult{}, errors.New("evaluate delay condition: clock readings must not be zero")
	}
	if now.Before(acceptedAt) {
		return ConditionResult{}, errors.New("evaluate delay condition: current time precedes acceptance")
	}
	if delay < 0 {
		return ConditionResult{}, errors.New("evaluate delay condition: delay must not be negative")
	}

	return ConditionResult{Satisfied: now.Sub(acceptedAt) >= delay}, nil
}

// FileKind is an optional required filesystem object type.
type FileKind string

// File kinds supported by the initial file-exists condition.
const (
	FileKindAny       FileKind = "any"
	FileKindRegular   FileKind = "regular"
	FileKindDirectory FileKind = "directory"
	FileKindSymlink   FileKind = "symlink"
)

// FileInspector supplies link-preserving metadata without requiring host
// filesystem access in policy tests.
type FileInspector interface {
	Lstat(path string) (fs.FileInfo, error)
}

// EvaluateFileExists evaluates one path and optional type requirement. Missing
// paths are ordinary unsatisfied observations; other errors follow fatalOnError.
func EvaluateFileExists(
	inspector FileInspector,
	path string,
	requiredKind FileKind,
	fatalOnError bool,
) ConditionResult {
	if inspector == nil {
		return fatalCondition(errors.New("evaluate file-exists condition: inspector must not be nil"))
	}
	if path == "" || strings.ContainsRune(path, '\x00') {
		return fatalCondition(errors.New("evaluate file-exists condition: path must be nonempty and contain no NUL"))
	}
	if !validFileKind(requiredKind) {
		return fatalCondition(fmt.Errorf("evaluate file-exists condition: unknown file kind %q", requiredKind))
	}

	information, err := inspector.Lstat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return ConditionResult{}
		}

		return ConditionResult{Fatal: fatalOnError, Err: fmt.Errorf("inspect wait path: %w", err)}
	}

	return ConditionResult{Satisfied: matchesFileKind(information.Mode(), requiredKind)}
}

func validFileKind(kind FileKind) bool {
	return kind == "" || kind == FileKindAny || kind == FileKindRegular ||
		kind == FileKindDirectory || kind == FileKindSymlink
}

func matchesFileKind(mode fs.FileMode, kind FileKind) bool {
	switch kind {
	case "", FileKindAny:
		return true
	case FileKindRegular:
		return mode.IsRegular()
	case FileKindDirectory:
		return mode.IsDir()
	case FileKindSymlink:
		return mode&fs.ModeSymlink != 0
	default:
		return false
	}
}

// ProbeSpec describes one direct, non-shell probe execution. ProbeRunner owns
// timeout enforcement and output truncation according to these bounds.
type ProbeSpec struct {
	Executable   string
	Arguments    []string
	Timeout      time.Duration
	OutputLimit  int64
	FatalOnError bool
}

// Validate checks that a probe is direct and bounded.
func (specification ProbeSpec) Validate() error {
	if specification.Executable == "" || strings.ContainsRune(specification.Executable, '\x00') {
		return errors.New("validate probe: executable must be nonempty and contain no NUL")
	}
	for _, argument := range specification.Arguments {
		if strings.ContainsRune(argument, '\x00') {
			return errors.New("validate probe: argument must not contain NUL")
		}
	}
	if specification.Timeout <= 0 {
		return errors.New("validate probe: timeout must be positive")
	}
	if specification.OutputLimit <= 0 {
		return errors.New("validate probe: output limit must be positive")
	}

	return nil
}

// ProbeResult records bounded output and a normal process exit code.
type ProbeResult struct {
	ExitCode  int
	Output    []byte
	Truncated bool
}

// ProbeRunner executes a probe directly, preserving argument boundaries. It
// must honor context cancellation, ProbeSpec.Timeout, and OutputLimit.
type ProbeRunner interface {
	RunProbe(ctx context.Context, specification ProbeSpec) (ProbeResult, error)
}

// EvaluateProbe runs and classifies one probe observation. Exit code zero is
// satisfied; nonzero is an ordinary unsatisfied result.
func EvaluateProbe(
	ctx context.Context,
	runner ProbeRunner,
	specification ProbeSpec,
) ConditionResult {
	if ctx == nil {
		return fatalCondition(errors.New("evaluate probe condition: context must not be nil"))
	}
	if runner == nil {
		return fatalCondition(errors.New("evaluate probe condition: runner must not be nil"))
	}
	if err := specification.Validate(); err != nil {
		return fatalCondition(err)
	}

	specification.Arguments = slices.Clone(specification.Arguments)
	result, err := runner.RunProbe(ctx, specification)
	if err != nil {
		return ConditionResult{
			Fatal: specification.FatalOnError,
			Err:   fmt.Errorf("run wait probe: %w", err),
		}
	}
	if result.ExitCode < 0 {
		return fatalCondition(errors.New("evaluate probe condition: runner returned a negative exit code"))
	}
	if int64(len(result.Output)) > specification.OutputLimit {
		return fatalCondition(errors.New("evaluate probe condition: runner exceeded output limit"))
	}

	return ConditionResult{Satisfied: result.ExitCode == 0}
}

// WouldStartAfter reports whether a nonnegative delay puts a start strictly
// after an absolute abort timestamp. Starting exactly at the timestamp is
// permitted.
func WouldStartAfter(now time.Time, delay time.Duration, abortAt time.Time) (bool, error) {
	if now.IsZero() || abortAt.IsZero() {
		return false, errors.New("evaluate start deadline: clock readings must not be zero")
	}
	if delay < 0 {
		return false, errors.New("evaluate start deadline: delay must not be negative")
	}
	if now.After(abortAt) {
		return true, nil
	}

	return delay > abortAt.Sub(now), nil
}

func fatalCondition(err error) ConditionResult {
	return ConditionResult{Fatal: true, Err: err}
}
