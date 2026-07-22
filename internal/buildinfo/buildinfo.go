// Package buildinfo contains release metadata injected by the build pipeline or
// embedded by the Go toolchain.
package buildinfo

import (
	"runtime/debug"
	"strings"
)

const (
	developmentVersion = "dev"
	unknownMetadata    = "unknown"
)

// These values are variables so GoReleaser and local builds can set them with
// -ldflags -X while source builds retain useful deterministic defaults.
var (
	Version = developmentVersion
	Commit  = unknownMetadata
	Date    = unknownMetadata
)

type metadata struct {
	version string
	commit  string
	date    string
}

func init() {
	info, available := debug.ReadBuildInfo()
	resolved := resolveMetadata(
		metadata{version: Version, commit: Commit, date: Date},
		info,
		available,
	)
	Version, Commit, Date = resolved.version, resolved.commit, resolved.date
}

// resolveMetadata fills linker defaults from metadata embedded by the Go
// toolchain. Linker-provided values are never overwritten. In particular, this
// lets binaries produced by `go install module@version` report the selected
// module version even though that build does not use Jobman's release ldflags.
func resolveMetadata(linked metadata, info *debug.BuildInfo, available bool) metadata {
	if !available || info == nil {
		return linked
	}

	resolved := linked
	if resolved.version == developmentVersion {
		if version := moduleVersion(info.Main.Version); version != "" {
			resolved.version = version
		}
	}

	settings := make(map[string]string, len(info.Settings))
	for _, setting := range info.Settings {
		settings[setting.Key] = strings.TrimSpace(setting.Value)
	}
	if resolved.commit == unknownMetadata && settings["vcs.revision"] != "" {
		resolved.commit = settings["vcs.revision"]
	}
	if resolved.date == unknownMetadata && settings["vcs.time"] != "" {
		resolved.date = settings["vcs.time"]
	}
	if linked.version == developmentVersion && settings["vcs.modified"] == "true" {
		resolved.version = modifiedVersion(resolved.version)
	}

	return resolved
}

func moduleVersion(version string) string {
	version = strings.TrimSpace(version)
	if version == "" || version == "(devel)" {
		return ""
	}

	return strings.TrimPrefix(version, "v")
}

func modifiedVersion(version string) string {
	if strings.Contains(version, "+dirty") || strings.HasSuffix(version, "-dirty") {
		return version
	}
	if strings.Contains(version, "+") {
		return version + ".dirty"
	}

	return version + "+dirty"
}

// Display returns a concise version string with an abbreviated commit when it
// is available.
func Display() string {
	return formatDisplay(Version, Commit)
}

func formatDisplay(version, fullCommit string) string {
	if fullCommit == "" || fullCommit == unknownMetadata {
		return version
	}
	commit := fullCommit
	if len(commit) > 12 {
		commit = commit[:12]
	}

	return strings.TrimSpace(version + " (" + commit + ")")
}
