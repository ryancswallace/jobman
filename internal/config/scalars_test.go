package config

import (
	"encoding/json"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

func TestDurationParsing(t *testing.T) {
	t.Parallel()

	tests := map[string]time.Duration{
		"0s":       0,
		"250ms":    250 * time.Millisecond,
		"1d":       24 * time.Hour,
		"1.5d":     36 * time.Hour,
		"1w2d3h":   (7*24 + 2*24 + 3) * time.Hour,
		"2h45m30s": 2*time.Hour + 45*time.Minute + 30*time.Second,
	}
	for input, want := range tests {
		got, err := ParseDuration(input)
		if err != nil {
			t.Fatalf("ParseDuration(%q) error = %v", input, err)
		}
		if got != want {
			t.Fatalf("ParseDuration(%q) = %v, want %v", input, got, want)
		}
	}
	for _, input := range []string{"", "-1s", "1", "tomorrow", "1M", "1mo", "999999999999999999999999d"} {
		if _, err := ParseDuration(input); err == nil {
			t.Fatalf("ParseDuration(%q) succeeded", input)
		}
	}
}

func TestByteSizeParsing(t *testing.T) {
	t.Parallel()

	tests := map[string]uint64{
		"0":    0,
		"42":   42,
		"0B":   0,
		"1KiB": 1 << 10,
		"2MiB": 2 << 20,
		"3GiB": 3 << 30,
		"1TiB": 1 << 40,
	}
	for input, want := range tests {
		got, err := ParseByteSize(input)
		if err != nil {
			t.Fatalf("ParseByteSize(%q) error = %v", input, err)
		}
		if got != want {
			t.Fatalf("ParseByteSize(%q) = %d, want %d", input, got, want)
		}
	}
	for _, input := range []string{
		"", "1KB", "1.5MiB", "-1", "-1B", "0x10", "18446744073709551616",
		"18446744073709551615EiB",
	} {
		if _, err := ParseByteSize(input); err == nil {
			t.Fatalf("ParseByteSize(%q) succeeded", input)
		}
	}
}

func TestStrictLimitScalars(t *testing.T) {
	t.Parallel()

	var value struct {
		Slots    SlotLimit     `yaml:"slots"`
		Count    IntegerLimit  `yaml:"count"`
		Duration DurationLimit `yaml:"duration"`
		Bytes    ByteLimit     `yaml:"bytes"`
	}
	if err := yaml.Unmarshal([]byte("slots: 2\ncount: unlimited\nduration: 1d\nbytes: 5MiB\n"), &value); err != nil {
		t.Fatalf("yaml.Unmarshal() error = %v", err)
	}
	if slots, finite := value.Slots.Value(); !finite || slots != 2 {
		t.Fatalf("Slots = (%d, %v)", slots, finite)
	}
	if !value.Count.IsUnlimited() {
		t.Fatal("Count is not unlimited")
	}
	if duration, finite := value.Duration.Value(); !finite || duration != 24*time.Hour {
		t.Fatalf("Duration = (%v, %v)", duration, finite)
	}
	if size, finite := value.Bytes.Value(); !finite || size != 5<<20 {
		t.Fatalf("Bytes = (%d, %v)", size, finite)
	}

	for _, input := range []string{
		"slots: 0\n",
		"slots: -1\n",
		"slots: 0x10\n",
		"count: -1\n",
		"duration: forever\n",
		"bytes: 1MB\n",
	} {
		if err := yaml.Unmarshal([]byte(input), &value); err == nil {
			t.Fatalf("yaml.Unmarshal(%q) succeeded", input)
		}
	}
}

func TestSecretReferenceParsingAndSerialization(t *testing.T) {
	t.Parallel()

	fileReference := "file:" + filepath.Join(t.TempDir(), "jobman-secret")
	var references map[string]SecretRef
	if err := yaml.Unmarshal([]byte("token: env:JOBMAN_TOKEN\nfile: "+strconv.Quote(fileReference)+"\n"), &references); err != nil {
		t.Fatalf("yaml.Unmarshal() error = %v", err)
	}
	if references["token"].Provider() != "env" || references["token"].Locator() != "JOBMAN_TOKEN" {
		t.Fatalf("token = %#v", references["token"])
	}
	encoded, err := json.Marshal(references)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	var roundTrip map[string]string
	if err := json.Unmarshal(encoded, &roundTrip); err != nil {
		t.Fatal(err)
	}
	if roundTrip["file"] != fileReference {
		t.Fatalf("json.Marshal() = %s, want file reference %q", encoded, fileReference)
	}

	for _, input := range []string{
		"secret: literal\n",
		"secret: env:lowercase\n",
		"secret: file:relative\n",
		"secret: vault:item\n",
	} {
		if err := yaml.Unmarshal([]byte(input), &references); err == nil {
			t.Fatalf("yaml.Unmarshal(%q) succeeded", input)
		}
	}
}

func TestScalarConstructors(t *testing.T) {
	t.Parallel()

	duration, err := NewDuration(time.Second)
	if err != nil || !duration.IsSet() {
		t.Fatalf("NewDuration() = (%v, %v)", duration, err)
	}
	if _, negativeErr := NewDuration(-time.Second); negativeErr == nil {
		t.Fatal("NewDuration() accepted a negative value")
	}
	slots, err := NewSlotLimit(2)
	if err != nil {
		t.Fatalf("NewSlotLimit() error = %v", err)
	}
	if value, finite := slots.Value(); !finite || value != 2 {
		t.Fatalf("NewSlotLimit(2).Value() = (%d, %v)", value, finite)
	}
	if _, err := NewSlotLimit(0); err == nil {
		t.Fatal("NewSlotLimit() accepted zero")
	}
	if !UnlimitedSlotLimit().IsUnlimited() || !UnlimitedIntegerLimit().IsUnlimited() ||
		!UnlimitedDurationLimit().IsUnlimited() || !UnlimitedByteLimit().IsUnlimited() {
		t.Fatal("an unlimited constructor returned a finite limit")
	}
	if !NewIntegerLimit(0).IsSet() || !NewByteLimit(0).IsSet() {
		t.Fatal("a finite constructor returned an unset limit")
	}
	if _, err := NewDurationLimit(-time.Second); err == nil {
		t.Fatal("NewDurationLimit() accepted a negative value")
	}
	if reference, err := ParseSecretRef("env:TOKEN"); err != nil || reference.Provider() != "env" {
		t.Fatalf("ParseSecretRef() = (%#v, %v)", reference, err)
	}
}

func TestScalarMarshalRepresentations(t *testing.T) {
	t.Parallel()

	for name, scalar := range map[string]interface {
		MarshalYAML() (any, error)
	}{
		"duration":       Duration{},
		"integer limit":  IntegerLimit{},
		"slot limit":     SlotLimit{},
		"duration limit": DurationLimit{},
		"byte limit":     ByteLimit{},
		"secret":         SecretRef{},
	} {
		encoded, err := scalar.MarshalYAML()
		if err != nil {
			t.Fatalf("%s MarshalYAML() error = %v", name, err)
		}
		node, ok := encoded.(*yaml.Node)
		if !ok || node.Tag != "!!null" {
			t.Fatalf("%s MarshalYAML() = %#v, want null node", name, encoded)
		}
	}
	for name, value := range map[string]any{
		"duration":       Duration{},
		"integer limit":  IntegerLimit{},
		"slot limit":     SlotLimit{},
		"duration limit": DurationLimit{},
		"byte limit":     ByteLimit{},
		"secret":         SecretRef{},
	} {
		encoded, err := json.Marshal(value)
		if err != nil || string(encoded) != "null" {
			t.Fatalf("%s JSON = %s, %v", name, encoded, err)
		}
	}
	if value, err := UnlimitedDurationLimit().MarshalYAML(); err != nil || value != Unlimited {
		t.Fatalf("unlimited DurationLimit.MarshalYAML() = %#v, %v", value, err)
	}
	finite, err := NewDurationLimit(time.Second)
	if err != nil {
		t.Fatalf("NewDurationLimit() error = %v", err)
	}
	if value, err := finite.MarshalYAML(); err != nil || value != "1s" {
		t.Fatalf("finite DurationLimit.MarshalYAML() = %#v, %v", value, err)
	}
	if _, err := scalarString(&yaml.Node{Kind: yaml.MappingNode}, "value"); err == nil {
		t.Fatal("scalarString(mapping) error = nil")
	}
}
