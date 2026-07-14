package jobman

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

// ErrUsage classifies invalid command syntax and option values.
var ErrUsage = errors.New("invalid command usage")

func usageError(err error) error {
	if err == nil || errors.Is(err, ErrUsage) {
		return err
	}

	return fmt.Errorf("%w: %w", ErrUsage, err)
}

func usageArgs(validate cobra.PositionalArgs) cobra.PositionalArgs {
	return func(command *cobra.Command, arguments []string) error {
		return usageError(validate(command, arguments))
	}
}
