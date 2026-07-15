package logstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestCleanupRunRefusesActiveCapture(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	run, err := CreateRun(stateDir, testJobID, 1)
	if err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	t.Cleanup(func() { _ = run.Close() })

	_, err = CleanupRun(t.Context(), stateDir, testJobID, 1, alwaysEligible)
	if !errors.Is(err, ErrActiveRun) {
		t.Fatalf("CleanupRun() error = %v, want ErrActiveRun", err)
	}
	if _, statErr := os.Stat(run.Paths().Directory); statErr != nil {
		t.Fatalf("active directory after refused cleanup: %v", statErr)
	}
}

func TestCleanupRunRemovesClosedRotatedRun(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	run, err := CreateRunWithOptions(stateDir, testJobID, 1, RunOptions{
		Rotation: RotationPolicy{SegmentBytes: 2, MaxSegmentsPerStream: 3},
	})
	if err != nil {
		t.Fatalf("CreateRunWithOptions() error = %v", err)
	}
	appendBytes(t, run, Stdout, []byte("abc"), time.Unix(1, 0))
	paths := run.Paths()
	if closeErr := run.Close(); closeErr != nil {
		t.Fatalf("Close() error = %v", closeErr)
	}

	eligibilityChecks := 0
	result, err := CleanupRun(t.Context(), stateDir, testJobID, 1, func(context.Context) (bool, error) {
		eligibilityChecks++

		return true, nil
	})
	if err != nil {
		t.Fatalf("CleanupRun() error = %v", err)
	}
	if eligibilityChecks != 2 {
		t.Errorf("cleanup eligibility checks = %d, want 2", eligibilityChecks)
	}
	if result.Files != 4 || result.Bytes == 0 {
		t.Errorf("CleanupRun() result = %+v, want four nonempty files", result)
	}
	if _, statErr := os.Stat(paths.Directory); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("cleaned run directory error = %v, want not exist", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(paths.Directory+".deleting", cleanupSummaryFilename)); statErr != nil {
		t.Fatalf("durable cleanup summary: %v", statErr)
	}
	repeated, err := CleanupRun(t.Context(), stateDir, testJobID, 1, alwaysEligible)
	if err != nil || repeated != result {
		t.Fatalf("CleanupRun(resume before metadata commit) = %+v, %v, want %+v", repeated, err, result)
	}
	if err := FinalizeCleanupRun(stateDir, testJobID, 1); err != nil {
		t.Fatalf("FinalizeCleanupRun() error = %v", err)
	}
	if err := FinalizeCleanupRun(stateDir, testJobID, 1); err != nil {
		t.Fatalf("FinalizeCleanupRun(repeated) error = %v", err)
	}
	if _, err := os.Stat(paths.Directory + ".deleting"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("finalized cleanup tombstone error = %v, want not exist", err)
	}
}

func TestCleanupRunRefusesIneligibleOrUnknownContent(t *testing.T) {
	t.Parallel()

	t.Run("ineligible", func(t *testing.T) {
		t.Parallel()

		stateDir, paths := closedRunFixture(t)
		_, err := CleanupRun(t.Context(), stateDir, testJobID, 1, func(context.Context) (bool, error) {
			return false, nil
		})
		if !errors.Is(err, ErrCleanupIneligible) {
			t.Fatalf("CleanupRun() error = %v, want ErrCleanupIneligible", err)
		}
		if _, statErr := os.Stat(paths.Directory); statErr != nil {
			t.Fatalf("ineligible directory was changed: %v", statErr)
		}
	})

	t.Run("unknown file", func(t *testing.T) {
		t.Parallel()

		stateDir, paths := closedRunFixture(t)
		unknown := filepath.Join(paths.Directory, "unexpected")
		if err := os.WriteFile(unknown, []byte("keep"), fileMode); err != nil {
			t.Fatalf("write unknown file: %v", err)
		}
		_, err := CleanupRun(t.Context(), stateDir, testJobID, 1, alwaysEligible)
		if !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("CleanupRun() error = %v, want ErrUnsafePath", err)
		}
		if data, readErr := os.ReadFile(unknown); readErr != nil || string(data) != "keep" {
			t.Fatalf("unknown file after refused cleanup = %q, %v", data, readErr)
		}
	})
}

func TestCleanupRunResumesClaimedTombstone(t *testing.T) {
	t.Parallel()

	stateDir, paths := closedRunFixture(t)
	tombstone := paths.Directory + ".deleting"
	if err := os.Rename(paths.Directory, tombstone); err != nil {
		t.Fatalf("create cleanup tombstone: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tombstone, cleanupSummaryFilename), []byte("partial"), fileMode); err != nil {
		t.Fatalf("create interrupted cleanup summary: %v", err)
	}
	result, err := CleanupRun(t.Context(), stateDir, testJobID, 1, alwaysEligible)
	if err != nil {
		t.Fatalf("CleanupRun() error = %v", err)
	}
	if result.Files != 3 {
		t.Errorf("CleanupRun() files = %d, want 3", result.Files)
	}
	if err := FinalizeCleanupRun(stateDir, testJobID, 1); err != nil {
		t.Fatalf("FinalizeCleanupRun() error = %v", err)
	}
	if _, err := os.Stat(tombstone); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("finalized cleanup tombstone error = %v, want not exist", err)
	}
}

func TestFinalizeCleanupRunRefusesIncompleteClaims(t *testing.T) {
	t.Parallel()

	t.Run("log files remain", func(t *testing.T) {
		stateDir, paths := closedRunFixture(t)
		if err := os.Rename(paths.Directory, paths.Directory+".deleting"); err != nil {
			t.Fatal(err)
		}
		if err := FinalizeCleanupRun(stateDir, testJobID, 1); !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("FinalizeCleanupRun(nonempty) error = %v, want ErrUnsafePath", err)
		}
	})

	t.Run("summary is corrupt", func(t *testing.T) {
		stateDir, paths := closedRunFixture(t)
		if _, err := CleanupRun(t.Context(), stateDir, testJobID, 1, alwaysEligible); err != nil {
			t.Fatal(err)
		}
		summary := filepath.Join(paths.Directory+".deleting", cleanupSummaryFilename)
		if err := os.WriteFile(summary, []byte("corrupt"), fileMode); err != nil {
			t.Fatal(err)
		}
		if _, err := CleanupRun(t.Context(), stateDir, testJobID, 1, alwaysEligible); !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("CleanupRun(corrupt summary) error = %v, want ErrUnsafePath", err)
		}
		if err := FinalizeCleanupRun(stateDir, testJobID, 1); !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("FinalizeCleanupRun(corrupt summary) error = %v, want ErrUnsafePath", err)
		}
	})
}

func TestFinalizeCleanupRunFailurePaths(t *testing.T) {
	t.Parallel()

	if err := FinalizeCleanupRun("", testJobID, 1); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("FinalizeCleanupRun(empty state) error = %v, want ErrUnsafePath", err)
	}
	if err := FinalizeCleanupRun(filepath.Join(t.TempDir(), "missing"), testJobID, 1); err == nil {
		t.Fatal("FinalizeCleanupRun(missing state) error = nil")
	}

	t.Run("tombstone is not a directory", func(t *testing.T) {
		stateDir, paths := closedRunFixture(t)
		if err := os.RemoveAll(paths.Directory); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(paths.Directory+".deleting", []byte("unsafe"), fileMode); err != nil {
			t.Fatal(err)
		}
		if err := FinalizeCleanupRun(stateDir, testJobID, 1); !errors.Is(err, ErrUnsafePath) {
			t.Fatalf("FinalizeCleanupRun(file tombstone) error = %v, want ErrUnsafePath", err)
		}
	})

	t.Run("summary removal fails", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Windows does not enforce POSIX directory write permissions")
		}
		stateDir, paths := closedRunFixture(t)
		if _, err := CleanupRun(t.Context(), stateDir, testJobID, 1, alwaysEligible); err != nil {
			t.Fatal(err)
		}
		tombstone := paths.Directory + ".deleting"
		if err := os.Chmod(tombstone, 0o500); err != nil { // #nosec G302 -- directory traversal is required while write access is intentionally removed.
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := os.Chmod(tombstone, directoryMode); err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Errorf("restore tombstone permissions: %v", err)
			}
		})
		if err := FinalizeCleanupRun(stateDir, testJobID, 1); err == nil {
			t.Fatal("FinalizeCleanupRun(read-only tombstone) error = nil")
		}
	})
}

func TestCleanupRunRefusesSymlink(t *testing.T) {
	t.Parallel()

	stateDir, paths := closedRunFixture(t)
	if err := os.Remove(paths.Stdout); err != nil {
		t.Fatalf("remove stdout fixture: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("outside"), fileMode); err != nil {
		t.Fatalf("write outside fixture: %v", err)
	}
	if err := os.Symlink(outside, paths.Stdout); err != nil {
		t.Skipf("create symlink: %v", err)
	}

	_, err := CleanupRun(t.Context(), stateDir, testJobID, 1, alwaysEligible)
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("CleanupRun() error = %v, want ErrUnsafePath", err)
	}
	if data, readErr := os.ReadFile(outside); readErr != nil || string(data) != "outside" {
		t.Fatalf("outside file after refused cleanup = %q, %v", data, readErr)
	}
}

func TestReleaseAbandonedRunRequiresProof(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	run, err := CreateRun(stateDir, testJobID, 1)
	if err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	paths := run.Paths()
	// Closing removes the real marker. Recreate one to model an abrupt writer
	// exit without leaking an open file handle from this test process.
	if closeErr := run.Close(); closeErr != nil {
		t.Fatalf("Close() error = %v", closeErr)
	}
	marker, err := createPrivateFile(paths.Active)
	if err != nil {
		t.Fatalf("recreate stale active marker: %v", err)
	}
	if err := marker.Close(); err != nil {
		t.Fatalf("close stale active marker: %v", err)
	}

	if err := ReleaseAbandonedRun(t.Context(), stateDir, testJobID, 1, func(context.Context) (bool, error) {
		return false, nil
	}); !errors.Is(err, ErrCleanupIneligible) {
		t.Fatalf("ReleaseAbandonedRun(ineligible) error = %v", err)
	}
	if err := ReleaseAbandonedRun(t.Context(), stateDir, testJobID, 1, alwaysEligible); err != nil {
		t.Fatalf("ReleaseAbandonedRun() error = %v", err)
	}
	if _, err := os.Stat(paths.Active); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("released marker error = %v, want not exist", err)
	}
}

func closedRunFixture(t *testing.T) (string, Paths) {
	t.Helper()

	stateDir := filepath.Join(t.TempDir(), "state")
	run, err := CreateRun(stateDir, testJobID, 1)
	if err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	appendBytes(t, run, Stdout, []byte("data"), time.Unix(1, 0))
	paths := run.Paths()
	if err := run.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	return stateDir, paths
}

func alwaysEligible(context.Context) (bool, error) {
	return true, nil
}
