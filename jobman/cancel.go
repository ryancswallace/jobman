package jobman

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ryancswallace/jobman/internal/app"
)

func newCancelCommand(dependencies Dependencies, root *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "cancel JOB",
		Short: "Cancel a managed job",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, arguments []string) error {
			return withBackend(command, dependencies, root, func(backend app.Backend) error {
				job, err := backend.Cancel(command.Context(), arguments[0])
				if err != nil {
					return err
				}
				if _, err := fmt.Fprintf(command.OutOrStdout(), "%s\t%s\n", job.ID, job.Phase); err != nil {
					return fmt.Errorf("write cancellation result: %w", err)
				}

				return nil
			})
		},
	}
}
