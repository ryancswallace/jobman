package config

import "fmt"

// ResolveJobSpec selects a named base specification and applies profiles in
// argument order. A profile may provide the base only when name is empty; two
// different bases are rejected rather than selected implicitly.
func (configuration Config) ResolveJobSpec(name string, profiles ...string) (JobSpec, error) {
	return configuration.resolveSelectedJobSpec(name, nil, profiles...)
}

// ResolveJobSpecWithCommand resolves the selected configuration while applying
// a direct CLI command at the highest precedence. This permits profiles that
// contain only policy overrides to be used with an explicit command.
func (configuration Config) ResolveJobSpecWithCommand(
	name string,
	command []string,
	profiles ...string,
) (JobSpec, error) {
	return configuration.resolveSelectedJobSpec(name, command, profiles...)
}

func (configuration Config) resolveSelectedJobSpec(name string, command []string, profiles ...string) (JobSpec, error) {
	baseName := name
	for _, profileName := range profiles {
		profile, found := configuration.Profiles[profileName]
		if !found {
			return JobSpec{}, fmt.Errorf("unknown profile %q", profileName)
		}
		if profile.JobSpec == "" {
			continue
		}
		if baseName != "" && baseName != profile.JobSpec {
			return JobSpec{}, fmt.Errorf(
				"profile %q selects job spec %q, conflicting with %q",
				profileName,
				profile.JobSpec,
				baseName,
			)
		}
		baseName = profile.JobSpec
	}

	var specification JobSpec
	if baseName != "" {
		base, found := configuration.JobSpecs[baseName]
		if !found {
			return JobSpec{}, fmt.Errorf("unknown job spec %q", baseName)
		}
		specification = cloneJobSpec(base)
	}
	for _, profileName := range profiles {
		applyOverride(&specification, configuration.Profiles[profileName].Overrides)
	}
	if command != nil {
		specification.Command = append([]string(nil), command...)
	}
	normalizeJobSpec(&specification)
	if err := validateJobSpec(specification, configuration); err != nil {
		return JobSpec{}, fmt.Errorf("resolve job specification: %w", err)
	}

	return specification, nil
}

//nolint:cyclop // Each independent optional field is applied only when present.
func applyOverride(specification *JobSpec, override JobSpecOverride) {
	if override.Command != nil {
		specification.Command = append([]string(nil), override.Command...)
	}
	if override.Name != nil {
		specification.Name = *override.Name
	}
	if override.Tags != nil {
		specification.Tags = append([]string(nil), override.Tags...)
	}
	if override.Groups != nil {
		specification.Groups = append([]string(nil), override.Groups...)
	}
	if override.WorkingDirectory != nil {
		specification.WorkingDirectory = *override.WorkingDirectory
	}
	if override.Environment != nil {
		mergeEnvironment(&specification.Environment, *override.Environment)
	}
	if override.Stdin != nil {
		specification.Stdin = *override.Stdin
	}
	if override.Stop != nil {
		specification.Stop = *override.Stop
	}
	if override.Dependencies != nil {
		specification.Dependencies = cloneDependencies(override.Dependencies)
	}
	if override.Wait != nil {
		specification.Wait = cloneWaitPolicy(*override.Wait)
	}
	if override.Admission != nil {
		specification.Admission = *override.Admission
	}
	if override.Completion != nil {
		specification.Completion = cloneCompletionPolicy(*override.Completion)
	}
	if override.Delay != nil {
		specification.Delay = *override.Delay
	}
	if override.Timeouts != nil {
		specification.Timeouts = *override.Timeouts
	}
	if override.Logging != nil {
		specification.Logging = *override.Logging
	}
	if override.Notification != nil {
		specification.Notification = cloneNotificationPolicy(*override.Notification)
	}
}

func mergeEnvironment(destination *Environment, overlay Environment) {
	if destination.Set == nil {
		destination.Set = map[string]string{}
	}
	for name, value := range overlay.Set {
		destination.Set[name] = value
	}
	if overlay.Unset != nil {
		destination.Unset = append([]string(nil), overlay.Unset...)
	}
	if destination.Secrets == nil {
		destination.Secrets = map[string]string{}
	}
	for name, value := range overlay.Secrets {
		destination.Secrets[name] = value
	}
}

func cloneJobSpec(specification JobSpec) JobSpec {
	clone := specification
	clone.Command = append([]string(nil), specification.Command...)
	clone.Tags = append([]string(nil), specification.Tags...)
	clone.Groups = append([]string(nil), specification.Groups...)
	clone.Environment = cloneEnvironment(specification.Environment)
	clone.Dependencies = cloneDependencies(specification.Dependencies)
	clone.Wait = cloneWaitPolicy(specification.Wait)
	clone.Completion = cloneCompletionPolicy(specification.Completion)
	clone.Notification = cloneNotificationPolicy(specification.Notification)

	return clone
}

func cloneDependencies(dependencies []Dependency) []Dependency {
	clones := make([]Dependency, len(dependencies))
	for index, dependency := range dependencies {
		clones[index] = Dependency{
			Job:      dependency.Job,
			Outcomes: append([]string(nil), dependency.Outcomes...),
		}
	}

	return clones
}

func cloneEnvironment(environment Environment) Environment {
	clone := Environment{
		Set:     make(map[string]string, len(environment.Set)),
		Unset:   append([]string(nil), environment.Unset...),
		Secrets: make(map[string]string, len(environment.Secrets)),
	}
	for name, value := range environment.Set {
		clone.Set[name] = value
	}
	for name, value := range environment.Secrets {
		clone.Secrets[name] = value
	}

	return clone
}

func cloneWaitPolicy(policy WaitPolicy) WaitPolicy {
	clone := policy
	clone.Conditions = append([]string(nil), policy.Conditions...)

	return clone
}

func cloneCompletionPolicy(policy CompletionPolicy) CompletionPolicy {
	clone := policy
	clone.SuccessExitCodes = append([]int(nil), policy.SuccessExitCodes...)
	clone.RetryableExitCodes = append([]int(nil), policy.RetryableExitCodes...)

	return clone
}

func cloneNotificationPolicy(policy NotificationPolicy) NotificationPolicy {
	clone := policy
	clone.Notifiers = append([]string(nil), policy.Notifiers...)
	clone.Events = append([]string(nil), policy.Events...)

	return clone
}
