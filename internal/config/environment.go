package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

type environmentValueKind uint8

const (
	environmentSlotLimit environmentValueKind = iota
	environmentIntegerLimit
	environmentDurationLimit
	environmentByteLimit
)

type environmentBinding struct {
	path string
	kind environmentValueKind
}

var environmentBindings = map[string]environmentBinding{
	"JOBMAN_CONCURRENCY_MAX_ACTIVE_SLOTS":         {"concurrency.max_active_slots", environmentSlotLimit},
	"JOBMAN_RETENTION_COMPLETED_METADATA_MAX_AGE": {"retention.completed_metadata_max_age", environmentDurationLimit},
	"JOBMAN_RETENTION_COMPLETED_LOG_MAX_AGE":      {"retention.completed_log_max_age", environmentDurationLimit},
	"JOBMAN_RETENTION_MAX_JOBS":                   {"retention.max_jobs", environmentIntegerLimit},
	"JOBMAN_RETENTION_MAX_RUNS_PER_JOB":           {"retention.max_runs_per_job", environmentIntegerLimit},
	"JOBMAN_RETENTION_MAX_LOG_BYTES_PER_JOB":      {"retention.max_log_bytes_per_job", environmentByteLimit},
	"JOBMAN_RETENTION_MAX_TOTAL_LOG_BYTES":        {"retention.max_total_log_bytes", environmentByteLimit},
}

// EnvironmentBindings returns the documented, reversible environment-to-YAML mapping.
func EnvironmentBindings() map[string]string {
	bindings := make(map[string]string, len(environmentBindings))
	for name, binding := range environmentBindings {
		bindings[name] = binding.path
	}

	return bindings
}

// EnvironmentPath returns the YAML path controlled by a documented variable.
func EnvironmentPath(name string) (string, bool) {
	binding, found := environmentBindings[name]

	return binding.path, found
}

// EnvironmentSource maps documented variables from an environ-style slice to
// a single override layer. Other JOBMAN_ variables belong to CLI/runtime
// configuration and are deliberately ignored here.
func EnvironmentSource(environ []string) (Source, bool, error) {
	root := &yaml.Node{Kind: yaml.MappingNode, Tag: yamlTagMap}
	seen := make(map[string]struct{}, len(environmentBindings))
	configured := false
	for _, assignment := range environ {
		name, value, found := strings.Cut(assignment, "=")
		if !found {
			continue
		}
		binding, known := environmentBindings[name]
		if !known {
			continue
		}
		if _, duplicate := seen[name]; duplicate {
			return Source{}, false, invalidError(fmt.Errorf("environment contains duplicate %s", name))
		}
		seen[name] = struct{}{}
		node, err := environmentScalar(value, binding.kind)
		if err != nil {
			return Source{}, false, invalidError(fmt.Errorf("%s: %w", name, err))
		}
		setYAMLPath(root, strings.Split(binding.path, "."), node)
		configured = true
	}
	if !configured {
		return Source{}, false, nil
	}

	document := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}
	data, err := yaml.Marshal(document)
	if err != nil {
		return Source{}, false, fmt.Errorf("encode environment configuration: %w", err)
	}

	return BytesSource(SourceEnvironment, "JOBMAN_ environment", data), true, nil
}

// CurrentEnvironmentSource reads documented overrides from os.Environ.
func CurrentEnvironmentSource() (Source, bool, error) {
	return EnvironmentSource(os.Environ())
}

func environmentScalar(value string, kind environmentValueKind) (*yaml.Node, error) {
	if value == "" {
		return nil, errors.New("value must not be empty")
	}
	if value == Unlimited {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: yamlTagString, Value: value}, nil
	}

	switch kind {
	case environmentSlotLimit, environmentIntegerLimit:
		if _, err := strconv.ParseUint(value, 10, 64); err != nil || strings.Trim(value, "0123456789") != "" {
			return nil, fmt.Errorf("value must be an unsigned decimal integer or %q", Unlimited)
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: yamlTagInteger, Value: value}, nil
	case environmentDurationLimit:
		if _, err := ParseDuration(value); err != nil {
			return nil, err
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: yamlTagString, Value: value}, nil
	case environmentByteLimit:
		if strings.Trim(value, "0123456789") == "" {
			if _, err := strconv.ParseUint(value, 10, 64); err != nil {
				return nil, fmt.Errorf("parse byte limit: %w", err)
			}
			return &yaml.Node{Kind: yaml.ScalarNode, Tag: yamlTagInteger, Value: value}, nil
		}
		if _, err := ParseByteSize(value); err != nil {
			return nil, err
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: yamlTagString, Value: value}, nil
	default:
		return nil, errors.New("unsupported environment binding type")
	}
}

func setYAMLPath(mapping *yaml.Node, path []string, value *yaml.Node) {
	current := mapping
	for index, component := range path {
		last := index == len(path)-1
		mappingPosition := mappingIndex(current, component)
		if last {
			if mappingPosition >= 0 {
				current.Content[mappingPosition+1] = value
			} else {
				current.Content = append(current.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Tag: yamlTagString, Value: component}, value,
				)
			}
			return
		}
		if mappingPosition < 0 {
			nested := &yaml.Node{Kind: yaml.MappingNode, Tag: yamlTagMap}
			current.Content = append(current.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: yamlTagString, Value: component}, nested,
			)
			current = nested
			continue
		}
		current = current.Content[mappingPosition+1]
	}
}
