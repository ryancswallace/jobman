package jobman

import (
	"context"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ryancswallace/jobman/internal/app"
)

func jobIDArgumentCompletion(
	dependencies dependencies,
	root *rootOptions,
) cobra.CompletionFunc {
	complete := jobIDCompletion(dependencies, root)

	return func(
		command *cobra.Command,
		arguments []string,
		toComplete string,
	) ([]cobra.Completion, cobra.ShellCompDirective) {
		if len(arguments) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		return complete(command, arguments, toComplete)
	}
}

func jobIDCompletion(dependencies dependencies, root *rootOptions) cobra.CompletionFunc {
	return func(
		command *cobra.Command,
		_ []string,
		toComplete string,
	) ([]cobra.Completion, cobra.ShellCompDirective) {
		if command.Context() == nil {
			command.SetContext(context.Background())
		}
		var completions []cobra.Completion
		err := withBackend(command, dependencies, root, func(backend app.Backend) error {
			jobs, listErr := backend.List(command.Context())
			if listErr != nil {
				return listErr
			}
			completions = make([]cobra.Completion, 0, len(jobs))
			for _, job := range jobs {
				id := job.ID.String()
				if strings.HasPrefix(id, toComplete) {
					completions = append(completions, id)
				}
			}

			return nil
		})
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}

		return completions, cobra.ShellCompDirectiveNoFileComp
	}
}

func jobIDOutcomeCompletion(dependencies dependencies, root *rootOptions) cobra.CompletionFunc {
	complete := jobIDCompletion(dependencies, root)

	return func(
		command *cobra.Command,
		arguments []string,
		toComplete string,
	) ([]cobra.Completion, cobra.ShellCompDirective) {
		if strings.Contains(toComplete, "=") {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		completions, directive := complete(command, arguments, toComplete)
		for index := range completions {
			completions[index] += "="
		}

		return completions, directive | cobra.ShellCompDirectiveNoSpace
	}
}

func registerJobIDFlagCompletion(
	command *cobra.Command,
	dependencies dependencies,
	root *rootOptions,
	flagNames ...string,
) {
	complete := jobIDCompletion(dependencies, root)
	for _, flagName := range flagNames {
		if err := command.RegisterFlagCompletionFunc(flagName, complete); err != nil {
			panic(err)
		}
	}
}
