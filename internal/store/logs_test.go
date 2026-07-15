package store

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/ryancswallace/jobman/internal/model"
)

func TestMarkRunLogsPrunedPersistsAnIdempotentTombstone(t *testing.T) {
	t.Parallel()

	database := openTestStore(t, "pruned-logs", newSequentialEventIDs(0xd100))
	jobID := mustJobID(t, 21, 1)
	runID := mustRunID(t, 21)
	supervisorID := mustSupervisorID(t, 21, 3)
	credential := bytes.Repeat([]byte{0x71}, 32)
	hash, err := model.NewCredentialHash(credential)
	if err != nil {
		t.Fatalf("NewCredentialHash() error = %v", err)
	}
	base := storeTestTime()
	if _, submitErr := database.Submit(
		t.Context(), jobID, testJobSpec(t, "pruned-logs"), hash, base, base.Add(time.Minute),
	); submitErr != nil {
		t.Fatalf("Submit() error = %v", submitErr)
	}
	if _, claimErr := database.Claim(
		t.Context(), jobID, credential, supervisorID, testProcessIdentity(401, "prune-supervisor"),
		base.Add(time.Second), base.Add(time.Minute),
	); claimErr != nil {
		t.Fatalf("Claim() error = %v", claimErr)
	}
	pendingLogs := testLogs(database, jobID, model.LogIntegrityPending, model.RecordingHealthy)
	if _, reserveErr := database.ReserveRun(t.Context(), jobID, runID, 1, pendingLogs, base.Add(2*time.Second)); reserveErr != nil {
		t.Fatalf("ReserveRun() error = %v", reserveErr)
	}
	if pruneErr := database.MarkRunLogsPruned(
		t.Context(), runID, base.Add(3*time.Second), 0, 0,
	); !errors.Is(pruneErr, ErrConflict) {
		t.Fatalf("MarkRunLogsPruned(active) error = %v, want ErrConflict", pruneErr)
	}

	finalLogs := pendingLogs
	finalLogs.Integrity = model.LogIntegrityValid
	if _, completionErr := database.MarkStartFailed(
		t.Context(), jobID, runID, finalLogs, "test_start_failure", base.Add(4*time.Second),
	); completionErr != nil {
		t.Fatalf("MarkStartFailed() error = %v", completionErr)
	}
	if pruneErr := database.MarkRunLogsPruned(
		t.Context(), runID, base.Add(3*time.Second), 3, 27,
	); !errors.Is(pruneErr, ErrConflict) {
		t.Fatalf("MarkRunLogsPruned(before completion) error = %v, want ErrConflict", pruneErr)
	}

	prunedAt := base.Add(5 * time.Second)
	if pruneErr := database.MarkRunLogsPruned(t.Context(), runID, prunedAt, 3, 27); pruneErr != nil {
		t.Fatalf("MarkRunLogsPruned() error = %v", pruneErr)
	}
	if pruneErr := database.MarkRunLogsPruned(
		t.Context(), runID, prunedAt.Add(time.Second), 99, 999,
	); pruneErr != nil {
		t.Fatalf("repeated MarkRunLogsPruned() error = %v", pruneErr)
	}

	run, err := database.GetRun(t.Context(), runID)
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}
	if run.Logs.Available() || run.Logs.PrunedAt == nil || !run.Logs.PrunedAt.Equal(prunedAt) {
		t.Fatalf("GetRun() pruning metadata = %+v, want unavailable at %s", run.Logs, prunedAt)
	}
	if run.Logs.PrunedFiles != 3 || run.Logs.PrunedBytes != 27 {
		t.Errorf("GetRun() removed counts = %d files/%d bytes, want 3/27", run.Logs.PrunedFiles, run.Logs.PrunedBytes)
	}
	if run.Logs.StdoutPath == "" || run.Logs.StderrPath == "" || run.Logs.IndexPath == "" {
		t.Error("GetRun() discarded historical log paths")
	}
}
