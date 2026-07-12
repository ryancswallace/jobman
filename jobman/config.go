package jobman

import (
	"fmt"

	"github.com/spf13/cobra"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Inspect and manage jobman configuration",
	Long:  "Show or update the effective configuration used by jobman commands.",
	Args:  cobra.NoArgs,
	Run: func(cmd *cobra.Command, _ []string) {
		fmt.Fprintln(cmd.OutOrStdout(), "config called")
	},
}

func init() {
	JobmanRootCmd.AddCommand(configCmd)
}
