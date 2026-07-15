package model

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

// JobSpecSchemaVersion is the current persisted immutable-specification schema.
const JobSpecSchemaVersion = 2

// EnvironmentInheritancePolicy identifies how the run obtains its base
// environment.
type EnvironmentInheritancePolicy string

// EnvironmentInheritSubmission preserves the submission process environment
// in the supervisor without persisting it wholesale.
const EnvironmentInheritSubmission EnvironmentInheritancePolicy = "submission"

// StdinPolicy identifies the source connected to the target's standard input.
type StdinPolicy string

// StdinNull connects the detached target to the platform null device.
const (
	StdinNull    StdinPolicy = "null"
	StdinLive    StdinPolicy = "live"
	StdinFile    StdinPolicy = "file"
	StdinInherit StdinPolicy = "inherit"
)

// StopPolicy describes graceful and forced target-tree termination.
type StopPolicy struct {
	GracePeriod     time.Duration
	ForceAfterGrace bool
}

// JobSpecInput contains caller-owned values used to construct an immutable
// JobSpec. NewJobSpec copies all slices and maps.
type JobSpecInput struct {
	Executable             string
	Arguments              []string
	WorkingDirectory       string
	Environment            map[string]string
	UnsetEnvironment       []string
	EnvironmentInheritance EnvironmentInheritancePolicy
	Name                   string
	StopPolicy             StopPolicy
	StdinPolicy            StdinPolicy
	ExecutionPolicy        ExecutionPolicy
}

// JobSpec is the immutable, canonically serializable execution specification.
type JobSpec struct {
	executable             string
	arguments              []string
	workingDirectory       string
	environment            map[string]string
	unsetEnvironment       []string
	environmentInheritance EnvironmentInheritancePolicy
	name                   string
	stopPolicy             StopPolicy
	stdinPolicy            StdinPolicy
	executionPolicy        ExecutionPolicy
}

type jobSpecWire struct {
	SchemaVersion    int                  `json:"schema_version"`
	Executable       string               `json:"executable"`
	Arguments        []string             `json:"arguments"`
	WorkingDirectory string               `json:"working_directory"`
	Environment      environmentWire      `json:"environment"`
	Name             string               `json:"name,omitempty"`
	StopPolicy       stopPolicyWire       `json:"stop_policy"`
	StdinPolicy      StdinPolicy          `json:"stdin_policy"`
	ExecutionPolicy  *executionPolicyWire `json:"execution_policy,omitempty"`
}

type environmentWire struct {
	Inheritance EnvironmentInheritancePolicy `json:"inheritance"`
	Set         map[string]string            `json:"set"`
	Unset       []string                     `json:"unset"`
}

type stopPolicyWire struct {
	GracePeriod     string `json:"grace_period"`
	ForceAfterGrace bool   `json:"force_after_grace"`
}

// NewJobSpec validates, normalizes, and defensively copies a specification.
func NewJobSpec(input JobSpecInput) (JobSpec, error) {
	if input.EnvironmentInheritance == "" {
		input.EnvironmentInheritance = EnvironmentInheritSubmission
	}
	if input.StdinPolicy == "" {
		input.StdinPolicy = StdinNull
	}
	input.ExecutionPolicy = withExecutionDefaults(input.ExecutionPolicy)

	specification := JobSpec{
		executable:             input.Executable,
		arguments:              append([]string(nil), input.Arguments...),
		workingDirectory:       filepath.Clean(input.WorkingDirectory),
		environment:            cloneStringMap(input.Environment),
		unsetEnvironment:       normalizeStrings(input.UnsetEnvironment),
		environmentInheritance: input.EnvironmentInheritance,
		name:                   input.Name,
		stopPolicy:             input.StopPolicy,
		stdinPolicy:            input.StdinPolicy,
		executionPolicy:        cloneExecutionPolicy(input.ExecutionPolicy),
	}

	if specification.arguments == nil {
		specification.arguments = []string{}
	}
	if specification.environment == nil {
		specification.environment = map[string]string{}
	}
	if specification.unsetEnvironment == nil {
		specification.unsetEnvironment = []string{}
	}

	if err := specification.Validate(); err != nil {
		return JobSpec{}, err
	}

	return specification, nil
}

// ParseJobSpecJSON strictly parses a versioned canonical specification.
func ParseJobSpecJSON(data []byte) (JobSpec, error) {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return JobSpec{}, fmt.Errorf("decode job specification: %w", err)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	var wire jobSpecWire
	if err := decoder.Decode(&wire); err != nil {
		return JobSpec{}, fmt.Errorf("decode job specification: %w", err)
	}
	if err := requireJSONEnd(decoder); err != nil {
		return JobSpec{}, fmt.Errorf("decode job specification: %w", err)
	}
	if wire.SchemaVersion != 1 && wire.SchemaVersion != JobSpecSchemaVersion {
		return JobSpec{}, invalid(
			"job specification schema version",
			fmt.Sprintf("must be 1 or %d", JobSpecSchemaVersion),
		)
	}
	if err := validatePersistedSpecShape(wire); err != nil {
		return JobSpec{}, err
	}

	gracePeriod, err := time.ParseDuration(wire.StopPolicy.GracePeriod)
	if err != nil {
		return JobSpec{}, invalid("stop policy grace period", "must be a Go duration")
	}

	executionPolicy := DefaultExecutionPolicy()
	if wire.SchemaVersion == JobSpecSchemaVersion {
		if wire.ExecutionPolicy == nil {
			return JobSpec{}, invalid("job specification execution policy", "must be present")
		}
		executionPolicy, err = executionPolicyFromWire(*wire.ExecutionPolicy)
		if err != nil {
			return JobSpec{}, err
		}
	}

	return NewJobSpec(JobSpecInput{
		Executable:             wire.Executable,
		Arguments:              wire.Arguments,
		WorkingDirectory:       wire.WorkingDirectory,
		Environment:            wire.Environment.Set,
		UnsetEnvironment:       wire.Environment.Unset,
		EnvironmentInheritance: wire.Environment.Inheritance,
		Name:                   wire.Name,
		StopPolicy: StopPolicy{
			GracePeriod:     gracePeriod,
			ForceAfterGrace: wire.StopPolicy.ForceAfterGrace,
		},
		StdinPolicy:     wire.StdinPolicy,
		ExecutionPolicy: executionPolicy,
	})
}

func validatePersistedSpecShape(wire jobSpecWire) error {
	if wire.Arguments == nil {
		return invalid("job specification arguments", "must be present")
	}
	if wire.Environment.Inheritance == "" || wire.Environment.Set == nil || wire.Environment.Unset == nil {
		return invalid("job specification environment", "must contain inheritance, set, and unset fields")
	}
	if wire.StdinPolicy == "" {
		return invalid("job specification stdin policy", "must be present")
	}
	if wire.SchemaVersion == JobSpecSchemaVersion && wire.ExecutionPolicy == nil {
		return invalid("job specification execution policy", "must be present")
	}

	return nil
}

// Validate checks all immutable specification invariants.
func (specification JobSpec) Validate() error {
	if err := validateCommand(specification.executable, specification.arguments); err != nil {
		return err
	}
	if err := validateWorkingDirectory(specification.workingDirectory); err != nil {
		return err
	}
	if err := validateEnvironmentPolicy(specification); err != nil {
		return err
	}
	if err := validateJobName(specification.name); err != nil {
		return err
	}
	if specification.stopPolicy.GracePeriod < 0 {
		return invalid("stop policy grace period", "must not be negative")
	}
	if specification.stdinPolicy != StdinNull && specification.stdinPolicy != StdinLive &&
		specification.stdinPolicy != StdinFile && specification.stdinPolicy != StdinInherit {
		return invalid("stdin policy", "is unknown")
	}
	if err := specification.executionPolicy.Validate(specification.stdinPolicy); err != nil {
		return err
	}

	return nil
}

func validateCommand(executable string, arguments []string) error {
	if executable == "" {
		return invalid("executable", "must not be empty")
	}
	if strings.ContainsRune(executable, '\x00') {
		return invalid("executable", "must not contain NUL")
	}

	for _, argument := range arguments {
		if strings.ContainsRune(argument, '\x00') {
			return invalid("argument", "must not contain NUL")
		}
	}

	return nil
}

func validateWorkingDirectory(directory string) error {
	if directory == "" || directory == "." {
		return invalid("working directory", "must not be empty")
	}
	if !filepath.IsAbs(directory) {
		return invalid("working directory", "must be absolute")
	}
	if strings.ContainsRune(directory, '\x00') {
		return invalid("working directory", "must not contain NUL")
	}

	return nil
}

func validateEnvironmentPolicy(specification JobSpec) error {
	if specification.environmentInheritance != EnvironmentInheritSubmission {
		return invalid("environment inheritance", "is unsupported")
	}

	return validateEnvironment(specification.environment, specification.unsetEnvironment)
}

func validateJobName(name string) error {
	if name == "" {
		return nil
	}
	if strings.TrimSpace(name) == "" {
		return invalid("job name", "must contain a non-space character")
	}
	if containsControl(name) {
		return invalid("job name", "must not contain control characters")
	}

	return nil
}

// CanonicalJSON returns the stable versioned JSON representation.
func (specification JobSpec) CanonicalJSON() ([]byte, error) {
	if err := specification.Validate(); err != nil {
		return nil, err
	}

	encoded, err := json.Marshal(specification.wire())
	if err != nil {
		return nil, fmt.Errorf("encode job specification: %w", err)
	}

	return encoded, nil
}

// MarshalJSON implements json.Marshaler using the canonical representation.
func (specification JobSpec) MarshalJSON() ([]byte, error) {
	return specification.CanonicalJSON()
}

// UnmarshalJSON implements json.Unmarshaler with strict schema validation.
func (specification *JobSpec) UnmarshalJSON(data []byte) error {
	parsed, err := ParseJobSpecJSON(data)
	if err != nil {
		return err
	}

	*specification = parsed

	return nil
}

// SchemaVersion returns the persisted specification schema version.
func (JobSpec) SchemaVersion() int { return JobSpecSchemaVersion }

// Executable returns the submitted executable.
func (specification JobSpec) Executable() string { return specification.executable }

// Arguments returns a defensive copy of the ordered arguments.
func (specification JobSpec) Arguments() []string {
	arguments := make([]string, len(specification.arguments))
	copy(arguments, specification.arguments)

	return arguments
}

// WorkingDirectory returns the canonical absolute working directory.
func (specification JobSpec) WorkingDirectory() string { return specification.workingDirectory }

// Environment returns a defensive copy of explicit environment additions.
func (specification JobSpec) Environment() map[string]string {
	return cloneStringMap(specification.environment)
}

// UnsetEnvironment returns a sorted copy of explicit environment removals.
func (specification JobSpec) UnsetEnvironment() []string {
	unset := make([]string, len(specification.unsetEnvironment))
	copy(unset, specification.unsetEnvironment)

	return unset
}

// EnvironmentInheritance returns the base-environment policy.
func (specification JobSpec) EnvironmentInheritance() EnvironmentInheritancePolicy {
	return specification.environmentInheritance
}

// Name returns the optional display name.
func (specification JobSpec) Name() string { return specification.name }

// StopPolicy returns the immutable stop policy.
func (specification JobSpec) StopPolicy() StopPolicy { return specification.stopPolicy }

// StdinPolicy returns the target stdin policy.
func (specification JobSpec) StdinPolicy() StdinPolicy { return specification.stdinPolicy }

// ExecutionPolicy returns a defensive copy of retry, timeout, waiting,
// admission, interaction, and notification policy.
func (specification JobSpec) ExecutionPolicy() ExecutionPolicy {
	return cloneExecutionPolicy(specification.executionPolicy)
}

func (specification JobSpec) wire() jobSpecWire {
	return jobSpecWire{
		SchemaVersion:    JobSpecSchemaVersion,
		Executable:       specification.executable,
		Arguments:        specification.Arguments(),
		WorkingDirectory: specification.workingDirectory,
		Environment: environmentWire{
			Inheritance: specification.environmentInheritance,
			Set:         specification.Environment(),
			Unset:       specification.UnsetEnvironment(),
		},
		Name: specification.name,
		StopPolicy: stopPolicyWire{
			GracePeriod:     specification.stopPolicy.GracePeriod.String(),
			ForceAfterGrace: specification.stopPolicy.ForceAfterGrace,
		},
		StdinPolicy: specification.stdinPolicy,
		ExecutionPolicy: func() *executionPolicyWire {
			wire := executionPolicyToWire(specification.executionPolicy)
			return &wire
		}(),
	}
}

func validateEnvironment(environment map[string]string, unset []string) error {
	for name, value := range environment {
		if !validEnvironmentName(name) {
			return invalid("environment variable name", fmt.Sprintf("%q is invalid", name))
		}
		if strings.ContainsRune(value, '\x00') {
			return invalid("environment variable value", fmt.Sprintf("%q contains NUL", name))
		}
	}

	for _, name := range unset {
		if !validEnvironmentName(name) {
			return invalid("unset environment variable name", fmt.Sprintf("%q is invalid", name))
		}
		if _, exists := environment[name]; exists {
			return invalid("environment", fmt.Sprintf("%q is both set and unset", name))
		}
	}

	return nil
}

func validEnvironmentName(name string) bool {
	if name == "" || (name[0] != '_' && !isASCIILetter(name[0])) {
		return false
	}

	for index := 1; index < len(name); index++ {
		character := name[index]
		if character != '_' && !isASCIILetter(character) && (character < '0' || character > '9') {
			return false
		}
	}

	return true
}

func isASCIILetter(character byte) bool {
	return character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z'
}

func containsControl(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) {
			return true
		}
	}

	return false
}

func cloneStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}

	clone := make(map[string]string, len(source))
	for key, value := range source {
		clone[key] = value
	}

	return clone
}

func normalizeStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	clone := append([]string(nil), values...)
	sort.Strings(clone)
	result := clone[:0]
	for _, value := range clone {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}

	return result
}

func rejectDuplicateJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := scanJSONValue(decoder); err != nil {
		return err
	}

	return requireJSONEnd(decoder)
}

func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}

	delimiter, composite := token.(json.Delim)
	if !composite {
		return nil
	}

	switch delimiter {
	case '{':
		return scanJSONObject(decoder)
	case '[':
		return scanJSONArray(decoder)
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
}

func scanJSONObject(decoder *json.Decoder) error {
	seen := make(map[string]struct{})
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := token.(string)
		if !ok {
			return invalid("JSON object", "contains a non-string key")
		}
		if _, duplicate := seen[key]; duplicate {
			return invalid("JSON object", fmt.Sprintf("contains duplicate key %q", key))
		}
		seen[key] = struct{}{}
		if err := scanJSONValue(decoder); err != nil {
			return err
		}
	}

	_, err := decoder.Token()

	return err
}

func scanJSONArray(decoder *json.Decoder) error {
	for decoder.More() {
		if err := scanJSONValue(decoder); err != nil {
			return err
		}
	}

	_, err := decoder.Token()

	return err
}

func requireJSONEnd(decoder *json.Decoder) error {
	var trailing any
	err := decoder.Decode(&trailing)
	if err == io.EOF {
		return nil
	}
	if err == nil {
		return invalid("JSON", "contains trailing data")
	}

	return err
}
