package jobman

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ryancswallace/jobman/internal/app"
)

type runOptions struct {
	name        string
	directory   string
	environment []string
}

func newRunCommand(dependencies Dependencies, root *rootOptions) *cobra.Command {
	options := &runOptions{}
	command := &cobra.Command{
		Use:   "run [OPTIONS] -- COMMAND [ARG...]",
		Short: "Submit a command as a managed job",
		Args:  usageArgs(validateRunArguments),
		RunE: func(command *cobra.Command, arguments []string) error {
			return run(command, dependencies, root, options, arguments)
		},
	}
	command.Flags().SetInterspersed(false)
	command.Flags().StringVar(&options.name, "name", "", "assign a display name")
	command.Flags().StringVar(&options.directory, "cwd", "", "set the target working directory")
	command.Flags().StringArrayVar(&options.environment, "env", nil, "set NAME=VALUE in the target environment")

	return command
}

func run(
	command *cobra.Command,
	dependencies Dependencies,
	root *rootOptions,
	options *runOptions,
	arguments []string,
) error {
	directory := options.directory
	if directory == "" {
		var err error
		directory, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("resolve working directory: %w", err)
		}
	}
	absoluteDirectory, err := filepath.Abs(directory)
	if err != nil {
		return fmt.Errorf("resolve working directory: %w", err)
	}

	environment, err := parseEnvironment(options.environment)
	if err != nil {
		return usageError(err)
	}

	return withBackend(command, dependencies, root, func(backend app.Backend) error {
		job, err := backend.Submit(command.Context(), app.SubmitRequest{
			Name:             options.name,
			Executable:       arguments[0],
			Arguments:        append([]string(nil), arguments[1:]...),
			WorkingDirectory: filepath.Clean(absoluteDirectory),
			Environment:      environment,
		})
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(command.OutOrStdout(), job.ID)
		if err != nil {
			return fmt.Errorf("write submitted job ID: %w", err)
		}

		return nil
	})
}

func validateRunArguments(command *cobra.Command, arguments []string) error {
	if err := cobra.MinimumNArgs(1)(command, arguments); err != nil {
		return err
	}
	if command.ArgsLenAtDash() < 0 {
		return errors.New("target command must follow --")
	}

	return nil
}

func parseEnvironment(values []string) (map[string]string, error) {
	result := make(map[string]string, len(values))
	for _, value := range values {
		name, content, ok := strings.Cut(value, "=")
		if !ok || name == "" || strings.ContainsRune(name, '\x00') || strings.ContainsRune(name, '=') {
			return nil, fmt.Errorf("invalid --env %q: expected NAME=VALUE", value)
		}
		result[name] = content
	}

	return result, nil
}
