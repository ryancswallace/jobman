package supervisor

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"sync"
	"time"

	"github.com/ryancswallace/jobman/internal/buildinfo"
	"github.com/ryancswallace/jobman/internal/executor"
	"github.com/ryancswallace/jobman/internal/logstore"
	"github.com/ryancswallace/jobman/internal/model"
	"github.com/ryancswallace/jobman/internal/platform"
	"github.com/ryancswallace/jobman/internal/store"
)

const (
	leaseDuration = 15 * time.Second
	leaseInterval = 5 * time.Second
)

// Run claims and owns one job until its single initial-slice run is terminal.
func Run(
	ctx context.Context,
	stateDir string,
	jobIDText string,
	credentialReader io.Reader,
	acknowledgementWriter io.Writer,
) error {
	jobID, err := model.ParseJobID(jobIDText)
	if err != nil {
		return err
	}
	credential := make([]byte, credentialSize)
	if _, readErr := io.ReadFull(credentialReader, credential); readErr != nil {
		return fmt.Errorf("read supervisor credential: %w", readErr)
	}

	ids, err := model.NewUUIDv7Generator(time.Now, rand.Reader)
	if err != nil {
		return fmt.Errorf("construct supervisor ID source: %w", err)
	}
	database, err := store.Open(ctx, store.Options{
		StateDir:      stateDir,
		JobmanVersion: buildinfo.Version,
		Now:           time.Now,
		EventIDs:      ids,
	})
	if err != nil {
		return err
	}
	defer database.Close()

	supervisorID, err := ids.NewSupervisorID()
	if err != nil {
		return err
	}
	identity, err := platform.Inspect(os.Getpid())
	if err != nil {
		return fmt.Errorf("inspect supervisor identity: %w", err)
	}
	now := time.Now().UTC()
	claim, err := database.Claim(
		ctx,
		jobID,
		credential,
		supervisorID,
		modelIdentity(identity),
		now,
		now.Add(leaseDuration),
	)
	if err != nil {
		return fmt.Errorf("claim job: %w", err)
	}

	acknowledgement := Acknowledgement{
		SchemaVersion: 1,
		JobID:         jobID,
		SupervisorID:  supervisorID,
	}
	if err := json.NewEncoder(acknowledgementWriter).Encode(acknowledgement); err != nil {
		return fmt.Errorf("acknowledge supervisor claim: %w", err)
	}
	if closer, ok := acknowledgementWriter.(io.Closer); ok {
		if err := closer.Close(); err != nil {
			return fmt.Errorf("close supervisor acknowledgement: %w", err)
		}
	}

	ownershipCtx := context.WithoutCancel(ctx)
	leaseCtx, stopLease := context.WithCancel(ownershipCtx)
	defer stopLease()
	go renewLease(leaseCtx, database, supervisorID)

	return executeClaimedJob(ctx, ownershipCtx, database, ids, stateDir, claim.Job)
}

func executeClaimedJob(
	stopCtx context.Context,
	operationCtx context.Context,
	database *store.Store,
	ids *model.UUIDv7Generator,
	stateDir string,
	job model.JobState,
) error {
	if err := recordContextCancellation(stopCtx, operationCtx, database, job.ID); err != nil {
		return err
	}
	current, err := database.GetJob(operationCtx, job.ID)
	if err != nil {
		return err
	}
	if current.Phase == model.JobPhaseStopping && current.Cancellation != nil {
		_, finalizeErr := database.FinalizeCancellationWithoutRun(operationCtx, current.ID, time.Now().UTC())

		return finalizeErr
	}

	runID, err := ids.NewRunID()
	if err != nil {
		return err
	}
	capture, err := logstore.CreateRun(stateDir, job.ID.String(), 1)
	if err != nil {
		return fmt.Errorf("create run logs: %w", err)
	}
	paths := capture.Paths()
	logs := model.LogMetadata{
		StdoutPath:      paths.Stdout,
		StderrPath:      paths.Stderr,
		IndexPath:       paths.Index,
		IndexVersion:    model.LogIndexVersion,
		Integrity:       model.LogIntegrityPending,
		RecordingHealth: model.RecordingHealthy,
	}
	reservedAt := time.Now().UTC()
	if _, reserveErr := database.ReserveRun(operationCtx, job.ID, runID, 1, logs, reservedAt); reserveErr != nil {
		return errors.Join(fmt.Errorf("reserve run: %w", reserveErr), capture.Close())
	}
	if stopErr := recordContextCancellation(stopCtx, operationCtx, database, job.ID); stopErr != nil {
		return errors.Join(stopErr, capture.Close())
	}
	if finalized, cancellationErr := finalizeReservedCancellation(
		operationCtx,
		database,
		capture,
		job.ID,
		runID,
		logs,
	); cancellationErr != nil || finalized {
		return cancellationErr
	}

	target, err := prepareTarget(job)
	if err != nil {
		return finalizeStartFailure(operationCtx, database, capture, job.ID, runID, logs, err)
	}
	defer target.stdin.Close()
	if startErr := target.command.Start(); startErr != nil {
		return finalizeStartFailure(
			operationCtx,
			database,
			capture,
			job.ID,
			runID,
			logs,
			errors.Join(startErr, target.closeOutputPipes()),
		)
	}

	targetIdentity, err := platform.Inspect(target.command.Process.Pid)
	if err != nil {
		killErr := target.command.Process.Kill()
		waitErr := target.command.Wait()

		return finalizeStartFailure(
			operationCtx,
			database,
			capture,
			job.ID,
			runID,
			logs,
			errors.Join(err, killErr, waitErr),
		)
	}

	return superviseStartedTarget(
		stopCtx,
		operationCtx,
		database,
		capture,
		job.ID,
		runID,
		logs,
		target,
		targetIdentity,
	)
}

func superviseStartedTarget(
	stopCtx context.Context,
	operationCtx context.Context,
	database *store.Store,
	capture *logstore.Run,
	jobID model.JobID,
	runID model.RunID,
	logs model.LogMetadata,
	target *preparedTarget,
	targetIdentity platform.ProcessIdentity,
) error {
	var captureGroup sync.WaitGroup
	captureErrors := make(chan error, 2)
	captureGroup.Add(2)
	go drainPipe(&captureGroup, target.stdout, capture, logstore.Stdout, captureErrors)
	go drainPipe(&captureGroup, target.stderr, capture, logstore.Stderr, captureErrors)

	startedAt := time.Now().UTC()
	if _, err := database.MarkProcessStarted(
		operationCtx,
		jobID,
		runID,
		target.resolved,
		modelIdentity(targetIdentity),
		startedAt,
	); err != nil {
		return handlePublishFailure(
			operationCtx,
			database,
			capture,
			jobID,
			runID,
			logs,
			target,
			targetIdentity,
			&captureGroup,
			captureErrors,
			err,
		)
	}

	return waitAndFinalizeRun(
		stopCtx,
		operationCtx,
		database,
		capture,
		jobID,
		runID,
		logs,
		target,
		targetIdentity,
		&captureGroup,
		captureErrors,
	)
}

func handlePublishFailure(
	ctx context.Context,
	database *store.Store,
	capture *logstore.Run,
	jobID model.JobID,
	runID model.RunID,
	logs model.LogMetadata,
	target *preparedTarget,
	targetIdentity platform.ProcessIdentity,
	captureGroup *sync.WaitGroup,
	captureErrors chan error,
	publishErr error,
) error {
	terminateErr := platform.Terminate(targetIdentity, true)
	captureGroup.Wait()
	waitErr := target.command.Wait()
	close(captureErrors)
	captureErr := collectCaptureErrors(captureErrors)
	closeErr := capture.Close()
	logs = completedLogMetadata(logs, captureErr, closeErr)
	latest, getErr := database.GetJob(ctx, jobID)
	if getErr == nil && latest.Cancellation != nil {
		exit := processExitInfo(target.command, waitErr, time.Now().UTC())
		_, finalizeErr := database.FinalizeRun(
			ctx,
			jobID,
			runID,
			model.RunOutcomeCancelled,
			exit,
			logs,
			time.Now().UTC(),
		)

		return errors.Join(terminateErr, finalizeErr, captureErr, closeErr)
	}
	_, lostErr := database.MarkOwnershipLost(
		ctx,
		jobID,
		&logs,
		"process_start_publish_failed",
		time.Now().UTC(),
	)

	return errors.Join(
		fmt.Errorf("publish process start: %w", publishErr),
		terminateErr,
		getErr,
		captureErr,
		closeErr,
		lostErr,
	)
}

func waitAndFinalizeRun(
	stopCtx context.Context,
	baseOperationCtx context.Context,
	database *store.Store,
	capture *logstore.Run,
	jobID model.JobID,
	runID model.RunID,
	logs model.LogMetadata,
	target *preparedTarget,
	targetIdentity platform.ProcessIdentity,
	captureGroup *sync.WaitGroup,
	captureErrors chan error,
) error {
	// StdoutPipe and StderrPipe require every read to finish before Wait. Calling
	// Wait first lets os/exec close a pipe underneath a drain goroutine and can
	// both lose tail bytes and falsely report degraded capture.
	completion := make(chan error, 1)
	go func() {
		captureGroup.Wait()
		completion <- target.command.Wait()
	}()
	waitErr, operationCtx, releaseOperation, controlErr := awaitTarget(
		stopCtx,
		baseOperationCtx,
		database,
		jobID,
		targetIdentity,
		completion,
	)
	defer releaseOperation()
	if waitErr == nil && controlErr != nil {
		return controlErr
	}
	close(captureErrors)
	captureErr := collectCaptureErrors(captureErrors)
	closeErr := capture.Close()
	logs = completedLogMetadata(logs, captureErr, closeErr)

	latest, getErr := database.GetJob(operationCtx, jobID)
	if getErr != nil {
		return errors.Join(waitErr, captureErr, closeErr, getErr)
	}
	outcome := model.RunOutcomeFailure
	if latest.Cancellation != nil {
		outcome = model.RunOutcomeCancelled
	} else if waitErr == nil {
		outcome = model.RunOutcomeSuccess
	}
	exit := processExitInfo(target.command, waitErr, time.Now().UTC())
	if _, err := database.FinalizeRun(
		operationCtx,
		jobID,
		runID,
		outcome,
		exit,
		logs,
		time.Now().UTC(),
	); err != nil {
		return errors.Join(fmt.Errorf("finalize run: %w", err), captureErr, closeErr)
	}

	return controlErr
}

func awaitTarget(
	stopCtx context.Context,
	baseOperationCtx context.Context,
	database *store.Store,
	jobID model.JobID,
	identity platform.ProcessIdentity,
	completion <-chan error,
) (waitErr error, operationCtx context.Context, release context.CancelFunc, controlErr error) {
	select {
	case waitErr = <-completion:
		return waitErr, baseOperationCtx, func() {}, nil
	case <-stopCtx.Done():
	}

	intentCtx, cancelIntent := context.WithTimeout(baseOperationCtx, 2*time.Second)
	result, err := database.RequestCancellation(intentCtx, jobID, time.Now().UTC())
	cancelIntent()
	if err != nil {
		fallbackCtx, cancel := context.WithTimeout(baseOperationCtx, 2*time.Second)

		return nil, fallbackCtx, cancel, fmt.Errorf("record cancellation after supervisor signal: %w", err)
	}

	grace := result.Job.Spec.StopPolicy().GracePeriod
	operationCtx, release = context.WithTimeout(baseOperationCtx, grace+10*time.Second)
	if err := platform.Terminate(identity, false); err != nil {
		return nil, operationCtx, release, fmt.Errorf("forward graceful supervisor signal: %w", err)
	}

	timer := time.NewTimer(grace)
	defer timer.Stop()
	select {
	case waitErr = <-completion:
		return waitErr, operationCtx, release, nil
	case <-timer.C:
	case <-operationCtx.Done():
		return nil, operationCtx, release, fmt.Errorf("wait after supervisor signal: %w", operationCtx.Err())
	}

	if !result.Job.Spec.StopPolicy().ForceAfterGrace {
		return nil, operationCtx, release, errors.New("target did not exit after forwarded graceful supervisor signal")
	}
	if err := platform.Terminate(identity, true); err != nil {
		return nil, operationCtx, release, fmt.Errorf("forward forced supervisor signal: %w", err)
	}

	select {
	case waitErr = <-completion:
		return waitErr, operationCtx, release, nil
	case <-operationCtx.Done():
		return nil, operationCtx, release, fmt.Errorf("wait after forced supervisor signal: %w", operationCtx.Err())
	}
}

func recordContextCancellation(
	stopCtx context.Context,
	operationCtx context.Context,
	database *store.Store,
	jobID model.JobID,
) error {
	if stopCtx.Err() == nil {
		return nil
	}
	if _, err := database.RequestCancellation(operationCtx, jobID, time.Now().UTC()); err != nil {
		return fmt.Errorf("record cancellation after supervisor signal: %w", err)
	}

	return nil
}

type preparedTarget struct {
	command  *exec.Cmd
	resolved string
	stdin    *os.File
	stdout   io.ReadCloser
	stderr   io.ReadCloser
}

func prepareTarget(job model.JobState) (*preparedTarget, error) {
	command, resolved, err := executor.Command(executor.Request{
		Executable: job.Spec.Executable(),
		Arguments:  job.Spec.Arguments(),
		Directory:  job.Spec.WorkingDirectory(),
		BaseEnv:    os.Environ(),
		AddEnv:     job.Spec.Environment(),
		RemoveEnv:  job.Spec.UnsetEnvironment(),
	})
	if err != nil {
		return nil, err
	}
	platform.ConfigureTarget(command)

	nullInput, err := os.Open(os.DevNull)
	if err != nil {
		return nil, err
	}
	command.Stdin = nullInput
	stdout, err := command.StdoutPipe()
	if err != nil {
		return nil, errors.Join(err, nullInput.Close())
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return nil, errors.Join(err, stdout.Close(), nullInput.Close())
	}

	return &preparedTarget{
		command:  command,
		resolved: resolved,
		stdin:    nullInput,
		stdout:   stdout,
		stderr:   stderr,
	}, nil
}

func (target *preparedTarget) closeOutputPipes() error {
	return errors.Join(target.stdout.Close(), target.stderr.Close())
}

func collectCaptureErrors(captureErrors <-chan error) error {
	var result error
	for item := range captureErrors {
		result = errors.Join(result, item)
	}

	return result
}

func finalizeReservedCancellation(
	ctx context.Context,
	database *store.Store,
	capture *logstore.Run,
	jobID model.JobID,
	runID model.RunID,
	logs model.LogMetadata,
) (bool, error) {
	current, err := database.GetJob(ctx, jobID)
	if err != nil {
		return false, err
	}
	if current.Cancellation == nil {
		return false, nil
	}

	closeErr := capture.Close()
	logs = completedLogMetadata(logs, nil, closeErr)
	_, transitionErr := database.FinalizeRun(
		ctx,
		jobID,
		runID,
		model.RunOutcomeCancelled,
		nil,
		logs,
		time.Now().UTC(),
	)

	return true, errors.Join(closeErr, transitionErr)
}

func finalizeStartFailure(
	ctx context.Context,
	database *store.Store,
	capture *logstore.Run,
	jobID model.JobID,
	runID model.RunID,
	logs model.LogMetadata,
	cause error,
) error {
	closeErr := capture.Close()
	// A target start failure does not imply that the empty raw streams or their
	// index were recorded incorrectly. Keep execution and recording health
	// independent as required by the persisted model.
	logs = completedLogMetadata(logs, nil, closeErr)
	_, transitionErr := database.MarkStartFailed(
		ctx,
		jobID,
		runID,
		logs,
		"target_start_failed",
		time.Now().UTC(),
	)

	return errors.Join(fmt.Errorf("start target: %w", cause), closeErr, transitionErr)
}

func drainPipe(
	group *sync.WaitGroup,
	source io.ReadCloser,
	capture *logstore.Run,
	stream logstore.Stream,
	errorsChannel chan<- error,
) {
	defer group.Done()
	captureErr := copyPipe(source, capture, stream)
	closeErr := source.Close()
	if err := errors.Join(captureErr, closeErr); err != nil {
		errorsChannel <- err
	}
}

func copyPipe(source io.Reader, capture *logstore.Run, stream logstore.Stream) error {
	writer, err := capture.Writer(stream)
	if err != nil {
		return errors.Join(err, drainDiscard(source))
	}
	if _, err := io.Copy(writer, source); err != nil {
		return errors.Join(err, drainDiscard(source))
	}

	return nil
}

func drainDiscard(source io.Reader) error {
	_, err := io.Copy(io.Discard, source)
	if err != nil {
		return fmt.Errorf("drain target output after capture failure: %w", err)
	}

	return nil
}

func completedLogMetadata(
	metadata model.LogMetadata,
	captureErr error,
	closeErr error,
) model.LogMetadata {
	metadata.Integrity = model.LogIntegrityValid
	metadata.RecordingHealth = model.RecordingHealthy
	stdout, stdoutErr := os.Stat(metadata.StdoutPath)
	stderr, stderrErr := os.Stat(metadata.StderrPath)
	if stdoutErr == nil {
		metadata.StdoutSize = stdout.Size()
	}
	if stderrErr == nil {
		metadata.StderrSize = stderr.Size()
	}
	if captureErr != nil || closeErr != nil || stdoutErr != nil || stderrErr != nil {
		metadata.Integrity = model.LogIntegrityPartial
		metadata.RecordingHealth = model.RecordingDegraded
		metadata.DiagnosticCode = "log_capture_degraded"
	}

	return metadata
}

func modelIdentity(identity platform.ProcessIdentity) model.ProcessIdentity {
	return model.ProcessIdentity{
		PID:        identity.PID,
		Platform:   runtime.GOOS,
		CreationID: identity.Creation,
		BootID:     identity.Boot,
		TreeID:     strconv.Itoa(identity.PID),
	}
}

func renewLease(ctx context.Context, database *store.Store, supervisorID model.SupervisorID) {
	ticker := time.NewTicker(leaseInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			_, renewErr := database.RenewLease(
				ctx,
				supervisorID,
				now.UTC(),
				now.UTC().Add(leaseDuration),
			)
			if renewErr != nil && ctx.Err() != nil {
				return
			}
		}
	}
}
