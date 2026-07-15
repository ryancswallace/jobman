package perf

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/ryancswallace/jobman/internal/logstore"
	"github.com/ryancswallace/jobman/internal/model"
	"github.com/ryancswallace/jobman/internal/store"
)

const (
	defaultLargeStoreJobs = 5000
	fairnessQueueDepth    = 256
)

type performanceFixture struct {
	store      *store.Store
	ids        *model.UUIDv7Generator
	spec       model.JobSpec
	credential model.CredentialHash
}

func BenchmarkLargeStoreList(benchmark *testing.B) {
	fixture := newPerformanceFixture(benchmark)
	populateJobs(benchmark, fixture, performanceStoreJobs())
	benchmark.ReportAllocs()
	benchmark.ResetTimer()
	for benchmark.Loop() {
		jobs, err := fixture.store.ListJobs(
			benchmark.Context(),
			store.ListJobsOptions{Limit: store.MaximumListLimit},
		)
		if err != nil {
			benchmark.Fatal(err)
		}
		if len(jobs) != store.MaximumListLimit {
			benchmark.Fatalf("listed %d jobs, want %d", len(jobs), store.MaximumListLimit)
		}
	}
}

func BenchmarkConcurrentSubmissions(benchmark *testing.B) {
	fixture := newPerformanceFixture(benchmark)
	var firstErr error
	var errOnce sync.Once
	benchmark.ReportAllocs()
	benchmark.ResetTimer()
	benchmark.RunParallel(func(parallel *testing.PB) {
		for parallel.Next() {
			if err := submitGeneratedJob(benchmark.Context(), fixture); err != nil {
				errOnce.Do(func() { firstErr = err })
			}
		}
	})
	if firstErr != nil {
		benchmark.Fatal(firstErr)
	}
}

func BenchmarkRotatedLogThroughput(benchmark *testing.B) {
	stateDir := filepath.Join(benchmark.TempDir(), "state")
	run, err := logstore.CreateRunWithOptions(
		stateDir,
		"018f0000-0000-7000-8000-000000000001",
		1,
		logstore.RunOptions{Rotation: logstore.RotationPolicy{SegmentBytes: 1 << 20}},
	)
	if err != nil {
		benchmark.Fatal(err)
	}
	benchmark.Cleanup(func() {
		if closeErr := run.Close(); closeErr != nil {
			benchmark.Errorf("close log capture: %v", closeErr)
		}
	})
	payload := make([]byte, 256<<10)
	benchmark.SetBytes(int64(len(payload)))
	benchmark.ReportAllocs()
	benchmark.ResetTimer()
	for benchmark.Loop() {
		if _, err := run.Append(logstore.Stdout, payload, time.Now().UTC()); err != nil {
			benchmark.Fatal(err)
		}
	}
}

func BenchmarkCleanupPlanningLargeStore(benchmark *testing.B) {
	now := time.Date(2026, time.July, 15, 0, 0, 0, 0, time.UTC)
	candidates := make([]logstore.RetentionCandidate, 100_000)
	for index := range candidates {
		candidates[index] = logstore.RetentionCandidate{
			JobID:       fmt.Sprintf("job-%06d", index/10),
			RunNumber:   uint64(index%10 + 1),
			CompletedAt: now.Add(-time.Duration(index) * time.Second),
			Bytes:       64 << 10,
		}
	}
	policy := logstore.RetentionPolicy{
		MaxAge:         logstore.RetentionAgeLimit{Maximum: 12 * time.Hour},
		MaxJobs:        logstore.RetentionLimit{Maximum: 2500},
		MaxRunsPerJob:  logstore.RetentionLimit{Maximum: 5},
		MaxBytesPerJob: logstore.RetentionLimit{Maximum: 256 << 10},
		MaxTotalBytes:  logstore.RetentionLimit{Maximum: 256 << 20},
	}
	benchmark.ReportAllocs()
	benchmark.ResetTimer()
	for benchmark.Loop() {
		selected, err := logstore.PlanRetention(now, candidates, policy)
		if err != nil {
			benchmark.Fatal(err)
		}
		if len(selected) == 0 {
			benchmark.Fatal("cleanup plan selected no candidates")
		}
	}
}

func BenchmarkRotatedLogCleanup(benchmark *testing.B) {
	stateDir := filepath.Join(benchmark.TempDir(), "state")
	ids := newIDGenerator(benchmark)
	payload := make([]byte, 256<<10)
	benchmark.ReportAllocs()
	for benchmark.Loop() {
		benchmark.StopTimer()
		jobID, err := ids.NewJobID()
		if err != nil {
			benchmark.Fatal(err)
		}
		run, err := logstore.CreateRunWithOptions(
			stateDir,
			jobID.String(),
			1,
			logstore.RunOptions{Rotation: logstore.RotationPolicy{SegmentBytes: 64 << 10}},
		)
		if err != nil {
			benchmark.Fatal(err)
		}
		if _, appendErr := run.Append(logstore.Stdout, payload, time.Now().UTC()); appendErr != nil {
			benchmark.Fatal(appendErr)
		}
		if closeErr := run.Close(); closeErr != nil {
			benchmark.Fatal(closeErr)
		}
		benchmark.StartTimer()
		result, err := logstore.CleanupRun(
			benchmark.Context(), stateDir, jobID.String(), 1,
			func(context.Context) (bool, error) { return true, nil },
		)
		if err != nil {
			benchmark.Fatal(err)
		}
		if result.Bytes < uint64(len(payload)) {
			benchmark.Fatalf("cleanup removed %d bytes, want at least %d", result.Bytes, len(payload))
		}
	}
}

func BenchmarkAdmissionFairnessQueue(benchmark *testing.B) {
	fixture := newPerformanceFixture(benchmark)
	queued := prepareFairnessQueue(benchmark, fixture, fairnessQueueDepth)
	youngest := queued[len(queued)-1]
	benchmark.ReportAllocs()
	benchmark.ResetTimer()
	for benchmark.Loop() {
		_, err := fixture.store.TryAcquireAdmission(
			benchmark.Context(), youngest, "", 1, time.Now().UTC(), time.Minute,
		)
		if !errors.Is(err, store.ErrCapacity) {
			benchmark.Fatalf("younger request bypassed queue: %v", err)
		}
	}
}

func TestPerformanceContractConcurrentSubmissions(test *testing.T) {
	test.Parallel()
	fixture := newPerformanceFixture(test)
	const workers = 8
	const jobsPerWorker = 25
	var wait sync.WaitGroup
	errorsFound := make(chan error, workers)
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for range jobsPerWorker {
				if err := submitGeneratedJob(test.Context(), fixture); err != nil {
					errorsFound <- err

					return
				}
			}
		}()
	}
	wait.Wait()
	close(errorsFound)
	for err := range errorsFound {
		test.Fatalf("concurrent submission: %v", err)
	}
	jobs, err := fixture.store.ListJobs(
		test.Context(), store.ListJobsOptions{Limit: workers * jobsPerWorker},
	)
	if err != nil {
		test.Fatal(err)
	}
	if len(jobs) != workers*jobsPerWorker {
		test.Fatalf("stored %d jobs, want %d", len(jobs), workers*jobsPerWorker)
	}
}

func TestPerformanceContractAdmissionFairness(test *testing.T) {
	test.Parallel()
	fixture := newPerformanceFixture(test)
	queued := prepareFairnessQueue(test, fixture, fairnessQueueDepth)
	for index, jobID := range queued {
		if index+1 < len(queued) {
			younger := queued[len(queued)-1]
			if younger != jobID {
				_, err := fixture.store.TryAcquireAdmission(
					test.Context(), younger, "", 1, time.Now().UTC(), time.Minute,
				)
				if !errors.Is(err, store.ErrCapacity) {
					test.Fatalf("job %s bypassed older job %s: %v", younger, jobID, err)
				}
			}
		}
		if _, err := fixture.store.TryAcquireAdmission(
			test.Context(), jobID, "", 1, time.Now().UTC(), time.Minute,
		); err != nil {
			test.Fatalf("acquire queued job %d: %v", index, err)
		}
		if err := fixture.store.ReleaseAdmission(test.Context(), jobID, time.Now().UTC()); err != nil {
			test.Fatalf("release queued job %d: %v", index, err)
		}
	}
}

func newPerformanceFixture(testingObject interface {
	Helper()
	TempDir() string
	Cleanup(func())
	Fatal(...any)
},
) *performanceFixture {
	testingObject.Helper()
	ids := newIDGenerator(testingObject)
	database, err := store.Open(context.Background(), store.Options{
		StateDir:      filepath.Join(testingObject.TempDir(), "state"),
		JobmanVersion: "performance-test",
		EventIDs:      ids,
	})
	if err != nil {
		testingObject.Fatal(err)
	}
	testingObject.Cleanup(func() {
		if closeErr := database.Close(); closeErr != nil {
			testingObject.Fatal(closeErr)
		}
	})
	specification, err := model.NewJobSpec(model.JobSpecInput{
		Executable: "/performance/target", WorkingDirectory: os.TempDir(), Name: "performance",
		EnvironmentInheritance: model.EnvironmentInheritSubmission, StdinPolicy: model.StdinNull,
		StopPolicy: model.StopPolicy{GracePeriod: time.Second, ForceAfterGrace: true},
	})
	if err != nil {
		testingObject.Fatal(err)
	}
	credential, err := model.NewCredentialHash(make([]byte, 32))
	if err != nil {
		testingObject.Fatal(err)
	}

	return &performanceFixture{store: database, ids: ids, spec: specification, credential: credential}
}

func newIDGenerator(testingObject interface {
	Helper()
	Fatal(...any)
},
) *model.UUIDv7Generator {
	testingObject.Helper()
	ids, err := model.NewUUIDv7Generator(time.Now, rand.Reader)
	if err != nil {
		testingObject.Fatal(err)
	}

	return ids
}

func submitGeneratedJob(ctx context.Context, fixture *performanceFixture) error {
	jobID, err := fixture.ids.NewJobID()
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	_, err = fixture.store.Submit(ctx, jobID, fixture.spec, fixture.credential, now, now.Add(time.Minute))

	return err
}

func populateJobs(testingObject interface {
	Helper()
	Fatal(...any)
}, fixture *performanceFixture, count int,
) {
	testingObject.Helper()
	for range count {
		if err := submitGeneratedJob(context.Background(), fixture); err != nil {
			testingObject.Fatal(err)
		}
	}
}

func prepareFairnessQueue(testingObject interface {
	Helper()
	Fatalf(string, ...any)
}, fixture *performanceFixture, depth int,
) []model.JobID {
	testingObject.Helper()
	capacity := uint64(1)
	now := time.Now().UTC()
	if err := fixture.store.SetConcurrencyLimit(context.Background(), "", &capacity, now); err != nil {
		testingObject.Fatalf("set admission capacity: %v", err)
	}
	blocker, idErr := fixture.ids.NewJobID()
	if idErr != nil {
		testingObject.Fatalf("create blocker ID: %v", idErr)
	}
	if submitErr := submitJob(context.Background(), fixture, blocker, now); submitErr != nil {
		testingObject.Fatalf("submit blocker: %v", submitErr)
	}
	if _, acquireErr := fixture.store.TryAcquireAdmission(
		context.Background(), blocker, "", 1, now, time.Minute,
	); acquireErr != nil {
		testingObject.Fatalf("acquire blocker: %v", acquireErr)
	}
	queued := make([]model.JobID, depth)
	for index := range queued {
		queued[index], idErr = fixture.ids.NewJobID()
		if idErr != nil {
			testingObject.Fatalf("create queued ID: %v", idErr)
		}
		if err := submitJob(context.Background(), fixture, queued[index], now); err != nil {
			testingObject.Fatalf("submit queued job: %v", err)
		}
	}
	sort.Slice(queued, func(left, right int) bool { return queued[left] < queued[right] })
	for index := len(queued) - 1; index >= 0; index-- {
		_, acquireErr := fixture.store.TryAcquireAdmission(
			context.Background(), queued[index], "", 1, now.Add(time.Second), time.Minute,
		)
		if !errors.Is(acquireErr, store.ErrCapacity) {
			testingObject.Fatalf("queue job %d: %v", index, acquireErr)
		}
	}
	if err := fixture.store.ReleaseAdmission(context.Background(), blocker, now.Add(2*time.Second)); err != nil {
		testingObject.Fatalf("release blocker: %v", err)
	}

	return queued
}

func submitJob(ctx context.Context, fixture *performanceFixture, jobID model.JobID, at time.Time) error {
	_, err := fixture.store.Submit(ctx, jobID, fixture.spec, fixture.credential, at, at.Add(time.Minute))

	return err
}

func performanceStoreJobs() int {
	value := os.Getenv("JOBMAN_PERF_STORE_JOBS")
	if value == "" {
		return defaultLargeStoreJobs
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < store.MaximumListLimit {
		return defaultLargeStoreJobs
	}

	return parsed
}
