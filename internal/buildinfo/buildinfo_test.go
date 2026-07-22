package buildinfo

import (
	"runtime/debug"
	"testing"
)

func TestResolveMetadata(t *testing.T) {
	t.Parallel()

	defaults := metadata{
		version: developmentVersion,
		commit:  unknownMetadata,
		date:    unknownMetadata,
	}
	tests := []struct {
		name      string
		linked    metadata
		info      *debug.BuildInfo
		available bool
		want      metadata
	}{
		{
			name:   "unavailable build information",
			linked: defaults,
			want:   defaults,
		},
		{
			name:      "nil build information",
			linked:    defaults,
			available: true,
			want:      defaults,
		},
		{
			name:   "versioned module with VCS metadata",
			linked: defaults,
			info: buildInformation("v1.2.3", map[string]string{
				"vcs.revision": "0123456789abcdef",
				"vcs.time":     "2026-07-22T12:34:56Z",
				"vcs.modified": "false",
			}),
			available: true,
			want: metadata{
				version: "1.2.3",
				commit:  "0123456789abcdef",
				date:    "2026-07-22T12:34:56Z",
			},
		},
		{
			name:   "pseudo-version installed from a revision",
			linked: defaults,
			info: buildInformation(
				"v1.2.4-0.20260722123456-0123456789ab",
				nil,
			),
			available: true,
			want: metadata{
				version: "1.2.4-0.20260722123456-0123456789ab",
				commit:  unknownMetadata,
				date:    unknownMetadata,
			},
		},
		{
			name:   "local development build",
			linked: defaults,
			info: buildInformation("(devel)", map[string]string{
				"vcs.revision": "fedcba9876543210",
				"vcs.time":     "2026-07-22T12:34:56Z",
				"vcs.modified": "false",
			}),
			available: true,
			want: metadata{
				version: developmentVersion,
				commit:  "fedcba9876543210",
				date:    "2026-07-22T12:34:56Z",
			},
		},
		{
			name:   "modified local development build",
			linked: defaults,
			info: buildInformation("(devel)", map[string]string{
				"vcs.modified": "true",
			}),
			available: true,
			want: metadata{
				version: "dev+dirty",
				commit:  unknownMetadata,
				date:    unknownMetadata,
			},
		},
		{
			name:   "modified module version with build metadata",
			linked: defaults,
			info: buildInformation("v1.2.3+portable", map[string]string{
				"vcs.modified": "true",
			}),
			available: true,
			want: metadata{
				version: "1.2.3+portable.dirty",
				commit:  unknownMetadata,
				date:    unknownMetadata,
			},
		},
		{
			name:   "existing dirty suffix is not duplicated",
			linked: defaults,
			info: buildInformation("v1.2.3+dirty", map[string]string{
				"vcs.modified": "true",
			}),
			available: true,
			want: metadata{
				version: "1.2.3+dirty",
				commit:  unknownMetadata,
				date:    unknownMetadata,
			},
		},
		{
			name: "linker metadata is authoritative",
			linked: metadata{
				version: "1.2.3-release",
				commit:  "release-commit",
				date:    "release-date",
			},
			info: buildInformation("v9.9.9", map[string]string{
				"vcs.revision": "embedded-commit",
				"vcs.time":     "embedded-date",
				"vcs.modified": "true",
			}),
			available: true,
			want: metadata{
				version: "1.2.3-release",
				commit:  "release-commit",
				date:    "release-date",
			},
		},
		{
			name: "missing linker fields use embedded metadata",
			linked: metadata{
				version: "1.2.3-release",
				commit:  unknownMetadata,
				date:    unknownMetadata,
			},
			info: buildInformation("v9.9.9", map[string]string{
				"vcs.revision": "embedded-commit",
				"vcs.time":     "embedded-date",
			}),
			available: true,
			want: metadata{
				version: "1.2.3-release",
				commit:  "embedded-commit",
				date:    "embedded-date",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := resolveMetadata(test.linked, test.info, test.available); got != test.want {
				t.Fatalf("resolveMetadata() = %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestDisplay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		version string
		commit  string
		want    string
	}{
		{
			name:    "abbreviates commit",
			version: "1.2.3",
			commit:  "0123456789abcdef",
			want:    "1.2.3 (0123456789ab)",
		},
		{
			name:    "unknown commit",
			version: "1.2.3",
			commit:  unknownMetadata,
			want:    "1.2.3",
		},
		{
			name:    "empty commit",
			version: "dev",
			want:    "dev",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if got := formatDisplay(test.version, test.commit); got != test.want {
				t.Fatalf("formatDisplay(%q, %q) = %q, want %q", test.version, test.commit, got, test.want)
			}
		})
	}
}

func TestDisplayUsesResolvedMetadata(t *testing.T) {
	t.Parallel()

	want := formatDisplay(Version, Commit)
	if got := Display(); got != want {
		t.Fatalf("Display() = %q, want %q", got, want)
	}
}

func buildInformation(version string, settings map[string]string) *debug.BuildInfo {
	info := &debug.BuildInfo{Main: debug.Module{Version: version}}
	for key, value := range settings {
		info.Settings = append(info.Settings, debug.BuildSetting{Key: key, Value: value})
	}

	return info
}
