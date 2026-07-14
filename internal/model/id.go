package model

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"sync"
	"time"
)

const (
	uuidTextLength = 36
	uuidByteLength = 16
	maxUUIDMillis  = 1<<48 - 1
)

// JobID uniquely identifies an immutable job submission.
type JobID string

// RunID uniquely identifies one target invocation.
type RunID string

// SupervisorID uniquely identifies one per-job supervisor.
type SupervisorID string

// EventID uniquely identifies one persisted state event.
type EventID string

// UUIDv7Generator creates canonical UUIDv7 identifiers from injected sources.
// Calls are serialized so deterministic readers and clocks are safe to use in
// concurrent tests.
type UUIDv7Generator struct {
	clock  func() time.Time
	random io.Reader
	mu     sync.Mutex
}

// NewUUIDv7Generator constructs an identifier generator.
func NewUUIDv7Generator(clock func() time.Time, random io.Reader) (*UUIDv7Generator, error) {
	if clock == nil {
		return nil, invalid("uuid clock", "must not be nil")
	}
	if random == nil {
		return nil, invalid("uuid random source", "must not be nil")
	}

	return &UUIDv7Generator{clock: clock, random: random}, nil
}

// NewJobID creates a job identifier.
func (generator *UUIDv7Generator) NewJobID() (JobID, error) {
	value, err := generator.newID()

	return JobID(value), err
}

// NewRunID creates a run identifier.
func (generator *UUIDv7Generator) NewRunID() (RunID, error) {
	value, err := generator.newID()

	return RunID(value), err
}

// NewSupervisorID creates a supervisor identifier.
func (generator *UUIDv7Generator) NewSupervisorID() (SupervisorID, error) {
	value, err := generator.newID()

	return SupervisorID(value), err
}

// NewEventID creates an event identifier.
func (generator *UUIDv7Generator) NewEventID() (EventID, error) {
	value, err := generator.newID()

	return EventID(value), err
}

// ParseJobID parses and validates a canonical UUIDv7 job identifier.
func ParseJobID(value string) (JobID, error) {
	if err := validateUUIDv7(value); err != nil {
		return "", fmt.Errorf("parse job ID: %w", err)
	}

	return JobID(value), nil
}

// ParseRunID parses and validates a canonical UUIDv7 run identifier.
func ParseRunID(value string) (RunID, error) {
	if err := validateUUIDv7(value); err != nil {
		return "", fmt.Errorf("parse run ID: %w", err)
	}

	return RunID(value), nil
}

// ParseSupervisorID parses and validates a canonical UUIDv7 supervisor ID.
func ParseSupervisorID(value string) (SupervisorID, error) {
	if err := validateUUIDv7(value); err != nil {
		return "", fmt.Errorf("parse supervisor ID: %w", err)
	}

	return SupervisorID(value), nil
}

// ParseEventID parses and validates a canonical UUIDv7 event identifier.
func ParseEventID(value string) (EventID, error) {
	if err := validateUUIDv7(value); err != nil {
		return "", fmt.Errorf("parse event ID: %w", err)
	}

	return EventID(value), nil
}

// String returns the canonical textual job identifier.
func (id JobID) String() string { return string(id) }

// String returns the canonical textual run identifier.
func (id RunID) String() string { return string(id) }

// String returns the canonical textual supervisor identifier.
func (id SupervisorID) String() string { return string(id) }

// String returns the canonical textual event identifier.
func (id EventID) String() string { return string(id) }

// Valid reports whether the job ID is a canonical UUIDv7.
func (id JobID) Valid() bool { return validateUUIDv7(string(id)) == nil }

// Valid reports whether the run ID is a canonical UUIDv7.
func (id RunID) Valid() bool { return validateUUIDv7(string(id)) == nil }

// Valid reports whether the supervisor ID is a canonical UUIDv7.
func (id SupervisorID) Valid() bool { return validateUUIDv7(string(id)) == nil }

// Valid reports whether the event ID is a canonical UUIDv7.
func (id EventID) Valid() bool { return validateUUIDv7(string(id)) == nil }

func (generator *UUIDv7Generator) newID() (string, error) {
	generator.mu.Lock()
	defer generator.mu.Unlock()

	milliseconds := generator.clock().UnixMilli()
	if milliseconds < 0 || milliseconds > maxUUIDMillis {
		return "", invalid("uuid timestamp", "must fit the 48-bit Unix millisecond field")
	}

	var raw [uuidByteLength]byte
	if _, err := io.ReadFull(generator.random, raw[6:]); err != nil {
		return "", fmt.Errorf("read UUID randomness: %w", err)
	}

	var timestamp [8]byte
	binary.BigEndian.PutUint64(timestamp[:], uint64(milliseconds))
	copy(raw[:6], timestamp[2:])

	raw[6] = raw[6]&0x0f | 0x70
	raw[8] = raw[8]&0x3f | 0x80

	return formatUUID(raw), nil
}

func formatUUID(raw [uuidByteLength]byte) string {
	var text [uuidTextLength]byte
	hex.Encode(text[0:8], raw[0:4])
	text[8] = '-'
	hex.Encode(text[9:13], raw[4:6])
	text[13] = '-'
	hex.Encode(text[14:18], raw[6:8])
	text[18] = '-'
	hex.Encode(text[19:23], raw[8:10])
	text[23] = '-'
	hex.Encode(text[24:36], raw[10:16])

	return string(text[:])
}

func validateUUIDv7(value string) error {
	if len(value) != uuidTextLength {
		return invalid("UUIDv7", "must contain 36 characters")
	}
	if value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return invalid("UUIDv7", "must use canonical hyphen placement")
	}

	var raw [uuidByteLength]byte
	encoded := [5]string{value[0:8], value[9:13], value[14:18], value[19:23], value[24:36]}
	offsets := [5]int{0, 4, 6, 8, 10}
	for index, part := range encoded {
		if !isLowerHex(part) {
			return invalid("UUIDv7", "must use lowercase hexadecimal")
		}
		if _, err := hex.Decode(raw[offsets[index]:], []byte(part)); err != nil {
			return invalid("UUIDv7", "contains a non-hexadecimal character")
		}
	}

	if raw[6]>>4 != 7 {
		return invalid("UUIDv7", "must have version 7")
	}
	if raw[8]>>6 != 2 {
		return invalid("UUIDv7", "must use the RFC 9562 variant")
	}

	return nil
}

func isLowerHex(value string) bool {
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}

	return true
}
