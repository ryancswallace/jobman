package jobman

import (
	"fmt"

	"github.com/spf13/cobra"
)

var killCmd = &cobra.Command{
	Use:   "kill",
	Short: "Stop a running job",
	Long:  "Request termination of a job and its managed child process.",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, _ []string) {
		fmt.Fprintln(cmd.OutOrStdout(), "kill called")
	},
}

func init() {
	JobmanRootCmd.AddCommand(killCmd)
}
