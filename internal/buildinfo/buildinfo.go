// Package buildinfo contains release metadata injected by the build pipeline.
package buildinfo

import "strings"

// These values are variables so GoReleaser and local builds can set them with
// -ldflags -X while source builds retain useful deterministic defaults.
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

// Display returns a concise version string with an abbreviated commit when it
// is available.
func Display() string {
	if Commit == "" || Commit == "unknown" {
		return Version
	}
	commit := Commit
	if len(commit) > 12 {
		commit = commit[:12]
	}

	return strings.TrimSpace(Version + " (" + commit + ")")
}
