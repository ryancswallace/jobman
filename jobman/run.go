package jobman

import (
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

var runCmd = &cobra.Command{
	Use:                "run COMMAND [ARG...]",
	Short:              "Run a command as a managed job",
	Long:               "Run a shell command while jobman tracks its execution and output.",
	DisableFlagParsing: true,
	RunE:               Run,
}

// Run is the entrypoint of the run command.
func Run(cmd *cobra.Command, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}

	cmdStr := strings.Join(args, " ")
	command := exec.CommandContext(cmd.Context(), "bash", "-c", cmdStr)
	command.Stdin = cmd.InOrStdin()
	command.Stdout = cmd.OutOrStdout()
	command.Stderr = cmd.ErrOrStderr()

	return command.Run()
}

func init() {
	JobmanRootCmd.AddCommand(runCmd)
}
