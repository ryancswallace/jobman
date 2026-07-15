package config

import (
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"
)

func TestScalarAccessorsAndZeroValues(t *testing.T) {
	t.Parallel()

	duration, durationErr := NewDuration(2 * time.Second)
	if durationErr != nil || duration.String() != "2s" || duration.IsZero() {
		t.Fatalf("NewDuration() = %#v, %v", duration, durationErr)
	}
	if (Duration{}).String() != "" {
		t.Fatal("zero Duration.String() is not empty")
	}
	if _, err := NewDuration(-1); err == nil {
		t.Fatal("NewDuration(-1) error = nil")
	}

	integer := NewIntegerLimit(0)
	if !integer.IsSet() || integer.IsUnlimited() || integer.IsZero() {
		t.Fatalf("finite IntegerLimit = %#v", integer)
	}
	if !(IntegerLimit{}).IsZero() {
		t.Fatal("zero IntegerLimit.IsZero() = false")
	}
	slots, slotsErr := NewSlotLimit(2)
	if slotsErr != nil || !slots.IsSet() || slots.IsZero() {
		t.Fatalf("NewSlotLimit() = %#v, %v", slots, slotsErr)
	}
	if _, err := NewSlotLimit(0); err == nil {
		t.Fatal("NewSlotLimit(0) error = nil")
	}
	if !(SlotLimit{}).IsZero() || !UnlimitedSlotLimit().IsSet() {
		t.Fatal("SlotLimit zero/set accessors mismatch")
	}
	durationLimit, err := NewDurationLimit(time.Minute)
	if err != nil || !durationLimit.IsSet() || durationLimit.IsZero() {
		t.Fatalf("NewDurationLimit() = %#v, %v", durationLimit, err)
	}
	if _, err := NewDurationLimit(-1); err == nil {
		t.Fatal("NewDurationLimit(-1) error = nil")
	}
	if !(DurationLimit{}).IsZero() || !UnlimitedDurationLimit().IsSet() {
		t.Fatal("DurationLimit zero/set accessors mismatch")
	}
	if !(ByteLimit{}).IsZero() || NewByteLimit(1).IsZero() {
		t.Fatal("ByteLimit.IsZero() mismatch")
	}
	if !(SecretRef{}).IsZero() || (SecretRef{}).String() != "" {
		t.Fatal("zero SecretRef accessors mismatch")
	}

	var redactor *Redactor
	if got := redactor.RedactField("token", "value"); got != "value" {
		t.Fatalf("nil RedactField() = %q", got)
	}
}

func TestResolveJobSpecWithEveryOverride(t *testing.T) {
	t.Parallel()

	configuration := Default()
	configuration.JobSpecs["base"] = baseJobSpec()
	name := "overridden"
	workingDirectory := t.TempDir()
	stdin := stdinNull
	stop := StopPolicy{GracePeriod: durationMust(time.Second), ForceAfterGrace: true}
	wait := WaitPolicy{Mode: waitModeAny}
	admission := Admission{Slots: 1}
	completion := configuration.JobSpecs["base"].Completion
	delay := DelayPolicy{Strategy: "constant", Initial: durationMust(time.Second)}
	timeouts := TimeoutPolicy{Run: UnlimitedDurationLimit(), Job: UnlimitedDurationLimit()}
	logging := LoggingPolicy{Capture: "none"}
	notification := NotificationPolicy{}
	configuration.Profiles["all"] = Profile{Overrides: JobSpecOverride{
		Command:          []string{"profile-command"},
		Name:             &name,
		Tags:             []string{"tag"},
		Groups:           []string{"group"},
		WorkingDirectory: &workingDirectory,
		Environment: &Environment{
			Set: map[string]string{"SET": "value"}, Unset: []string{"UNSET"},
		},
		Stdin:        &stdin,
		Stop:         &stop,
		Dependencies: []Dependency{},
		Wait:         &wait,
		Admission:    &admission,
		Completion:   &completion,
		Delay:        &delay,
		Timeouts:     &timeouts,
		Logging:      &logging,
		Notification: &notification,
	}}
	resolved, err := configuration.ResolveJobSpecWithCommand("base", []string{"cli-command", "arg"}, "all")
	if err != nil {
		t.Fatalf("ResolveJobSpecWithCommand() error = %v", err)
	}
	if !reflect.DeepEqual(resolved.Command, []string{"cli-command", "arg"}) || resolved.Name != name ||
		resolved.WorkingDirectory != workingDirectory || resolved.Environment.Set["SET"] != "value" {
		t.Fatalf("resolved specification = %#v", resolved)
	}
}

func TestStateDirectoryDefaultsAndEnvironmentSource(t *testing.T) {
	if runtime.GOOS == goosDarwin || runtime.GOOS == goosWindows {
		t.Skip("test expectations use XDG paths")
	}
	stateHome := t.TempDir()
	t.Setenv(stateDirEnv, "")
	t.Setenv("XDG_STATE_HOME", stateHome)
	got, stateErr := StateDir("")
	if stateErr != nil {
		t.Fatalf("StateDir(default) error = %v", stateErr)
	}
	if got != filepath.Join(stateHome, "jobman") {
		t.Fatalf("StateDir(default) = %q", got)
	}
	explicit := filepath.Join(t.TempDir(), "explicit")
	t.Setenv(stateDirEnv, explicit)
	if got, err := StateDir(""); err != nil || got != explicit {
		t.Fatalf("StateDir(environment) = %q, %v", got, err)
	}

	t.Setenv("JOBMAN_RETENTION_MAX_JOBS", "12")
	source, found, err := CurrentEnvironmentSource()
	if err != nil || !found || source.Kind != SourceEnvironment {
		t.Fatalf("CurrentEnvironmentSource() = %#v, %t, %v", source, found, err)
	}
}
