package jobman

import (
	"errors"
	"fmt"
	"math"
	"net"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ryancswallace/jobman/internal/app"
	"github.com/ryancswallace/jobman/internal/config"
	"github.com/ryancswallace/jobman/internal/model"
	"github.com/ryancswallace/jobman/internal/policy"
)

const defaultWaitPollInterval = 250 * time.Millisecond

//nolint:gocognit,cyclop // Conversion intentionally maps every independent strict configuration policy.
func submitRequestFromConfig(
	configuration config.Config,
	specification config.JobSpec,
) (app.SubmitRequest, error) {
	request := app.SubmitRequest{
		Name:             specification.Name,
		Environment:      cloneStringMap(specification.Environment.Set),
		UnsetEnvironment: slices.Clone(specification.Environment.Unset),
		ExecutionPolicy:  model.DefaultExecutionPolicy(),
	}
	if len(specification.Command) > 0 {
		request.Executable = specification.Command[0]
		request.Arguments = slices.Clone(specification.Command[1:])
	}
	if specification.WorkingDirectory != "" {
		absolute, err := filepath.Abs(specification.WorkingDirectory)
		if err != nil {
			return app.SubmitRequest{}, fmt.Errorf("resolve configured working directory: %w", err)
		}
		request.WorkingDirectory = filepath.Clean(absolute)
	}

	request.StdinPolicy = model.StdinPolicy(specification.Stdin)
	if request.StdinPolicy == "" {
		request.StdinPolicy = model.StdinNull
	}
	if grace, configured := specification.Stop.GracePeriod.Value(); configured {
		request.StopPolicy = model.StopPolicy{
			GracePeriod: grace, ForceAfterGrace: specification.Stop.ForceAfterGrace,
		}
		request.StopPolicySet = true
	}

	completion, err := completionFromConfig(specification.Completion)
	if err != nil {
		return app.SubmitRequest{}, err
	}
	request.ExecutionPolicy.Completion = completion
	request.ExecutionPolicy.Classification = classificationFromConfig(specification.Completion)
	request.ExecutionPolicy.FailureDelay, err = delayFromConfig(specification.Delay)
	if err != nil {
		return app.SubmitRequest{}, err
	}
	request.ExecutionPolicy.SuccessDelay = request.ExecutionPolicy.FailureDelay
	if value, finite := specification.Timeouts.Run.Value(); finite {
		request.ExecutionPolicy.RunTimeout = value
	}
	if value, finite := specification.Timeouts.Job.Value(); finite {
		request.ExecutionPolicy.JobTimeout = value
	}
	request.ExecutionPolicy.Concurrency = model.ConcurrencyPolicy{
		Pool:  specification.Admission.Pool,
		Slots: uint64(specification.Admission.Slots), //nolint:gosec // Configuration validation requires a positive int.
	}
	if request.ExecutionPolicy.Concurrency.Slots == 0 {
		request.ExecutionPolicy.Concurrency.Slots = 1
	}
	request.ExecutionPolicy.Groups = slices.Clone(specification.Groups)
	request.ExecutionPolicy.Tags = slices.Clone(specification.Tags)
	request.ExecutionPolicy.SecretEnv, err = secretEnvironmentFromConfig(configuration, specification.Environment.Secrets)
	if err != nil {
		return app.SubmitRequest{}, err
	}
	request.Dependencies, err = dependenciesFromConfig(specification.Dependencies)
	if err != nil {
		return app.SubmitRequest{}, err
	}
	request.ExecutionPolicy.WaitMode = policy.WaitMode(specification.Wait.Mode)
	if request.ExecutionPolicy.WaitMode == "" {
		request.ExecutionPolicy.WaitMode = policy.WaitModeAll
	}
	request.ExecutionPolicy.WaitConditions, err = waitsFromConfig(configuration, specification.Wait)
	if err != nil {
		return app.SubmitRequest{}, err
	}
	if size, finite := specification.Logging.SegmentBytes.Value(); finite {
		if size > math.MaxInt64 {
			return app.SubmitRequest{}, errors.New("configured log segment size exceeds the supported range")
		}
		request.ExecutionPolicy.LogRotateSize = int64(size)
	}
	if segments, finite := specification.Logging.SegmentsPerRun.Value(); finite {
		if segments > uint64(^uint16(0)) {
			return app.SubmitRequest{}, errors.New("configured log segment count exceeds the supported range")
		}
		request.ExecutionPolicy.LogMaxSegmentsPerStream = int(segments)
	}
	request.ExecutionPolicy.LogCapture = specification.Logging.Capture
	retention := specification.Logging.CompletedLogMaxAge
	if !retention.IsSet() {
		retention = configuration.Retention.CompletedLogMaxAge
	}
	if retention.IsUnlimited() {
		request.ExecutionPolicy.LogRetentionUnlimited = true
	} else if maximum, finite := retention.Value(); finite {
		request.ExecutionPolicy.LogRetentionMaxAge = maximum
	}
	request.ExecutionPolicy.LogRetentionConfigured = true
	request.ExecutionPolicy.Notifications = notificationSubscriptions(configuration, specification.Notification)
	request.ExecutionPolicy.NotifierDefinitions, err = notifierDefinitions(
		configuration,
		request.ExecutionPolicy.Notifications,
	)
	if err != nil {
		return app.SubmitRequest{}, err
	}

	return request, nil
}

func completionFromConfig(configuration config.CompletionPolicy) (policy.CompletionPolicy, error) {
	maxRuns, err := limitFromConfig("max runs", configuration.MaxRuns)
	if err != nil {
		return policy.CompletionPolicy{}, err
	}
	successes, err := limitFromConfig("success target", configuration.SuccessTarget)
	if err != nil {
		return policy.CompletionPolicy{}, err
	}
	failures, err := limitFromConfig("failure limit", configuration.MaxFailures)
	if err != nil {
		return policy.CompletionPolicy{}, err
	}

	return policy.CompletionPolicy{MaxRuns: maxRuns, SuccessTarget: successes, FailureLimit: failures}, nil
}

func limitFromConfig(name string, configured config.IntegerLimit) (policy.Limit, error) {
	if configured.IsUnlimited() {
		return policy.UnlimitedLimit(), nil
	}
	if value, finite := configured.Value(); finite {
		limit, err := policy.FiniteLimit(value)
		if err != nil {
			return policy.Limit{}, fmt.Errorf("configured %s: %w", name, err)
		}
		return limit, nil
	}

	return policy.FiniteLimit(1)
}

func classificationFromConfig(configuration config.CompletionPolicy) policy.ClassificationPolicy {
	var retryable []policy.ExitCodeRange
	if len(configuration.RetryableExitCodes) > 0 {
		retryable = make([]policy.ExitCodeRange, len(configuration.RetryableExitCodes))
	}
	for index, code := range configuration.RetryableExitCodes {
		retryable[index] = policy.ExitCodeRange{First: code, Last: code}
	}
	return policy.ClassificationPolicy{
		SuccessExitCodes:   slices.Clone(configuration.SuccessExitCodes),
		RetryableExitCodes: retryable,
		RetryTimeout:       configuration.RetryTimeouts,
		RetryStartFailure:  configuration.RetryStartFailures,
		RetryCancellation:  false,
	}
}

func delayFromConfig(configuration config.DelayPolicy) (policy.DelayPolicy, error) {
	initial, _ := configuration.Initial.Value()
	jitter, _ := configuration.Jitter.Value()
	result := policy.DelayPolicy{
		Base: initial, Backoff: policy.Backoff(configuration.Strategy),
		ExponentialBase: uint64(configuration.Base), Jitter: jitter,
	}
	if result.Backoff == "" {
		result.Backoff = policy.BackoffConstant
	}
	if result.ExponentialBase == 0 {
		result.ExponentialBase = 2
	}
	if maximum, finite := configuration.MaxDelay.Value(); finite {
		result.MaxDelay = maximum
		result.HasMaxDelay = true
	}
	if err := result.Validate(); err != nil {
		return policy.DelayPolicy{}, fmt.Errorf("configured delay: %w", err)
	}

	return result, nil
}

func secretEnvironmentFromConfig(
	configuration config.Config,
	bindings map[string]string,
) (map[string]model.SecretReference, error) {
	result := make(map[string]model.SecretReference, len(bindings))
	for environmentName, registryName := range bindings {
		reference, found := configuration.Secrets[registryName]
		if !found {
			return nil, fmt.Errorf("secret environment %q references unknown secret %q", environmentName, registryName)
		}
		result[environmentName] = model.SecretReference{
			Provider: reference.Provider(), Name: reference.Locator(),
		}
	}

	return result, nil
}

func dependenciesFromConfig(configured []config.Dependency) ([]app.DependencyRequest, error) {
	result := make([]app.DependencyRequest, 0, len(configured))
	for _, dependency := range configured {
		predicate, err := dependencyPredicate(dependency.Outcomes)
		if err != nil {
			return nil, fmt.Errorf("dependency %q: %w", dependency.Job, err)
		}
		result = append(result, app.DependencyRequest{Selector: dependency.Job, Predicate: predicate})
	}

	return result, nil
}

func dependencyPredicate(outcomes []string) (string, error) {
	if len(outcomes) == 0 {
		return "", errors.New("at least one outcome is required")
	}
	canonical := slices.Clone(outcomes)
	for index, outcome := range canonical {
		if !model.JobOutcome(outcome).Valid() || outcome == "" {
			return "", fmt.Errorf("unknown outcome %q", outcome)
		}
		canonical[index] = outcome
	}
	sort.Strings(canonical)
	canonical = slices.Compact(canonical)
	if len(canonical) == 1 {
		if canonical[0] == string(model.JobOutcomeFailure) {
			return "failed", nil
		}
		return canonical[0], nil
	}

	return "outcomes:" + strings.Join(canonical, ","), nil
}

//nolint:gocognit,cyclop // The wait-condition union requires kind-specific conversion and validation.
func waitsFromConfig(configuration config.Config, configured config.WaitPolicy) ([]model.WaitCondition, error) {
	abortAt, err := parseOptionalTimestamp(configured.AbortAt)
	if err != nil {
		return nil, fmt.Errorf("configured wait abort time: %w", err)
	}
	result := make([]model.WaitCondition, 0, len(configured.Conditions))
	for _, name := range configured.Conditions {
		condition, found := configuration.WaitConditions[name]
		if !found {
			return nil, fmt.Errorf("unknown wait condition %q", name)
		}
		kind := model.WaitConditionKind(condition.Type)
		if condition.Type == "file-exists" {
			kind = model.WaitFileExists
		}
		converted := model.WaitCondition{Kind: kind, PollInterval: defaultWaitPollInterval, AbortAt: abortAt}
		switch converted.Kind {
		case model.WaitUntil:
			converted.Until, err = parseOptionalTimestamp(condition.Until)
		case model.WaitDelay:
			converted.Delay, _ = condition.Delay.Value()
		case model.WaitFileExists:
			if condition.FileExists == nil {
				return nil, fmt.Errorf("wait condition %q is missing file_exists", name)
			}
			converted.Path = condition.FileExists.Path
			converted.FileKind = policy.FileKind(condition.FileExists.Type)
			switch converted.FileKind {
			case "":
				converted.FileKind = policy.FileKindAny
			case "file":
				converted.FileKind = policy.FileKindRegular
			default:
				// Other validated file kinds retain their canonical value.
			}
		case model.WaitProbe:
			if condition.Probe == nil {
				return nil, fmt.Errorf("wait condition %q is missing probe", name)
			}
			probe := condition.Probe
			timeout, _ := probe.Timeout.Value()
			poll, _ := probe.PollInterval.Value()
			output, _ := probe.OutputLimit.Value()
			converted.PollInterval = poll
			if output > math.MaxInt64 {
				return nil, fmt.Errorf("wait condition %q output limit exceeds the supported range", name)
			}
			converted.Probe = policy.ProbeSpec{
				Executable: probe.Command[0], Arguments: slices.Clone(probe.Command[1:]),
				Timeout:      timeout,
				OutputLimit:  int64(output),
				FatalOnError: probe.FatalOnError,
			}
			converted.ProbeDirectory = probe.WorkingDirectory
			converted.ProbeEnvironment = cloneStringMap(probe.Environment.Set)
			converted.ProbeUnsetEnvironment = slices.Clone(probe.Environment.Unset)
			converted.ProbeSecretEnv, err = secretEnvironmentFromConfig(configuration, probe.Environment.Secrets)
		default:
			return nil, fmt.Errorf("wait condition %q has unsupported type %q", name, condition.Type)
		}
		if err != nil {
			return nil, fmt.Errorf("wait condition %q: %w", name, err)
		}
		if err := converted.Validate(); err != nil {
			return nil, fmt.Errorf("wait condition %q: %w", name, err)
		}
		result = append(result, converted)
	}

	return result, nil
}

func notificationSubscriptions(
	configuration config.Config,
	configured config.NotificationPolicy,
) []model.NotificationSubscription {
	result := make([]model.NotificationSubscription, 0, len(configured.Notifiers))
	for _, name := range configured.Notifiers {
		events := slices.Clone(configured.Events)
		if len(events) == 0 {
			events = slices.Clone(configuration.Notifiers[name].Events)
		}
		result = append(result, model.NotificationSubscription{Notifier: name, Events: events})
	}

	return result
}

//nolint:gocognit,cyclop // The validated notifier union requires transport-specific conversion and secret references.
func notifierDefinitions(
	configuration config.Config,
	subscriptions []model.NotificationSubscription,
) ([]model.NotifierDefinition, error) {
	definitions := make([]model.NotifierDefinition, 0, len(subscriptions))
	seen := make(map[string]struct{}, len(subscriptions))
	for _, subscription := range subscriptions {
		if _, duplicate := seen[subscription.Notifier]; duplicate {
			continue
		}
		seen[subscription.Notifier] = struct{}{}
		configured, found := configuration.Notifiers[subscription.Notifier]
		if !found {
			return nil, fmt.Errorf("unknown notifier %q", subscription.Notifier)
		}
		timeout, _ := configured.Timeout.Value()
		delay, _ := configured.Retry.Delay.Value()
		maxDelay, _ := configured.Retry.MaxDelay.Value()
		definition := model.NotifierDefinition{
			Name: subscription.Notifier, Kind: model.NotifierKind(configured.Type), Timeout: timeout,
			Retry: model.NotifierRetryPolicy{
				MaxAttempts: configured.Retry.MaxAttempts, Delay: delay, MaxDelay: maxDelay,
			},
		}
		switch configured.Type {
		case "command":
			if configured.Command == nil || len(configured.Command.Command) == 0 {
				return nil, fmt.Errorf("notifier %q has no command", subscription.Notifier)
			}
			if !filepath.IsAbs(configured.Command.Command[0]) {
				return nil, fmt.Errorf("command notifier %q executable must be absolute", subscription.Notifier)
			}
			outputLimit, _ := configured.Command.OutputLimit.Value()
			if outputLimit > math.MaxInt64 {
				return nil, fmt.Errorf("command notifier %q output limit exceeds the supported range", subscription.Notifier)
			}
			secrets, err := secretEnvironmentFromConfig(configuration, configured.Command.Environment.Secrets)
			if err != nil {
				return nil, fmt.Errorf("command notifier %q: %w", subscription.Notifier, err)
			}
			definition.Command = &model.CommandNotifierDefinition{
				Executable: configured.Command.Command[0], Arguments: slices.Clone(configured.Command.Command[1:]),
				Environment: cloneStringMap(configured.Command.Environment.Set), SecretEnvironment: secrets,
				OutputLimit: int64(outputLimit),
			}
		case "http":
			if configured.HTTP == nil {
				return nil, fmt.Errorf("notifier %q has no HTTP configuration", subscription.Notifier)
			}
			secretHeaders := make(map[string]model.SecretReference, len(configured.HTTP.SecretHeaders))
			for header, name := range configured.HTTP.SecretHeaders {
				reference, ok := configuration.Secrets[name]
				if !ok {
					return nil, fmt.Errorf("HTTP notifier %q references unknown secret %q", subscription.Notifier, name)
				}
				secretHeaders[header] = model.SecretReference{Provider: reference.Provider(), Name: reference.Locator()}
			}
			var signing *model.SecretReference
			if configured.HTTP.SigningSecret != "" {
				reference := configuration.Secrets[configured.HTTP.SigningSecret]
				value := model.SecretReference{Provider: reference.Provider(), Name: reference.Locator()}
				signing = &value
			}
			definition.Webhook = &model.WebhookNotifierDefinition{
				URL: configured.HTTP.EffectiveURL(), Headers: cloneStringMap(configured.HTTP.Headers),
				SecretHeaders: secretHeaders, SigningSecret: signing, ResponseLimit: 64 * 1024,
				AllowInsecureHTTP:   configured.HTTP.AllowHTTP,
				AllowPrivateNetwork: configured.HTTP.AllowPrivateHosts,
				FollowRedirects:     configured.HTTP.FollowRedirects,
			}
		case "smtp":
			if configured.SMTP == nil {
				return nil, fmt.Errorf("notifier %q has no SMTP configuration", subscription.Notifier)
			}
			host, _, splitErr := net.SplitHostPort(configured.SMTP.Address)
			if splitErr != nil {
				return nil, fmt.Errorf("SMTP notifier %q address: %w", subscription.Notifier, splitErr)
			}
			var password *model.SecretReference
			if configured.SMTP.PasswordSecret != "" {
				reference := configuration.Secrets[configured.SMTP.PasswordSecret]
				value := model.SecretReference{Provider: reference.Provider(), Name: reference.Locator()}
				password = &value
			}
			definition.SMTP = &model.SMTPNotifierDefinition{
				Address: configured.SMTP.Address, ServerName: host, Username: configured.SMTP.Username,
				PasswordSecret: password, From: configured.SMTP.From, To: slices.Clone(configured.SMTP.To),
				SubjectPrefix: configured.SMTP.SubjectPrefix, Mode: configured.SMTP.TLS, MessageLimit: 1024 * 1024,
			}
		default:
			return nil, fmt.Errorf("notifier %q has unsupported type %q", subscription.Notifier, configured.Type)
		}
		if err := definition.Validate(); err != nil {
			return nil, fmt.Errorf("notifier %q: %w", subscription.Notifier, err)
		}
		definitions = append(definitions, definition)
	}

	return definitions, nil
}

func parseOptionalTimestamp(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, errors.New("must be an RFC3339 timestamp")
	}

	return parsed.UTC(), nil
}

func parseLimitFlag(name, value string) (policy.Limit, error) {
	if value == config.Unlimited {
		return policy.UnlimitedLimit(), nil
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil || parsed == 0 {
		return policy.Limit{}, fmt.Errorf("--%s must be a positive integer or %q", name, config.Unlimited)
	}

	return policy.FiniteLimit(parsed)
}

func appendDependencyFlags(options *runOptions) ([]app.DependencyRequest, error) {
	result := make([]app.DependencyRequest, 0,
		len(options.afterSuccess)+len(options.afterFinish)+len(options.afterFailed)+len(options.afterOutcome))
	for _, selector := range options.afterSuccess {
		result = append(result, app.DependencyRequest{Selector: selector, Predicate: "success"})
	}
	for _, selector := range options.afterFinish {
		result = append(result, app.DependencyRequest{Selector: selector, Predicate: "finish"})
	}
	for _, selector := range options.afterFailed {
		result = append(result, app.DependencyRequest{Selector: selector, Predicate: "failed"})
	}
	for _, encoded := range options.afterOutcome {
		selector, values, ok := strings.Cut(encoded, "=")
		if !ok || strings.TrimSpace(selector) == "" {
			return nil, fmt.Errorf("invalid --after-outcome %q: expected JOB=OUTCOME[,OUTCOME...]", encoded)
		}
		predicate, err := dependencyPredicate(strings.Split(values, ","))
		if err != nil {
			return nil, fmt.Errorf("invalid --after-outcome %q: %w", encoded, err)
		}
		result = append(result, app.DependencyRequest{Selector: selector, Predicate: predicate})
	}

	return result, nil
}

func flagChanged(command *cobra.Command, name string) bool {
	return command.Flags().Changed(name)
}

func cloneStringMap(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for name, value := range source {
		result[name] = value
	}

	return result
}
