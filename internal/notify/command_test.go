package notify

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

type commandRunnerFunc func(context.Context, CommandRequest) (CommandResult, error)

func (function commandRunnerFunc) Run(ctx context.Context, request CommandRequest) (CommandResult, error) {
	return function(ctx, request)
}

func TestCommandDeliversExactInvocationAndMinimalEnvironment(t *testing.T) {
	t.Parallel()

	var received CommandRequest
	notifier := Command{
		NameValue:  "audit",
		Executable: filepath.Join(string(filepath.Separator), "opt", "jobman", "hook"),
		Arguments:  []string{"one argument", "$(not-a-shell)"},
		Directory:  filepath.Join(string(filepath.Separator), "var", "empty"),
		Environment: map[string]string{
			"HOOK_MODE": "audit",
		},
		Runner: commandRunnerFunc(func(_ context.Context, request CommandRequest) (CommandResult, error) {
			received = request

			return CommandResult{ExitCode: 0}, nil
		}),
	}
	result, err := notifier.Deliver(t.Context(), testEvent())
	if err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}
	if result.ExitCode != 0 {
		t.Fatalf("Deliver() exit code = %d, want 0", result.ExitCode)
	}
	if received.Executable != notifier.Executable || !reflect.DeepEqual(received.Arguments, notifier.Arguments) {
		t.Fatalf("invocation = %q %#v", received.Executable, received.Arguments)
	}
	if received.Directory != notifier.Directory {
		t.Fatalf("directory = %q, want %q", received.Directory, notifier.Directory)
	}
	wantEnvironment := []string{
		"HOOK_MODE=audit",
		"JOBMAN_EVENT_ID=evt_01",
		"JOBMAN_EVENT_SCHEMA_VERSION=1",
		"JOBMAN_EVENT_TYPE=run_succeeded",
	}
	if !reflect.DeepEqual(received.Environment, wantEnvironment) {
		t.Fatalf("environment = %#v, want %#v", received.Environment, wantEnvironment)
	}
	var event Event
	if err := json.Unmarshal(received.Stdin, &event); err != nil {
		t.Fatalf("decode stdin: %v", err)
	}
	if event.ID != "evt_01" || event.JobID != "job_01" {
		t.Fatalf("stdin event = %#v", event)
	}
}

func TestCommandBoundsInjectedRunnerOutput(t *testing.T) {
	t.Parallel()

	notifier := Command{
		NameValue:   "audit",
		Executable:  filepath.Join(string(filepath.Separator), "hook"),
		OutputLimit: 4,
		Runner: commandRunnerFunc(func(_ context.Context, _ CommandRequest) (CommandResult, error) {
			return CommandResult{Stdout: []byte("codes"), Stderr: []byte("logsX")}, nil
		}),
	}
	result, err := notifier.Deliver(t.Context(), testEvent())
	if err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}
	if string(result.Stdout) != "code" || string(result.Stderr) != "logs" || !result.Truncated {
		t.Fatalf("Deliver() result = %#v", result)
	}
}

func TestCommandHidesRunnerError(t *testing.T) {
	t.Parallel()

	notifier := Command{
		NameValue:  "audit",
		Executable: filepath.Join(string(filepath.Separator), "hook"),
		Runner: commandRunnerFunc(func(_ context.Context, _ CommandRequest) (CommandResult, error) {
			return CommandResult{}, errors.New("password=top-secret")
		}),
	}
	_, err := notifier.Deliver(t.Context(), testEvent())
	if err == nil {
		t.Fatal("Deliver() error = nil")
	}
	if strings.Contains(err.Error(), "top-secret") {
		t.Fatalf("Deliver() error = %q, contains secret", err)
	}
	if !IsRetryable(err) {
		t.Fatal("Deliver() error is not retryable")
	}
}

func TestCommandRejectsUnsafeConfiguration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		command Command
		name    string
	}{
		{name: "relative executable", command: Command{NameValue: "x", Executable: "hook"}},
		{name: "unclean executable", command: Command{NameValue: "x", Executable: filepath.Join(string(filepath.Separator), "a", "..", "hook") + string(filepath.Separator) + ".."}},
		{name: "relative directory", command: Command{NameValue: "x", Executable: filepath.Join(string(filepath.Separator), "hook"), Directory: "tmp"}},
		{name: "invalid environment", command: Command{NameValue: "x", Executable: filepath.Join(string(filepath.Separator), "hook"), Environment: map[string]string{"A=B": "x"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := test.command.Deliver(t.Context(), testEvent()); err == nil {
				t.Fatal("Deliver() error = nil")
			}
		})
	}
}

func TestExecRunnerStartFailureDoesNotPanic(t *testing.T) {
	t.Parallel()

	executable := filepath.Join(string(filepath.Separator), "path", "that", "cannot", "exist")
	if runtime.GOOS == "windows" {
		executable = `Z:\path\that\cannot\exist.exe`
	}
	result, err := (ExecRunner{}).Run(t.Context(), CommandRequest{Executable: executable, OutputLimit: 8})
	if err == nil {
		t.Fatal("Run() error = nil")
	}
	if result.ExitCode != -1 {
		t.Fatalf("Run() exit code = %d, want -1", result.ExitCode)
	}
	if len(result.Stdout) != 0 {
		t.Fatalf("Run() stdout = %q, want empty", result.Stdout)
	}
}

func TestCommandNameAndExecRunnerSuccess(t *testing.T) {
	t.Parallel()

	if got := (Command{NameValue: "audit"}).Name(); got != "audit" {
		t.Fatalf("Command.Name() = %q", got)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable() error = %v", err)
	}
	result, err := (ExecRunner{}).Run(t.Context(), CommandRequest{
		Executable:  executable,
		Arguments:   []string{"-test.run=^$"},
		Directory:   t.TempDir(),
		Environment: os.Environ(),
		OutputLimit: 1024,
	})
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("ExecRunner.Run() = %#v, %v", result, err)
	}
}
