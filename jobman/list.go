package jobman

import (
	"fmt"

	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List known jobs",
	Long:  "List running and retained jobs with their current status.",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, _ []string) {
		fmt.Fprintln(cmd.OutOrStdout(), "list called")
	},
}

func init() {
	JobmanRootCmd.AddCommand(listCmd)
}
