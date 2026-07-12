package jobman

import (
	"fmt"

	"github.com/spf13/cobra"
)

var showCmd = &cobra.Command{
	Use:   "show",
	Short: "Show details for a job",
	Long:  "Display the command, status, timing, and retry details recorded for a job.",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, _ []string) {
		fmt.Fprintln(cmd.OutOrStdout(), "show called")
	},
}

func init() {
	JobmanRootCmd.AddCommand(showCmd)
}
