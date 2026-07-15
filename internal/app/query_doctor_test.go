package app

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ryancswallace/jobman/internal/config"
	"github.com/ryancswallace/jobman/internal/model"
	"github.com/ryancswallace/jobman/internal/store"
)

func TestServiceListJobsFiltersAndIncludesRuns(test *testing.T) {
	test.Parallel()
	service, clock := newTestService(test)
	active, err := service.Submit(test.Context(), SubmitRequest{
		Name: "active", Executable: "true", WorkingDirectory: test.TempDir(),
		ExecutionPolicy: model.ExecutionPolicy{Groups: []string{"workers"}},
	})
	if err != nil {
		test.Fatal(err)
	}
	completed, completedRun, _ := completeCapturedRun(test, service, clock)

	listed, err := service.ListJobs(test.Context(), ListRequest{Limit: 10, ShowRuns: true})
	if err != nil {
		test.Fatal(err)
	}
	if len(listed) != 2 {
		test.Fatalf("ListJobs() count = %d, want 2", len(listed))
	}
	foundRun := false
	for _, item := range listed {
		if item.Job.ID == completed.ID && len(item.Runs) == 1 && item.Runs[0].ID == completedRun.ID {
			foundRun = true
		}
	}
	if !foundRun {
		test.Fatalf("ListJobs() = %+v, want completed run", listed)
	}

	tests := []struct {
		name    string
		request ListRequest
		want    model.JobID
	}{
		{name: "active", request: ListRequest{Active: true, Limit: 10}, want: active.ID},
		{name: "completed", request: ListRequest{Completed: true, Limit: 10}, want: completed.ID},
		{name: "phase", request: ListRequest{Phase: active.Phase, Limit: 10}, want: active.ID},
		{name: "outcome", request: ListRequest{Outcome: completed.Outcome, Limit: 10}, want: completed.ID},
		{name: "name", request: ListRequest{Name: "active", Limit: 10}, want: active.ID},
		{name: "group", request: ListRequest{Group: "workers", Limit: 10}, want: active.ID},
	}
	for _, item := range tests {
		test.Run(item.name, func(test *testing.T) {
			jobs, listErr := service.ListJobs(test.Context(), item.request)
			if listErr != nil {
				test.Fatal(listErr)
			}
			if len(jobs) != 1 || jobs[0].Job.ID != item.want {
				test.Fatalf("ListJobs(%s) = %+v, want %s", item.name, jobs, item.want)
			}
		})
	}
}

func TestListRequestValidationAndTimeMatching(test *testing.T) {
	test.Parallel()
	service, _ := newTestService(test)
	invalid := []ListRequest{
		{},
		{Limit: store.MaximumListLimit + 1},
		{Limit: 1, Active: true, Completed: true},
		{Limit: 1, Phase: "invalid"},
		{Limit: 1, Outcome: "invalid"},
	}
	for _, request := range invalid {
		if _, err := service.ListJobs(test.Context(), request); err == nil {
			test.Errorf("ListJobs(%+v) error = nil", request)
		}
	}
	at := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)
	job := model.JobState{SubmittedAt: at, Phase: model.JobPhaseCompleted}
	if !jobMatchesListRequest(job, ListRequest{
		SubmittedAfter: at.Add(-time.Second), SubmittedBefore: at.Add(time.Second), Completed: true,
	}) {
		test.Fatal("bounded completed job did not match")
	}
	for _, request := range []ListRequest{
		{SubmittedAfter: at},
		{SubmittedBefore: at},
		{Active: true},
		{Group: "missing"},
	} {
		if jobMatchesListRequest(job, request) {
			test.Errorf("jobMatchesListRequest(%+v) = true", request)
		}
	}
}

func TestServiceDoctorBackupAndRepair(test *testing.T) {
	test.Parallel()
	service, _ := newTestService(test)
	backup := filepath.Join(test.TempDir(), "doctor-backup.db")
	report, err := service.Doctor(test.Context(), DoctorRequest{BackupPath: backup})
	if err != nil {
		test.Fatal(err)
	}
	if !report.Store.Healthy || report.BackupPath != backup {
		test.Fatalf("Doctor(backup) = %+v", report)
	}
	if _, statErr := os.Stat(backup); statErr != nil {
		test.Fatalf("backup: %v", statErr)
	}
	report, err = service.Doctor(test.Context(), DoctorRequest{Repair: true})
	if err != nil {
		test.Fatal(err)
	}
	if !report.Store.Healthy || !report.Store.WALCheckpointed ||
		!report.StaleOwnershipReconciled || !report.NotificationsRecovered {
		test.Fatalf("Doctor(repair) = %+v", report)
	}
	if _, err := service.Doctor(test.Context(), DoctorRequest{BackupPath: backup}); err == nil {
		test.Fatal("Doctor(existing backup) error = nil")
	}
}

func TestListValidationErrorCategories(test *testing.T) {
	test.Parallel()
	for _, request := range []ListRequest{
		{Limit: 1, Active: true, Completed: true},
		{Limit: 1, Phase: "bad"},
		{Limit: 1, Outcome: "bad"},
	} {
		if err := validateListRequest(request); !errors.Is(err, ErrConflict) {
			test.Errorf("validateListRequest(%+v) = %v, want conflict", request, err)
		}
	}
}

func TestServicePolicyCleanPrunesExpiredMetadata(test *testing.T) {
	test.Parallel()
	service, clock := newTestService(test)
	job, _, _ := completeCapturedRun(test, service, clock)
	metadataAge, err := config.NewDurationLimit(0)
	if err != nil {
		test.Fatal(err)
	}
	service.ConfigureInvocation(config.Config{Retention: config.Retention{
		CompletedMetadataMaxAge: metadataAge,
		CompletedLogMaxAge:      config.UnlimitedDurationLimit(),
	}})
	result, err := service.Clean(test.Context(), CleanRequest{UsePolicy: true})
	if err != nil {
		test.Fatal(err)
	}
	if result.Runs != 1 || result.Jobs != 1 {
		test.Fatalf("Clean(policy) = %+v, want one run and one job", result)
	}
	if _, err := service.Inspect(test.Context(), job.ID.String()); !errors.Is(err, ErrNotFound) {
		test.Fatalf("Inspect(pruned) error = %v, want not found", err)
	}
}
