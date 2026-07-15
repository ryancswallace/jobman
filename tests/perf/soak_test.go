package perf

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ryancswallace/jobman/internal/logstore"
	"github.com/ryancswallace/jobman/internal/model"
	"github.com/ryancswallace/jobman/internal/store"
)

func TestSoakStorageLogsCleanupAndAdmissionFairness(test *testing.T) {
	if os.Getenv("JOBMAN_SOAK") != "1" {
		test.Skip("set JOBMAN_SOAK=1 to run the soak suite")
	}
	duration, err := time.ParseDuration(os.Getenv("JOBMAN_SOAK_DURATION"))
	if err != nil || duration < time.Second {
		test.Fatalf("JOBMAN_SOAK_DURATION must be at least one second: %q", os.Getenv("JOBMAN_SOAK_DURATION"))
	}
	fixture := newPerformanceFixture(test)
	ctx, cancel := context.WithTimeout(test.Context(), duration)
	defer cancel()
	var submissions, logBytes, cleanups, fairnessCycles atomic.Uint64
	firstErr := make(chan error, 1)
	recordError := func(err error) {
		select {
		case firstErr <- err:
			cancel()
		default:
		}
	}

	var wait sync.WaitGroup
	workers := min(max(runtime.GOMAXPROCS(0), 2), 8)
	for worker := range workers {
		wait.Add(1)
		go func(worker int) {
			defer wait.Done()
			soakStorageAndLogs(ctx, fixture, worker, &submissions, &logBytes, &cleanups, recordError)
		}(worker)
	}
	wait.Add(1)
	go func() {
		defer wait.Done()
		soakAdmissionFairness(ctx, fixture, &fairnessCycles, recordError)
	}()
	wait.Wait()
	select {
	case soakErr := <-firstErr:
		test.Fatal(soakErr)
	default:
	}
	if submissions.Load() == 0 || logBytes.Load() == 0 || cleanups.Load() == 0 || fairnessCycles.Load() == 0 {
		test.Fatalf(
			"soak made insufficient progress: submissions=%d log_bytes=%d cleanups=%d fairness_cycles=%d",
			submissions.Load(), logBytes.Load(), cleanups.Load(), fairnessCycles.Load(),
		)
	}
	test.Logf(
		"soak totals: submissions=%d log_bytes=%d cleanups=%d fairness_cycles=%d",
		submissions.Load(), logBytes.Load(), cleanups.Load(), fairnessCycles.Load(),
	)
}

func soakStorageAndLogs(
	ctx context.Context,
	fixture *performanceFixture,
	worker int,
	submissions, logBytes, cleanups *atomic.Uint64,
	recordError func(error),
) {
	payload := make([]byte, 16<<10)
	for ctx.Err() == nil {
		jobID, err := fixture.ids.NewJobID()
		if err != nil {
			recordError(err)

			return
		}
		if submitErr := submitJob(ctx, fixture, jobID, time.Now().UTC()); submitErr != nil {
			if ctx.Err() == nil {
				recordError(fmt.Errorf("worker %d submit: %w", worker, submitErr))
			}

			return
		}
		submissions.Add(1)
		run, err := logstore.CreateRunWithOptions(
			fixture.store.StateDir(),
			jobID.String(),
			1,
			logstore.RunOptions{Rotation: logstore.RotationPolicy{SegmentBytes: 8 << 10, MaxSegmentsPerStream: 4}},
		)
		if err != nil {
			recordError(fmt.Errorf("worker %d create logs: %w", worker, err))

			return
		}
		if _, err := run.Append(logstore.Stdout, payload, time.Now().UTC()); err != nil {
			recordError(errors.Join(fmt.Errorf("worker %d append logs: %w", worker, err), run.Close()))

			return
		}
		if err := run.Close(); err != nil {
			recordError(fmt.Errorf("worker %d close logs: %w", worker, err))

			return
		}
		logBytes.Add(uint64(len(payload)))
		if _, err := logstore.CleanupRun(
			ctx, fixture.store.StateDir(), jobID.String(), 1,
			func(context.Context) (bool, error) { return true, nil },
		); err != nil {
			if ctx.Err() == nil {
				recordError(fmt.Errorf("worker %d cleanup logs: %w", worker, err))
			}

			return
		}
		cleanups.Add(1)
		if submissions.Load()%100 == 0 {
			if _, err := fixture.store.ListJobs(ctx, store.ListJobsOptions{Limit: store.MaximumListLimit}); err != nil && ctx.Err() == nil {
				recordError(fmt.Errorf("worker %d list large store: %w", worker, err))

				return
			}
		}
	}
}

func soakAdmissionFairness(
	ctx context.Context,
	fixture *performanceFixture,
	cycles *atomic.Uint64,
	recordError func(error),
) {
	capacity := uint64(1)
	if err := fixture.store.SetConcurrencyLimit(ctx, "", &capacity, time.Now().UTC()); err != nil {
		recordError(fmt.Errorf("configure soak admission: %w", err))

		return
	}
	for ctx.Err() == nil {
		if err := runFairnessCycle(ctx, fixture); err != nil {
			if ctx.Err() == nil {
				recordError(err)
			}

			return
		}
		cycles.Add(1)
	}
}

func runFairnessCycle(ctx context.Context, fixture *performanceFixture) error {
	ids := make([]model.JobID, 3)
	for index := range ids {
		jobID, err := fixture.ids.NewJobID()
		if err != nil {
			return err
		}
		ids[index] = jobID
		if err := submitJob(ctx, fixture, jobID, time.Now().UTC()); err != nil {
			return fmt.Errorf("submit fairness job: %w", err)
		}
	}
	blocker := ids[0]
	queued := ids[1:]
	sort.Slice(queued, func(left, right int) bool { return queued[left] < queued[right] })
	now := time.Now().UTC()
	if _, err := fixture.store.TryAcquireAdmission(ctx, blocker, "", 1, now, time.Minute); err != nil {
		return fmt.Errorf("acquire fairness blocker: %w", err)
	}
	for index := len(queued) - 1; index >= 0; index-- {
		if _, err := fixture.store.TryAcquireAdmission(
			ctx, queued[index], "", 1, now.Add(time.Second), time.Minute,
		); !errors.Is(err, store.ErrCapacity) {
			return fmt.Errorf("queue fairness job %d: %w", index, err)
		}
	}
	if err := fixture.store.ReleaseAdmission(ctx, blocker, now.Add(2*time.Second)); err != nil {
		return fmt.Errorf("release fairness blocker: %w", err)
	}
	if _, err := fixture.store.TryAcquireAdmission(
		ctx, queued[1], "", 1, now.Add(3*time.Second), time.Minute,
	); !errors.Is(err, store.ErrCapacity) {
		return fmt.Errorf("younger fairness job bypassed older request: %w", err)
	}
	for index, jobID := range queued {
		if _, err := fixture.store.TryAcquireAdmission(
			ctx, jobID, "", 1, now.Add(time.Duration(index+4)*time.Second), time.Minute,
		); err != nil {
			return fmt.Errorf("acquire fairness job %d: %w", index, err)
		}
		if err := fixture.store.ReleaseAdmission(ctx, jobID, now.Add(time.Duration(index+5)*time.Second)); err != nil {
			return fmt.Errorf("release fairness job %d: %w", index, err)
		}
	}

	return nil
}
