// Package jobman implements the Jobman command-line interface.
package jobman

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/ryancswallace/jobman/internal/app"
	"github.com/ryancswallace/jobman/internal/buildinfo"
	"github.com/ryancswallace/jobman/internal/config"
)

// OpenBackend opens one application backend for a command invocation.
type OpenBackend func(context.Context, string) (app.Backend, error)

// Supervise runs the hidden per-job supervisor entry point.
type Supervise func(context.Context, string, string, io.Reader, io.Writer) error

// Dependencies are the runtime seams used to construct an isolated command
// tree. Zero-value dependencies are sufficient for help generation.
type Dependencies struct {
	OpenBackend OpenBackend
	Supervise   Supervise
}

type rootOptions struct {
	stateDir string
}

// NewCommand constructs an independent Jobman command tree.
func NewCommand(dependencies Dependencies) *cobra.Command {
	options := &rootOptions{}
	command := &cobra.Command{
		Use:           "jobman",
		Short:         "Run and manage background jobs without a shared daemon",
		Long:          "Jobman starts and manages durable per-user command-line jobs without a continuously running shared daemon.",
		Version:       buildinfo.Display(),
		Args:          usageArgs(cobra.NoArgs),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(command *cobra.Command, _ []string) error {
			return command.Help()
		},
	}
	command.SetVersionTemplate("{{.Name}} {{.Version}}\n")
	command.PersistentFlags().StringVar(
		&options.stateDir,
		"state-dir",
		"",
		"override the per-user state directory",
	)
	command.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return usageError(err)
	})

	command.AddCommand(
		newRunCommand(dependencies, options),
		newListCommand(dependencies, options),
		newStatusCommand(dependencies, options),
		newShowCommand(dependencies, options),
		newLogsCommand(dependencies, options),
		newCancelCommand(dependencies, options),
		newSupervisorCommand(dependencies, options),
	)

	return command
}

// Execute runs a production command tree using process-global CLI streams.
func Execute() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	command := NewCommand(defaultDependencies())
	command.SetArgs(os.Args[1:])
	command.SetIn(os.Stdin)
	command.SetOut(os.Stdout)
	command.SetErr(os.Stderr)

	return command.ExecuteContext(ctx)
}

// ExitCode maps a returned command error to Jobman's stable process status.
func ExitCode(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, ErrUsage):
		return 2
	case errors.Is(err, app.ErrNotFound):
		return 3
	case errors.Is(err, app.ErrAmbiguous):
		return 4
	case errors.Is(err, app.ErrConflict):
		return 5
	default:
		return 1
	}
}

func openBackend(
	ctx context.Context,
	dependencies Dependencies,
	options *rootOptions,
) (app.Backend, error) {
	if dependencies.OpenBackend == nil {
		return nil, errors.New("application backend is unavailable")
	}

	stateDir, err := config.StateDir(options.stateDir)
	if err != nil {
		return nil, err
	}
	backend, err := dependencies.OpenBackend(ctx, stateDir)
	if err != nil {
		return nil, fmt.Errorf("open job manager: %w", err)
	}

	return backend, nil
}

func withBackend(
	command *cobra.Command,
	dependencies Dependencies,
	options *rootOptions,
	operation func(app.Backend) error,
) (returned error) {
	backend, err := openBackend(command.Context(), dependencies, options)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := backend.Close(); closeErr != nil {
			returned = errors.Join(returned, fmt.Errorf("close job manager: %w", closeErr))
		}
	}()

	return operation(backend)
}
