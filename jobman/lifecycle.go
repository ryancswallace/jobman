package jobman

import (
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/ryancswallace/jobman/internal/app"
)

func newPauseCommand(dependencies Dependencies, root *rootOptions) *cobra.Command {
	return lifecycleCommand("pause", "Pause a managed job", dependencies, root, func(
		command *cobra.Command,
		backend app.LifecycleBackend,
		selector string,
	) error {
		job, err := backend.Pause(command.Context(), selector)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(command.OutOrStdout(), "%s\t%s\n", job.ID, job.Phase)
		return err
	})
}

func newResumeCommand(dependencies Dependencies, root *rootOptions) *cobra.Command {
	return lifecycleCommand("resume", "Resume a paused job", dependencies, root, func(
		command *cobra.Command,
		backend app.LifecycleBackend,
		selector string,
	) error {
		job, err := backend.Resume(command.Context(), selector)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(command.OutOrStdout(), "%s\t%s\n", job.ID, job.Phase)
		return err
	})
}

func newWaitCommand(dependencies Dependencies, root *rootOptions) *cobra.Command {
	return lifecycleCommand("wait", "Wait for a job to finish", dependencies, root, func(
		command *cobra.Command,
		backend app.LifecycleBackend,
		selector string,
	) error {
		job, err := backend.Wait(command.Context(), selector)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(command.OutOrStdout(), "%s\t%s\n", job.ID, job.Outcome)
		return err
	})
}

func lifecycleCommand(
	use,
	short string,
	dependencies Dependencies,
	root *rootOptions,
	operation func(*cobra.Command, app.LifecycleBackend, string) error,
) *cobra.Command {
	return &cobra.Command{
		Use:   use + " JOB",
		Short: short,
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, arguments []string) error {
			return withBackend(command, dependencies, root, func(backend app.Backend) error {
				extended, ok := backend.(app.LifecycleBackend)
				if !ok {
					return errors.New("application backend does not support lifecycle controls")
				}

				return operation(command, extended, arguments[0])
			})
		},
	}
}

func newInputCommand(dependencies Dependencies, root *rootOptions) *cobra.Command {
	var sendEOF bool
	command := &cobra.Command{
		Use:   "input JOB",
		Short: "Send bytes to a live-input job",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, arguments []string) error {
			source := command.InOrStdin()
			return withBackend(command, dependencies, root, func(backend app.Backend) error {
				inputBackend, ok := backend.(app.InputBackend)
				if !ok {
					return errors.New("application backend does not support live input")
				}
				result, err := inputBackend.SendInput(command.Context(), arguments[0], source, sendEOF)
				if err != nil {
					return err
				}
				_, err = fmt.Fprintf(command.OutOrStdout(), "%d\n", result.Delivered)
				return err
			})
		},
	}
	command.Flags().BoolVar(&sendEOF, "eof", false, "close the target's standard input")

	return command
}

func newRerunCommand(dependencies Dependencies, root *rootOptions) *cobra.Command {
	var name string
	command := &cobra.Command{
		Use:   "rerun JOB",
		Short: "Submit a new job from an existing specification",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, arguments []string) error {
			return withBackend(command, dependencies, root, func(backend app.Backend) error {
				extended, ok := backend.(app.LifecycleBackend)
				if !ok {
					return errors.New("application backend does not support rerun")
				}
				job, err := extended.Rerun(command.Context(), arguments[0], app.RerunRequest{Name: name})
				if err != nil {
					return err
				}
				_, err = fmt.Fprintln(command.OutOrStdout(), job.ID)
				return err
			})
		},
	}
	command.Flags().StringVar(&name, "name", "", "override the new job's display name")

	return command
}

func newCleanCommand(dependencies Dependencies, root *rootOptions) *cobra.Command {
	var olderThan time.Duration
	var dryRun bool
	var force bool
	command := &cobra.Command{
		Use:   "clean [JOB]",
		Short: "Safely remove completed job logs",
		Args:  usageArgs(cobra.MaximumNArgs(1)),
		RunE: func(command *cobra.Command, arguments []string) error {
			if !dryRun && !force {
				return usageError(errors.New("destructive cleanup requires --force or use --dry-run"))
			}
			selector := ""
			if len(arguments) == 1 {
				selector = arguments[0]
			}
			return withBackend(command, dependencies, root, func(backend app.Backend) error {
				cleaner, ok := backend.(app.CleanupBackend)
				if !ok {
					return errors.New("application backend does not support cleanup")
				}
				result, err := cleaner.Clean(command.Context(), app.CleanRequest{
					Selector: selector, OlderThan: olderThan, DryRun: dryRun,
					UsePolicy: !command.Flags().Changed("older-than"),
				})
				if err != nil {
					return err
				}
				mode := "removed"
				if dryRun {
					mode = "would remove"
				}
				_, err = fmt.Fprintf(
					command.OutOrStdout(),
					"%s %d runs, %d files, %d bytes\n",
					mode,
					result.Runs,
					result.Files,
					result.Bytes,
				)
				return err
			})
		},
	}
	command.Flags().DurationVar(&olderThan, "older-than", 0, "select logs completed at least this long ago")
	command.Flags().BoolVar(&dryRun, "dry-run", true, "report eligible logs without removing them")
	command.Flags().BoolVar(&force, "force", false, "confirm cleanup when --dry-run=false")

	return command
}
