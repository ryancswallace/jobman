package model

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/ryancswallace/jobman/internal/policy"
)

// WaitConditionKind identifies a persisted pre-run prerequisite.
type WaitConditionKind string

// Supported wait-condition kinds.
const (
	WaitUntil      WaitConditionKind = "until"
	WaitDelay      WaitConditionKind = "delay"
	WaitFileExists WaitConditionKind = "file_exists"
	WaitProbe      WaitConditionKind = "probe"
)

// WaitCondition is one immutable prerequisite. Only fields belonging to Kind
// may be populated.
type WaitCondition struct {
	Kind                  WaitConditionKind
	Until                 time.Time
	Delay                 time.Duration
	Path                  string
	FileKind              policy.FileKind
	Probe                 policy.ProbeSpec
	ProbeDirectory        string
	ProbeEnvironment      map[string]string
	ProbeUnsetEnvironment []string
	ProbeSecretEnv        map[string]SecretReference
	PollInterval          time.Duration
	AbortAt               time.Time
}

// Validate checks one persisted prerequisite.
func (condition WaitCondition) Validate() error {
	if condition.PollInterval <= 0 {
		return invalid("wait poll interval", "must be positive")
	}
	if !condition.AbortAt.IsZero() && condition.AbortAt.Before(time.Unix(0, 0)) {
		return invalid("wait abort time", "must follow the Unix epoch")
	}
	switch condition.Kind {
	case WaitUntil:
		return condition.validateUntil()
	case WaitDelay:
		return condition.validateDelay()
	case WaitFileExists:
		return condition.validateFile()
	case WaitProbe:
		return condition.validateProbe()
	default:
		return invalid("wait condition kind", "is unknown")
	}
}

func (condition WaitCondition) validateUntil() error {
	if condition.Until.IsZero() {
		return invalid("wait until time", "must be present")
	}

	return nil
}

func (condition WaitCondition) validateDelay() error {
	if condition.Delay < 0 {
		return invalid("wait delay", "must not be negative")
	}

	return nil
}

func (condition WaitCondition) validateFile() error {
	if condition.Path == "" || strings.ContainsRune(condition.Path, '\x00') {
		return invalid("wait file path", "must be nonempty and contain no NUL")
	}
	switch condition.FileKind {
	case "", policy.FileKindAny, policy.FileKindRegular, policy.FileKindDirectory, policy.FileKindSymlink:
		return nil
	default:
		return invalid("wait file kind", "is unknown")
	}
}

func (condition WaitCondition) validateProbe() error {
	if err := condition.Probe.Validate(); err != nil {
		return fmt.Errorf("validate wait probe: %w", err)
	}
	if condition.ProbeDirectory != "" && (!filepath.IsAbs(condition.ProbeDirectory) ||
		filepath.Clean(condition.ProbeDirectory) != condition.ProbeDirectory) {
		return invalid("wait probe working directory", "must be clean and absolute")
	}
	for name, value := range condition.ProbeEnvironment {
		if !validEnvironmentName(name) || strings.ContainsRune(value, '\x00') {
			return invalid("wait probe environment", "contains an invalid entry")
		}
	}
	for _, name := range condition.ProbeUnsetEnvironment {
		if !validEnvironmentName(name) {
			return invalid("wait probe environment removal", "contains an invalid name")
		}
	}
	for name, reference := range condition.ProbeSecretEnv {
		if !validEnvironmentName(name) || reference.Provider == "" || reference.Name == "" {
			return invalid("wait probe secret environment", "contains an invalid reference")
		}
	}

	return nil
}

// DependencyRequirement contains a selector resolved to a canonical immutable
// job ID at submission time and the required terminal predicate.
type DependencyRequirement struct {
	JobID     JobID
	Predicate string
}

// ConcurrencyPolicy configures transactional global and optional pool slots.
type ConcurrencyPolicy struct {
	Pool  string
	Slots uint64
}

// NotificationSubscription selects named notifiers and lifecycle events.
type NotificationSubscription struct {
	Notifier string
	Events   []string
}

// SecretReference persists only a reference; its value is resolved in the
// supervisor and must never be written to metadata or diagnostics.
type SecretReference struct {
	Provider string `json:"provider"`
	Name     string `json:"name"`
}

// ExecutionPolicy is the immutable policy input for the per-job supervisor.
type ExecutionPolicy struct {
	Completion              policy.CompletionPolicy
	Classification          policy.ClassificationPolicy
	FailureDelay            policy.DelayPolicy
	SuccessDelay            policy.DelayPolicy
	RunTimeout              time.Duration
	JobTimeout              time.Duration
	WaitMode                policy.WaitMode
	WaitConditions          []WaitCondition
	Dependencies            []DependencyRequirement
	Concurrency             ConcurrencyPolicy
	Notifications           []NotificationSubscription
	NotifierDefinitions     []NotifierDefinition
	Tags                    []string
	Groups                  []string
	SecretEnv               map[string]SecretReference
	Foreground              bool
	StdinPath               string
	LogRotateSize           int64
	LogMaxSegmentsPerStream int
	LogCapture              string
	LogRetentionMaxAge      time.Duration
	LogRetentionUnlimited   bool
	LogRetentionConfigured  bool
}

// DefaultExecutionPolicy returns the ordinary one-run, unlimited-concurrency,
// detached policy.
func DefaultExecutionPolicy() ExecutionPolicy {
	return ExecutionPolicy{
		Completion:             policy.DefaultCompletionPolicy(),
		FailureDelay:           policy.DelayPolicy{Backoff: policy.BackoffConstant},
		SuccessDelay:           policy.DelayPolicy{Backoff: policy.BackoffConstant},
		WaitMode:               policy.WaitModeAll,
		Concurrency:            ConcurrencyPolicy{Slots: 1},
		LogCapture:             "both",
		LogRetentionMaxAge:     30 * 24 * time.Hour,
		LogRetentionConfigured: true,
	}
}

func withExecutionDefaults(configuration ExecutionPolicy) ExecutionPolicy {
	defaults := DefaultExecutionPolicy()
	if configuration.Completion.MaxRuns == (policy.Limit{}) &&
		configuration.Completion.SuccessTarget == (policy.Limit{}) &&
		configuration.Completion.FailureLimit == (policy.Limit{}) {
		configuration.Completion = defaults.Completion
	}
	if configuration.FailureDelay.Backoff == "" {
		configuration.FailureDelay.Backoff = policy.BackoffConstant
	}
	if configuration.SuccessDelay.Backoff == "" {
		configuration.SuccessDelay.Backoff = policy.BackoffConstant
	}
	if configuration.WaitMode == "" {
		configuration.WaitMode = policy.WaitModeAll
	}
	if configuration.Concurrency.Slots == 0 {
		configuration.Concurrency.Slots = 1
	}
	if configuration.LogCapture == "" {
		configuration.LogCapture = defaults.LogCapture
	}
	if !configuration.LogRetentionConfigured {
		configuration.LogRetentionMaxAge = defaults.LogRetentionMaxAge
		configuration.LogRetentionUnlimited = defaults.LogRetentionUnlimited
		configuration.LogRetentionConfigured = true
	}

	return configuration
}

// Validate checks cross-policy invariants before durable state is created.
func (configuration ExecutionPolicy) Validate(stdin StdinPolicy) error {
	if err := configuration.validateRunPolicies(); err != nil {
		return err
	}
	if err := configuration.validateDependenciesAndConcurrency(); err != nil {
		return err
	}
	if err := configuration.validateStdin(stdin); err != nil {
		return err
	}
	if err := configuration.validateLogging(); err != nil {
		return err
	}
	if err := configuration.validateMetadata(); err != nil {
		return err
	}

	return validateNotifierDefinitions(configuration.NotifierDefinitions, configuration.Notifications)
}

func (configuration ExecutionPolicy) validateRunPolicies() error {
	if err := configuration.Completion.Validate(); err != nil {
		return fmt.Errorf("validate completion policy: %w", err)
	}
	if _, err := policy.NewClassifier(configuration.Classification); err != nil {
		return err
	}
	if err := configuration.FailureDelay.Validate(); err != nil {
		return err
	}
	if err := configuration.SuccessDelay.Validate(); err != nil {
		return err
	}
	if configuration.RunTimeout < 0 || configuration.JobTimeout < 0 {
		return invalid("timeout", "must not be negative")
	}
	if configuration.WaitMode != policy.WaitModeAll && configuration.WaitMode != policy.WaitModeAny {
		return invalid("wait mode", "must be all or any")
	}
	for _, condition := range configuration.WaitConditions {
		if err := condition.Validate(); err != nil {
			return err
		}
	}

	return nil
}

func (configuration ExecutionPolicy) validateDependenciesAndConcurrency() error {
	seenDependencies := make(map[string]struct{}, len(configuration.Dependencies))
	for _, dependency := range configuration.Dependencies {
		if !dependency.JobID.Valid() || dependency.Predicate == "" {
			return invalid("dependency", "must contain a canonical job ID and predicate")
		}
		key := dependency.JobID.String() + "\x00" + dependency.Predicate
		if _, exists := seenDependencies[key]; exists {
			return invalid("dependency", "must not be duplicated")
		}
		seenDependencies[key] = struct{}{}
	}
	if strings.TrimSpace(configuration.Concurrency.Pool) != configuration.Concurrency.Pool {
		return invalid("concurrency pool", "must be trimmed")
	}
	if configuration.Concurrency.Slots == 0 {
		return invalid("concurrency slots", "must be positive")
	}

	return nil
}

func (configuration ExecutionPolicy) validateStdin(stdin StdinPolicy) error {
	if configuration.Foreground && stdin == StdinLive {
		return invalid("stdin policy", "live input is only available for detached jobs")
	}
	if stdin == StdinFile {
		if configuration.StdinPath == "" || !filepath.IsAbs(configuration.StdinPath) ||
			filepath.Clean(configuration.StdinPath) != configuration.StdinPath {
			return invalid("stdin file path", "must be clean and absolute")
		}
	} else if configuration.StdinPath != "" {
		return invalid("stdin file path", "is valid only with file stdin")
	}
	if stdin == StdinInherit && !configuration.Foreground {
		return invalid("stdin policy", "inherited stdin requires foreground mode")
	}

	return nil
}

func (configuration ExecutionPolicy) validateLogging() error {
	if configuration.LogRotateSize < 0 || configuration.LogMaxSegmentsPerStream < 0 ||
		configuration.LogMaxSegmentsPerStream > int(^uint16(0)) {
		return invalid("log rotation", "limits must not be negative")
	}
	switch configuration.LogCapture {
	case "both", "stdout", "stderr", "none":
	default:
		return invalid("log capture", "must be both, stdout, stderr, or none")
	}
	if configuration.LogRetentionMaxAge < 0 ||
		configuration.LogRetentionUnlimited && configuration.LogRetentionMaxAge != 0 {
		return invalid("log retention", "must be nonnegative and unambiguous")
	}

	return nil
}

func (configuration ExecutionPolicy) validateMetadata() error {
	if err := validateNames("tag", configuration.Tags); err != nil {
		return err
	}
	if err := validateNames("group", configuration.Groups); err != nil {
		return err
	}
	for environmentName, reference := range configuration.SecretEnv {
		if !validEnvironmentName(environmentName) || reference.Provider == "" || reference.Name == "" ||
			strings.ContainsRune(reference.Name, '\x00') {
			return invalid("secret environment reference", "is invalid")
		}
	}

	return nil
}

func validateNames(kind string, names []string) error {
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		if name == "" || strings.TrimSpace(name) != name || containsControl(name) {
			return invalid(kind, "must be nonempty, trimmed, and contain no controls")
		}
		if _, exists := seen[name]; exists {
			return invalid(kind, "must not be duplicated")
		}
		seen[name] = struct{}{}
	}

	return nil
}

func cloneExecutionPolicy(source ExecutionPolicy) ExecutionPolicy {
	clone := source
	clone.Classification.SuccessExitCodes = slices.Clone(source.Classification.SuccessExitCodes)
	clone.Classification.RetryableExitCodes = slices.Clone(source.Classification.RetryableExitCodes)
	clone.Classification.RetryableSignals = slices.Clone(source.Classification.RetryableSignals)
	clone.Classification.RetryablePlatformReasons = slices.Clone(source.Classification.RetryablePlatformReasons)
	clone.WaitConditions = slices.Clone(source.WaitConditions)
	if clone.WaitConditions == nil {
		clone.WaitConditions = []WaitCondition{}
	}
	for index := range clone.WaitConditions {
		clone.WaitConditions[index].Probe.Arguments = slices.Clone(source.WaitConditions[index].Probe.Arguments)
		clone.WaitConditions[index].ProbeEnvironment = cloneStringMap(source.WaitConditions[index].ProbeEnvironment)
		clone.WaitConditions[index].ProbeUnsetEnvironment = slices.Clone(source.WaitConditions[index].ProbeUnsetEnvironment)
		clone.WaitConditions[index].ProbeSecretEnv = cloneSecretReferences(source.WaitConditions[index].ProbeSecretEnv)
	}
	clone.Dependencies = slices.Clone(source.Dependencies)
	if clone.Dependencies == nil {
		clone.Dependencies = []DependencyRequirement{}
	}
	clone.Notifications = slices.Clone(source.Notifications)
	if clone.Notifications == nil {
		clone.Notifications = []NotificationSubscription{}
	}
	for index := range clone.Notifications {
		clone.Notifications[index].Events = slices.Clone(source.Notifications[index].Events)
	}
	clone.NotifierDefinitions = cloneNotifierDefinitions(source.NotifierDefinitions)
	clone.Tags = normalizeStrings(source.Tags)
	clone.Groups = normalizeStrings(source.Groups)
	if clone.Tags == nil {
		clone.Tags = []string{}
	}
	if clone.Groups == nil {
		clone.Groups = []string{}
	}
	if source.SecretEnv != nil {
		clone.SecretEnv = make(map[string]SecretReference, len(source.SecretEnv))
		for name, reference := range source.SecretEnv {
			clone.SecretEnv[name] = reference
		}
	} else {
		clone.SecretEnv = map[string]SecretReference{}
	}

	return clone
}
