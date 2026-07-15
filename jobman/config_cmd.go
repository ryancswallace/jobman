package jobman

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ryancswallace/jobman/internal/config"
)

func newConfigCommand(root *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "config",
		Short: "Inspect and validate effective configuration",
		Args:  usageArgs(cobra.NoArgs),
	}
	command.AddCommand(newConfigShowCommand(root), newConfigPathsCommand(root), newConfigValidateCommand(root))

	return command
}

func newConfigShowCommand(root *rootOptions) *cobra.Command {
	var origins bool
	command := &cobra.Command{
		Use:   "show",
		Short: "Show the merged effective configuration",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			loaded, err := loadConfiguration(root)
			if err != nil {
				return err
			}
			if origins {
				return writeJSON(command, loaded)
			}

			return writeJSON(command, loaded.Config)
		},
	}
	command.Flags().BoolVar(&origins, "origins", false, "include source and field provenance")

	return command
}

func newConfigPathsCommand(root *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "paths",
		Short: "Show configuration search paths and selected sources",
		Args:  usageArgs(cobra.NoArgs),
		RunE: func(command *cobra.Command, _ []string) error {
			systemPath, userPath, err := config.DefaultConfigPaths()
			if err != nil {
				return err
			}
			if _, writeErr := fmt.Fprintf(command.OutOrStdout(), "system\t%s\nuser\t%s\n", systemPath, userPath); writeErr != nil {
				return fmt.Errorf("write configuration paths: %w", writeErr)
			}
			if root.configPath != "" {
				if _, writeErr := fmt.Fprintf(command.OutOrStdout(), "explicit\t%s\n", root.configPath); writeErr != nil {
					return fmt.Errorf("write explicit configuration path: %w", writeErr)
				}
			}
			workingDirectory, err := os.Getwd()
			if err != nil {
				return err
			}
			loaded, err := loadConfiguration(root)
			if err != nil {
				return err
			}
			projectPath, found, err := config.FindTrustedProjectConfig(
				workingDirectory,
				loaded.Config.TrustedProjectRoots,
			)
			if err != nil {
				return err
			}
			if found {
				if _, err := fmt.Fprintf(command.OutOrStdout(), "project\t%s\n", projectPath); err != nil {
					return fmt.Errorf("write project configuration path: %w", err)
				}
			}

			return nil
		},
	}
}

func newConfigValidateCommand(root *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "validate [PATH]",
		Short: "Strictly validate effective configuration",
		Args:  usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(command *cobra.Command, arguments []string) error {
			selected := *root
			if len(arguments) == 1 {
				if root.configPath != "" {
					return usageError(errors.New("PATH and --config are mutually exclusive"))
				}
				selected.configPath = arguments[0]
			}
			loaded, err := loadConfiguration(&selected)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(command.OutOrStdout(), "valid\t%d sources\n", len(loaded.Sources))
			if err != nil {
				return fmt.Errorf("write validation result: %w", err)
			}

			return nil
		},
	}
}
