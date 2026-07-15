package notify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const commandWaitDelay = 2 * time.Second

// CommandRequest is the exact, direct invocation passed to a CommandRunner.
type CommandRequest struct {
	Stdin       []byte
	Environment []string
	Arguments   []string
	Executable  string
	Directory   string
	OutputLimit int64
}

// CommandResult is the bounded result of a command-hook invocation.
type CommandResult struct {
	Stdout    []byte
	Stderr    []byte
	ExitCode  int
	Truncated bool
}

// CommandRunner executes a hook without shell interpretation.
type CommandRunner interface {
	Run(context.Context, CommandRequest) (CommandResult, error)
}

// ExecRunner directly executes command hooks using os/exec.
type ExecRunner struct{}

// Run executes the exact executable and argument vector in request.
func (ExecRunner) Run(ctx context.Context, request CommandRequest) (CommandResult, error) {
	stdout := newBoundedBuffer(request.OutputLimit)
	stderr := newBoundedBuffer(request.OutputLimit)
	command := exec.CommandContext(ctx, request.Executable, request.Arguments...) //nolint:gosec // Direct execution with preserved arguments is the notifier contract.
	command.Dir = request.Directory
	command.Env = request.Environment
	command.Stdin = bytes.NewReader(request.Stdin)
	command.Stdout = stdout
	command.Stderr = stderr
	command.WaitDelay = commandWaitDelay

	err := command.Run()
	exitCode := -1
	if command.ProcessState != nil {
		exitCode = command.ProcessState.ExitCode()
	}
	result := CommandResult{
		Stdout:    stdout.Bytes(),
		Stderr:    stderr.Bytes(),
		ExitCode:  exitCode,
		Truncated: stdout.truncated || stderr.truncated,
	}
	if err != nil {
		return result, deliveryError(ErrorRejected, true)
	}

	return result, nil
}

// Command delivers an event to a direct executable. Environment is the whole
// hook environment: the parent process environment is never inherited.
type Command struct {
	Runner      CommandRunner
	Environment map[string]string
	NameValue   string
	Executable  string
	Arguments   []string
	Directory   string
	Timeout     time.Duration
	OutputLimit int64
}

// Name returns the configured notifier identity.
func (command Command) Name() string {
	return command.NameValue
}

// Deliver writes one versioned JSON event to the hook's standard input.
func (command Command) Deliver(parent context.Context, event Event) (Result, error) {
	payload, err := marshalEvent(event)
	if err != nil {
		return Result{}, err
	}
	limit, err := command.validate()
	if err != nil {
		return Result{}, err
	}
	environment, err := command.environment(event)
	if err != nil {
		return Result{}, err
	}
	runner := command.Runner
	if runner == nil {
		runner = ExecRunner{}
	}
	ctx, cancel, err := withTimeout(parent, command.Timeout)
	if err != nil {
		return Result{}, err
	}
	defer cancel()

	commandResult, runErr := runner.Run(ctx, CommandRequest{
		Executable:  command.Executable,
		Arguments:   append([]string(nil), command.Arguments...),
		Directory:   command.Directory,
		Environment: environment,
		Stdin:       payload,
		OutputLimit: limit,
	})
	result := Result{
		Stdout:    boundedCopy(commandResult.Stdout, limit),
		Stderr:    boundedCopy(commandResult.Stderr, limit),
		ExitCode:  commandResult.ExitCode,
		Truncated: commandResult.Truncated || int64(len(commandResult.Stdout)) > limit || int64(len(commandResult.Stderr)) > limit,
	}
	if runErr != nil {
		var classified *DeliveryError
		if errors.As(runErr, &classified) {
			return result, classifyContext(ctx, ErrorRejected, IsRetryable(runErr))
		}

		return result, classifyContext(ctx, ErrorTransport, true)
	}

	return result, nil
}

func (command Command) validate() (int64, error) {
	if strings.TrimSpace(command.NameValue) == "" {
		return 0, errors.New("command notifier name is required")
	}
	if !filepath.IsAbs(command.Executable) || filepath.Clean(command.Executable) != command.Executable {
		return 0, errors.New("command notifier executable must be a clean absolute path")
	}
	if command.Directory != "" && (!filepath.IsAbs(command.Directory) || filepath.Clean(command.Directory) != command.Directory) {
		return 0, errors.New("command notifier directory must be a clean absolute path")
	}

	return byteLimit(command.OutputLimit)
}

func (command Command) environment(event Event) ([]string, error) {
	environment := make(map[string]string, len(command.Environment)+3)
	for name, value := range command.Environment {
		if name == "" || strings.ContainsAny(name, "=\x00") || strings.ContainsRune(value, '\x00') {
			return nil, fmt.Errorf("command notifier has invalid environment variable %q", name)
		}
		environment[name] = value
	}
	environment["JOBMAN_EVENT_ID"] = event.ID
	environment["JOBMAN_EVENT_TYPE"] = string(event.Type)
	environment["JOBMAN_EVENT_SCHEMA_VERSION"] = strconv.Itoa(EventSchemaVersion)

	names := make([]string, 0, len(environment))
	for name := range environment {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]string, 0, len(names))
	for _, name := range names {
		result = append(result, name+"="+environment[name])
	}

	return result, nil
}

func boundedCopy(data []byte, limit int64) []byte {
	return bytes.Clone(data[:min(int64(len(data)), limit)])
}
