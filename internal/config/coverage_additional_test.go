package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/ryancswallace/jobman/internal/model"
)

func TestSourceAndDiscoveryFailureContracts(t *testing.T) {
	t.Parallel()

	for name, source := range map[string]Source{
		"unknown kind":    {Kind: SourceKind("unknown"), Data: []byte("{}")},
		"missing input":   {Kind: SourceExplicit},
		"ambiguous input": {Kind: SourceExplicit, Path: "config.yml", Data: []byte("{}")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := validateSource(source); err == nil {
				t.Fatal("validateSource() error = nil")
			}
		})
	}
	if got := sourceLabel(Source{Kind: SourceExplicit}); got != "explicit configuration" {
		t.Fatalf("sourceLabel() = %q", got)
	}
	if _, err := Load(
		BytesSource(SourceExplicit, "explicit", []byte("{}")),
		BytesSource(SourceSystem, "system", []byte("{}")),
	); err == nil {
		t.Fatal("Load(out-of-order sources) error = nil")
	}

	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := FindTrustedProjectConfig(file, nil); err == nil {
		t.Fatal("FindTrustedProjectConfig(file start) error = nil")
	}
	if _, _, err := FindTrustedProjectConfig(root, []string{file}); err == nil {
		t.Fatal("FindTrustedProjectConfig(file trust root) error = nil")
	}
	project := filepath.Join(root, projectConfigName)
	if err := os.Mkdir(project, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := FindTrustedProjectConfig(root, []string{root}); err == nil {
		t.Fatal("FindTrustedProjectConfig(directory config) error = nil")
	}
}

func TestLoaderStructuralAndFilesystemBoundaries(t *testing.T) {
	t.Parallel()

	if precedence, ok := SourceBuiltIn.Precedence(); !ok || precedence != 0 {
		t.Fatalf("SourceBuiltIn.Precedence() = (%d, %v)", precedence, ok)
	}
	if _, err := Load(BytesSource(SourceExplicit, "", []byte("{}"))); err != nil {
		t.Fatalf("Load(unnamed source) error = %v", err)
	}
	if _, err := Load(BytesSource(SourceExplicit, "invalid", []byte("concurrency:\n  max_active_slots: 0\n"))); err == nil {
		t.Fatal("Load(invalid effective config) error = nil")
	}

	directory := t.TempDir()
	if _, _, err := readSource(FileSource(SourceExplicit, directory)); err == nil {
		t.Fatal("readSource(directory) error = nil")
	}
	large := filepath.Join(directory, "large.yml")
	if err := os.WriteFile(large, make([]byte, maxConfigBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readSource(FileSource(SourceExplicit, large)); err == nil {
		t.Fatal("readSource(large file) error = nil")
	}
	if _, err := decodeYAML([]byte("{}\n---\n["), "trailing"); err == nil {
		t.Fatal("decodeYAML(malformed trailing document) error = nil")
	}

	scalar := &yaml.Node{Kind: yaml.ScalarNode, Tag: yamlTagString, Value: "value"}
	counter := maxYAMLNodes
	if err := validateYAMLNode(scalar, 0, &counter); err == nil {
		t.Fatal("validateYAMLNode(node limit) error = nil")
	}
	counter = 0
	if err := validateYAMLNode(scalar, maxYAMLDepth+1, &counter); err == nil {
		t.Fatal("validateYAMLNode(depth limit) error = nil")
	}
	merge := &yaml.Node{Kind: yaml.MappingNode, Tag: yamlTagMap, Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Tag: yamlTagString, Value: "<<"}, scalar,
	}}
	counter = 0
	if err := validateYAMLNode(merge, 0, &counter); err == nil {
		t.Fatal("validateYAMLNode(merge key) error = nil")
	}
}

func TestPlatformDefaultFallbacksAndSecretBoundaries(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_STATE_HOME", "")
	t.Setenv("HOME", t.TempDir())
	configFragment := filepath.Join(".config", "jobman")
	stateFragment := filepath.Join(".local", "state", "jobman")
	switch runtime.GOOS {
	case "darwin":
		configFragment = filepath.Join("Library", "Application Support", "jobman")
		stateFragment = configFragment
	case "windows":
		configFragment = filepath.Join("AppData", "Roaming", "Jobman")
		stateFragment = filepath.Join("AppData", "Local", "Jobman")
	}
	if directory, err := defaultUserConfigDir(); err != nil || !strings.Contains(directory, configFragment) {
		t.Fatalf("defaultUserConfigDir() = (%q, %v)", directory, err)
	}
	if directory, err := defaultStateDir(); err != nil || !strings.Contains(directory, stateFragment) {
		t.Fatalf("defaultStateDir() = (%q, %v)", directory, err)
	}
	if _, _, err := DefaultConfigPaths(); err != nil {
		t.Fatalf("DefaultConfigPaths() error = %v", err)
	}
	if _, err := DefaultFileSources(); err != nil {
		t.Fatalf("DefaultFileSources() error = %v", err)
	}

	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("value"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := resolveSecretFile(ctx, secret); !errors.Is(err, context.Canceled) {
		t.Fatalf("resolveSecretFile(canceled) error = %v", err)
	}
	large := filepath.Join(t.TempDir(), "large")
	if err := os.WriteFile(large, make([]byte, maxSecretBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := resolveSecretFile(t.Context(), large); err == nil {
		t.Fatal("resolveSecretFile(oversize) error = nil")
	}
}

func TestEnvironmentMergeInitializesDestinationMaps(t *testing.T) {
	t.Parallel()
	destination := Environment{}
	mergeEnvironment(&destination, Environment{
		Set: map[string]string{"A": "B"}, Secrets: map[string]string{"TOKEN": "secret"},
	})
	if destination.Set["A"] != "B" || destination.Secrets["TOKEN"] != "secret" {
		t.Fatalf("mergeEnvironment() = %+v", destination)
	}
}

func TestRemainingReferenceAndUnionValidationBranches(t *testing.T) {
	t.Parallel()

	configuration := Default()
	oneSecond, err := NewDuration(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	probe := WaitCondition{
		Type:  string(model.WaitProbe),
		Probe: &ProbeCondition{Command: nil, Timeout: oneSecond, PollInterval: oneSecond, OutputLimit: NewByteLimit(1)},
	}
	if err := validateWaitCondition(probe, configuration.Secrets); err == nil {
		t.Fatal("validateWaitCondition(invalid probe) error = nil")
	}

	notifier := baseHTTPNotifier()
	notifier.Type = "command"
	notifier.Command = nil
	notifier.HTTP = nil
	if err := validateNotifier(notifier, nil); err == nil {
		t.Fatal("validateNotifier(missing command variant) error = nil")
	}

	configuration.Profiles["missing-base"] = Profile{JobSpec: "missing"}
	if err := validateProfiles(configuration); err == nil {
		t.Fatal("validateProfiles(missing base) error = nil")
	}

	badAdmission := Admission{Pool: "missing", Slots: 0}
	if err := validateOverride(JobSpecOverride{Admission: &badAdmission}, Default()); err == nil {
		t.Fatal("validateOverride(invalid admission) error = nil")
	}

	dependencies := []Dependency{
		{Job: "job", Outcomes: []string{"success"}},
		{Job: "job", Outcomes: []string{"success"}},
	}
	if err := validateDependencies(dependencies); err != nil {
		t.Fatalf("validateDependencies(identical duplicate) error = %v", err)
	}

	for _, candidate := range []HTTPNotifier{
		{URL: "://bad"},
		{URL: "https://user@example.test/path"},
	} {
		if err := validateHTTPURL(candidate); err == nil {
			t.Errorf("validateHTTPURL(%q) error = nil", candidate.URL)
		}
	}
}

func TestTopLevelConfigurationGuardBranches(t *testing.T) {
	t.Parallel()
	tests := map[string]func(*Config){
		"schema": func(value *Config) { value.SchemaVersion++ },
		"global capacity": func(value *Config) {
			value.Concurrency.MaxActiveSlots = SlotLimit{}
		},
		"pool": func(value *Config) {
			value.Concurrency.Pools["bad name"] = UnlimitedSlotLimit()
		},
		"retention": func(value *Config) { value.Retention = Retention{} },
		"secret":    func(value *Config) { value.Secrets["secret"] = SecretRef{} },
		"redaction": func(value *Config) { value.Redaction.Patterns = []string{"["} },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			configuration := Default()
			mutate(&configuration)
			if err := configuration.Validate(); err == nil {
				t.Fatal("Validate() error = nil")
			}
		})
	}
}

func TestScalarDecoderFailureBranches(t *testing.T) {
	t.Parallel()

	mapping := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	invalidDuration := &yaml.Node{Kind: yaml.ScalarNode, Tag: yamlTagString, Value: "invalid"}
	tooLargeSlot := &yaml.Node{Kind: yaml.ScalarNode, Tag: yamlTagInteger, Value: "4294967296"}
	badInteger := &yaml.Node{Kind: yaml.ScalarNode, Tag: yamlTagInteger, Value: "not-a-number"}
	boolean := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"}
	overflow := &yaml.Node{Kind: yaml.ScalarNode, Tag: yamlTagInteger, Value: "18446744073709551616"}

	operations := map[string]func() error{
		"duration shape":       func() error { return new(Duration).UnmarshalYAML(mapping) },
		"duration value":       func() error { return new(Duration).UnmarshalYAML(invalidDuration) },
		"slot overflow":        func() error { return new(SlotLimit).UnmarshalYAML(tooLargeSlot) },
		"duration limit shape": func() error { return new(DurationLimit).UnmarshalYAML(mapping) },
		"byte limit shape":     func() error { return new(ByteLimit).UnmarshalYAML(mapping) },
		"byte limit integer":   func() error { return new(ByteLimit).UnmarshalYAML(badInteger) },
		"byte limit tag":       func() error { return new(ByteLimit).UnmarshalYAML(boolean) },
		"secret shape":         func() error { return new(SecretRef).UnmarshalYAML(mapping) },
		"integer shape": func() error {
			_, _, err := parseIntegerLimit(mapping, true)
			return err
		},
		"decimal overflow": func() error {
			_, _, err := parseIntegerLimit(overflow, true)
			return err
		},
	}
	for name, operation := range operations {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if err := operation(); err == nil {
				t.Fatal("error = nil")
			}
		})
	}

	if _, err := parseDecimalUint(strings.Repeat("9", 64), "test"); err == nil {
		t.Fatal("parseDecimalUint(overflow) error = nil")
	}
	if _, err := ParseByteSize(strings.Repeat("9", 64) + "EiB"); err == nil {
		t.Fatal("ParseByteSize(overflow) error = nil")
	}
	if _, err := ParseByteSize(strings.Repeat("9", 64) + "B"); err == nil {
		t.Fatal("ParseByteSize(decimal overflow) error = nil")
	}
}

func TestEnvironmentScalarAndPathConstructionBranches(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		value string
		kind  environmentValueKind
	}{
		"empty":            {value: "", kind: environmentSlotLimit},
		"invalid integer":  {value: "1.5", kind: environmentIntegerLimit},
		"invalid duration": {value: "soon", kind: environmentDurationLimit},
		"overflow bytes":   {value: strings.Repeat("9", 64), kind: environmentByteLimit},
		"invalid bytes":    {value: "lots", kind: environmentByteLimit},
		"unknown kind":     {value: "value", kind: environmentValueKind(99)},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := environmentScalar(test.value, test.kind); err == nil {
				t.Fatal("environmentScalar() error = nil")
			}
		})
	}
	if node, err := environmentScalar(Unlimited, environmentSlotLimit); err != nil || node.Value != Unlimited {
		t.Fatalf("environmentScalar(unlimited) = (%+v, %v)", node, err)
	}

	root := &yaml.Node{Kind: yaml.MappingNode, Tag: yamlTagMap}
	setYAMLPath(root, []string{"nested", "value"}, &yaml.Node{Kind: yaml.ScalarNode, Value: "first"})
	setYAMLPath(root, []string{"nested", "value"}, &yaml.Node{Kind: yaml.ScalarNode, Value: "second"})
	if got := root.Content[1].Content[1].Value; got != "second" {
		t.Fatalf("overlaid YAML value = %q", got)
	}
}

func TestNormalizationDefaultBranches(t *testing.T) {
	t.Parallel()
	condition := WaitCondition{Probe: &ProbeCondition{}}
	normalizeWaitCondition(&condition)
	if _, set := condition.Probe.Timeout.Value(); !set {
		t.Fatal("probe timeout was not defaulted")
	}
	if _, set := condition.Probe.PollInterval.Value(); !set {
		t.Fatal("probe poll interval was not defaulted")
	}
	if _, set := condition.Probe.OutputLimit.Value(); !set {
		t.Fatal("probe output limit was not defaulted")
	}
	withoutProbe := WaitCondition{}
	normalizeWaitCondition(&withoutProbe)
}

func TestNamedCollectionGuardBranches(t *testing.T) {
	t.Parallel()
	configuration := Default()
	configuration.WaitConditions["bad name"] = WaitCondition{}
	if err := validateWaitConditions(configuration); err == nil {
		t.Fatal("validateWaitConditions(invalid name) error = nil")
	}
	configuration = Default()
	configuration.Notifiers["bad name"] = baseHTTPNotifier()
	if err := validateNotifiers(configuration); err == nil {
		t.Fatal("validateNotifiers(invalid name) error = nil")
	}
	configuration = Default()
	configuration.JobSpecs["bad name"] = baseJobSpec()
	if err := validateJobSpecs(configuration); err == nil {
		t.Fatal("validateJobSpecs(invalid name) error = nil")
	}
	configuration = Default()
	configuration.Profiles["bad name"] = Profile{}
	if err := validateProfiles(configuration); err == nil {
		t.Fatal("validateProfiles(invalid name) error = nil")
	}
}

func TestNotifierUnionAndCommandGuardBranches(t *testing.T) {
	t.Parallel()
	base := baseHTTPNotifier()
	tests := []Notifier{
		func() Notifier {
			value := base
			value.Type = "command"
			return value
		}(),
		func() Notifier {
			value := base
			value.Type = "smtp"
			return value
		}(),
	}
	for _, notifier := range tests {
		if err := validateNotifier(notifier, nil); err == nil {
			t.Fatal("validateNotifier(mismatched union) error = nil")
		}
	}
	command := CommandNotifier{OutputLimit: byteLimit(1)}
	if err := validateCommandNotifier(command, nil); err == nil {
		t.Fatal("validateCommandNotifier(empty command) error = nil")
	}
	command.Command = []string{"true"}
	command.Environment = Environment{Unset: []string{"BAD=NAME"}}
	if err := validateCommandNotifier(command, nil); err == nil {
		t.Fatal("validateCommandNotifier(invalid environment) error = nil")
	}
}

func TestProfileAndReferenceGuardBranches(t *testing.T) {
	t.Parallel()
	configuration := Default()
	configuration.Profiles["profile"] = Profile{
		Overrides: JobSpecOverride{Command: []string{}},
	}
	if err := validateProfiles(configuration); err == nil {
		t.Fatal("validateProfiles(invalid override) error = nil")
	}

	configuration = Default()
	bad := "\n"
	configuration.Profiles["profile"] = Profile{
		Overrides: JobSpecOverride{Name: &bad},
	}
	if err := validateProfiles(configuration); err == nil {
		t.Fatal("validateProfiles(invalid produced spec) error = nil")
	}

	specification := baseJobSpec()
	configuration = Default()
	configuration.Notifiers["empty"] = baseHTTPNotifier()
	specification.Notification.Notifiers = []string{"empty"}
	specification.Notification.Events = nil
	if err := validateJobSpec(specification, configuration); err == nil {
		t.Fatal("validateJobSpec(unsubscribed notifier) error = nil")
	}
}

func TestPrimitiveValidationGuardBranches(t *testing.T) {
	t.Parallel()
	concurrency := Default().Concurrency
	concurrency.Pools["small"] = SlotLimit{value: 1, set: true}
	if err := validateAdmission(Admission{Slots: 2, Pool: "small"}, concurrency); err == nil {
		t.Fatal("validateAdmission(pool overflow) error = nil")
	}
	if !privateHTTPHost("service.localhost") || !privateHTTPHost("service.local") ||
		privateHTTPHost("example.com") {
		t.Fatal("privateHTTPHost() returned unexpected classification")
	}
	if validHTTPHeaderName("") {
		t.Fatal("validHTTPHeaderName(empty) = true")
	}
	if err := validateCommand([]string{"true", "bad\x00argument"}); err == nil {
		t.Fatal("validateCommand(NUL) error = nil")
	}
	if err := validateDisplayName("   "); err == nil {
		t.Fatal("validateDisplayName(spaces) error = nil")
	}
	if err := validateDisplayName("bad\nname"); err == nil {
		t.Fatal("validateDisplayName(control) error = nil")
	}
	if err := validateReferences("item", []string{"one", "one"}, map[string]bool{"one": true}); err == nil {
		t.Fatal("validateReferences(duplicate) error = nil")
	}
	if err := validateEvents([]string{"job_started", "job_started"}); err == nil {
		t.Fatal("validateEvents(duplicate) error = nil")
	}
	if !validEnvironmentName("NAME_2") || validEnvironmentName("2NAME") {
		t.Fatal("validEnvironmentName() returned unexpected classification")
	}
}

func TestRedactorLimitGuardBranches(t *testing.T) {
	t.Parallel()
	patterns := make([]string, maxRedactionPatterns+1)
	if _, err := NewRedactor(RedactionConfig{Patterns: patterns}, nil); err == nil {
		t.Fatal("NewRedactor(too many patterns) error = nil")
	}
	secrets := make(map[string]string, maxConfiguredSecrets+1)
	for index := range maxConfiguredSecrets + 1 {
		secrets[strings.Repeat("x", index+1)] = time.Second.String()
	}
	if _, err := NewRedactor(RedactionConfig{}, secrets); err == nil {
		t.Fatal("NewRedactor(too many secrets) error = nil")
	}
}
