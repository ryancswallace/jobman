package model

import (
	"bytes"
	cryptorand "crypto/rand"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestUUIDv7GeneratorCanonicalLayout(t *testing.T) {
	t.Parallel()

	generator, err := NewUUIDv7Generator(
		func() time.Time { return time.UnixMilli(0x000102030405) },
		bytes.NewReader([]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}),
	)
	if err != nil {
		t.Fatalf("construct generator: %v", err)
	}

	id, err := generator.NewJobID()
	if err != nil {
		t.Fatalf("generate ID: %v", err)
	}

	const expected = JobID("00010203-0405-7001-8203-040506070809")
	if id != expected {
		t.Fatalf("ID = %q, want %q", id, expected)
	}
	if !id.Valid() || id.String() != string(expected) {
		t.Fatalf("generated ID is not valid and canonical: %q", id)
	}
}

func TestUUIDv7GeneratorTypedIDs(t *testing.T) {
	t.Parallel()

	generator, err := NewUUIDv7Generator(
		func() time.Time { return testTime },
		bytes.NewReader(make([]byte, 40)),
	)
	if err != nil {
		t.Fatalf("construct generator: %v", err)
	}

	jobID, jobErr := generator.NewJobID()
	runID, runErr := generator.NewRunID()
	supervisorID, supervisorErr := generator.NewSupervisorID()
	eventID, eventErr := generator.NewEventID()
	for name, candidate := range map[string]struct {
		valid bool
		err   error
	}{
		"job":        {jobID.Valid(), jobErr},
		"run":        {runID.Valid(), runErr},
		"supervisor": {supervisorID.Valid(), supervisorErr},
		"event":      {eventID.Valid(), eventErr},
	} {
		if candidate.err != nil || !candidate.valid {
			t.Errorf("%s ID: valid=%v error=%v", name, candidate.valid, candidate.err)
		}
	}
}

func TestNewUUIDv7GeneratorRejectsNilDependencies(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		clock  func() time.Time
		random io.Reader
	}{
		"clock":  {random: bytes.NewReader(nil)},
		"random": {clock: func() time.Time { return testTime }},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := NewUUIDv7Generator(test.clock, test.random); err == nil {
				t.Fatal("constructor succeeded with a nil dependency")
			}
		})
	}
}

func TestUUIDv7GeneratorErrors(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("random failure")
	tests := map[string]struct {
		clock  func() time.Time
		random io.Reader
		is     error
	}{
		"negative timestamp": {
			clock:  func() time.Time { return time.UnixMilli(-1) },
			random: bytes.NewReader(make([]byte, 10)),
		},
		"timestamp overflow": {
			clock:  func() time.Time { return time.UnixMilli(maxUUIDMillis + 1) },
			random: bytes.NewReader(make([]byte, 10)),
		},
		"random failure": {
			clock:  func() time.Time { return testTime },
			random: errorReader{err: sentinel},
			is:     sentinel,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			generator, err := NewUUIDv7Generator(test.clock, test.random)
			if err != nil {
				t.Fatalf("construct generator: %v", err)
			}
			_, err = generator.NewJobID()
			if err == nil {
				t.Fatal("generation succeeded, want error")
			}
			if test.is != nil && !errors.Is(err, test.is) {
				t.Fatalf("error = %v, want errors.Is(_, %v)", err, test.is)
			}
		})
	}
}

func TestParseTypedIDs(t *testing.T) {
	t.Parallel()

	if parsed, err := ParseJobID(testJobID.String()); err != nil || parsed != testJobID {
		t.Errorf("ParseJobID() = %q, %v", parsed, err)
	}
	if parsed, err := ParseRunID(testRunID.String()); err != nil || parsed != testRunID {
		t.Errorf("ParseRunID() = %q, %v", parsed, err)
	}
	if parsed, err := ParseSupervisorID(testSupervisorID.String()); err != nil || parsed != testSupervisorID {
		t.Errorf("ParseSupervisorID() = %q, %v", parsed, err)
	}
	if parsed, err := ParseEventID(testEventID.String()); err != nil || parsed != testEventID {
		t.Errorf("ParseEventID() = %q, %v", parsed, err)
	}
}

func TestParseIDRejectsNonCanonicalUUIDv7(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"empty":       "",
		"short":       "01890f4e",
		"uppercase":   strings.ToUpper(testJobID.String()),
		"bad hyphens": "01890f4e_4c00-7000-8000-000000000001",
		"bad hex":     "z1890f4e-4c00-7000-8000-000000000001",
		"version":     "01890f4e-4c00-6000-8000-000000000001",
		"variant":     "01890f4e-4c00-7000-4000-000000000001",
	}

	for name, value := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := ParseJobID(value); err == nil {
				t.Fatalf("ParseJobID(%q) succeeded", value)
			}
		})
	}
}

func TestUUIDv7GeneratorConcurrentUniqueness(t *testing.T) {
	t.Parallel()

	generator, err := NewUUIDv7Generator(func() time.Time { return testTime }, cryptorand.Reader)
	if err != nil {
		t.Fatalf("construct generator: %v", err)
	}

	const count = 128
	identifiers := make(chan JobID, count)
	errorsChannel := make(chan error, count)
	var waitGroup sync.WaitGroup
	for range count {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			identifier, generationError := generator.NewJobID()
			identifiers <- identifier
			errorsChannel <- generationError
		}()
	}
	waitGroup.Wait()
	close(identifiers)
	close(errorsChannel)

	seen := make(map[JobID]struct{}, count)
	for generationError := range errorsChannel {
		if generationError != nil {
			t.Fatalf("generate ID: %v", generationError)
		}
	}
	for identifier := range identifiers {
		if _, duplicate := seen[identifier]; duplicate {
			t.Fatalf("duplicate identifier %q", identifier)
		}
		seen[identifier] = struct{}{}
	}
}

type errorReader struct {
	err error
}

func (reader errorReader) Read([]byte) (int, error) {
	return 0, reader.err
}
