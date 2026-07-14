package buildinfo

import "testing"

func TestDisplay(t *testing.T) {
	originalVersion, originalCommit := Version, Commit
	t.Cleanup(func() {
		Version, Commit = originalVersion, originalCommit
	})

	Version, Commit = "1.2.3", "0123456789abcdef"
	if got := Display(); got != "1.2.3 (0123456789ab)" {
		t.Fatalf("Display() = %q", got)
	}
	Commit = "unknown"
	if got := Display(); got != "1.2.3" {
		t.Fatalf("Display() without commit = %q", got)
	}
}
