package jobman

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/ryancswallace/jobman/internal/app"
	"github.com/ryancswallace/jobman/internal/config"
	"github.com/ryancswallace/jobman/internal/model"
)

func TestOptionalCommandsRejectMinimalBackends(t *testing.T) {
	t.Parallel()

	backend := &basicBackend{base: newFakeBackend(t)}
	for _, arguments := range [][]string{
		{"pause", testJobID},
		{"resume", testJobID},
		{"wait", testJobID},
		{"rerun", testJobID},
		{"input", testJobID},
		{"clean", "--dry-run"},
		{"logs", testJobID, "--follow"},
		{"run", "--rerun", testJobID},
		{"run", "--foreground", "--", "true"},
	} {
		if _, err := executeCommand(t, dependenciesFor(backend), arguments); err == nil {
			t.Errorf("%v with minimal backend error = nil", arguments)
		}
		backend.base.closed = false
	}
}

func TestLogsPropagatePerRunBackendFailures(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("read selected run failed")
	backend := &runReadFailureBackend{fakeBackend: newFakeBackend(t), err: wantErr}
	backend.details.Runs = []model.RunState{{
		ID: "01980f4c-7b2a-7a6f-8c10-0123456789ac", Number: 1,
	}}
	for _, arguments := range [][]string{
		{"logs", testJobID, "--all"},
		{"logs", testJobID, "--run", "1"},
	} {
		if _, err := executeCommand(t, dependenciesFor(backend), arguments); !errors.Is(err, wantErr) {
			t.Errorf("%v error = %v, want %v", arguments, err, wantErr)
		}
		backend.closed = false
	}
}

func TestConfigCommandsUseSelectedAndTrustedProjectFiles(t *testing.T) {
	root := t.TempDir()
	projectConfig := filepath.Join(root, ".jobman.yml")
	if err := os.WriteFile(projectConfig, []byte("schema_version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	selected := filepath.Join(root, "selected.yml")
	contents := "schema_version: 1\ntrusted_project_roots:\n  - " + root + "\n"
	if err := os.WriteFile(selected, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	if output, err := executeCommand(t, dependencies{}, []string{"config", "validate", selected}); err != nil ||
		!strings.Contains(output, "valid") {
		t.Fatalf("config validate selected file = (%q, %v)", output, err)
	}

	t.Chdir(root)
	output, err := executeCommand(t, dependencies{}, []string{"--config", selected, "config", "paths"})
	if err != nil {
		t.Fatalf("config paths trusted project = (%q, %v)", output, err)
	}

	var reportedProjectConfig string
	for line := range strings.SplitSeq(output, "\n") {
		kind, path, found := strings.Cut(line, "\t")
		if found && kind == "project" {
			reportedProjectConfig = path

			break
		}
	}
	if reportedProjectConfig == "" {
		t.Fatalf("config paths output missing project entry: %q", output)
	}
	expectedInfo, statErr := os.Stat(projectConfig)
	if statErr != nil {
		t.Fatalf("stat expected project configuration: %v", statErr)
	}
	reportedInfo, statErr := os.Stat(reportedProjectConfig)
	if statErr != nil {
		t.Fatalf("stat reported project configuration %q: %v", reportedProjectConfig, statErr)
	}
	if !os.SameFile(expectedInfo, reportedInfo) {
		t.Fatalf("reported project configuration %q does not identify %q", reportedProjectConfig, projectConfig)
	}
}

func TestRunArgumentValidatorReportsFlagTypeErrors(t *testing.T) {
	t.Parallel()

	wrongJobSpec := &cobra.Command{}
	wrongJobSpec.Flags().Int("job-spec", 0, "test wrong flag type")
	if err := validateRunArguments(wrongJobSpec, nil); err == nil {
		t.Error("validateRunArguments(job-spec type mismatch) error = nil")
	}

	wrongRerun := &cobra.Command{}
	wrongRerun.Flags().String("job-spec", "", "test flag")
	wrongRerun.Flags().Int("rerun", 0, "test wrong flag type")
	if err := validateRunArguments(wrongRerun, nil); err == nil {
		t.Error("validateRunArguments(rerun type mismatch) error = nil")
	}

	wrongProfile := &cobra.Command{}
	wrongProfile.Flags().String("job-spec", "", "test flag")
	wrongProfile.Flags().String("rerun", "", "test flag")
	wrongProfile.Flags().Int("profile", 0, "test wrong flag type")
	if err := validateRunArguments(wrongProfile, nil); err == nil {
		t.Error("validateRunArguments(profile type mismatch) error = nil")
	}
}

func TestTabularWritersReportFailuresAtEveryOutputBoundary(t *testing.T) {
	t.Parallel()

	backend := newFakeBackend(t)
	run := model.RunState{
		ID:       "01980f4c-7b2a-7a6f-8c10-0123456789ac",
		JobID:    backend.details.Job.ID,
		Number:   1,
		Phase:    model.RunPhaseCompleted,
		Outcome:  model.RunOutcomeSuccess,
		Revision: 3,
	}
	details := backend.details
	details.Runs = []model.RunState{run}
	listed := []app.ListedJob{{Job: details.Job, Runs: details.Runs}}

	writers := map[string]func(*cobra.Command) error{
		"run details": func(command *cobra.Command) error {
			return writeRunDetails(command, run)
		},
		"job details": func(command *cobra.Command) error {
			return writeJobDetails(command, details)
		},
		"job list": func(command *cobra.Command) error {
			return writeJobList(command, listed, false, true)
		},
		"JSON job list": func(command *cobra.Command) error {
			return writeJobList(command, listed, true, true)
		},
	}
	for name, write := range writers {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			sawFailure := false
			sawSuccess := false
			for allowed := range 128 {
				command := &cobra.Command{}
				command.SetContext(t.Context())
				command.SetOut(&failAfterWrites{allowed: allowed})
				if err := write(command); err != nil {
					sawFailure = true
					continue
				}
				sawSuccess = true
				break
			}
			if !sawFailure || !sawSuccess {
				t.Fatalf("writer boundaries exercised failure=%t success=%t", sawFailure, sawSuccess)
			}
		})
	}
}

func TestBackendLifecycleFailureBoundaries(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("backend lifecycle failed")
	if _, err := openBackend(t.Context(), dependencies{OpenBackend: func(context.Context, string) (app.Backend, error) {
		return nil, wantErr
	}}, &rootOptions{stateDir: t.TempDir()}); !errors.Is(err, wantErr) {
		t.Fatalf("openBackend() error = %v, want %v", err, wantErr)
	}

	if err := applyBackendConfiguration(
		t.Context(), &unconfigurableBackend{Backend: newFakeBackend(t)}, config.Default(),
	); err == nil {
		t.Fatal("applyBackendConfiguration(minimal backend) error = nil")
	}

	configurationPath := filepath.Join(t.TempDir(), "jobman.yml")
	if err := os.WriteFile(configurationPath, []byte("schema_version: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, operation := range map[string]func(*cobra.Command, dependencies, *rootOptions) error{
		"plain backend": func(command *cobra.Command, deps dependencies, options *rootOptions) error {
			return withBackend(command, deps, options, func(app.Backend) error { return nil })
		},
		"configured backend": func(command *cobra.Command, deps dependencies, options *rootOptions) error {
			return withLoadedBackend(command, deps, options, func(app.Backend, config.Loaded) error { return nil })
		},
	} {
		t.Run(name+" close", func(t *testing.T) {
			t.Parallel()

			backend := &closeFailureBackend{fakeBackend: newFakeBackend(t), err: wantErr}
			command := &cobra.Command{}
			command.SetContext(t.Context())
			options := &rootOptions{stateDir: t.TempDir(), configPath: configurationPath}
			if err := operation(command, dependenciesFor(backend), options); !errors.Is(err, wantErr) {
				t.Fatalf("backend close error = %v, want %v", err, wantErr)
			}
		})
	}
}

type runReadFailureBackend struct {
	*fakeBackend
	err error
}

type closeFailureBackend struct {
	*fakeBackend
	err error
}

type unconfigurableBackend struct{ app.Backend }

func (backend *closeFailureBackend) Close() error {
	backend.closed = true

	return backend.err
}

func (backend *runReadFailureBackend) ReadRunLogs(
	context.Context,
	string,
	app.LogStream,
	uint64,
) ([]byte, error) {
	return nil, backend.err
}

func (backend *runReadFailureBackend) FollowLogs(
	context.Context,
	string,
	app.LogStream,
	uint64,
	io.Writer,
) error {
	return backend.err
}
