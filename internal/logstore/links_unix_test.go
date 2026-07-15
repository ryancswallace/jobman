//go:build linux || darwin

package logstore

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCleanupRunRefusesHardLinkedLog(t *testing.T) {
	t.Parallel()

	stateDir, paths := closedRunFixture(t)
	outsideLink := filepath.Join(t.TempDir(), "stdout-link")
	if err := os.Link(paths.Stdout, outsideLink); err != nil {
		t.Skipf("create hard link: %v", err)
	}

	_, err := CleanupRun(t.Context(), stateDir, testJobID, 1, alwaysEligible)
	if !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("CleanupRun() error = %v, want ErrUnsafePath", err)
	}
	if _, err := os.Stat(outsideLink); err != nil {
		t.Fatalf("outside hard link after refused cleanup: %v", err)
	}
}
