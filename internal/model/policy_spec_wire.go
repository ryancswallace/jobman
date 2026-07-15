package model

import (
	"fmt"
	"time"

	"github.com/ryancswallace/jobman/internal/policy"
)

type executionPolicyWire struct {
	Completion              completionPolicyWire        `json:"completion"`
	Classification          classificationPolicyWire    `json:"classification"`
	FailureDelay            delayPolicyWire             `json:"failure_delay"`
	SuccessDelay            delayPolicyWire             `json:"success_delay"`
	RunTimeout              string                      `json:"run_timeout"`
	JobTimeout              string                      `json:"job_timeout"`
	WaitMode                policy.WaitMode             `json:"wait_mode"`
	WaitConditions          []waitConditionWire         `json:"wait_conditions"`
	Dependencies            []dependencyRequirementWire `json:"dependencies"`
	Concurrency             concurrencyPolicyWire       `json:"concurrency"`
	Notifications           []notificationWire          `json:"notifications"`
	NotifierDefinitions     []notifierDefinitionWire    `json:"notifier_definitions"`
	Tags                    []string                    `json:"tags"`
	Groups                  []string                    `json:"groups"`
	SecretEnv               map[string]SecretReference  `json:"secret_environment"`
	Foreground              bool                        `json:"foreground"`
	StdinPath               string                      `json:"stdin_path,omitempty"`
	LogRotateSize           int64                       `json:"log_rotate_size"`
	LogMaxSegmentsPerStream int                         `json:"log_max_segments_per_stream"`
	LogCapture              string                      `json:"log_capture"`
	LogRetentionMaxAge      string                      `json:"log_retention_max_age"`
	LogRetentionUnlimited   bool                        `json:"log_retention_unlimited"`
	LogRetentionConfigured  bool                        `json:"log_retention_configured"`
}

type completionPolicyWire struct {
	MaxRuns       limitWire `json:"max_runs"`
	SuccessTarget limitWire `json:"success_target"`
	FailureLimit  limitWire `json:"failure_limit"`
	RetryAbortAt  string    `json:"retry_abort_at,omitempty"`
}

type limitWire struct {
	Value     uint64 `json:"value,omitempty"`
	Unlimited bool   `json:"unlimited"`
}

type classificationPolicyWire struct {
	SuccessExitCodes         []int               `json:"success_exit_codes"`
	RetryableExitCodes       []exitCodeRangeWire `json:"retryable_exit_codes"`
	RetryableSignals         []string            `json:"retryable_signals"`
	RetryablePlatformReasons []string            `json:"retryable_platform_reasons"`
	RetryTimeout             bool                `json:"retry_timeout"`
	RetryStartFailure        bool                `json:"retry_start_failure"`
	RetryCancellation        bool                `json:"retry_cancellation"`
}

// The capitalized field names preserve the schema-v2 representation emitted
// before ExitCodeRange received a dedicated wire type.
type exitCodeRangeWire struct {
	First int `json:"First"`
	Last  int `json:"Last"`
}

type delayPolicyWire struct {
	Base            string         `json:"base"`
	Backoff         policy.Backoff `json:"backoff"`
	ExponentialBase uint64         `json:"exponential_base"`
	MaxDelay        *string        `json:"max_delay"`
	Jitter          string         `json:"jitter"`
}

type waitConditionWire struct {
	Kind         WaitConditionKind `json:"kind"`
	Until        string            `json:"until,omitempty"`
	Delay        string            `json:"delay,omitempty"`
	Path         string            `json:"path,omitempty"`
	FileKind     policy.FileKind   `json:"file_kind,omitempty"`
	Probe        *probeWire        `json:"probe,omitempty"`
	PollInterval string            `json:"poll_interval"`
	AbortAt      string            `json:"abort_at,omitempty"`
}

type probeWire struct {
	Executable       string                     `json:"executable"`
	Arguments        []string                   `json:"arguments"`
	WorkingDirectory string                     `json:"working_directory"`
	Environment      map[string]string          `json:"environment"`
	UnsetEnvironment []string                   `json:"unset_environment"`
	SecretEnv        map[string]SecretReference `json:"secret_environment"`
	Timeout          string                     `json:"timeout"`
	OutputLimit      int64                      `json:"output_limit"`
	FatalOnError     bool                       `json:"fatal_on_error"`
}

type dependencyRequirementWire struct {
	JobID     string `json:"job_id"`
	Predicate string `json:"predicate"`
}

type concurrencyPolicyWire struct {
	Pool  string `json:"pool,omitempty"`
	Slots uint64 `json:"slots"`
}

type notificationWire struct {
	Notifier string   `json:"notifier"`
	Events   []string `json:"events"`
}

type notifierDefinitionWire struct {
	Name    string                  `json:"name"`
	Kind    NotifierKind            `json:"kind"`
	Timeout string                  `json:"timeout"`
	Retry   notifierRetryPolicyWire `json:"retry"`
	Command *commandNotifierWire    `json:"command,omitempty"`
	Webhook *webhookNotifierWire    `json:"http,omitempty"`
	SMTP    *smtpNotifierWire       `json:"smtp,omitempty"`
}

type notifierRetryPolicyWire struct {
	MaxAttempts int    `json:"max_attempts"`
	Delay       string `json:"delay"`
	MaxDelay    string `json:"max_delay"`
}

type commandNotifierWire struct {
	Executable        string                         `json:"executable"`
	Arguments         []string                       `json:"arguments"`
	WorkingDirectory  string                         `json:"working_directory,omitempty"`
	Environment       map[string]string              `json:"environment"`
	SecretEnvironment map[string]secretReferenceWire `json:"secret_environment"`
	OutputLimit       int64                          `json:"output_limit"`
}

type webhookNotifierWire struct {
	URL                 string                         `json:"url"`
	Headers             map[string]string              `json:"headers"`
	SecretHeaders       map[string]secretReferenceWire `json:"secret_headers"`
	SigningSecret       *secretReferenceWire           `json:"signing_secret,omitempty"`
	SignatureHeader     string                         `json:"signature_header,omitempty"`
	ResponseLimit       int64                          `json:"response_limit"`
	AllowInsecureHTTP   bool                           `json:"allow_insecure_http"`
	AllowPrivateNetwork bool                           `json:"allow_private_network"`
	FollowRedirects     bool                           `json:"follow_redirects"`
}

type smtpNotifierWire struct {
	Address        string               `json:"address"`
	ServerName     string               `json:"server_name,omitempty"`
	Username       string               `json:"username,omitempty"`
	PasswordSecret *secretReferenceWire `json:"password_secret,omitempty"`
	From           string               `json:"from"`
	To             []string             `json:"to"`
	SubjectPrefix  string               `json:"subject_prefix,omitempty"`
	Mode           string               `json:"mode"`
	MessageLimit   int64                `json:"message_limit"`
}

type secretReferenceWire struct {
	Provider string `json:"provider"`
	Name     string `json:"name"`
}

func executionPolicyToWire(configuration ExecutionPolicy) executionPolicyWire {
	return executionPolicyWire{
		Completion: completionPolicyWire{
			MaxRuns:       limitToWire(configuration.Completion.MaxRuns),
			SuccessTarget: limitToWire(configuration.Completion.SuccessTarget),
			FailureLimit:  limitToWire(configuration.Completion.FailureLimit),
			RetryAbortAt:  optionalTimestamp(configuration.Completion.RetryAbortAt, configuration.Completion.HasRetryAbortAt),
		},
		Classification: classificationPolicyWire{
			SuccessExitCodes:         configuration.Classification.SuccessExitCodes,
			RetryableExitCodes:       exitCodeRangesToWire(configuration.Classification.RetryableExitCodes),
			RetryableSignals:         configuration.Classification.RetryableSignals,
			RetryablePlatformReasons: configuration.Classification.RetryablePlatformReasons,
			RetryTimeout:             configuration.Classification.RetryTimeout,
			RetryStartFailure:        configuration.Classification.RetryStartFailure,
			RetryCancellation:        configuration.Classification.RetryCancellation,
		},
		FailureDelay:            delayToWire(configuration.FailureDelay),
		SuccessDelay:            delayToWire(configuration.SuccessDelay),
		RunTimeout:              configuration.RunTimeout.String(),
		JobTimeout:              configuration.JobTimeout.String(),
		WaitMode:                configuration.WaitMode,
		WaitConditions:          waitsToWire(configuration.WaitConditions),
		Dependencies:            dependenciesToWire(configuration.Dependencies),
		Concurrency:             concurrencyPolicyWire{Pool: configuration.Concurrency.Pool, Slots: configuration.Concurrency.Slots},
		Notifications:           notificationsToWire(configuration.Notifications),
		NotifierDefinitions:     notifierDefinitionsToWire(configuration.NotifierDefinitions),
		Tags:                    configuration.Tags,
		Groups:                  configuration.Groups,
		SecretEnv:               configuration.SecretEnv,
		Foreground:              configuration.Foreground,
		StdinPath:               configuration.StdinPath,
		LogRotateSize:           configuration.LogRotateSize,
		LogMaxSegmentsPerStream: configuration.LogMaxSegmentsPerStream,
		LogCapture:              configuration.LogCapture,
		LogRetentionMaxAge:      configuration.LogRetentionMaxAge.String(),
		LogRetentionUnlimited:   configuration.LogRetentionUnlimited,
		LogRetentionConfigured:  configuration.LogRetentionConfigured,
	}
}

func executionPolicyFromWire(wire executionPolicyWire) (ExecutionPolicy, error) {
	runTimeout, err := parseDurationField("run timeout", wire.RunTimeout)
	if err != nil {
		return ExecutionPolicy{}, err
	}
	jobTimeout, err := parseDurationField("job timeout", wire.JobTimeout)
	if err != nil {
		return ExecutionPolicy{}, err
	}
	encodedLogRetention := wire.LogRetentionMaxAge
	if encodedLogRetention == "" {
		encodedLogRetention = "0s"
	}
	logRetention, err := parseDurationField("log retention maximum age", encodedLogRetention)
	if err != nil {
		return ExecutionPolicy{}, err
	}
	failureDelay, err := delayFromWire("failure delay", wire.FailureDelay)
	if err != nil {
		return ExecutionPolicy{}, err
	}
	successDelay, err := delayFromWire("success delay", wire.SuccessDelay)
	if err != nil {
		return ExecutionPolicy{}, err
	}
	completion := policy.CompletionPolicy{
		MaxRuns:       limitFromWire(wire.Completion.MaxRuns),
		SuccessTarget: limitFromWire(wire.Completion.SuccessTarget),
		FailureLimit:  limitFromWire(wire.Completion.FailureLimit),
	}
	if wire.Completion.RetryAbortAt != "" {
		completion.RetryAbortAt, err = time.Parse(time.RFC3339Nano, wire.Completion.RetryAbortAt)
		if err != nil {
			return ExecutionPolicy{}, invalid("retry abort time", "must be RFC3339")
		}
		completion.HasRetryAbortAt = true
	}
	waits, err := waitsFromWire(wire.WaitConditions)
	if err != nil {
		return ExecutionPolicy{}, err
	}
	dependencies := make([]DependencyRequirement, len(wire.Dependencies))
	for index, encoded := range wire.Dependencies {
		id, parseErr := ParseJobID(encoded.JobID)
		if parseErr != nil {
			return ExecutionPolicy{}, fmt.Errorf("parse dependency job ID: %w", parseErr)
		}
		dependencies[index] = DependencyRequirement{JobID: id, Predicate: encoded.Predicate}
	}
	notifications := make([]NotificationSubscription, len(wire.Notifications))
	for index, encoded := range wire.Notifications {
		notifications[index] = NotificationSubscription(encoded)
	}
	notifierDefinitions, err := notifierDefinitionsFromWire(wire.NotifierDefinitions)
	if err != nil {
		return ExecutionPolicy{}, err
	}

	return ExecutionPolicy{
		Completion: completion,
		Classification: policy.ClassificationPolicy{
			SuccessExitCodes:         wire.Classification.SuccessExitCodes,
			RetryableExitCodes:       exitCodeRangesFromWire(wire.Classification.RetryableExitCodes),
			RetryableSignals:         wire.Classification.RetryableSignals,
			RetryablePlatformReasons: wire.Classification.RetryablePlatformReasons,
			RetryTimeout:             wire.Classification.RetryTimeout,
			RetryStartFailure:        wire.Classification.RetryStartFailure,
			RetryCancellation:        wire.Classification.RetryCancellation,
		},
		FailureDelay:            failureDelay,
		SuccessDelay:            successDelay,
		RunTimeout:              runTimeout,
		JobTimeout:              jobTimeout,
		WaitMode:                wire.WaitMode,
		WaitConditions:          waits,
		Dependencies:            dependencies,
		Concurrency:             ConcurrencyPolicy{Pool: wire.Concurrency.Pool, Slots: wire.Concurrency.Slots},
		Notifications:           notifications,
		NotifierDefinitions:     notifierDefinitions,
		Tags:                    wire.Tags,
		Groups:                  wire.Groups,
		SecretEnv:               wire.SecretEnv,
		Foreground:              wire.Foreground,
		StdinPath:               wire.StdinPath,
		LogRotateSize:           wire.LogRotateSize,
		LogMaxSegmentsPerStream: wire.LogMaxSegmentsPerStream,
		LogCapture:              wire.LogCapture,
		LogRetentionMaxAge:      logRetention,
		LogRetentionUnlimited:   wire.LogRetentionUnlimited,
		LogRetentionConfigured:  wire.LogRetentionConfigured,
	}, nil
}

func limitToWire(limit policy.Limit) limitWire {
	return limitWire{Value: limit.Value, Unlimited: limit.Unlimited}
}

func limitFromWire(wire limitWire) policy.Limit {
	return policy.Limit{Value: wire.Value, Unlimited: wire.Unlimited}
}

func exitCodeRangesToWire(ranges []policy.ExitCodeRange) []exitCodeRangeWire {
	if ranges == nil {
		return nil
	}
	result := make([]exitCodeRangeWire, len(ranges))
	for index, value := range ranges {
		result[index] = exitCodeRangeWire(value)
	}

	return result
}

func exitCodeRangesFromWire(ranges []exitCodeRangeWire) []policy.ExitCodeRange {
	if ranges == nil {
		return nil
	}
	result := make([]policy.ExitCodeRange, len(ranges))
	for index, value := range ranges {
		result[index] = policy.ExitCodeRange(value)
	}

	return result
}

func delayToWire(configuration policy.DelayPolicy) delayPolicyWire {
	var maximum *string
	if configuration.HasMaxDelay {
		value := configuration.MaxDelay.String()
		maximum = &value
	}

	return delayPolicyWire{
		Base: configuration.Base.String(), Backoff: configuration.Backoff,
		ExponentialBase: configuration.ExponentialBase, MaxDelay: maximum,
		Jitter: configuration.Jitter.String(),
	}
}

func delayFromWire(field string, wire delayPolicyWire) (policy.DelayPolicy, error) {
	base, err := parseDurationField(field+" base", wire.Base)
	if err != nil {
		return policy.DelayPolicy{}, err
	}
	jitter, err := parseDurationField(field+" jitter", wire.Jitter)
	if err != nil {
		return policy.DelayPolicy{}, err
	}
	configuration := policy.DelayPolicy{
		Base: base, Backoff: wire.Backoff, ExponentialBase: wire.ExponentialBase, Jitter: jitter,
	}
	if wire.MaxDelay != nil {
		configuration.MaxDelay, err = parseDurationField(field+" maximum", *wire.MaxDelay)
		configuration.HasMaxDelay = true
	}

	return configuration, err
}

func waitsToWire(conditions []WaitCondition) []waitConditionWire {
	result := make([]waitConditionWire, len(conditions))
	for index, condition := range conditions {
		wire := waitConditionWire{
			Kind:         condition.Kind,
			Delay:        condition.Delay.String(),
			Path:         condition.Path,
			FileKind:     condition.FileKind,
			PollInterval: condition.PollInterval.String(),
			Until:        optionalTimestamp(condition.Until, !condition.Until.IsZero()),
			AbortAt:      optionalTimestamp(condition.AbortAt, !condition.AbortAt.IsZero()),
		}
		if condition.Kind == WaitProbe {
			wire.Probe = &probeWire{
				Executable: condition.Probe.Executable, Arguments: condition.Probe.Arguments,
				WorkingDirectory: condition.ProbeDirectory,
				Environment:      condition.ProbeEnvironment,
				UnsetEnvironment: condition.ProbeUnsetEnvironment,
				SecretEnv:        condition.ProbeSecretEnv,
				Timeout:          condition.Probe.Timeout.String(), OutputLimit: condition.Probe.OutputLimit,
				FatalOnError: condition.Probe.FatalOnError,
			}
		}
		result[index] = wire
	}

	return result
}

func waitsFromWire(wires []waitConditionWire) ([]WaitCondition, error) {
	result := make([]WaitCondition, len(wires))
	for index, wire := range wires {
		condition, err := waitFromWire(wire)
		if err != nil {
			return nil, err
		}
		result[index] = condition
	}

	return result, nil
}

func waitFromWire(wire waitConditionWire) (WaitCondition, error) {
	poll, err := parseDurationField("wait poll interval", wire.PollInterval)
	if err != nil {
		return WaitCondition{}, err
	}
	condition := WaitCondition{Kind: wire.Kind, Path: wire.Path, FileKind: wire.FileKind, PollInterval: poll}
	if wire.Delay != "" {
		condition.Delay, err = parseDurationField("wait delay", wire.Delay)
		if err != nil {
			return WaitCondition{}, err
		}
	}
	if err := decodeWaitTimestamps(wire, &condition); err != nil {
		return WaitCondition{}, err
	}
	if err := decodeWaitProbe(wire.Probe, &condition); err != nil {
		return WaitCondition{}, err
	}

	return condition, nil
}

func decodeWaitTimestamps(wire waitConditionWire, condition *WaitCondition) error {
	var err error
	if wire.Until != "" {
		condition.Until, err = time.Parse(time.RFC3339Nano, wire.Until)
		if err != nil {
			return invalid("wait until", "must be RFC3339")
		}
	}
	if wire.AbortAt != "" {
		condition.AbortAt, err = time.Parse(time.RFC3339Nano, wire.AbortAt)
		if err != nil {
			return invalid("wait abort time", "must be RFC3339")
		}
	}

	return nil
}

func decodeWaitProbe(wire *probeWire, condition *WaitCondition) error {
	if wire == nil {
		return nil
	}
	timeout, err := parseDurationField("wait probe timeout", wire.Timeout)
	if err != nil {
		return err
	}
	condition.Probe = policy.ProbeSpec{
		Executable: wire.Executable, Arguments: wire.Arguments,
		Timeout: timeout, OutputLimit: wire.OutputLimit, FatalOnError: wire.FatalOnError,
	}
	condition.ProbeDirectory = wire.WorkingDirectory
	condition.ProbeEnvironment = wire.Environment
	condition.ProbeUnsetEnvironment = wire.UnsetEnvironment
	condition.ProbeSecretEnv = wire.SecretEnv

	return nil
}

func dependenciesToWire(dependencies []DependencyRequirement) []dependencyRequirementWire {
	result := make([]dependencyRequirementWire, len(dependencies))
	for index, dependency := range dependencies {
		result[index] = dependencyRequirementWire{JobID: dependency.JobID.String(), Predicate: dependency.Predicate}
	}

	return result
}

func notificationsToWire(subscriptions []NotificationSubscription) []notificationWire {
	result := make([]notificationWire, len(subscriptions))
	for index, subscription := range subscriptions {
		result[index] = notificationWire(subscription)
	}

	return result
}

func notifierDefinitionsToWire(definitions []NotifierDefinition) []notifierDefinitionWire {
	result := make([]notifierDefinitionWire, len(definitions))
	for index, definition := range definitions {
		wire := notifierDefinitionWire{
			Name:    definition.Name,
			Kind:    definition.Kind,
			Timeout: definition.Timeout.String(),
			Retry: notifierRetryPolicyWire{
				MaxAttempts: definition.Retry.MaxAttempts,
				Delay:       definition.Retry.Delay.String(),
				MaxDelay:    definition.Retry.MaxDelay.String(),
			},
		}
		if definition.Command != nil {
			wire.Command = &commandNotifierWire{
				Executable:        definition.Command.Executable,
				Arguments:         definition.Command.Arguments,
				WorkingDirectory:  definition.Command.WorkingDirectory,
				Environment:       definition.Command.Environment,
				SecretEnvironment: secretReferencesToWire(definition.Command.SecretEnvironment),
				OutputLimit:       definition.Command.OutputLimit,
			}
		}
		if definition.Webhook != nil {
			wire.Webhook = &webhookNotifierWire{
				URL:                 definition.Webhook.URL,
				Headers:             definition.Webhook.Headers,
				SecretHeaders:       secretReferencesToWire(definition.Webhook.SecretHeaders),
				SigningSecret:       optionalSecretReferenceToWire(definition.Webhook.SigningSecret),
				SignatureHeader:     definition.Webhook.SignatureHeader,
				ResponseLimit:       definition.Webhook.ResponseLimit,
				AllowInsecureHTTP:   definition.Webhook.AllowInsecureHTTP,
				AllowPrivateNetwork: definition.Webhook.AllowPrivateNetwork,
				FollowRedirects:     definition.Webhook.FollowRedirects,
			}
		}
		if definition.SMTP != nil {
			wire.SMTP = &smtpNotifierWire{
				Address:        definition.SMTP.Address,
				ServerName:     definition.SMTP.ServerName,
				Username:       definition.SMTP.Username,
				PasswordSecret: optionalSecretReferenceToWire(definition.SMTP.PasswordSecret),
				From:           definition.SMTP.From,
				To:             definition.SMTP.To,
				SubjectPrefix:  definition.SMTP.SubjectPrefix,
				Mode:           definition.SMTP.Mode,
				MessageLimit:   definition.SMTP.MessageLimit,
			}
		}
		result[index] = wire
	}

	return result
}

func notifierDefinitionsFromWire(wires []notifierDefinitionWire) ([]NotifierDefinition, error) {
	result := make([]NotifierDefinition, len(wires))
	for index, wire := range wires {
		timeout, err := parseDurationField("notifier timeout", wire.Timeout)
		if err != nil {
			return nil, err
		}
		delay, err := parseDurationField("notifier retry delay", wire.Retry.Delay)
		if err != nil {
			return nil, err
		}
		maximumDelay, err := parseDurationField("notifier retry maximum delay", wire.Retry.MaxDelay)
		if err != nil {
			return nil, err
		}
		definition := NotifierDefinition{
			Name: wire.Name, Kind: wire.Kind, Timeout: timeout,
			Retry: NotifierRetryPolicy{MaxAttempts: wire.Retry.MaxAttempts, Delay: delay, MaxDelay: maximumDelay},
		}
		if wire.Command != nil {
			definition.Command = &CommandNotifierDefinition{
				Executable:        wire.Command.Executable,
				Arguments:         wire.Command.Arguments,
				WorkingDirectory:  wire.Command.WorkingDirectory,
				Environment:       wire.Command.Environment,
				SecretEnvironment: secretReferencesFromWire(wire.Command.SecretEnvironment),
				OutputLimit:       wire.Command.OutputLimit,
			}
		}
		if wire.Webhook != nil {
			definition.Webhook = &WebhookNotifierDefinition{
				URL:                 wire.Webhook.URL,
				Headers:             wire.Webhook.Headers,
				SecretHeaders:       secretReferencesFromWire(wire.Webhook.SecretHeaders),
				SigningSecret:       optionalSecretReferenceFromWire(wire.Webhook.SigningSecret),
				SignatureHeader:     wire.Webhook.SignatureHeader,
				ResponseLimit:       wire.Webhook.ResponseLimit,
				AllowInsecureHTTP:   wire.Webhook.AllowInsecureHTTP,
				AllowPrivateNetwork: wire.Webhook.AllowPrivateNetwork,
				FollowRedirects:     wire.Webhook.FollowRedirects,
			}
		}
		if wire.SMTP != nil {
			definition.SMTP = &SMTPNotifierDefinition{
				Address:        wire.SMTP.Address,
				ServerName:     wire.SMTP.ServerName,
				Username:       wire.SMTP.Username,
				PasswordSecret: optionalSecretReferenceFromWire(wire.SMTP.PasswordSecret),
				From:           wire.SMTP.From,
				To:             wire.SMTP.To,
				SubjectPrefix:  wire.SMTP.SubjectPrefix,
				Mode:           wire.SMTP.Mode,
				MessageLimit:   wire.SMTP.MessageLimit,
			}
		}
		result[index] = definition
	}

	return result, nil
}

func secretReferencesToWire(references map[string]SecretReference) map[string]secretReferenceWire {
	result := make(map[string]secretReferenceWire, len(references))
	for name, reference := range references {
		result[name] = secretReferenceWire(reference)
	}

	return result
}

func secretReferencesFromWire(references map[string]secretReferenceWire) map[string]SecretReference {
	result := make(map[string]SecretReference, len(references))
	for name, reference := range references {
		result[name] = SecretReference(reference)
	}

	return result
}

func optionalSecretReferenceToWire(reference *SecretReference) *secretReferenceWire {
	if reference == nil {
		return nil
	}

	return &secretReferenceWire{Provider: reference.Provider, Name: reference.Name}
}

func optionalSecretReferenceFromWire(reference *secretReferenceWire) *SecretReference {
	if reference == nil {
		return nil
	}

	return &SecretReference{Provider: reference.Provider, Name: reference.Name}
}

func parseDurationField(field, encoded string) (time.Duration, error) {
	value, err := time.ParseDuration(encoded)
	if err != nil {
		return 0, invalid(field, "must be a Go duration")
	}

	return value, nil
}

func optionalTimestamp(value time.Time, present bool) string {
	if !present {
		return ""
	}

	return value.UTC().Format(time.RFC3339Nano)
}
