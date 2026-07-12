package jobman

import (
	"fmt"

	"github.com/spf13/cobra"
)

var logsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Read output captured for a job",
	Long:  "Print the standard output and standard error retained for a managed job.",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, _ []string) {
		fmt.Fprintln(cmd.OutOrStdout(), "logs called")
	},
}

func init() {
	JobmanRootCmd.AddCommand(logsCmd)
}
