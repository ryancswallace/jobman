package jobman

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ryancswallace/jobman/internal/app"
)

func newDoctorCommand(dependencies dependencies, root *rootOptions) *cobra.Command {
	var repair, jsonOutput bool
	var backupPath string
	command := &cobra.Command{
		Use:   "doctor",
		Short: "Verify state and perform explicit conservative recovery",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			return withBackend(command, dependencies, root, func(backend app.Backend) error {
				doctor, ok := backend.(app.DoctorBackend)
				if !ok {
					return errors.New("application backend does not support health checks")
				}
				report, err := doctor.Doctor(command.Context(), app.DoctorRequest{
					Repair: repair, BackupPath: backupPath,
				})
				if err != nil {
					return err
				}
				if jsonOutput {
					return writeJSON(command, report)
				}
				_, err = fmt.Fprintf(
					command.OutOrStdout(),
					"healthy\tschema=%d\tsqlite=%s\tforeign_keys=%d\n",
					report.Store.SchemaVersion,
					report.Store.SQLiteVersion,
					report.Store.ForeignKeyViolations,
				)

				return err
			})
		},
	}
	command.Flags().BoolVar(&repair, "repair", false, "checkpoint WAL and reconcile stale state and notifications")
	command.Flags().StringVar(&backupPath, "backup", "", "write a consistent database backup to a new path")
	command.Flags().BoolVar(&jsonOutput, "json", false, "emit versioned JSON")

	return command
}
