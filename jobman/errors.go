package jobman

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

var errUsage = errors.New("invalid command usage")

func usageError(err error) error {
	if err == nil || errors.Is(err, errUsage) {
		return err
	}

	return fmt.Errorf("%w: %w", errUsage, err)
}

func usageArgs(validate cobra.PositionalArgs) cobra.PositionalArgs {
	return func(command *cobra.Command, arguments []string) error {
		return usageError(validate(command, arguments))
	}
}
