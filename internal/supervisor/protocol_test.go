package supervisor

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/ryancswallace/jobman/internal/model"
	"github.com/ryancswallace/jobman/internal/store"
)

const (
	protocolJobID        = model.JobID("01890f4e-4c00-7000-8000-000000000011")
	protocolSupervisorID = model.SupervisorID("01890f4e-4c00-7000-8000-000000000012")
)

func TestDecodeAcknowledgement(t *testing.T) {
	t.Parallel()

	valid := fmt.Sprintf(
		`{"schema_version":1,"job_id":%q,"supervisor_id":%q}`,
		protocolJobID,
		protocolSupervisorID,
	)
	tests := map[string]struct {
		input string
		valid bool
	}{
		"valid": {
			input: valid,
			valid: true,
		},
		"valid with whitespace": {
			input: " \n" + valid + "\t ",
			valid: true,
		},
		"unsupported schema": {
			input: strings.Replace(valid, `"schema_version":1`, `"schema_version":2`, 1),
		},
		"unknown field": {
			input: strings.Replace(valid, `"schema_version":1`, `"schema_version":1,"extra":true`, 1),
		},
		"malformed": {
			input: `{"schema_version":1`,
		},
		"trailing JSON": {
			input: valid + `{}`,
		},
		"invalid job ID": {
			input: strings.Replace(valid, protocolJobID.String(), "invalid", 1),
		},
		"invalid supervisor ID": {
			input: strings.Replace(valid, protocolSupervisorID.String(), "invalid", 1),
		},
		"oversized after valid object": {
			input: valid + strings.Repeat(" ", maximumAckSize+1-len(valid)),
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			reader := &trackingReadCloser{Reader: strings.NewReader(test.input)}
			acknowledgement, err := decodeAcknowledgement(reader)
			if (err == nil) != test.valid {
				t.Fatalf("decodeAcknowledgement() = %#v, %v; valid=%v", acknowledgement, err, test.valid)
			}
			if !reader.closed {
				t.Fatal("decodeAcknowledgement() did not close its reader")
			}
			if test.valid &&
				(acknowledgement.JobID != protocolJobID || acknowledgement.SupervisorID != protocolSupervisorID) {
				t.Fatalf("acknowledgement identity = %#v", acknowledgement)
			}
		})
	}
}

func TestDecodeAcknowledgementReadFailure(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("read failed")
	reader := &trackingReadCloser{Reader: errorReader{err: sentinel}}
	_, err := decodeAcknowledgement(reader)
	if !errors.Is(err, sentinel) {
		t.Fatalf("decodeAcknowledgement() error = %v, want errors.Is(_, %v)", err, sentinel)
	}
	if !reader.closed {
		t.Fatal("decodeAcknowledgement() did not close a failing reader")
	}
}

func TestLaunchValidation(t *testing.T) {
	t.Parallel()

	base := LaunchOptions{
		Store:      new(store.Store),
		Executable: "/jobman",
		StateDir:   "/state",
		JobID:      protocolJobID,
		Credential: bytes.Repeat([]byte{0x42}, credentialSize),
		Timeout:    time.Second,
	}
	tests := map[string]func(*LaunchOptions){
		"nil store":        func(options *LaunchOptions) { options.Store = nil },
		"empty executable": func(options *LaunchOptions) { options.Executable = "" },
		"empty state dir":  func(options *LaunchOptions) { options.StateDir = "" },
		"invalid job ID":   func(options *LaunchOptions) { options.JobID = "invalid" },
		"short credential": func(options *LaunchOptions) { options.Credential = make([]byte, credentialSize-1) },
		"negative timeout": func(options *LaunchOptions) { options.Timeout = -time.Second },
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			options := base
			options.Credential = append([]byte(nil), base.Credential...)
			mutate(&options)
			if _, err := Launch(t.Context(), options); err == nil {
				t.Fatal("Launch() succeeded with invalid options")
			}
		})
	}
}

func TestLaunchRejectsNilContext(t *testing.T) {
	t.Parallel()

	//nolint:staticcheck // This test exercises Launch's explicit defensive nil-context check.
	_, err := Launch(nil, LaunchOptions{Store: new(store.Store)})
	if err == nil || !strings.Contains(err.Error(), "context") {
		t.Fatalf("Launch(nil) error = %v", err)
	}
}

func TestWithSupervisorStateDirReplacesInheritedValue(t *testing.T) {
	t.Parallel()

	inherited := []string{
		"PATH=/bin",
		"JOBMAN_STATE_DIR=/old",
		"jobman_state_dir=/also-old",
		"NO_EQUALS",
	}
	got := withSupervisorStateDir(inherited, "/canonical")
	want := []string{"PATH=/bin", "NO_EQUALS", "JOBMAN_STATE_DIR=/canonical"}
	if !slices.Equal(got, want) {
		t.Fatalf("withSupervisorStateDir() = %#v, want %#v", got, want)
	}
	if inherited[1] != "JOBMAN_STATE_DIR=/old" {
		t.Fatal("withSupervisorStateDir() mutated its input")
	}
}

func TestAcknowledgementJSONContract(t *testing.T) {
	t.Parallel()

	encoded, err := json.Marshal(Acknowledgement{
		SchemaVersion: 1,
		JobID:         protocolJobID,
		SupervisorID:  protocolSupervisorID,
	})
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	const want = `{"schema_version":1,"job_id":"01890f4e-4c00-7000-8000-000000000011",` +
		`"supervisor_id":"01890f4e-4c00-7000-8000-000000000012"}`
	if string(encoded) != want {
		t.Fatalf("acknowledgement JSON = %s, want %s", encoded, want)
	}
}

type trackingReadCloser struct {
	io.Reader
	closed bool
}

func (reader *trackingReadCloser) Close() error {
	reader.closed = true

	return nil
}

type errorReader struct {
	err error
}

func (reader errorReader) Read([]byte) (int, error) {
	return 0, reader.err
}
