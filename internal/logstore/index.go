package logstore

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"time"
)

const (
	indexVersion1 = 1
	indexVersion2 = 2
	recordSize    = 52

	// IndexVersionUnsegmented is the original single-file format.
	IndexVersionUnsegmented = indexVersion1
	// IndexVersionSegmented records per-stream segment identifiers.
	IndexVersionSegmented = indexVersion2

	// MaxChunkSize bounds a single index record and its corresponding read.
	MaxChunkSize = 16 * 1024 * 1024
)

var (
	indexMagic       = [4]byte{'J', 'M', 'L', 'I'}
	indexChecksumTab = crc32.MakeTable(crc32.Castagnoli)
)

// Chunk describes one observed append to a raw stream.
type Chunk struct {
	ObservedAt time.Time
	Sequence   uint64
	Offset     uint64
	Length     uint64
	Stream     Stream
	// Segment is zero for the original unsegmented version 1 format. Version 2
	// records use positive, per-stream segment numbers beginning at one.
	Segment uint16
}

// IndexStatus describes the valid index prefix and any raw bytes beyond it.
type IndexStatus struct {
	Records              uint64
	ValidIndexBytes      uint64
	IndexedStdoutBytes   uint64
	IndexedStderrBytes   uint64
	UnindexedStdoutBytes uint64
	UnindexedStderrBytes uint64
	TornBytes            int
	TornTail             bool
}

func encodeRecord(chunk Chunk) ([recordSize]byte, error) {
	var record [recordSize]byte
	if err := validateStream(chunk.Stream); err != nil {
		return record, err
	}
	if chunk.Sequence == 0 {
		return record, errors.New("encode log chunk: sequence must be positive")
	}
	if chunk.Length == 0 || chunk.Length > MaxChunkSize {
		return record, fmt.Errorf("encode log chunk: length %d is outside 1..%d", chunk.Length, MaxChunkSize)
	}

	copy(record[0:4], indexMagic[:])
	if chunk.Segment == 0 {
		record[4] = indexVersion1
	} else {
		record[4] = indexVersion2
		binary.LittleEndian.PutUint16(record[6:8], chunk.Segment)
	}
	record[5] = byte(chunk.Stream)
	binary.LittleEndian.PutUint64(record[8:16], chunk.Sequence)
	binary.LittleEndian.PutUint64(record[16:24], chunk.Offset)
	binary.LittleEndian.PutUint64(record[24:32], chunk.Length)
	if _, err := binary.Encode(record[32:40], binary.LittleEndian, chunk.ObservedAt.Unix()); err != nil {
		return record, fmt.Errorf("encode log chunk timestamp seconds: %w", err)
	}
	nanosecond := int64(chunk.ObservedAt.Nanosecond())
	if _, err := binary.Encode(record[40:48], binary.LittleEndian, nanosecond); err != nil {
		return record, fmt.Errorf("encode log chunk timestamp nanoseconds: %w", err)
	}
	binary.LittleEndian.PutUint32(record[48:52], crc32.Checksum(record[:48], indexChecksumTab))

	return record, nil
}

func decodeRecord(record [recordSize]byte) (Chunk, error) {
	if !bytes.Equal(record[0:4], indexMagic[:]) {
		return Chunk{}, fmt.Errorf("%w: invalid record magic", ErrCorruptIndex)
	}
	checksum := binary.LittleEndian.Uint32(record[48:52])
	if checksum != crc32.Checksum(record[:48], indexChecksumTab) {
		return Chunk{}, fmt.Errorf("%w: record checksum mismatch", ErrCorruptIndex)
	}
	segment, err := decodeSegment(record)
	if err != nil {
		return Chunk{}, err
	}

	stream := Stream(record[5])
	if streamErr := validateStream(stream); streamErr != nil {
		return Chunk{}, fmt.Errorf("%w: invalid stream: %w", ErrCorruptIndex, streamErr)
	}
	length := binary.LittleEndian.Uint64(record[24:32])
	if length == 0 || length > MaxChunkSize {
		return Chunk{}, fmt.Errorf("%w: invalid chunk length %d", ErrCorruptIndex, length)
	}
	observedAt, err := decodeTimestamp(record)
	if err != nil {
		return Chunk{}, err
	}

	return Chunk{
		Sequence:   binary.LittleEndian.Uint64(record[8:16]),
		Stream:     stream,
		Offset:     binary.LittleEndian.Uint64(record[16:24]),
		Length:     length,
		ObservedAt: observedAt,
		Segment:    segment,
	}, nil
}

func decodeSegment(record [recordSize]byte) (uint16, error) {
	segment := binary.LittleEndian.Uint16(record[6:8])
	switch record[4] {
	case indexVersion1:
		if segment != 0 {
			return 0, fmt.Errorf("%w: reserved record bits are set", ErrCorruptIndex)
		}
	case indexVersion2:
		if segment == 0 {
			return 0, fmt.Errorf("%w: version 2 record has zero segment", ErrCorruptIndex)
		}
	default:
		return 0, fmt.Errorf("%w: unsupported record version %d", ErrCorruptIndex, record[4])
	}

	return segment, nil
}

func decodeTimestamp(record [recordSize]byte) (time.Time, error) {
	var seconds int64
	if _, err := binary.Decode(record[32:40], binary.LittleEndian, &seconds); err != nil {
		return time.Time{}, fmt.Errorf("%w: decode timestamp seconds: %w", ErrCorruptIndex, err)
	}
	var nanosecond int64
	if _, err := binary.Decode(record[40:48], binary.LittleEndian, &nanosecond); err != nil {
		return time.Time{}, fmt.Errorf("%w: decode timestamp nanoseconds: %w", ErrCorruptIndex, err)
	}
	if nanosecond < 0 || nanosecond >= int64(time.Second) {
		return time.Time{}, fmt.Errorf("%w: invalid timestamp nanoseconds %d", ErrCorruptIndex, nanosecond)
	}

	return time.Unix(seconds, nanosecond).UTC(), nil
}

type indexState struct {
	status       IndexStatus
	nextSequence uint64
	segmented    bool
	legacy       bool
	segments     [3]uint16
	expected     [3]uint64
}

func scanIndex(reader io.Reader, visit func(Chunk) error) (IndexStatus, error) {
	state := indexState{nextSequence: 1}

	return scanIndexFrom(reader, &state, visit)
}

func scanIndexFrom(reader io.Reader, state *indexState, visit func(Chunk) error) (IndexStatus, error) {
	state.status.TornBytes = 0
	state.status.TornTail = false
	for {
		var encoded [recordSize]byte
		count, err := io.ReadFull(reader, encoded[:])
		if errors.Is(err, io.EOF) {
			return state.status, nil
		}
		if errors.Is(err, io.ErrUnexpectedEOF) {
			state.status.TornTail = true
			state.status.TornBytes = count

			return state.status, nil
		}
		if err != nil {
			return state.status, fmt.Errorf("read log chunk index: %w", err)
		}

		chunk, err := decodeRecord(encoded)
		if err != nil {
			return state.status, err
		}
		if err := state.accept(chunk); err != nil {
			return state.status, err
		}
		if visit != nil {
			if err := visit(chunk); err != nil {
				return state.status, err
			}
		}
	}
}

func (state *indexState) accept(chunk Chunk) error {
	if chunk.Sequence != state.nextSequence {
		return fmt.Errorf("%w: sequence %d follows %d", ErrCorruptIndex, chunk.Sequence, state.nextSequence-1)
	}

	streamIndex := int(chunk.Stream)
	expectedOffset := &state.expected[streamIndex]
	currentSegment := &state.segments[streamIndex]
	if err := state.acceptSegment(chunk, currentSegment, expectedOffset); err != nil {
		return err
	}
	if chunk.Offset != *expectedOffset {
		return fmt.Errorf(
			"%w: %s offset %d does not follow %d",
			ErrCorruptIndex,
			chunk.Stream,
			chunk.Offset,
			*expectedOffset,
		)
	}
	if chunk.Offset > ^uint64(0)-chunk.Length {
		return fmt.Errorf("%w: %s offset overflows", ErrCorruptIndex, chunk.Stream)
	}

	*expectedOffset += chunk.Length
	state.nextSequence++
	state.status.Records++
	state.status.ValidIndexBytes += recordSize
	indexedBytes := &state.status.IndexedStdoutBytes
	if chunk.Stream == Stderr {
		indexedBytes = &state.status.IndexedStderrBytes
	}
	if *indexedBytes > ^uint64(0)-chunk.Length {
		return fmt.Errorf("%w: %s indexed byte count overflows", ErrCorruptIndex, chunk.Stream)
	}
	*indexedBytes += chunk.Length

	return nil
}

func (state *indexState) acceptSegment(chunk Chunk, currentSegment *uint16, expectedOffset *uint64) error {
	if chunk.Segment == 0 {
		if state.segmented {
			return fmt.Errorf("%w: version 1 record follows segmented records", ErrCorruptIndex)
		}
		state.legacy = true

		return nil
	}
	if state.legacy {
		return fmt.Errorf("%w: segmented record follows version 1 records", ErrCorruptIndex)
	}
	state.segmented = true

	switch {
	case *currentSegment == 0 && chunk.Segment != 1:
		return fmt.Errorf("%w: %s starts at segment %d", ErrCorruptIndex, chunk.Stream, chunk.Segment)
	case *currentSegment == 0:
		*currentSegment = 1
	case chunk.Segment == *currentSegment:
		return nil
	case *currentSegment != ^uint16(0) && chunk.Segment == *currentSegment+1:
		*currentSegment = chunk.Segment
		*expectedOffset = 0
	default:
		return fmt.Errorf(
			"%w: %s segment %d does not follow %d",
			ErrCorruptIndex,
			chunk.Stream,
			chunk.Segment,
			*currentSegment,
		)
	}

	return nil
}
