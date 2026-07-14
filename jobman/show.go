package jobman

import (
	"fmt"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/ryancswallace/jobman/internal/app"
)

func newShowCommand(dependencies Dependencies, root *rootOptions) *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{
		Use:   "show JOB",
		Short: "Show a job and its run history",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, arguments []string) error {
			return withBackend(command, dependencies, root, func(backend app.Backend) error {
				value, err := backend.Inspect(command.Context(), arguments[0])
				if err != nil {
					return err
				}
				if jsonOutput {
					return writeJSON(command, detail(value))
				}

				return writeJobDetails(command, value)
			})
		},
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "emit versioned JSON")

	return command
}

func writeJobDetails(command *cobra.Command, value app.JobDetails) error {
	writer := tabwriter.NewWriter(command.OutOrStdout(), 0, 4, 2, ' ', 0)
	fields := [][2]string{
		{"ID", value.Job.ID.String()},
		{"Name", value.Job.Spec.Name()},
		{"Phase", string(value.Job.Phase)},
		{"Outcome", string(value.Job.Outcome)},
		{"Submitted", value.Job.SubmittedAt.UTC().Format(timeFormat)},
		{"Executable", value.Job.Spec.Executable()},
		{"Working directory", value.Job.Spec.WorkingDirectory()},
	}
	for _, field := range fields {
		if _, err := fmt.Fprintf(writer, "%s:\t%s\n", field[0], field[1]); err != nil {
			return fmt.Errorf("write job details: %w", err)
		}
	}
	if len(value.Runs) > 0 {
		if _, err := fmt.Fprintln(writer, "\nRUN\tPHASE\tOUTCOME\tSTARTED\tCOMPLETED"); err != nil {
			return fmt.Errorf("write run header: %w", err)
		}
	}
	for _, run := range value.Runs {
		if _, err := fmt.Fprintf(
			writer,
			"%d\t%s\t%s\t%s\t%s\n",
			run.Number,
			run.Phase,
			run.Outcome,
			formatOptionalTime(run.StartedAt),
			formatOptionalTime(run.CompletedAt),
		); err != nil {
			return fmt.Errorf("write run details: %w", err)
		}
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("flush job details: %w", err)
	}

	return nil
}

func formatOptionalTime(value *time.Time) string {
	if value == nil {
		return ""
	}

	return value.UTC().Format(timeFormat)
}
