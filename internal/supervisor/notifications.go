package supervisor

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/ryancswallace/jobman/internal/config"
	"github.com/ryancswallace/jobman/internal/model"
	"github.com/ryancswallace/jobman/internal/notify"
	"github.com/ryancswallace/jobman/internal/store"
)

const (
	notificationClaimLease   = 30 * time.Second
	notificationLeaseRenewal = 10 * time.Second
)

func deliverNotifications(
	ctx context.Context,
	database *store.Store,
	job model.JobState,
	eventID model.EventID,
	eventType notify.EventType,
	_ model.RunID,
	_ time.Time,
) error {
	policy := job.Spec.ExecutionPolicy()
	if len(policy.Notifications) == 0 {
		return nil
	}
	definitions := make(map[string]model.NotifierDefinition, len(policy.NotifierDefinitions))
	for _, definition := range policy.NotifierDefinitions {
		definitions[definition.Name] = definition
	}
	work := make([]store.QueueNotificationDeliveryInput, 0, len(policy.Notifications))
	for _, subscription := range policy.Notifications {
		if !slices.Contains(subscription.Events, string(eventType)) {
			continue
		}
		maxAttempts := 1
		if definition, found := definitions[subscription.Notifier]; found {
			maxAttempts = definition.Retry.MaxAttempts
		}
		work = append(work, store.QueueNotificationDeliveryInput{
			JobID: job.ID, EventID: eventID, NotifierName: subscription.Notifier,
			EventType: string(eventType), MaxAttempts: maxAttempts,
		})
	}
	if len(work) == 0 {
		return nil
	}
	if _, err := database.QueueNotificationDeliveries(ctx, work); err != nil {
		return err
	}

	deliveryCtx, cancelDelivery, err := notificationBudgetContext(ctx, database, job)
	if err != nil {
		return err
	}
	defer cancelDelivery()

	return processNotificationQueue(deliveryCtx, database, eventID, true)
}

// RecoverNotifications processes all notification work whose retry or expired
// claim is ready now. It does not wait for future retry times, so a new
// per-job supervisor can opportunistically recover work left by an abruptly
// terminated supervisor without becoming a shared daemon.
func RecoverNotifications(ctx context.Context, database *store.Store) error {
	if database == nil {
		return errors.New("recover notifications: store is nil")
	}

	return processNotificationQueue(ctx, database, "", false)
}

func processNotificationQueue(
	ctx context.Context,
	database *store.Store,
	eventID model.EventID,
	waitForFuture bool,
) error {
	for {
		now := time.Now().UTC()
		delivery, err := database.ClaimNotificationDelivery(
			ctx,
			eventID,
			now,
			now.Add(notificationClaimLease),
		)
		if err == nil {
			if processErr := processClaimedNotification(ctx, database, delivery); processErr != nil {
				return processErr
			}

			continue
		}
		if !errors.Is(err, store.ErrNotFound) {
			return err
		}
		next, pending, nextErr := database.NextNotificationDeliveryAt(ctx, eventID)
		if nextErr != nil {
			return nextErr
		}
		if !pending || !waitForFuture {
			return nil
		}
		delay := time.Until(next)
		if delay <= 0 {
			continue
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}

			return ctx.Err()
		case <-timer.C:
		}
	}
}

//nolint:cyclop,gocognit // Claim renewal, bounded delivery, retry classification, and durable completion form one transaction lifecycle.
func processClaimedNotification(
	ctx context.Context,
	database *store.Store,
	delivery store.NotificationDelivery,
) error {
	job, err := database.GetJob(ctx, delivery.JobID)
	if err != nil {
		return fmt.Errorf("load notification job: %w", err)
	}
	definition, found := notificationDefinition(job, delivery.NotifierName)
	deadline, hasDeadline, err := notificationDeadline(ctx, database, job, time.Now().UTC())
	if err != nil {
		return err
	}

	deliveryCtx, cancelDelivery := context.WithCancel(ctx)
	if hasDeadline {
		deliveryCtx, cancelDelivery = context.WithDeadline(ctx, deadline)
	}
	renewed := make(chan error, 1)
	go maintainNotificationClaim(deliveryCtx, cancelDelivery, database, delivery, renewed)

	startedAt := time.Now().UTC()
	var result notify.Result
	var deliveryErr error
	switch {
	case hasDeadline && !startedAt.Before(deadline):
		deliveryErr = &notify.DeliveryError{Kind: notify.ErrorTimeout, Retryable: false}
	case !found:
		deliveryErr = &notify.DeliveryError{Kind: notify.ErrorInvalid, Retryable: false}
	default:
		var destination notify.Notifier
		destination, err = instantiateNotifier(deliveryCtx, definition)
		if err != nil {
			deliveryErr = &notify.DeliveryError{Kind: notify.ErrorInvalid, Retryable: false}
		} else {
			result, deliveryErr = destination.Deliver(deliveryCtx, notify.Event{
				SchemaVersion: notify.EventSchemaVersion,
				ID:            delivery.EventID.String(),
				Type:          notify.EventType(delivery.EventType),
				JobID:         delivery.JobID.String(),
				RunID:         delivery.RunID.String(),
				OccurredAt:    delivery.OccurredAt,
			})
		}
	}
	finishedAt := time.Now().UTC()
	cancelDelivery()
	if renewalErr := <-renewed; renewalErr != nil {
		return renewalErr
	}

	diagnostic := ""
	retryable := false
	succeeded := deliveryErr == nil
	if deliveryErr != nil {
		diagnostic = string(notify.ErrorInternal)
		var classified *notify.DeliveryError
		if errors.As(deliveryErr, &classified) {
			diagnostic = string(classified.Kind)
			retryable = classified.Retryable
		}
	}
	if hasDeadline && !finishedAt.Before(deadline) {
		diagnostic = string(notify.ErrorTimeout)
		retryable = false
		succeeded = false
	}
	var nextAttemptAt *time.Time
	if retryable && delivery.NextAttemptNumber() < delivery.MaxAttempts {
		retryPolicy := notify.RetryPolicy{MaxAttempts: delivery.MaxAttempts}
		if found {
			retryPolicy.Delay = definition.Retry.Delay
			retryPolicy.MaxDelay = definition.Retry.MaxDelay
		}
		next := finishedAt.Add(notificationRetryDelay(retryPolicy, delivery.NextAttemptNumber()))
		if !hasDeadline || next.Before(deadline) {
			nextAttemptAt = &next
		} else {
			retryable = false
		}
	}
	completion := store.CompleteNotificationDeliveryInput{
		EventID: delivery.EventID, ClaimToken: delivery.ClaimToken,
		NotifierName: delivery.NotifierName, AttemptNumber: delivery.NextAttemptNumber(),
		StartedAt: startedAt, CompletedAt: finishedAt, NextAttemptAt: nextAttemptAt,
		DiagnosticCode: diagnostic, Retryable: retryable, Succeeded: succeeded,
		MessageID: result.MessageID, ResponseTruncated: result.Truncated,
	}
	if found && definition.Kind == model.NotifierWebhook && result.StatusCode != 0 {
		completion.ResponseStatusCode = &result.StatusCode
	}
	if found && definition.Kind == model.NotifierCommand {
		completion.CommandExitCode = &result.ExitCode
	}
	persistCtx, cancelPersist := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancelPersist()
	_, err = database.CompleteNotificationDelivery(persistCtx, completion)

	return err
}

func notificationBudgetContext(
	ctx context.Context,
	database *store.Store,
	job model.JobState,
) (context.Context, context.CancelFunc, error) {
	deadline, bounded, err := notificationDeadline(ctx, database, job, time.Now().UTC())
	if err != nil {
		return nil, nil, err
	}
	if !bounded {
		return ctx, func() {}, nil
	}
	boundedCtx, cancel := context.WithDeadline(ctx, deadline)

	return boundedCtx, cancel, nil
}

func notificationDeadline(
	ctx context.Context,
	database *store.Store,
	job model.JobState,
	now time.Time,
) (time.Time, bool, error) {
	limit := job.Spec.ExecutionPolicy().JobTimeout
	if limit == 0 || job.ClaimedAt == nil {
		return time.Time{}, false, nil
	}
	runtimeState, err := database.GetRuntime(ctx, job.ID)
	if err != nil {
		return time.Time{}, false, err
	}
	paused := runtimeState.TotalPaused
	if runtimeState.PausedAt != nil {
		paused += now.Sub(*runtimeState.PausedAt)
	}

	return job.ClaimedAt.Add(limit + paused), true, nil
}

func maintainNotificationClaim(
	ctx context.Context,
	cancelDelivery context.CancelFunc,
	database *store.Store,
	delivery store.NotificationDelivery,
	completed chan<- error,
) {
	maintainNotificationClaimAtInterval(
		ctx, cancelDelivery, database, delivery, completed, notificationLeaseRenewal,
	)
}

func maintainNotificationClaimAtInterval(
	ctx context.Context,
	cancelDelivery context.CancelFunc,
	database *store.Store,
	delivery store.NotificationDelivery,
	completed chan<- error,
	interval time.Duration,
) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			completed <- nil

			return
		case now := <-ticker.C:
			if err := database.RenewNotificationDelivery(
				ctx,
				delivery.EventID,
				delivery.NotifierName,
				delivery.ClaimToken,
				now.UTC(),
				now.UTC().Add(notificationClaimLease),
			); err != nil {
				cancelDelivery()
				completed <- err

				return
			}
		}
	}
}

func notificationDefinition(job model.JobState, name string) (model.NotifierDefinition, bool) {
	for _, definition := range job.Spec.ExecutionPolicy().NotifierDefinitions {
		if definition.Name == name {
			return definition, true
		}
	}

	return model.NotifierDefinition{}, false
}

func notifyTransition(
	ctx context.Context,
	database *store.Store,
	job model.JobState,
	entity model.EntityKind,
	entityID string,
	revision uint64,
	eventType notify.EventType,
	runID model.RunID,
	occurredAt time.Time,
) {
	event, err := database.TransitionEvent(ctx, entity, entityID, revision)
	if err != nil {
		return
	}
	ignoreNotificationError(deliverNotifications(ctx, database, job, event.ID, eventType, runID, occurredAt))
}

func ignoreNotificationError(_ error) {
	// Notification transport and recovery failures remain durable queue work and
	// must never change the managed job or run outcome.
}

func notifyCompletedRun(
	ctx context.Context,
	database *store.Store,
	result model.TransitionResult,
	runOutcome model.RunOutcome,
	occurredAt time.Time,
) {
	if result.Run == nil {
		return
	}
	runEvent := notify.EventRunFailed
	switch runOutcome {
	case model.RunOutcomeSuccess:
		runEvent = notify.EventRunSucceeded
	case model.RunOutcomeTimedOut:
		runEvent = notify.EventRunTimedOut
	case model.RunOutcomeCancelled:
		runEvent = notify.EventRunCancelled
	case model.RunOutcomeLost:
		runEvent = notify.EventRunLost
	case model.RunOutcomeFailure, model.RunOutcomeStartFailed:
		runEvent = notify.EventRunFailed
	}
	notifyTransition(ctx, database, result.Job, model.EntityRun, result.Run.ID.String(), result.Run.Revision,
		runEvent, result.Run.ID, occurredAt)
	if result.Job.Phase != model.JobPhaseCompleted {
		notifyTransition(ctx, database, result.Job, model.EntityJob, result.Job.ID.String(), result.Job.Revision,
			notify.EventRetryScheduled, result.Run.ID, occurredAt)
		return
	}
	notifyTerminalJob(ctx, database, result.Job, result.Run.ID, occurredAt)
}

func notifyTerminalJob(
	ctx context.Context,
	database *store.Store,
	job model.JobState,
	runID model.RunID,
	occurredAt time.Time,
) {
	eventType := notify.EventJobFailed
	switch job.Outcome {
	case model.JobOutcomeSuccess:
		eventType = notify.EventJobSucceeded
	case model.JobOutcomeTimedOut:
		eventType = notify.EventJobTimedOut
	case model.JobOutcomeCancelled:
		eventType = notify.EventJobCancelled
	case model.JobOutcomeAborted:
		eventType = notify.EventJobAborted
	case model.JobOutcomeLost:
		eventType = notify.EventJobLost
	case model.JobOutcomeSubmissionFailed:
		eventType = notify.EventSubmissionFailed
	case model.JobOutcomeFailure:
		eventType = notify.EventJobFailed
	case model.JobOutcomeNone:
		return
	}
	notifyTransition(ctx, database, job, model.EntityJob, job.ID.String(), job.Revision,
		eventType, runID, occurredAt)
}

func notifyJobStarted(ctx context.Context, database *store.Store, job model.JobState, at time.Time) {
	notifyTransition(ctx, database, job, model.EntityJob, job.ID.String(), job.Revision,
		notify.EventJobStarted, "", at)
}

func notifyRunStarted(ctx context.Context, database *store.Store, result model.TransitionResult, at time.Time) {
	if result.Run == nil {
		return
	}
	notifyTransition(ctx, database, result.Job, model.EntityRun, result.Run.ID.String(), result.Run.Revision,
		notify.EventRunStarted, result.Run.ID, at)
}

//nolint:gocognit // The persisted tagged union requires kind-specific secret resolution and construction.
func instantiateNotifier(ctx context.Context, definition model.NotifierDefinition) (notify.Notifier, error) {
	switch definition.Kind {
	case model.NotifierCommand:
		configured := definition.Command
		environment := cloneNotificationStrings(configured.Environment)
		for name, reference := range configured.SecretEnvironment {
			value, err := resolveNotificationSecret(ctx, reference)
			if err != nil {
				return nil, fmt.Errorf("resolve command notifier %q secret %q: %w", definition.Name, name, err)
			}
			environment[name] = value
		}
		return notify.Command{
			NameValue: definition.Name, Executable: configured.Executable,
			Arguments: slices.Clone(configured.Arguments), Directory: configured.WorkingDirectory,
			Environment: environment, Timeout: definition.Timeout, OutputLimit: configured.OutputLimit,
		}, nil
	case model.NotifierWebhook:
		configured := definition.Webhook
		headers := cloneNotificationStrings(configured.Headers)
		for name, reference := range configured.SecretHeaders {
			value, err := resolveNotificationSecret(ctx, reference)
			if err != nil {
				return nil, fmt.Errorf("resolve HTTP notifier %q secret header %q: %w", definition.Name, name, err)
			}
			headers[name] = value
		}
		var signature []byte
		if configured.SigningSecret != nil {
			value, err := resolveNotificationSecret(ctx, *configured.SigningSecret)
			if err != nil {
				return nil, fmt.Errorf("resolve HTTP notifier %q signing secret: %w", definition.Name, err)
			}
			signature = []byte(value)
		}
		return notify.Webhook{
			NameValue: definition.Name, URL: configured.URL, Headers: headers,
			SignatureHeader: configured.SignatureHeader, SignatureSecret: signature,
			Timeout: definition.Timeout, ResponseLimit: configured.ResponseLimit,
			AllowInsecureHTTP:   configured.AllowInsecureHTTP,
			AllowPrivateNetwork: configured.AllowPrivateNetwork,
			FollowRedirects:     configured.FollowRedirects,
		}, nil
	case model.NotifierSMTP:
		configured := definition.SMTP
		var password []byte
		if configured.PasswordSecret != nil {
			value, err := resolveNotificationSecret(ctx, *configured.PasswordSecret)
			if err != nil {
				return nil, fmt.Errorf("resolve SMTP notifier %q password: %w", definition.Name, err)
			}
			password = []byte(value)
		}
		return notify.SMTP{
			NameValue: definition.Name, Address: configured.Address, ServerName: configured.ServerName,
			Username: configured.Username, Password: password, From: configured.From,
			To: slices.Clone(configured.To), SubjectPrefix: configured.SubjectPrefix,
			Mode: notify.SMTPMode(configured.Mode), Timeout: definition.Timeout,
			MessageLimit: configured.MessageLimit,
		}, nil
	default:
		return nil, fmt.Errorf("instantiate notifier %q: unsupported kind %q", definition.Name, definition.Kind)
	}
}

func resolveNotificationSecret(ctx context.Context, reference model.SecretReference) (string, error) {
	parsed, err := config.ParseSecretRef(reference.Provider + ":" + reference.Name)
	if err != nil {
		return "", err
	}

	return (config.LocalSecretResolver{}).ResolveSecret(ctx, parsed)
}

func cloneNotificationStrings(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for name, value := range source {
		result[name] = value
	}

	return result
}

func notificationRetryDelay(policy notify.RetryPolicy, completedAttempt int) time.Duration {
	delay := policy.Delay
	for range completedAttempt - 1 {
		if delay > time.Duration(1<<63-1)/2 {
			delay = time.Duration(1<<63 - 1)
			break
		}
		delay *= 2
	}
	if policy.MaxDelay != 0 && delay > policy.MaxDelay {
		return policy.MaxDelay
	}

	return delay
}
