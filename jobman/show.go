package jobman

import (
	"errors"
	"fmt"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/ryancswallace/jobman/internal/app"
	"github.com/ryancswallace/jobman/internal/model"
	"github.com/ryancswallace/jobman/internal/store"
)

func newShowCommand(dependencies dependencies, root *rootOptions) *cobra.Command {
	var jsonOutput bool
	runCommand := &cobra.Command{
		Use:   "run JOB RUN",
		Short: "Show one run by number or negative index",
		Args:  usageArgs(cobra.ExactArgs(2)),
		RunE: func(command *cobra.Command, arguments []string) error {
			return showRun(command, dependencies, root, arguments[0], arguments[1], jsonOutput)
		},
	}
	runCommand.Flags().SetInterspersed(false)
	command := &cobra.Command{
		Use:   "show JOB",
		Short: "Show a job and its run history",
		Args:  usageArgs(cobra.ExactArgs(1)),
		RunE: func(command *cobra.Command, arguments []string) error {
			return showJob(command, dependencies, root, arguments[0], jsonOutput)
		},
	}
	command.PersistentFlags().BoolVar(&jsonOutput, "json", false, "emit versioned JSON")
	command.AddCommand(
		&cobra.Command{
			Use:   "job JOB",
			Short: "Show a job and its run history",
			Args:  usageArgs(cobra.ExactArgs(1)),
			RunE: func(command *cobra.Command, arguments []string) error {
				return showJob(command, dependencies, root, arguments[0], jsonOutput)
			},
		},
		runCommand,
	)

	return command
}

func showJob(
	command *cobra.Command,
	dependencies dependencies,
	root *rootOptions,
	selector string,
	jsonOutput bool,
) error {
	return withBackend(command, dependencies, root, func(backend app.Backend) error {
		value, err := backend.Inspect(command.Context(), selector)
		if err != nil {
			return err
		}
		if jsonOutput {
			return writeJSON(command, detail(value))
		}

		return writeJobDetails(command, value)
	})
}

func showRun(
	command *cobra.Command,
	dependencies dependencies,
	root *rootOptions,
	selector,
	runSelector string,
	jsonOutput bool,
) error {
	return withBackend(command, dependencies, root, func(backend app.Backend) error {
		value, err := backend.Inspect(command.Context(), selector)
		if err != nil {
			return err
		}
		run, err := selectRun(value.Runs, runSelector)
		if err != nil {
			return err
		}
		presented := presentRuns([]model.RunState{run})[0]
		if jsonOutput {
			return writeJSON(command, presented)
		}

		return writeRunDetails(command, run)
	})
}

func selectRun(runs []model.RunState, selector string) (model.RunState, error) {
	value, err := strconv.ParseInt(selector, 10, 64)
	if err != nil || value == 0 {
		return model.RunState{}, usageError(errors.New("RUN must be a nonzero run number or negative index"))
	}
	if value < 0 {
		index := int64(len(runs)) + value
		if index < 0 || index >= int64(len(runs)) {
			return model.RunState{}, fmt.Errorf("show run %s: %w", selector, app.ErrNotFound)
		}

		return runs[index], nil
	}
	for _, run := range runs {
		if run.Number == uint64(value) {
			return run, nil
		}
	}

	return model.RunState{}, fmt.Errorf("show run %s: %w", selector, app.ErrNotFound)
}

func writeRunDetails(command *cobra.Command, run model.RunState) error {
	writer := tabwriter.NewWriter(command.OutOrStdout(), 0, 4, 2, ' ', 0)
	fields := [][2]string{
		{"ID", run.ID.String()},
		{"Number", strconv.FormatUint(run.Number, 10)},
		{"Phase", string(run.Phase)},
		{"Outcome", string(run.Outcome)},
		{"Revision", strconv.FormatUint(run.Revision, 10)},
		{"Resolved executable", run.ResolvedExecutable},
		{"Reserved", run.ReservedAt.UTC().Format(timeFormat)},
		{"Started", formatOptionalTime(run.StartedAt)},
		{"Completed", formatOptionalTime(run.CompletedAt)},
		{"Logs", formatLogAvailability(run.Logs.Available(), run.Logs.PrunedAt)},
	}
	for _, field := range fields {
		if _, err := fmt.Fprintf(
			writer,
			"%s:\t%s\n",
			field[0],
			redactField(command, field[0], field[1]),
		); err != nil {
			return fmt.Errorf("write run details: %w", err)
		}
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("flush run details: %w", err)
	}

	return nil
}

func writeJobDetails(command *cobra.Command, value app.JobDetails) error {
	writer := tabwriter.NewWriter(command.OutOrStdout(), 0, 4, 2, ' ', 0)
	fields := [][2]string{
		{"ID", value.Job.ID.String()},
		{"Name", value.Job.Spec.Name()},
		{"Phase", string(value.Job.Phase)},
		{"Outcome", string(value.Job.Outcome)},
		{"Submitted", value.Job.SubmittedAt.UTC().Format(timeFormat)},
		{"Executable", value.Job.Spec.Executable()},
		{"Working directory", value.Job.Spec.WorkingDirectory()},
		{"Completed runs", strconv.FormatUint(value.Runtime.RunCount, 10)},
		{"Successful runs", strconv.FormatUint(value.Runtime.SuccessCount, 10)},
		{"Failed runs", strconv.FormatUint(value.Runtime.FailureCount, 10)},
		{"Dependencies", strconv.Itoa(len(value.Dependencies))},
		{"Wait evaluations", strconv.Itoa(len(value.WaitEvaluations))},
		{"Admission", formatAdmission(value.Admission)},
		{"Notification deliveries", strconv.Itoa(len(value.NotificationDeliveries))},
		{"Pending notifications", strconv.Itoa(pendingNotificationDeliveries(value.NotificationDeliveries))},
		{"Notification attempts", strconv.Itoa(len(value.NotificationAttempts))},
	}
	for _, field := range fields {
		if _, err := fmt.Fprintf(
			writer,
			"%s:\t%s\n",
			field[0],
			redactField(command, field[0], field[1]),
		); err != nil {
			return fmt.Errorf("write job details: %w", err)
		}
	}
	if len(value.Runs) > 0 {
		if _, err := fmt.Fprintln(writer, "\nRUN\tPHASE\tOUTCOME\tSTARTED\tCOMPLETED\tLOGS"); err != nil {
			return fmt.Errorf("write run header: %w", err)
		}
	}
	for _, run := range value.Runs {
		if _, err := fmt.Fprintf(
			writer,
			"%d\t%s\t%s\t%s\t%s\t%s\n",
			run.Number,
			run.Phase,
			run.Outcome,
			formatOptionalTime(run.StartedAt),
			formatOptionalTime(run.CompletedAt),
			formatLogAvailability(run.Logs.Available(), run.Logs.PrunedAt),
		); err != nil {
			return fmt.Errorf("write run details: %w", err)
		}
	}
	if err := writer.Flush(); err != nil {
		return fmt.Errorf("flush job details: %w", err)
	}

	return nil
}

func formatAdmission(admission *store.Admission) string {
	if admission == nil {
		return "none"
	}
	scope := "global"
	if admission.Pool != "" {
		scope = "pool " + admission.Pool
	}
	state := "active"
	if admission.ReleasedAt != nil {
		state = "released"
	}

	return fmt.Sprintf("%s, %s, %d slot(s)", state, scope, admission.Slots)
}

func pendingNotificationDeliveries(deliveries []store.NotificationDelivery) int {
	pending := 0
	for _, delivery := range deliveries {
		if delivery.Status == store.NotificationDeliveryPending ||
			delivery.Status == store.NotificationDeliveryDelivering {
			pending++
		}
	}

	return pending
}

func formatLogAvailability(available bool, prunedAt *time.Time) string {
	if available {
		return "available"
	}
	if prunedAt == nil {
		return "unavailable"
	}

	return "pruned " + prunedAt.UTC().Format(timeFormat)
}

func formatOptionalTime(value *time.Time) string {
	if value == nil {
		return ""
	}

	return value.UTC().Format(timeFormat)
}
