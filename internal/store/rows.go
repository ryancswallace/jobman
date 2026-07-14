package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"time"

	"github.com/ryancswallace/jobman/internal/model"
)

const jobColumns = `
    id, name, spec_json, phase, outcome, revision,
    submitted_at_ns, claimed_at_ns, started_at_ns, completed_at_ns,
    active_run_id, supervisor_id,
    cancellation_requested_at_ns, cancellation_reason,
    last_diagnostic_code, launch_credential_hash, claim_deadline_ns`

const runColumns = `
    id, job_id, run_number, phase, outcome, revision, resolved_executable,
    reserved_at_ns, started_at_ns, stop_requested_at_ns, stop_reason, completed_at_ns,
    process_pid, process_identity_json,
    exit_code, exit_signal, exit_platform_reason, exit_observed_at_ns,
    stdout_path, stderr_path, index_path, stdout_size, stderr_size,
    log_index_version, log_integrity, recording_health, log_diagnostic_code,
    last_diagnostic_code`

const supervisorColumns = `
    id, job_id, revision, process_pid, process_identity_json,
    claimed_at_ns, lease_renewed_at_ns, lease_expires_at_ns, released_at_ns`

type rowScanner interface {
	Scan(...any) error
}

func scanJob(row rowScanner) (model.JobState, error) {
	var (
		id                      string
		name                    sql.NullString
		specificationJSON       string
		phase                   string
		outcome                 sql.NullString
		revision                int64
		submittedAt             int64
		claimedAt               sql.NullInt64
		startedAt               sql.NullInt64
		completedAt             sql.NullInt64
		activeRunID             sql.NullString
		supervisorID            sql.NullString
		cancellationRequestedAt sql.NullInt64
		cancellationReason      sql.NullString
		lastDiagnosticCode      sql.NullString
		launchCredentialHash    []byte
		claimDeadline           sql.NullInt64
	)
	if err := row.Scan(
		&id,
		&name,
		&specificationJSON,
		&phase,
		&outcome,
		&revision,
		&submittedAt,
		&claimedAt,
		&startedAt,
		&completedAt,
		&activeRunID,
		&supervisorID,
		&cancellationRequestedAt,
		&cancellationReason,
		&lastDiagnosticCode,
		&launchCredentialHash,
		&claimDeadline,
	); err != nil {
		return model.JobState{}, err
	}

	jobID, err := model.ParseJobID(id)
	if err != nil {
		return model.JobState{}, err
	}
	specification, err := model.ParseJobSpecJSON([]byte(specificationJSON))
	if err != nil {
		return model.JobState{}, err
	}
	if name.Valid != (specification.Name() != "") || name.String != specification.Name() {
		return model.JobState{}, errors.New("persisted job name does not match specification")
	}
	jobRevision, err := uintFromDatabase("job revision", revision)
	if err != nil {
		return model.JobState{}, err
	}

	state := model.JobState{
		ID:                 jobID,
		Spec:               specification,
		Phase:              model.JobPhase(phase),
		Revision:           jobRevision,
		SubmittedAt:        timeFromDatabase(submittedAt),
		ClaimedAt:          optionalTime(claimedAt),
		StartedAt:          optionalTime(startedAt),
		CompletedAt:        optionalTime(completedAt),
		LastDiagnosticCode: lastDiagnosticCode.String,
		ClaimDeadline:      optionalTime(claimDeadline),
	}
	if outcome.Valid {
		state.Outcome = model.JobOutcome(outcome.String)
	}
	if err := populateOptionalJobFields(&state, activeRunID, supervisorID, launchCredentialHash); err != nil {
		return model.JobState{}, err
	}
	if cancellationRequestedAt.Valid {
		state.Cancellation = &model.CancellationIntent{
			RequestedAt: timeFromDatabase(cancellationRequestedAt.Int64),
			Reason:      model.StopReason(cancellationReason.String),
		}
	}
	if err := state.Validate(); err != nil {
		return model.JobState{}, fmt.Errorf("validate persisted job %s: %w", id, err)
	}

	return state, nil
}

func populateOptionalJobFields(
	state *model.JobState,
	activeRunID sql.NullString,
	supervisorID sql.NullString,
	launchCredentialHash []byte,
) error {
	var err error
	if activeRunID.Valid {
		state.ActiveRunID, err = model.ParseRunID(activeRunID.String)
		if err != nil {
			return err
		}
	}
	if supervisorID.Valid {
		state.SupervisorID, err = model.ParseSupervisorID(supervisorID.String)
		if err != nil {
			return err
		}
	}
	if launchCredentialHash != nil {
		state.LaunchCredentialHash, err = model.CredentialHashFromBytes(launchCredentialHash)
		if err != nil {
			return err
		}
	}

	return nil
}

func scanRun(row rowScanner) (model.RunState, error) {
	var (
		id                  string
		jobID               string
		runNumber           int64
		phase               string
		outcome             sql.NullString
		revision            int64
		resolvedExecutable  sql.NullString
		reservedAt          int64
		startedAt           sql.NullInt64
		stopRequestedAt     sql.NullInt64
		stopReason          sql.NullString
		completedAt         sql.NullInt64
		processPID          sql.NullInt64
		processIdentityJSON sql.NullString
		exitCode            sql.NullInt64
		exitSignal          sql.NullString
		exitPlatformReason  sql.NullString
		exitObservedAt      sql.NullInt64
		stdoutPath          string
		stderrPath          string
		indexPath           string
		stdoutSize          int64
		stderrSize          int64
		logIndexVersion     int
		logIntegrity        string
		recordingHealth     string
		logDiagnosticCode   sql.NullString
		lastDiagnosticCode  sql.NullString
	)
	if err := row.Scan(
		&id,
		&jobID,
		&runNumber,
		&phase,
		&outcome,
		&revision,
		&resolvedExecutable,
		&reservedAt,
		&startedAt,
		&stopRequestedAt,
		&stopReason,
		&completedAt,
		&processPID,
		&processIdentityJSON,
		&exitCode,
		&exitSignal,
		&exitPlatformReason,
		&exitObservedAt,
		&stdoutPath,
		&stderrPath,
		&indexPath,
		&stdoutSize,
		&stderrSize,
		&logIndexVersion,
		&logIntegrity,
		&recordingHealth,
		&logDiagnosticCode,
		&lastDiagnosticCode,
	); err != nil {
		return model.RunState{}, err
	}

	runID, err := model.ParseRunID(id)
	if err != nil {
		return model.RunState{}, err
	}
	parsedJobID, err := model.ParseJobID(jobID)
	if err != nil {
		return model.RunState{}, err
	}
	parsedRunNumber, err := uintFromDatabase("run number", runNumber)
	if err != nil {
		return model.RunState{}, err
	}
	parsedRevision, err := uintFromDatabase("run revision", revision)
	if err != nil {
		return model.RunState{}, err
	}

	state := model.RunState{
		ID:                 runID,
		JobID:              parsedJobID,
		Number:             parsedRunNumber,
		Phase:              model.RunPhase(phase),
		Revision:           parsedRevision,
		ResolvedExecutable: resolvedExecutable.String,
		ReservedAt:         timeFromDatabase(reservedAt),
		StartedAt:          optionalTime(startedAt),
		StopRequestedAt:    optionalTime(stopRequestedAt),
		CompletedAt:        optionalTime(completedAt),
		StopReason:         model.StopReason(stopReason.String),
		Logs: model.LogMetadata{
			StdoutPath:      stdoutPath,
			StderrPath:      stderrPath,
			IndexPath:       indexPath,
			IndexVersion:    logIndexVersion,
			StdoutSize:      stdoutSize,
			StderrSize:      stderrSize,
			Integrity:       model.LogIntegrity(logIntegrity),
			RecordingHealth: model.RecordingHealth(recordingHealth),
			DiagnosticCode:  logDiagnosticCode.String,
		},
		LastDiagnosticCode: lastDiagnosticCode.String,
	}
	if outcome.Valid {
		state.Outcome = model.RunOutcome(outcome.String)
	}
	if processIdentityJSON.Valid {
		var identity model.ProcessIdentity
		if err := decodeStrictJSON([]byte(processIdentityJSON.String), &identity); err != nil {
			return model.RunState{}, fmt.Errorf("decode run process identity: %w", err)
		}
		if !processPID.Valid || identity.PID != int(processPID.Int64) {
			return model.RunState{}, errors.New("persisted run process PID does not match identity")
		}
		state.Process = &identity
	}
	if exitObservedAt.Valid {
		exit := model.ExitInfo{
			Signal:         exitSignal.String,
			PlatformReason: exitPlatformReason.String,
			ObservedAt:     timeFromDatabase(exitObservedAt.Int64),
		}
		if exitCode.Valid {
			if exitCode.Int64 > math.MaxInt {
				return model.RunState{}, errors.New("persisted exit code exceeds platform integer range")
			}
			value := int(exitCode.Int64)
			exit.ExitCode = &value
		}
		state.Exit = &exit
	}
	if err := state.Validate(); err != nil {
		return model.RunState{}, fmt.Errorf("validate persisted run %s: %w", id, err)
	}

	return state, nil
}

func scanSupervisor(row rowScanner) (model.SupervisorState, error) {
	var (
		id                  string
		jobID               string
		revision            int64
		processPID          int64
		processIdentityJSON string
		claimedAt           int64
		leaseRenewedAt      int64
		leaseExpiresAt      int64
		releasedAt          sql.NullInt64
	)
	if err := row.Scan(
		&id,
		&jobID,
		&revision,
		&processPID,
		&processIdentityJSON,
		&claimedAt,
		&leaseRenewedAt,
		&leaseExpiresAt,
		&releasedAt,
	); err != nil {
		return model.SupervisorState{}, err
	}

	supervisorID, err := model.ParseSupervisorID(id)
	if err != nil {
		return model.SupervisorState{}, err
	}
	parsedJobID, err := model.ParseJobID(jobID)
	if err != nil {
		return model.SupervisorState{}, err
	}
	parsedRevision, err := uintFromDatabase("supervisor revision", revision)
	if err != nil {
		return model.SupervisorState{}, err
	}
	var identity model.ProcessIdentity
	if err := decodeStrictJSON([]byte(processIdentityJSON), &identity); err != nil {
		return model.SupervisorState{}, fmt.Errorf("decode supervisor process identity: %w", err)
	}
	if identity.PID != int(processPID) {
		return model.SupervisorState{}, errors.New("persisted supervisor PID does not match identity")
	}

	state := model.SupervisorState{
		ID:             supervisorID,
		JobID:          parsedJobID,
		Revision:       parsedRevision,
		Process:        identity,
		ClaimedAt:      timeFromDatabase(claimedAt),
		LeaseRenewedAt: timeFromDatabase(leaseRenewedAt),
		LeaseExpiresAt: timeFromDatabase(leaseExpiresAt),
		ReleasedAt:     optionalTime(releasedAt),
	}
	if err := state.Validate(); err != nil {
		return model.SupervisorState{}, fmt.Errorf("validate persisted supervisor %s: %w", id, err)
	}

	return state, nil
}

func timeFromDatabase(nanoseconds int64) time.Time {
	return time.Unix(0, nanoseconds).UTC()
}

func optionalTime(value sql.NullInt64) *time.Time {
	if !value.Valid {
		return nil
	}

	parsed := timeFromDatabase(value.Int64)

	return &parsed
}

func uintFromDatabase(field string, value int64) (uint64, error) {
	if value <= 0 {
		return 0, fmt.Errorf("persisted %s must be positive", field)
	}

	return uint64(value), nil
}

func databaseUint(field string, value uint64) (int64, error) {
	if value == 0 || value > math.MaxInt64 {
		return 0, fmt.Errorf("%s must fit a positive SQLite INTEGER", field)
	}

	return int64(value), nil
}

func decodeStrictJSON(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}

		return err
	}

	return nil
}

func jsonValue(value any) ([]byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode persisted JSON: %w", err)
	}

	return encoded, nil
}

func getJobWithQueryer(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id model.JobID,
) (model.JobState, error) {
	state, err := scanJob(queryer.QueryRowContext(ctx, "SELECT "+jobColumns+" FROM jobs WHERE id = ?", id.String()))
	if errors.Is(err, sql.ErrNoRows) {
		return model.JobState{}, fmt.Errorf("get job %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return model.JobState{}, fmt.Errorf("get job %s: %w", id, err)
	}

	return state, nil
}

func getRunWithQueryer(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id model.RunID,
) (model.RunState, error) {
	state, err := scanRun(queryer.QueryRowContext(ctx, "SELECT "+runColumns+" FROM runs WHERE id = ?", id.String()))
	if errors.Is(err, sql.ErrNoRows) {
		return model.RunState{}, fmt.Errorf("get run %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return model.RunState{}, fmt.Errorf("get run %s: %w", id, err)
	}

	return state, nil
}

func getSupervisorWithQueryer(ctx context.Context, queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, id model.SupervisorID,
) (model.SupervisorState, error) {
	state, err := scanSupervisor(queryer.QueryRowContext(
		ctx,
		"SELECT "+supervisorColumns+" FROM supervisors WHERE id = ?",
		id.String(),
	))
	if errors.Is(err, sql.ErrNoRows) {
		return model.SupervisorState{}, fmt.Errorf("get supervisor %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return model.SupervisorState{}, fmt.Errorf("get supervisor %s: %w", id, err)
	}

	return state, nil
}
