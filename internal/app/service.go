package app

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/ryancswallace/jobman/internal/buildinfo"
	"github.com/ryancswallace/jobman/internal/logstore"
	"github.com/ryancswallace/jobman/internal/model"
	"github.com/ryancswallace/jobman/internal/platform"
	"github.com/ryancswallace/jobman/internal/store"
	"github.com/ryancswallace/jobman/internal/supervisor"
)

const (
	defaultClaimWindow = 10 * time.Second
	defaultStopGrace   = 5 * time.Second
)

// Service is one short-lived client over a local metadata store.
type Service struct {
	store      *store.Store
	stateDir   string
	executable string
	launch     func(context.Context, supervisor.LaunchOptions) (supervisor.Acknowledgement, error)
	now        func() time.Time
	random     io.Reader
	ids        *model.UUIDv7Generator
}

// Open constructs a production application backend rooted at stateDir.
func Open(ctx context.Context, stateDir string) (Backend, error) {
	executable, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate Jobman executable: %w", err)
	}
	ids, err := model.NewUUIDv7Generator(time.Now, rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("construct identifier source: %w", err)
	}
	database, err := store.Open(ctx, store.Options{
		StateDir:      stateDir,
		JobmanVersion: buildinfo.Version,
		Now:           time.Now,
		EventIDs:      ids,
	})
	if err != nil {
		return nil, err
	}

	return &Service{
		store:      database,
		stateDir:   stateDir,
		executable: executable,
		launch:     supervisor.Launch,
		now:        time.Now,
		random:     rand.Reader,
		ids:        ids,
	}, nil
}

// Close releases the client store connection.
func (service *Service) Close() error {
	return service.store.Close()
}

// Submit validates and durably transfers one job to a detached supervisor.
func (service *Service) Submit(
	ctx context.Context,
	request SubmitRequest,
) (model.JobState, error) {
	specification, err := model.NewJobSpec(model.JobSpecInput{
		Executable:             request.Executable,
		Arguments:              request.Arguments,
		WorkingDirectory:       request.WorkingDirectory,
		Environment:            request.Environment,
		EnvironmentInheritance: model.EnvironmentInheritSubmission,
		Name:                   request.Name,
		StopPolicy: model.StopPolicy{
			GracePeriod:     defaultStopGrace,
			ForceAfterGrace: true,
		},
		StdinPolicy: model.StdinNull,
	})
	if err != nil {
		return model.JobState{}, fmt.Errorf("validate job specification: %w", err)
	}
	jobID, err := service.ids.NewJobID()
	if err != nil {
		return model.JobState{}, fmt.Errorf("generate job ID: %w", err)
	}
	credential := make([]byte, 32)
	if _, readErr := io.ReadFull(service.random, credential); readErr != nil {
		return model.JobState{}, fmt.Errorf("generate supervisor credential: %w", readErr)
	}
	hash, err := model.NewCredentialHash(credential)
	if err != nil {
		return model.JobState{}, err
	}
	submittedAt := service.now().UTC()
	if _, submitErr := service.store.Submit(
		ctx,
		jobID,
		specification,
		hash,
		submittedAt,
		submittedAt.Add(defaultClaimWindow),
	); submitErr != nil {
		return model.JobState{}, translateStoreError("submit job", submitErr)
	}

	if _, launchErr := service.launch(ctx, supervisor.LaunchOptions{
		Store:      service.store,
		Executable: service.executable,
		StateDir:   service.stateDir,
		JobID:      jobID,
		Credential: credential,
	}); launchErr != nil {
		return service.reconcileFailedLaunch(ctx, jobID, launchErr)
	}

	loadCtx, cancelLoad := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancelLoad()
	job, err := service.store.GetJob(loadCtx, jobID)
	if err != nil {
		return model.JobState{}, translateStoreError("load submitted job", err)
	}

	return job, nil
}

// List returns jobs newest first.
func (service *Service) List(ctx context.Context) ([]model.JobState, error) {
	jobs, err := service.store.ListJobs(ctx, store.ListJobsOptions{})
	if err != nil {
		return nil, translateStoreError("list jobs", err)
	}
	changed := false
	for _, job := range jobs {
		if reconciled, reconcileErr := service.reconcileExpiredSubmission(ctx, job); reconcileErr != nil {
			return nil, reconcileErr
		} else if reconciled {
			changed = true
		}
	}
	if changed {
		jobs, err = service.store.ListJobs(ctx, store.ListJobsOptions{})
		if err != nil {
			return nil, translateStoreError("reload reconciled jobs", err)
		}
	}

	return jobs, nil
}

// Inspect resolves one selector and returns a transactionally consistent job
// snapshot and ordered run history.
func (service *Service) Inspect(ctx context.Context, selector string) (JobDetails, error) {
	job, runs, err := service.store.GetJobWithRuns(ctx, selector)
	if err != nil {
		return JobDetails{}, translateStoreError("inspect job", err)
	}
	if reconciled, reconcileErr := service.reconcileExpiredSubmission(ctx, job); reconcileErr != nil {
		return JobDetails{}, reconcileErr
	} else if reconciled {
		job, runs, err = service.store.GetJobWithRuns(ctx, selector)
		if err != nil {
			return JobDetails{}, translateStoreError("reload reconciled job", err)
		}
	}
	if reconciled, reconcileErr := service.reconcileStaleOwnership(ctx, job, runs); reconcileErr != nil {
		return JobDetails{}, reconcileErr
	} else if reconciled {
		job, runs, err = service.store.GetJobWithRuns(ctx, selector)
		if err != nil {
			return JobDetails{}, translateStoreError("reload reconciled ownership", err)
		}
	}

	return JobDetails{Job: job, Runs: runs}, nil
}

func (service *Service) reconcileFailedLaunch(
	ctx context.Context,
	jobID model.JobID,
	launchErr error,
) (model.JobState, error) {
	job, getErr := service.store.GetJob(ctx, jobID)
	if getErr == nil && job.SupervisorID.Valid() && job.Phase != model.JobPhaseSubmitting {
		return job, nil
	}
	if getErr == nil {
		_, reconcileErr := service.reconcileExpiredSubmission(ctx, job)
		if reconcileErr != nil {
			return model.JobState{}, errors.Join(
				fmt.Errorf("launch job supervisor: %w", launchErr),
				reconcileErr,
			)
		}
	}

	return model.JobState{}, errors.Join(
		fmt.Errorf("launch job supervisor: %w", launchErr),
		getErr,
	)
}

func (service *Service) reconcileExpiredSubmission(
	ctx context.Context,
	job model.JobState,
) (bool, error) {
	if job.Phase != model.JobPhaseSubmitting || job.ClaimDeadline == nil ||
		service.now().UTC().Before(*job.ClaimDeadline) {
		return false, nil
	}
	_, err := service.store.MarkSubmissionFailed(
		ctx,
		job.ID,
		"supervisor_claim_expired",
		service.now().UTC(),
	)
	if err == nil {
		return true, nil
	}
	if model.IsConflict(err) || errors.Is(err, store.ErrConflict) {
		// A concurrently claiming supervisor or another client won the compare-
		// and-swap. Reloading by the caller yields the authoritative result.
		return true, nil
	}

	return false, translateStoreError("reconcile expired submission", err)
}

func (service *Service) reconcileStaleOwnership(
	ctx context.Context,
	job model.JobState,
	runs []model.RunState,
) (bool, error) {
	if job.Phase == model.JobPhaseCompleted || !job.SupervisorID.Valid() {
		return false, nil
	}
	owner, err := service.store.GetSupervisor(ctx, job.SupervisorID)
	if err != nil {
		return false, translateStoreError("load job supervisor", err)
	}
	if service.now().UTC().Before(owner.LeaseExpiresAt) {
		return false, nil
	}

	identity := platform.ProcessIdentity{
		PID:      owner.Process.PID,
		Creation: owner.Process.CreationID,
		Boot:     owner.Process.BootID,
	}
	alive, aliveErr := platform.Alive(identity)
	if aliveErr != nil && !errors.Is(aliveErr, platform.ErrIdentityMismatch) {
		return false, fmt.Errorf("reconcile stale supervisor identity: %w", aliveErr)
	}
	if alive {
		return false, nil
	}

	activeRun := findRun(runs, job.ActiveRunID)
	var logs *model.LogMetadata
	if activeRun != nil {
		recovered := recoverLogMetadata(service.stateDir, job.ID, *activeRun)
		logs = &recovered
	}
	_, err = service.store.MarkOwnershipLost(
		ctx,
		job.ID,
		logs,
		"supervisor_lease_expired",
		service.now().UTC(),
	)
	if err == nil || model.IsConflict(err) || errors.Is(err, store.ErrConflict) {
		return true, nil
	}

	return false, translateStoreError("record lost job ownership", err)
}

func findRun(runs []model.RunState, id model.RunID) *model.RunState {
	for index := range runs {
		if runs[index].ID == id {
			return &runs[index]
		}
	}

	return nil
}

func recoverLogMetadata(
	stateDir string,
	jobID model.JobID,
	run model.RunState,
) model.LogMetadata {
	logs := run.Logs
	logs.Integrity = model.LogIntegrityPartial
	logs.RecordingHealth = model.RecordingDegraded
	logs.DiagnosticCode = "supervisor_lease_expired"
	if stdout, err := os.Stat(logs.StdoutPath); err == nil {
		logs.StdoutSize = stdout.Size()
	}
	if stderr, err := os.Stat(logs.StderrPath); err == nil {
		logs.StderrSize = stderr.Size()
	}
	reader, err := logstore.OpenRun(stateDir, jobID.String(), run.Number)
	if err != nil {
		return logs
	}
	status, err := reader.ScanIndex(nil)
	if err != nil {
		if errors.Is(err, logstore.ErrCorruptIndex) {
			logs.Integrity = model.LogIntegrityCorrupt
		}

		return logs
	}
	if !status.TornTail && status.UnindexedStdoutBytes == 0 && status.UnindexedStderrBytes == 0 {
		logs.Integrity = model.LogIntegrityValid
	}

	return logs
}

// ReadLogs reads the latest run using raw stream or observed combined order.
func (service *Service) ReadLogs(
	ctx context.Context,
	selector string,
	stream LogStream,
) ([]byte, error) {
	details, err := service.Inspect(ctx, selector)
	if err != nil {
		return nil, err
	}
	if len(details.Runs) == 0 {
		return nil, fmt.Errorf("read job logs: %w", ErrConflict)
	}
	run := details.Runs[len(details.Runs)-1]
	reader, err := logstore.OpenRun(service.stateDir, details.Job.ID.String(), run.Number)
	if err != nil {
		return nil, fmt.Errorf("open job logs: %w", err)
	}

	var output bytes.Buffer
	switch stream {
	case LogStdout:
		_, err = reader.CopyStream(&output, logstore.Stdout)
	case LogStderr:
		_, err = reader.CopyStream(&output, logstore.Stderr)
	case LogBoth:
		_, _, err = reader.CopyCombined(&output)
	default:
		return nil, fmt.Errorf("read job logs: invalid stream %q", stream)
	}
	if err != nil {
		return nil, fmt.Errorf("read job logs: %w", err)
	}

	return output.Bytes(), nil
}

// Cancel durably records cancellation before signaling the verified tree.
func (service *Service) Cancel(ctx context.Context, selector string) (model.JobState, error) {
	job, err := service.store.ResolveJob(ctx, selector)
	if err != nil {
		return model.JobState{}, translateStoreError("resolve job", err)
	}
	result, err := service.store.RequestCancellation(ctx, job.ID, service.now().UTC())
	if err != nil {
		return model.JobState{}, translateStoreError("request cancellation", err)
	}
	if result.Run == nil || result.Run.Process == nil {
		return result.Job, nil
	}

	identity := platform.ProcessIdentity{
		PID:      result.Run.Process.PID,
		Creation: result.Run.Process.CreationID,
		Boot:     result.Run.Process.BootID,
	}
	if terminateErr := platform.Terminate(identity, false); terminateErr != nil {
		return result.Job, fmt.Errorf("request graceful target termination: %w", terminateErr)
	}
	if !result.Job.Spec.StopPolicy().ForceAfterGrace {
		return result.Job, nil
	}
	if waitErr := waitForExit(ctx, identity, result.Job.Spec.StopPolicy().GracePeriod); waitErr != nil {
		return result.Job, waitErr
	}
	alive, err := platform.Alive(identity)
	if err != nil && !errors.Is(err, platform.ErrIdentityMismatch) {
		return result.Job, fmt.Errorf("recheck target before forced termination: %w", err)
	}
	if alive {
		if forceErr := platform.Terminate(identity, true); forceErr != nil {
			return result.Job, fmt.Errorf("force target termination: %w", forceErr)
		}
	}

	return result.Job, nil
}

func waitForExit(ctx context.Context, identity platform.ProcessIdentity, grace time.Duration) error {
	if grace <= 0 {
		return nil
	}
	timer := time.NewTimer(grace)
	defer timer.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for target termination: %w", ctx.Err())
		case <-timer.C:
			return nil
		case <-ticker.C:
			alive, err := platform.Alive(identity)
			if errors.Is(err, platform.ErrIdentityMismatch) || (!alive && err == nil) {
				return nil
			}
			if err != nil {
				return fmt.Errorf("wait for target termination: %w", err)
			}
		}
	}
}

func translateStoreError(operation string, err error) error {
	switch {
	case errors.Is(err, store.ErrNotFound):
		return fmt.Errorf("%s: %w", operation, ErrNotFound)
	case errors.Is(err, store.ErrAmbiguous):
		return fmt.Errorf("%s: %w", operation, ErrAmbiguous)
	case errors.Is(err, store.ErrConflict), model.IsConflict(err):
		return fmt.Errorf("%s: %w", operation, ErrConflict)
	default:
		return fmt.Errorf("%s: %w", operation, err)
	}
}

var _ Backend = (*Service)(nil)
