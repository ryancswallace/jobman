package jobman

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/ryancswallace/jobman/internal/config"
)

func newSupervisorCommand(dependencies Dependencies, root *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:    "__supervise JOB",
		Hidden: true,
		Args:   usageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, arguments []string) error {
			if dependencies.Supervise == nil {
				return errors.New("supervisor runtime is unavailable")
			}
			stateDir, err := config.StateDir(root.stateDir)
			if err != nil {
				return err
			}

			return dependencies.Supervise(
				command.Context(),
				stateDir,
				arguments[0],
				command.InOrStdin(),
				command.OutOrStdout(),
			)
		},
	}

	return command
}
