package jobman

import (
	"fmt"

	"github.com/spf13/cobra"
)

var cleanCmd = &cobra.Command{
	Use:   "clean",
	Short: "Remove completed job metadata and artifacts",
	Long:  "Remove retained state and artifacts for jobs that are no longer running.",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, _ []string) {
		fmt.Fprintln(cmd.OutOrStdout(), "clean called")
	},
}

func init() {
	JobmanRootCmd.AddCommand(cleanCmd)
}
