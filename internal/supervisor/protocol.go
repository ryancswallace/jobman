// Package supervisor owns detached per-job execution.
package supervisor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/ryancswallace/jobman/internal/model"
	"github.com/ryancswallace/jobman/internal/platform"
	"github.com/ryancswallace/jobman/internal/store"
)

const (
	credentialSize    = 32
	maximumAckSize    = 4 * 1024
	defaultAckTimeout = 12 * time.Second
)

// Acknowledgement is the bounded, versioned supervisor-claim response.
type Acknowledgement struct {
	SchemaVersion int                `json:"schema_version"`
	JobID         model.JobID        `json:"job_id"`
	SupervisorID  model.SupervisorID `json:"supervisor_id"`
}

// LaunchOptions configures one detached supervisor launch.
type LaunchOptions struct {
	Store      *store.Store
	Executable string
	StateDir   string
	JobID      model.JobID
	Credential []byte
	Timeout    time.Duration
}

// Launch starts a detached supervisor and waits until its durable claim is
// acknowledged or reconciled from the store.
func Launch(ctx context.Context, options LaunchOptions) (Acknowledgement, error) {
	if ctx == nil {
		return Acknowledgement{}, errors.New("launch supervisor: context is required")
	}
	timeout, err := validateLaunchOptions(options)
	if err != nil {
		return Acknowledgement{}, err
	}

	// The accepted supervisor must not retain the submitting client's
	// cancellation. WithoutCancel makes this lifetime boundary explicit while
	// retaining CommandContext's safer construction contract.
	command := exec.CommandContext( // #nosec G204 -- executable is the current trusted Jobman binary.
		context.WithoutCancel(ctx),
		options.Executable,
		"__supervise",
		options.JobID.String(),
	)
	command.Env = withSupervisorStateDir(os.Environ(), options.StateDir)
	platform.ConfigureSupervisor(command)
	stdin, err := command.StdinPipe()
	if err != nil {
		return Acknowledgement{}, fmt.Errorf("create supervisor credential pipe: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return Acknowledgement{}, errors.Join(
			fmt.Errorf("create supervisor acknowledgement pipe: %w", err),
			stdin.Close(),
		)
	}
	command.Stderr = io.Discard

	if err := command.Start(); err != nil {
		return Acknowledgement{}, errors.Join(
			fmt.Errorf("start supervisor: %w", err),
			stdin.Close(),
			stdout.Close(),
		)
	}
	if _, err := stdin.Write(options.Credential); err != nil {
		return reconcileLaunch(
			ctx,
			options,
			command,
			errors.Join(
				fmt.Errorf("send supervisor credential: %w", err),
				stdin.Close(),
				stdout.Close(),
			),
		)
	}
	if err := stdin.Close(); err != nil {
		return reconcileLaunch(
			ctx,
			options,
			command,
			errors.Join(
				fmt.Errorf("close supervisor credential pipe: %w", err),
				stdout.Close(),
			),
		)
	}

	type response struct {
		ack Acknowledgement
		err error
	}
	responses := make(chan response, 1)
	go func() {
		ack, decodeErr := decodeAcknowledgement(stdout)
		responses <- response{ack: ack, err: decodeErr}
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case result := <-responses:
		if result.err != nil {
			return reconcileLaunch(ctx, options, command, result.err)
		}
		if result.ack.JobID != options.JobID || !result.ack.SupervisorID.Valid() {
			return Acknowledgement{}, errors.Join(
				errors.New("validate supervisor acknowledgement: identity mismatch"),
				command.Process.Release(),
			)
		}
		if err := command.Process.Release(); err != nil {
			// The claim and acknowledgement are already durable and authoritative.
			// Returning failure here could cause a caller to retry a live job; the
			// short-lived client process will release its remaining OS resources.
			return result.ack, nil
		}

		return result.ack, nil
	case <-timer.C:
		return reconcileLaunch(ctx, options, command, errors.New("supervisor acknowledgement timed out"))
	case <-ctx.Done():
		return reconcileLaunch(ctx, options, command, ctx.Err())
	}
}

func validateLaunchOptions(options LaunchOptions) (time.Duration, error) {
	if options.Store == nil {
		return 0, errors.New("launch supervisor: store is required")
	}
	if options.Executable == "" || options.StateDir == "" || !options.JobID.Valid() {
		return 0, errors.New("launch supervisor: executable, state directory, and job ID are required")
	}
	if len(options.Credential) != credentialSize {
		return 0, fmt.Errorf("launch supervisor: credential must contain %d bytes", credentialSize)
	}
	if options.Timeout < 0 {
		return 0, errors.New("launch supervisor: timeout must not be negative")
	}
	if options.Timeout == 0 {
		return defaultAckTimeout, nil
	}

	return options.Timeout, nil
}

func withSupervisorStateDir(environment []string, stateDir string) []string {
	const name = "JOBMAN_STATE_DIR"

	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		key, _, found := strings.Cut(entry, "=")
		if found && strings.EqualFold(key, name) {
			continue
		}
		result = append(result, entry)
	}

	return append(result, name+"="+stateDir)
}

func decodeAcknowledgement(reader io.ReadCloser) (Acknowledgement, error) {
	defer reader.Close()
	encoded, err := io.ReadAll(io.LimitReader(reader, maximumAckSize+1))
	if err != nil {
		return Acknowledgement{}, fmt.Errorf("read supervisor acknowledgement: %w", err)
	}
	if len(encoded) > maximumAckSize {
		return Acknowledgement{}, fmt.Errorf(
			"decode supervisor acknowledgement: response exceeds %d bytes",
			maximumAckSize,
		)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var acknowledgement Acknowledgement
	if err := decoder.Decode(&acknowledgement); err != nil {
		return Acknowledgement{}, fmt.Errorf("decode supervisor acknowledgement: %w", err)
	}
	if acknowledgement.SchemaVersion != 1 {
		return Acknowledgement{}, fmt.Errorf(
			"decode supervisor acknowledgement: unsupported schema version %d",
			acknowledgement.SchemaVersion,
		)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("trailing JSON value")
		}

		return Acknowledgement{}, fmt.Errorf("decode supervisor acknowledgement: %w", err)
	}
	if !acknowledgement.JobID.Valid() || !acknowledgement.SupervisorID.Valid() {
		return Acknowledgement{}, errors.New("decode supervisor acknowledgement: invalid identity")
	}

	return acknowledgement, nil
}

func reconcileLaunch(
	ctx context.Context,
	options LaunchOptions,
	command *exec.Cmd,
	cause error,
) (Acknowledgement, error) {
	// Parent cancellation is one of the reasons reconciliation is needed. Use a
	// fresh bounded context so a committed claim is not misreported merely
	// because the submitting command's context has already been canceled.
	reconcileCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()

	job, err := options.Store.GetJob(reconcileCtx, options.JobID)
	if err == nil && job.SupervisorID.Valid() && job.Phase != model.JobPhaseSubmitting {
		if releaseErr := command.Process.Release(); releaseErr != nil {
			return Acknowledgement{}, errors.Join(cause, releaseErr)
		}

		return Acknowledgement{
			SchemaVersion: 1,
			JobID:         options.JobID,
			SupervisorID:  job.SupervisorID,
		}, nil
	}
	if err != nil {
		cause = errors.Join(cause, fmt.Errorf("reload submission: %w", err))
	}
	if releaseErr := command.Process.Release(); releaseErr != nil {
		cause = errors.Join(cause, fmt.Errorf("release failed supervisor handle: %w", releaseErr))
	}

	return Acknowledgement{}, cause
}
