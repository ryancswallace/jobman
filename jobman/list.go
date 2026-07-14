package jobman

import (
	"encoding/json"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/ryancswallace/jobman/internal/app"
	"github.com/ryancswallace/jobman/internal/model"
)

func newListCommand(dependencies Dependencies, root *rootOptions) *cobra.Command {
	var jsonOutput bool
	command := &cobra.Command{
		Use:   "list",
		Short: "List known jobs",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			return withBackend(command, dependencies, root, func(backend app.Backend) error {
				jobs, err := backend.List(command.Context())
				if err != nil {
					return err
				}

				return writeJobList(command, jobs, jsonOutput)
			})
		},
	}
	command.Flags().BoolVar(&jsonOutput, "json", false, "emit versioned JSON")

	return command
}

func writeJobList(command *cobra.Command, jobs []model.JobState, jsonOutput bool) error {
	if jsonOutput {
		return writeJSON(command, struct {
			Jobs []jobSummary `json:"jobs"`
		}{Jobs: summaries(jobs)})
	}

	writer := tabwriter.NewWriter(command.OutOrStdout(), 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(writer, "ID\tNAME\tPHASE\tOUTCOME\tSUBMITTED"); err != nil {
		return fmt.Errorf("write job list header: %w", err)
	}
	for _, job := range jobs {
		if _, err := fmt.Fprintf(
			writer,
			"%s\t%s\t%s\t%s\t%s\n",
			job.ID,
			job.Spec.Name(),
			job.Phase,
			job.Outcome,
			job.SubmittedAt.UTC().Format(timeFormat),
		); err != nil {
			return fmt.Errorf("write job list row: %w", err)
		}
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("flush job list: %w", err)
	}

	return nil
}

func writeJSON(command *cobra.Command, data any) error {
	encoder := json.NewEncoder(command.OutOrStdout())
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(struct {
		SchemaVersion int `json:"schema_version"`
		Data          any `json:"data"`
	}{SchemaVersion: 1, Data: data}); err != nil {
		return fmt.Errorf("encode JSON output: %w", err)
	}

	return nil
}
