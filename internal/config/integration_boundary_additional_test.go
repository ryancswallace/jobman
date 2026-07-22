package config

import (
	"errors"
	"strings"
	"testing"
)

func TestConfigurationIntegrationFailureBoundaries(t *testing.T) {
	t.Parallel()

	if _, err := Load(Source{}); err == nil {
		t.Fatal("Load(invalid source) error = nil")
	}
	if _, err := Parse([]byte("[]\n")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Parse(non-mapping YAML) error = %v, want ErrInvalid", err)
	}

	configuration := Default()
	if _, err := configuration.ResolveJobSpec("missing"); err == nil {
		t.Fatal("ResolveJobSpec(missing job spec) error = nil")
	}
	if _, err := configuration.ResolveJobSpecWithCommand("", []string{}); err == nil {
		t.Fatal("ResolveJobSpecWithCommand(empty command) error = nil")
	}

	if node, err := environmentScalar("42", environmentByteLimit); err != nil || node.Value != "42" {
		t.Fatalf("environmentScalar(decimal bytes) = (%+v, %v)", node, err)
	}
	if _, err := ParseDuration(strings.Repeat("9", 400) + "d"); err == nil {
		t.Fatal("ParseDuration(overflowing extended unit) error = nil")
	}

	notifier := baseHTTPNotifier()
	notifier.HTTP = nil
	notifier.Command = &CommandNotifier{}
	if err := validateNotifier(notifier, nil); err == nil {
		t.Fatal("validateNotifier(http without HTTP configuration) error = nil")
	}

	if got := invalidError(ErrInvalid); !errors.Is(got, ErrInvalid) {
		t.Fatalf("invalidError(ErrInvalid) = %v", got)
	}
}
