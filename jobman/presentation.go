package jobman

import (
	"time"

	"github.com/ryancswallace/jobman/internal/app"
	"github.com/ryancswallace/jobman/internal/model"
	"github.com/ryancswallace/jobman/internal/store"
)

const timeFormat = time.RFC3339Nano

type jobSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	Phase       string `json:"phase"`
	Outcome     string `json:"outcome,omitempty"`
	Revision    uint64 `json:"revision"`
	SubmittedAt string `json:"submitted_at"`
}

type listedJobDetail struct {
	Summary jobSummary  `json:"summary"`
	Runs    []runDetail `json:"runs,omitempty"`
}

func presentListedJobs(values []app.ListedJob, showRuns bool) []listedJobDetail {
	result := make([]listedJobDetail, 0, len(values))
	for _, value := range values {
		item := listedJobDetail{Summary: summary(value.Job)}
		if showRuns {
			item.Runs = presentRuns(value.Runs)
		}
		result = append(result, item)
	}

	return result
}

type jobDetail struct {
	Summary                jobSummary                   `json:"summary"`
	Specification          model.JobSpec                `json:"specification"`
	ClaimedAt              *time.Time                   `json:"claimed_at,omitempty"`
	StartedAt              *time.Time                   `json:"started_at,omitempty"`
	CompletedAt            *time.Time                   `json:"completed_at,omitempty"`
	CancellationRequested  *time.Time                   `json:"cancellation_requested_at,omitempty"`
	Runs                   []runDetail                  `json:"runs"`
	Runtime                runtimeDetail                `json:"runtime"`
	Dependencies           []dependencyDetail           `json:"dependencies"`
	WaitEvaluations        []waitEvaluationDetail       `json:"wait_evaluations"`
	Admission              *admissionDetail             `json:"admission,omitempty"`
	NotificationDeliveries []notificationDeliveryDetail `json:"notification_deliveries"`
	NotificationAttempts   []notificationAttemptDetail  `json:"notification_attempts"`
}

type runtimeDetail struct {
	Revision                 uint64     `json:"revision"`
	RunCount                 uint64     `json:"run_count"`
	SuccessCount             uint64     `json:"success_count"`
	FailureCount             uint64     `json:"failure_count"`
	NextRunAt                *time.Time `json:"next_run_at,omitempty"`
	WaitingReason            string     `json:"waiting_reason,omitempty"`
	PausedFrom               string     `json:"paused_from,omitempty"`
	PausedAt                 *time.Time `json:"paused_at,omitempty"`
	TotalPaused              string     `json:"total_paused"`
	PrerequisitesSatisfiedAt *time.Time `json:"prerequisites_satisfied_at,omitempty"`
	InputEndpoint            string     `json:"input_endpoint,omitempty"`
	InputEOFRequested        bool       `json:"input_eof_requested"`
}

type dependencyDetail struct {
	JobID            string     `json:"job_id"`
	DependsOn        string     `json:"depends_on"`
	Predicate        string     `json:"predicate"`
	ObservedRevision uint64     `json:"observed_revision,omitempty"`
	ObservedOutcome  string     `json:"observed_outcome,omitempty"`
	SatisfiedAt      *time.Time `json:"satisfied_at,omitempty"`
}

type waitEvaluationDetail struct {
	ConditionIndex     int        `json:"condition_index"`
	ConditionKind      string     `json:"condition_kind"`
	EvaluatedAt        *time.Time `json:"evaluated_at,omitempty"`
	SatisfiedAt        *time.Time `json:"satisfied_at,omitempty"`
	AttemptCount       uint64     `json:"attempt_count"`
	LastDiagnosticCode string     `json:"last_diagnostic_code,omitempty"`
}

type admissionDetail struct {
	JobID        string     `json:"job_id"`
	RunID        string     `json:"run_id,omitempty"`
	Pool         string     `json:"pool,omitempty"`
	Slots        uint64     `json:"slots"`
	AcquiredAt   time.Time  `json:"acquired_at"`
	LeaseExpires time.Time  `json:"lease_expires_at"`
	ReleasedAt   *time.Time `json:"released_at,omitempty"`
}

type notificationDeliveryDetail struct {
	JobID          string                           `json:"job_id"`
	EventID        string                           `json:"event_id"`
	RunID          string                           `json:"run_id,omitempty"`
	NotifierName   string                           `json:"notifier"`
	EventType      string                           `json:"event_type"`
	Status         store.NotificationDeliveryStatus `json:"status"`
	OccurredAt     time.Time                        `json:"occurred_at"`
	CreatedAt      time.Time                        `json:"created_at"`
	NextAttemptAt  *time.Time                       `json:"next_attempt_at,omitempty"`
	ClaimedAt      *time.Time                       `json:"claimed_at,omitempty"`
	ClaimExpiresAt *time.Time                       `json:"claim_expires_at,omitempty"`
	CompletedAt    *time.Time                       `json:"completed_at,omitempty"`
	MaxAttempts    int                              `json:"max_attempts"`
	AttemptCount   int                              `json:"attempt_count"`
}

type notificationAttemptDetail struct {
	ID                 string                          `json:"id"`
	JobID              string                          `json:"job_id"`
	EventID            string                          `json:"event_id"`
	NotifierName       string                          `json:"notifier"`
	EventType          string                          `json:"event_type"`
	AttemptNumber      int                             `json:"attempt_number"`
	Status             store.NotificationAttemptStatus `json:"status"`
	CreatedAt          time.Time                       `json:"created_at"`
	StartedAt          *time.Time                      `json:"started_at,omitempty"`
	CompletedAt        *time.Time                      `json:"completed_at,omitempty"`
	NextAttemptAt      *time.Time                      `json:"next_attempt_at,omitempty"`
	DiagnosticCode     string                          `json:"diagnostic_code,omitempty"`
	Retryable          bool                            `json:"retryable"`
	ResponseStatusCode *int                            `json:"response_status_code,omitempty"`
	CommandExitCode    *int                            `json:"command_exit_code,omitempty"`
	MessageID          string                          `json:"message_id,omitempty"`
	ResponseTruncated  bool                            `json:"response_truncated"`
}

type runDetail struct {
	ID                 string         `json:"id"`
	Number             uint64         `json:"number"`
	Phase              string         `json:"phase"`
	Outcome            string         `json:"outcome,omitempty"`
	Revision           uint64         `json:"revision"`
	ResolvedExecutable string         `json:"resolved_executable,omitempty"`
	Process            *processDetail `json:"process,omitempty"`
	ReservedAt         time.Time      `json:"reserved_at"`
	StartedAt          *time.Time     `json:"started_at,omitempty"`
	CompletedAt        *time.Time     `json:"completed_at,omitempty"`
	Exit               *exitDetail    `json:"exit,omitempty"`
	Logs               logDetail      `json:"logs"`
}

type processDetail struct {
	PID        int    `json:"pid"`
	Platform   string `json:"platform"`
	CreationID string `json:"creation_id"`
	BootID     string `json:"boot_id"`
	TreeID     string `json:"tree_id,omitempty"`
}

type exitDetail struct {
	ExitCode       *int      `json:"exit_code,omitempty"`
	Signal         string    `json:"signal,omitempty"`
	PlatformReason string    `json:"platform_reason,omitempty"`
	ObservedAt     time.Time `json:"observed_at"`
}

type logDetail struct {
	Available       bool       `json:"available"`
	StdoutPath      string     `json:"stdout_path,omitempty"`
	StderrPath      string     `json:"stderr_path,omitempty"`
	IndexPath       string     `json:"index_path,omitempty"`
	IndexVersion    int        `json:"index_version"`
	StdoutSize      int64      `json:"stdout_size,omitempty"`
	StderrSize      int64      `json:"stderr_size,omitempty"`
	Integrity       string     `json:"integrity"`
	RecordingHealth string     `json:"recording_health"`
	DiagnosticCode  string     `json:"diagnostic_code,omitempty"`
	PrunedAt        *time.Time `json:"pruned_at,omitempty"`
	PrunedFiles     uint64     `json:"pruned_files,omitempty"`
	PrunedBytes     uint64     `json:"pruned_bytes,omitempty"`
}

func summary(job model.JobState) jobSummary {
	return jobSummary{
		ID:          job.ID.String(),
		Name:        job.Spec.Name(),
		Phase:       string(job.Phase),
		Outcome:     string(job.Outcome),
		Revision:    job.Revision,
		SubmittedAt: job.SubmittedAt.UTC().Format(timeFormat),
	}
}

func summaries(jobs []model.JobState) []jobSummary {
	result := make([]jobSummary, 0, len(jobs))
	for _, job := range jobs {
		result = append(result, summary(job))
	}

	return result
}

func detail(value app.JobDetails) jobDetail {
	runs := presentRuns(value.Runs)

	var canceled *time.Time
	if value.Job.Cancellation != nil {
		requested := value.Job.Cancellation.RequestedAt.UTC()
		canceled = &requested
	}

	return jobDetail{
		Summary:                summary(value.Job),
		Specification:          value.Job.Spec,
		ClaimedAt:              value.Job.ClaimedAt,
		StartedAt:              value.Job.StartedAt,
		CompletedAt:            value.Job.CompletedAt,
		CancellationRequested:  canceled,
		Runs:                   runs,
		Runtime:                presentRuntime(value.Runtime),
		Dependencies:           presentDependencies(value.Dependencies),
		WaitEvaluations:        presentWaitEvaluations(value.WaitEvaluations),
		Admission:              presentAdmission(value.Admission),
		NotificationDeliveries: presentNotificationDeliveries(value.NotificationDeliveries),
		NotificationAttempts:   presentNotificationAttempts(value.NotificationAttempts),
	}
}

func presentRuns(values []model.RunState) []runDetail {
	runs := make([]runDetail, 0, len(values))
	for _, run := range values {
		runs = append(runs, runDetail{
			ID:                 run.ID.String(),
			Number:             run.Number,
			Phase:              string(run.Phase),
			Outcome:            string(run.Outcome),
			Revision:           run.Revision,
			ResolvedExecutable: run.ResolvedExecutable,
			Process:            presentProcess(run.Process),
			ReservedAt:         run.ReservedAt.UTC(),
			StartedAt:          run.StartedAt,
			CompletedAt:        run.CompletedAt,
			Exit:               presentExit(run.Exit),
			Logs:               presentLogs(run.Logs),
		})
	}

	return runs
}

func presentRuntime(runtime store.JobRuntime) runtimeDetail {
	return runtimeDetail{
		Revision: runtime.Revision, RunCount: runtime.RunCount,
		SuccessCount: runtime.SuccessCount, FailureCount: runtime.FailureCount,
		NextRunAt: utcTime(runtime.NextRunAt), WaitingReason: runtime.WaitingReason,
		PausedFrom: string(runtime.PausedFrom), PausedAt: utcTime(runtime.PausedAt),
		TotalPaused:              runtime.TotalPaused.String(),
		PrerequisitesSatisfiedAt: utcTime(runtime.PrerequisitesSatisfiedAt),
		InputEndpoint:            runtime.InputEndpoint, InputEOFRequested: runtime.InputEOFRequested,
	}
}

func presentDependencies(values []store.Dependency) []dependencyDetail {
	result := make([]dependencyDetail, 0, len(values))
	for _, value := range values {
		result = append(result, dependencyDetail{
			JobID: value.JobID.String(), DependsOn: value.DependsOn.String(),
			Predicate: string(value.Predicate), ObservedRevision: value.ObservedRevision,
			ObservedOutcome: string(value.ObservedOutcome), SatisfiedAt: utcTime(value.SatisfiedAt),
		})
	}

	return result
}

func presentWaitEvaluations(values []store.WaitEvaluation) []waitEvaluationDetail {
	result := make([]waitEvaluationDetail, 0, len(values))
	for _, value := range values {
		result = append(result, waitEvaluationDetail{
			ConditionIndex: value.ConditionIndex, ConditionKind: string(value.ConditionKind),
			EvaluatedAt: utcTime(value.EvaluatedAt), SatisfiedAt: utcTime(value.SatisfiedAt),
			AttemptCount: value.AttemptCount, LastDiagnosticCode: value.LastDiagnosticCode,
		})
	}

	return result
}

func presentAdmission(value *store.Admission) *admissionDetail {
	if value == nil {
		return nil
	}

	return &admissionDetail{
		JobID: value.JobID.String(), RunID: value.RunID.String(), Pool: value.Pool,
		Slots: value.Slots, AcquiredAt: value.AcquiredAt.UTC(),
		LeaseExpires: value.LeaseExpires.UTC(), ReleasedAt: utcTime(value.ReleasedAt),
	}
}

func presentNotificationDeliveries(values []store.NotificationDelivery) []notificationDeliveryDetail {
	result := make([]notificationDeliveryDetail, 0, len(values))
	for _, value := range values {
		result = append(result, notificationDeliveryDetail{
			JobID: value.JobID.String(), EventID: value.EventID.String(), RunID: value.RunID.String(),
			NotifierName: value.NotifierName, EventType: value.EventType, Status: value.Status,
			OccurredAt: value.OccurredAt.UTC(), CreatedAt: value.CreatedAt.UTC(),
			NextAttemptAt: utcTime(value.NextAttemptAt), ClaimedAt: utcTime(value.ClaimedAt),
			ClaimExpiresAt: utcTime(value.ClaimExpiresAt), CompletedAt: utcTime(value.CompletedAt),
			MaxAttempts: value.MaxAttempts, AttemptCount: value.AttemptCount,
		})
	}

	return result
}

func presentNotificationAttempts(values []store.NotificationAttempt) []notificationAttemptDetail {
	result := make([]notificationAttemptDetail, 0, len(values))
	for _, value := range values {
		result = append(result, notificationAttemptDetail{
			ID: value.ID.String(), JobID: value.JobID.String(), EventID: value.EventID.String(),
			NotifierName: value.NotifierName, EventType: value.EventType,
			AttemptNumber: value.AttemptNumber, Status: value.Status, CreatedAt: value.CreatedAt.UTC(),
			StartedAt: utcTime(value.StartedAt), CompletedAt: utcTime(value.CompletedAt),
			NextAttemptAt: utcTime(value.NextAttemptAt), DiagnosticCode: value.DiagnosticCode,
			Retryable: value.Retryable, ResponseStatusCode: value.ResponseStatusCode,
			CommandExitCode: value.CommandExitCode, MessageID: value.MessageID,
			ResponseTruncated: value.ResponseTruncated,
		})
	}

	return result
}

func utcTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	result := value.UTC()

	return &result
}

func presentLogs(logs model.LogMetadata) logDetail {
	result := logDetail{
		Available:       logs.Available(),
		IndexVersion:    logs.IndexVersion,
		Integrity:       string(logs.Integrity),
		RecordingHealth: string(logs.RecordingHealth),
		DiagnosticCode:  logs.DiagnosticCode,
	}
	if logs.Available() {
		result.StdoutPath = logs.StdoutPath
		result.StderrPath = logs.StderrPath
		result.IndexPath = logs.IndexPath
		result.StdoutSize = logs.StdoutSize
		result.StderrSize = logs.StderrSize

		return result
	}
	prunedAt := logs.PrunedAt.UTC()
	result.PrunedAt = &prunedAt
	result.PrunedFiles = logs.PrunedFiles
	result.PrunedBytes = logs.PrunedBytes

	return result
}

func presentProcess(process *model.ProcessIdentity) *processDetail {
	if process == nil {
		return nil
	}

	return &processDetail{
		PID:        process.PID,
		Platform:   process.Platform,
		CreationID: process.CreationID,
		BootID:     process.BootID,
		TreeID:     process.TreeID,
	}
}

func presentExit(exit *model.ExitInfo) *exitDetail {
	if exit == nil {
		return nil
	}

	return &exitDetail{
		ExitCode:       exit.ExitCode,
		Signal:         exit.Signal,
		PlatformReason: exit.PlatformReason,
		ObservedAt:     exit.ObservedAt.UTC(),
	}
}
