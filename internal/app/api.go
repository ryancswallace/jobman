// Package app coordinates Jobman's domain, store, supervisor, and log use cases.
package app

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/ryancswallace/jobman/internal/config"
	"github.com/ryancswallace/jobman/internal/liveinput"
	"github.com/ryancswallace/jobman/internal/model"
	"github.com/ryancswallace/jobman/internal/store"
)

// Stable application error categories.
var (
	ErrNotFound  = errors.New("job not found")
	ErrAmbiguous = errors.New("job selector is ambiguous")
	ErrConflict  = errors.New("operation conflicts with current job state")
)

// SubmitRequest is a validated CLI submission request before canonical model
// construction.
type SubmitRequest struct {
	Name             string
	Executable       string
	Arguments        []string
	WorkingDirectory string
	Environment      map[string]string
	UnsetEnvironment []string
	StdinPolicy      model.StdinPolicy
	StopPolicy       model.StopPolicy
	StopPolicySet    bool
	ExecutionPolicy  model.ExecutionPolicy
	Dependencies     []DependencyRequest
}

// DependencyRequest is a user selector plus required terminal predicate. It is
// resolved to an immutable job ID before submission becomes visible.
type DependencyRequest struct {
	Selector  string
	Predicate string
}

// JobDetails contains a job snapshot and its ordered run history.
type JobDetails struct {
	Job                    model.JobState               `json:"job"`
	Runs                   []model.RunState             `json:"runs"`
	Runtime                store.JobRuntime             `json:"runtime"`
	Dependencies           []store.Dependency           `json:"dependencies"`
	WaitEvaluations        []store.WaitEvaluation       `json:"wait_evaluations"`
	Admission              *store.Admission             `json:"admission,omitempty"`
	NotificationDeliveries []store.NotificationDelivery `json:"notification_deliveries"`
	NotificationAttempts   []store.NotificationAttempt  `json:"notification_attempts"`
}

// LogStream selects captured target output.
type LogStream string

// Supported log streams.
const (
	LogStdout LogStream = "stdout"
	LogStderr LogStream = "stderr"
	LogBoth   LogStream = "both"
)

// Backend is the application boundary consumed by the command package.
type Backend interface {
	io.Closer
	Submit(context.Context, SubmitRequest) (model.JobState, error)
	List(context.Context) ([]model.JobState, error)
	Inspect(context.Context, string) (JobDetails, error)
	ReadLogs(context.Context, string, LogStream) ([]byte, error)
	Cancel(context.Context, string) (model.JobState, error)
}

// LifecycleBackend exposes optional v1 lifecycle controls to the CLI while
// retaining the small base interface used by existing embedders.
type LifecycleBackend interface {
	Pause(context.Context, string) (model.JobState, error)
	Resume(context.Context, string) (model.JobState, error)
	Wait(context.Context, string) (model.JobState, error)
	Rerun(context.Context, string, RerunRequest) (model.JobState, error)
}

// InputBackend delivers bounded bytes through a supervisor-owned local IPC
// endpoint.
type InputBackend interface {
	SendInput(context.Context, string, io.Reader, bool) (liveinput.Result, error)
}

// FollowBackend streams durable log growth without buffering it in memory.
type FollowBackend interface {
	ReadRunLogs(context.Context, string, LogStream, uint64) ([]byte, error)
	FollowLogs(context.Context, string, LogStream, uint64, io.Writer) error
}

// CleanupBackend removes eligible inactive logs after rechecking durable state.
type CleanupBackend interface {
	Clean(context.Context, CleanRequest) (CleanResult, error)
}

// ConfigurableBackend applies store-wide mutable settings from the effective
// configuration before a command performs work.
type ConfigurableBackend interface {
	ApplyConfig(context.Context, config.Config) error
}

// RerunRequest contains the intentionally small set of overrides applied to a
// source job's immutable specification.
type RerunRequest struct {
	Name string
}

// CleanRequest selects completed log sets. A zero OlderThan includes every
// completed run. DryRun performs no filesystem mutation.
type CleanRequest struct {
	Selector  string
	OlderThan time.Duration
	DryRun    bool
	UsePolicy bool
}

// CleanResult summarizes deterministic cleanup work.
type CleanResult struct {
	Runs  uint64 `json:"runs"`
	Files uint64 `json:"files"`
	Bytes uint64 `json:"bytes"`
}
