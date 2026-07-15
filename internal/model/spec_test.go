package model

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ryancswallace/jobman/internal/policy"
)

func TestNewJobSpecCanonicalizesAndCopiesInput(t *testing.T) {
	t.Parallel()

	arguments := []string{"hello", ""}
	environment := map[string]string{"B": "2", "A": "1"}
	unset := []string{"D", "C", "C"}
	input := JobSpecInput{
		Executable:       "echo",
		Arguments:        arguments,
		WorkingDirectory: filepath.Join(string(filepath.Separator), "tmp", "parent", "..", "work"),
		Environment:      environment,
		UnsetEnvironment: unset,
		Name:             "test",
		StopPolicy: StopPolicy{
			GracePeriod:     5 * time.Second,
			ForceAfterGrace: true,
		},
	}

	specification, err := NewJobSpec(input)
	if err != nil {
		t.Fatalf("NewJobSpec() error = %v", err)
	}
	arguments[0] = "mutated"
	environment["A"] = "mutated"
	unset[0] = "MUTATED"

	if got := specification.Arguments(); !reflect.DeepEqual(got, []string{"hello", ""}) {
		t.Fatalf("Arguments() = %#v", got)
	}
	if got := specification.Environment(); !reflect.DeepEqual(got, map[string]string{"A": "1", "B": "2"}) {
		t.Fatalf("Environment() = %#v", got)
	}
	if got := specification.UnsetEnvironment(); !reflect.DeepEqual(got, []string{"C", "D"}) {
		t.Fatalf("UnsetEnvironment() = %#v", got)
	}
	if got, want := specification.WorkingDirectory(), filepath.Join(string(filepath.Separator), "tmp", "work"); got != want {
		t.Fatalf("WorkingDirectory() = %q, want %q", got, want)
	}
	if specification.EnvironmentInheritance() != EnvironmentInheritSubmission {
		t.Fatalf("EnvironmentInheritance() = %q", specification.EnvironmentInheritance())
	}
	if specification.StdinPolicy() != StdinNull {
		t.Fatalf("StdinPolicy() = %q", specification.StdinPolicy())
	}

	returnedArguments := specification.Arguments()
	returnedArguments[0] = "changed"
	returnedEnvironment := specification.Environment()
	returnedEnvironment["A"] = "changed"
	if specification.Arguments()[0] != "hello" || specification.Environment()["A"] != "1" {
		t.Fatal("caller mutated JobSpec through a getter")
	}
}

func TestJobSpecCanonicalJSON(t *testing.T) {
	t.Parallel()

	workingDirectory := filepath.Join(string(filepath.Separator), "tmp", "work")
	specification, err := NewJobSpec(JobSpecInput{
		Executable:       "echo",
		Arguments:        []string{"hello", ""},
		WorkingDirectory: workingDirectory,
		Environment:      map[string]string{"B": "2", "A": "1"},
		UnsetEnvironment: []string{"C"},
		Name:             "test",
		StopPolicy: StopPolicy{
			GracePeriod:     5 * time.Second,
			ForceAfterGrace: true,
		},
	})
	if err != nil {
		t.Fatalf("NewJobSpec() error = %v", err)
	}

	encoded, err := specification.CanonicalJSON()
	if err != nil {
		t.Fatalf("CanonicalJSON() error = %v", err)
	}
	wantPrefix := `{"schema_version":2,"executable":"echo","arguments":["hello",""],` +
		`"working_directory":` + strconv.Quote(workingDirectory) + `,"environment":{"inheritance":"submission",` +
		`"set":{"A":"1","B":"2"},"unset":["C"]},"name":"test",` +
		`"stop_policy":{"grace_period":"5s","force_after_grace":true},"stdin_policy":"null",` +
		`"execution_policy":`
	if !strings.HasPrefix(string(encoded), wantPrefix) {
		t.Fatalf("CanonicalJSON() = %s\nwant prefix = %s", encoded, wantPrefix)
	}

	parsed, err := ParseJobSpecJSON(encoded)
	if err != nil {
		t.Fatalf("ParseJobSpecJSON() error = %v", err)
	}
	reencoded, err := json.Marshal(parsed)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if !bytes.Equal(reencoded, encoded) {
		t.Fatalf("round trip = %s\nwant = %s", reencoded, encoded)
	}
	if parsed.SchemaVersion() != JobSpecSchemaVersion || parsed.Executable() != "echo" || parsed.Name() != "test" {
		t.Fatalf("parsed getters returned unexpected values: %#v", parsed)
	}
}

func TestJobSpecCanonicalJSONPreservesEmptyCollections(t *testing.T) {
	t.Parallel()

	specification, err := NewJobSpec(JobSpecInput{
		Executable:       "true",
		WorkingDirectory: filepath.Join(string(filepath.Separator), "tmp"),
	})
	if err != nil {
		t.Fatalf("NewJobSpec() error = %v", err)
	}
	encoded, err := specification.CanonicalJSON()
	if err != nil {
		t.Fatalf("CanonicalJSON() error = %v", err)
	}
	if !bytes.Contains(encoded, []byte(`"arguments":[]`)) ||
		!bytes.Contains(encoded, []byte(`"set":{}`)) ||
		!bytes.Contains(encoded, []byte(`"unset":[]`)) {
		t.Fatalf("CanonicalJSON() encoded absent collections as null: %s", encoded)
	}
	if _, err := ParseJobSpecJSON(encoded); err != nil {
		t.Fatalf("ParseJobSpecJSON(CanonicalJSON()) error = %v", err)
	}
}

func TestJobSpecCanonicalJSONPreservesRetryableExitCodeDefault(t *testing.T) {
	t.Parallel()

	base := JobSpecInput{Executable: "true", WorkingDirectory: filepath.Join(string(filepath.Separator), "tmp")}
	defaultSpec, err := NewJobSpec(base)
	if err != nil {
		t.Fatalf("NewJobSpec(default) error = %v", err)
	}
	encoded, err := defaultSpec.CanonicalJSON()
	if err != nil {
		t.Fatalf("CanonicalJSON(default) error = %v", err)
	}
	parsed, err := ParseJobSpecJSON(encoded)
	if err != nil {
		t.Fatalf("ParseJobSpecJSON(default) error = %v", err)
	}
	if parsed.ExecutionPolicy().Classification.RetryableExitCodes != nil {
		t.Fatalf("default retryable exit codes = %#v, want nil default", parsed.ExecutionPolicy().Classification.RetryableExitCodes)
	}

	explicitPolicy := DefaultExecutionPolicy()
	explicitPolicy.Classification.RetryableExitCodes = []policy.ExitCodeRange{}
	base.ExecutionPolicy = explicitPolicy
	explicitSpec, err := NewJobSpec(base)
	if err != nil {
		t.Fatalf("NewJobSpec(explicit empty) error = %v", err)
	}
	encoded, err = explicitSpec.CanonicalJSON()
	if err != nil {
		t.Fatalf("CanonicalJSON(explicit empty) error = %v", err)
	}
	parsed, err = ParseJobSpecJSON(encoded)
	if err != nil {
		t.Fatalf("ParseJobSpecJSON(explicit empty) error = %v", err)
	}
	if parsed.ExecutionPolicy().Classification.RetryableExitCodes == nil ||
		len(parsed.ExecutionPolicy().Classification.RetryableExitCodes) != 0 {
		t.Fatalf("explicit retryable exit codes = %#v, want non-nil empty slice", parsed.ExecutionPolicy().Classification.RetryableExitCodes)
	}
}

func TestNewJobSpecValidation(t *testing.T) {
	t.Parallel()

	base := JobSpecInput{
		Executable:       "example",
		WorkingDirectory: filepath.Join(string(filepath.Separator), "tmp"),
	}
	tests := map[string]func(*JobSpecInput){
		"empty executable": func(input *JobSpecInput) { input.Executable = "" },
		"executable NUL":   func(input *JobSpecInput) { input.Executable = "bad\x00name" },
		"argument NUL": func(input *JobSpecInput) {
			input.Arguments = []string{"bad\x00argument"}
		},
		"relative working directory": func(input *JobSpecInput) { input.WorkingDirectory = "relative" },
		"environment name": func(input *JobSpecInput) {
			input.Environment = map[string]string{"1INVALID": "value"}
		},
		"environment value NUL": func(input *JobSpecInput) {
			input.Environment = map[string]string{"VALID": "bad\x00value"}
		},
		"set and unset overlap": func(input *JobSpecInput) {
			input.Environment = map[string]string{"DUPLICATE": "value"}
			input.UnsetEnvironment = []string{"DUPLICATE"}
		},
		"unset environment name": func(input *JobSpecInput) {
			input.UnsetEnvironment = []string{"BAD-NAME"}
		},
		"blank name":      func(input *JobSpecInput) { input.Name = " \t " },
		"control in name": func(input *JobSpecInput) { input.Name = "name\nnext" },
		"negative grace": func(input *JobSpecInput) {
			input.StopPolicy.GracePeriod = -time.Second
		},
		"environment inheritance": func(input *JobSpecInput) {
			input.EnvironmentInheritance = "unknown"
		},
		"stdin policy": func(input *JobSpecInput) { input.StdinPolicy = "unknown" },
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			input := base
			mutate(&input)
			_, err := NewJobSpec(input)
			if err == nil {
				t.Fatal("NewJobSpec() succeeded")
			}
			var validation *ValidationError
			if !errors.As(err, &validation) {
				t.Fatalf("error = %T %v, want ValidationError", err, err)
			}
		})
	}
}

func TestParseJobSpecJSONStrictness(t *testing.T) {
	t.Parallel()

	valid, err := validSpec(t).CanonicalJSON()
	if err != nil {
		t.Fatalf("encode valid specification: %v", err)
	}

	tests := map[string]string{
		"unknown field": strings.Replace(
			string(valid),
			`"schema_version":2`,
			`"schema_version":2,"unknown":true`,
			1,
		),
		"duplicate field": strings.Replace(
			string(valid),
			`"schema_version":2`,
			`"schema_version":2,"schema_version":2`,
			1,
		),
		"wrong version":     strings.Replace(string(valid), `"schema_version":2`, `"schema_version":99`, 1),
		"missing arguments": strings.Replace(string(valid), `"arguments":["first","second value"],`, "", 1),
		"null environment set": strings.Replace(
			string(valid),
			`"set":{"JOBMAN_TEST":"true"}`,
			`"set":null`,
			1,
		),
		"missing stdin policy": strings.Replace(string(valid), `,"stdin_policy":"null"`, "", 1),
		"bad duration":         strings.Replace(string(valid), `"grace_period":"5s"`, `"grace_period":"soon"`, 1),
		"trailing value":       string(valid) + `{}`,
		"empty":                "",
	}

	for name, input := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := ParseJobSpecJSON([]byte(input)); err == nil {
				t.Fatalf("ParseJobSpecJSON(%q) succeeded", input)
			}
		})
	}
}

func TestJobSpecUnmarshalJSONDoesNotMutateOnFailure(t *testing.T) {
	t.Parallel()

	specification := validSpec(t)
	original, err := specification.CanonicalJSON()
	if err != nil {
		t.Fatalf("encode original: %v", err)
	}
	unmarshalError := json.Unmarshal([]byte(`{"schema_version":99}`), &specification)
	if unmarshalError == nil {
		t.Fatal("json.Unmarshal() succeeded")
	}
	after, err := specification.CanonicalJSON()
	if err != nil {
		t.Fatalf("encode specification after failure: %v", err)
	}
	if !bytes.Equal(after, original) {
		t.Fatalf("failed unmarshal mutated specification: %s != %s", after, original)
	}
}

func FuzzParseJobSpecJSON(fuzz *testing.F) {
	valid, err := validSpec(fuzz).CanonicalJSON()
	if err != nil {
		fuzz.Fatalf("encode seed specification: %v", err)
	}
	seeds := [][]byte{
		valid,
		{},
		[]byte(`null`),
		[]byte(`{"schema_version":1}`),
		[]byte(`{"schema_version":1,"schema_version":1}`),
		[]byte(`{"outer":{"key":1,"key":2}}`),
		append(bytes.Clone(valid), []byte(` {}`)...),
		{0xff, 0xfe, 0xfd},
	}
	for _, seed := range seeds {
		fuzz.Add(seed)
	}

	fuzz.Fuzz(func(t *testing.T, data []byte) {
		specification, parseError := ParseJobSpecJSON(data)
		if parseError != nil {
			return
		}
		encoded, encodeError := specification.CanonicalJSON()
		if encodeError != nil {
			t.Fatalf("accepted specification cannot be encoded: %v", encodeError)
		}
		if !json.Valid(encoded) {
			t.Fatalf("CanonicalJSON() returned invalid JSON: %q", encoded)
		}
		roundTripped, secondParseError := ParseJobSpecJSON(encoded)
		if secondParseError != nil {
			t.Fatalf("canonical specification cannot be parsed: %v", secondParseError)
		}
		reencoded, secondEncodeError := roundTripped.CanonicalJSON()
		if secondEncodeError != nil {
			t.Fatalf("round-tripped specification cannot be encoded: %v", secondEncodeError)
		}
		if !bytes.Equal(encoded, reencoded) {
			t.Fatalf("canonical encoding is unstable: %q != %q", encoded, reencoded)
		}
	})
}
