package jobman

import (
	"time"

	"github.com/ryancswallace/jobman/internal/app"
	"github.com/ryancswallace/jobman/internal/model"
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

type jobDetail struct {
	Summary               jobSummary    `json:"summary"`
	Specification         model.JobSpec `json:"specification"`
	ClaimedAt             *time.Time    `json:"claimed_at,omitempty"`
	StartedAt             *time.Time    `json:"started_at,omitempty"`
	CompletedAt           *time.Time    `json:"completed_at,omitempty"`
	CancellationRequested *time.Time    `json:"cancellation_requested_at,omitempty"`
	Runs                  []runDetail   `json:"runs"`
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
	StdoutPath      string `json:"stdout_path"`
	StderrPath      string `json:"stderr_path"`
	IndexPath       string `json:"index_path"`
	IndexVersion    int    `json:"index_version"`
	StdoutSize      int64  `json:"stdout_size"`
	StderrSize      int64  `json:"stderr_size"`
	Integrity       string `json:"integrity"`
	RecordingHealth string `json:"recording_health"`
	DiagnosticCode  string `json:"diagnostic_code,omitempty"`
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
	runs := make([]runDetail, 0, len(value.Runs))
	for _, run := range value.Runs {
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
			Logs: logDetail{
				StdoutPath:      run.Logs.StdoutPath,
				StderrPath:      run.Logs.StderrPath,
				IndexPath:       run.Logs.IndexPath,
				IndexVersion:    run.Logs.IndexVersion,
				StdoutSize:      run.Logs.StdoutSize,
				StderrSize:      run.Logs.StderrSize,
				Integrity:       string(run.Logs.Integrity),
				RecordingHealth: string(run.Logs.RecordingHealth),
				DiagnosticCode:  run.Logs.DiagnosticCode,
			},
		})
	}

	var canceled *time.Time
	if value.Job.Cancellation != nil {
		requested := value.Job.Cancellation.RequestedAt.UTC()
		canceled = &requested
	}

	return jobDetail{
		Summary:               summary(value.Job),
		Specification:         value.Job.Spec,
		ClaimedAt:             value.Job.ClaimedAt,
		StartedAt:             value.Job.StartedAt,
		CompletedAt:           value.Job.CompletedAt,
		CancellationRequested: canceled,
		Runs:                  runs,
	}
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
