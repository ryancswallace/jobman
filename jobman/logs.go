package jobman

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ryancswallace/jobman/internal/app"
)

func newLogsCommand(dependencies Dependencies, root *rootOptions) *cobra.Command {
	stream := string(app.LogBoth)
	command := &cobra.Command{
		Use:   "logs JOB",
		Short: "Read output captured for a job",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, arguments []string) error {
			selected := app.LogStream(stream)
			if selected != app.LogStdout && selected != app.LogStderr && selected != app.LogBoth {
				return usageError(fmt.Errorf("invalid --stream %q: expected stdout, stderr, or both", stream))
			}

			return withBackend(command, dependencies, root, func(backend app.Backend) error {
				content, err := backend.ReadLogs(command.Context(), arguments[0], selected)
				if err != nil {
					return err
				}
				if _, err := command.OutOrStdout().Write(content); err != nil {
					return fmt.Errorf("write job logs: %w", err)
				}

				return nil
			})
		},
	}
	command.Flags().StringVar(&stream, "stream", stream, "select stdout, stderr, or both")

	return command
}
