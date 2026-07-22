package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

var extendedDurationUnit = regexp.MustCompile(`(?i)(\d+(?:\.\d+)?)([dw])`)

// UnmarshalYAML strictly decodes a configured duration scalar.
func (duration *Duration) UnmarshalYAML(node *yaml.Node) error {
	value, err := scalarString(node, "duration")
	if err != nil {
		return err
	}
	parsed, err := ParseDuration(value)
	if err != nil {
		return err
	}

	duration.value = parsed
	duration.set = true

	return nil
}

// MarshalYAML encodes a duration as its canonical string form.
func (duration Duration) MarshalYAML() (any, error) {
	if !duration.set {
		return nullYAMLNode(), nil
	}

	return duration.value.String(), nil
}

// MarshalJSON encodes a duration as a string or null.
func (duration Duration) MarshalJSON() ([]byte, error) {
	if !duration.set {
		return []byte("null"), nil
	}

	return json.Marshal(duration.value.String())
}

// UnmarshalYAML strictly decodes an integer limit.
func (limit *IntegerLimit) UnmarshalYAML(node *yaml.Node) error {
	value, unlimited, err := parseIntegerLimit(node, true)
	if err != nil {
		return err
	}

	limit.value = value
	limit.unlimited = unlimited
	limit.set = true

	return nil
}

// MarshalYAML encodes an integer limit as a number, unlimited, or null.
func (limit IntegerLimit) MarshalYAML() (any, error) {
	return marshalLimit(limit.value, limit.unlimited, limit.set)
}

// MarshalJSON encodes an integer limit as a number, unlimited, or null.
func (limit IntegerLimit) MarshalJSON() ([]byte, error) {
	return marshalJSONLimit(limit.value, limit.unlimited, limit.set)
}

// UnmarshalYAML strictly decodes a positive slot limit.
func (limit *SlotLimit) UnmarshalYAML(node *yaml.Node) error {
	value, unlimited, err := parseIntegerLimit(node, false)
	if err != nil {
		return err
	}
	if value > math.MaxUint32 {
		return fmt.Errorf("slot limit exceeds %d", uint64(math.MaxUint32))
	}

	limit.value = uint32(value)
	limit.unlimited = unlimited
	limit.set = true

	return nil
}

// MarshalYAML encodes a slot limit as a number, unlimited, or null.
func (limit SlotLimit) MarshalYAML() (any, error) {
	return marshalLimit(uint64(limit.value), limit.unlimited, limit.set)
}

// MarshalJSON encodes a slot limit as a number, unlimited, or null.
func (limit SlotLimit) MarshalJSON() ([]byte, error) {
	return marshalJSONLimit(uint64(limit.value), limit.unlimited, limit.set)
}

// UnmarshalYAML strictly decodes a duration limit.
func (limit *DurationLimit) UnmarshalYAML(node *yaml.Node) error {
	value, err := scalarString(node, "duration limit")
	if err != nil {
		return err
	}
	if value == Unlimited {
		*limit = DurationLimit{unlimited: true, set: true}

		return nil
	}
	parsed, err := ParseDuration(value)
	if err != nil {
		return err
	}

	*limit = DurationLimit{value: parsed, set: true}

	return nil
}

// MarshalYAML encodes a duration limit as a string, unlimited, or null.
func (limit DurationLimit) MarshalYAML() (any, error) {
	if !limit.set {
		return nullYAMLNode(), nil
	}
	if limit.unlimited {
		return Unlimited, nil
	}

	return limit.value.String(), nil
}

// MarshalJSON encodes a duration limit as a string, unlimited, or null.
func (limit DurationLimit) MarshalJSON() ([]byte, error) {
	if !limit.set {
		return []byte("null"), nil
	}
	if limit.unlimited {
		return json.Marshal(Unlimited)
	}

	return json.Marshal(limit.value.String())
}

// UnmarshalYAML strictly decodes a byte limit.
func (limit *ByteLimit) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("byte limit must be an integer, IEC size, or %q", Unlimited)
	}
	if node.Tag == yamlTagInteger {
		value, err := parseDecimalUint(node.Value, "byte limit")
		if err != nil {
			return err
		}
		*limit = ByteLimit{value: value, set: true}

		return nil
	}
	if node.Tag != yamlTagString {
		return fmt.Errorf("byte limit must be an integer, IEC size, or %q", Unlimited)
	}
	if node.Value == Unlimited {
		*limit = ByteLimit{unlimited: true, set: true}

		return nil
	}
	value, err := ParseByteSize(node.Value)
	if err != nil {
		return err
	}
	*limit = ByteLimit{value: value, set: true}

	return nil
}

// MarshalYAML encodes a byte limit as bytes, unlimited, or null.
func (limit ByteLimit) MarshalYAML() (any, error) {
	return marshalLimit(limit.value, limit.unlimited, limit.set)
}

// MarshalJSON encodes a byte limit as bytes, unlimited, or null.
func (limit ByteLimit) MarshalJSON() ([]byte, error) {
	return marshalJSONLimit(limit.value, limit.unlimited, limit.set)
}

// UnmarshalYAML strictly decodes a provider:locator secret reference.
func (reference *SecretRef) UnmarshalYAML(node *yaml.Node) error {
	value, err := scalarString(node, "secret reference")
	if err != nil {
		return err
	}
	parsed, err := parseSecretReference(value)
	if err != nil {
		return err
	}
	*reference = parsed

	return nil
}

// MarshalYAML encodes a secret reference without resolving its value.
func (reference SecretRef) MarshalYAML() (any, error) {
	if reference.provider == "" {
		return nullYAMLNode(), nil
	}

	return reference.String(), nil
}

// MarshalJSON encodes a secret reference without resolving its value.
func (reference SecretRef) MarshalJSON() ([]byte, error) {
	if reference.provider == "" {
		return []byte("null"), nil
	}

	return json.Marshal(reference.String())
}

func parseSecretReference(value string) (SecretRef, error) {
	provider, locator, found := strings.Cut(value, ":")
	if !found || locator == "" {
		return SecretRef{}, errors.New("secret reference must use provider:locator syntax")
	}

	switch provider {
	case "env":
		if !validEnvironmentName(locator) {
			return SecretRef{}, fmt.Errorf("environment secret locator %q is invalid", locator)
		}
	case fileKind:
		if !filepath.IsAbs(locator) || filepath.Clean(locator) != locator {
			return SecretRef{}, errors.New("file secret locator must be a clean absolute path")
		}
	default:
		return SecretRef{}, fmt.Errorf("secret provider %q is unsupported", provider)
	}

	return SecretRef{provider: provider, locator: locator}, nil
}

// ParseDuration parses the duration grammar shared by configuration and CLI
// values. In addition to Go duration units, d is exactly 24 hours and w is
// exactly seven days. Negative and empty durations are rejected.
func ParseDuration(value string) (time.Duration, error) {
	if value == "" || strings.HasPrefix(value, "-") {
		return 0, errors.New("duration must be nonnegative and nonempty")
	}

	normalized := extendedDurationUnit.ReplaceAllStringFunc(value, func(component string) string {
		unit := component[len(component)-1]
		number := component[:len(component)-1]
		amount, parseErr := strconv.ParseFloat(number, 64)
		if parseErr != nil {
			return ""
		}
		if unit == 'w' || unit == 'W' {
			amount *= 168
		} else {
			amount *= 24
		}

		return strconv.FormatFloat(amount, 'f', -1, 64) + "h"
	})
	parsed, err := time.ParseDuration(normalized)
	if err != nil || parsed < 0 {
		return 0, fmt.Errorf("duration %q is invalid", value)
	}

	return parsed, nil
}

func parseIntegerLimit(node *yaml.Node, allowZero bool) (value uint64, unlimited bool, err error) {
	if node.Kind != yaml.ScalarNode {
		return 0, false, fmt.Errorf("integer limit must be a decimal integer or %q", Unlimited)
	}
	if node.Tag == yamlTagString && node.Value == Unlimited {
		return 0, true, nil
	}
	if node.Tag != yamlTagInteger {
		return 0, false, fmt.Errorf("integer limit must be a decimal integer or %q", Unlimited)
	}
	value, err = parseDecimalUint(node.Value, "integer limit")
	if err != nil {
		return 0, false, err
	}
	if value == 0 && !allowZero {
		return 0, false, errors.New("slot limit must be positive")
	}

	return value, false, nil
}

func parseDecimalUint(value, description string) (uint64, error) {
	if value == "" || strings.Trim(value, "0123456789") != "" {
		return 0, fmt.Errorf("%s must be an unsigned decimal integer", description)
	}
	parsed, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse %s: %w", description, err)
	}

	return parsed, nil
}

// ParseByteSize parses the byte-size grammar shared by configuration and CLI
// values. Values may be decimal bytes or an integer followed by an IEC suffix
// from B through EiB.
func ParseByteSize(value string) (uint64, error) {
	if value != "" && strings.Trim(value, "0123456789") == "" {
		return parseDecimalUint(value, "byte size")
	}

	units := []struct {
		suffix     string
		multiplier uint64
	}{
		{"EiB", 1 << 60},
		{"PiB", 1 << 50},
		{"TiB", 1 << 40},
		{"GiB", 1 << 30},
		{"MiB", 1 << 20},
		{"KiB", 1 << 10},
		{"B", 1},
	}
	for _, unit := range units {
		if !strings.HasSuffix(value, unit.suffix) {
			continue
		}
		amount := strings.TrimSuffix(value, unit.suffix)
		parsed, err := parseDecimalUint(amount, "byte size")
		if err != nil {
			return 0, err
		}
		if parsed > math.MaxUint64/unit.multiplier {
			return 0, fmt.Errorf("byte size %q overflows uint64", value)
		}

		return parsed * unit.multiplier, nil
	}

	return 0, fmt.Errorf("byte size %q must be decimal bytes or use an IEC suffix", value)
}

func scalarString(node *yaml.Node, description string) (string, error) {
	if node.Kind != yaml.ScalarNode || node.Tag != yamlTagString || node.Value == "" {
		return "", fmt.Errorf("%s must be a nonempty string", description)
	}

	return node.Value, nil
}

func marshalLimit(value uint64, unlimited, set bool) (any, error) {
	if !set {
		return nullYAMLNode(), nil
	}
	if unlimited {
		return Unlimited, nil
	}

	return value, nil
}

func nullYAMLNode() *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}
}

func marshalJSONLimit(value uint64, unlimited, set bool) ([]byte, error) {
	if !set {
		return []byte("null"), nil
	}
	if unlimited {
		return json.Marshal(Unlimited)
	}

	return json.Marshal(value)
}
