// Package app coordinates Jobman's domain, store, supervisor, and log use cases.
package app

import (
	"context"
	"errors"
	"io"

	"github.com/ryancswallace/jobman/internal/model"
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
}

// JobDetails contains a job snapshot and its ordered run history.
type JobDetails struct {
	Job  model.JobState   `json:"job"`
	Runs []model.RunState `json:"runs"`
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
