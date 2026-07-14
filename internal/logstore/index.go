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
	indexVersion = 1
	recordSize   = 52

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
	record[4] = indexVersion
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
	if record[4] != indexVersion {
		return Chunk{}, fmt.Errorf("%w: unsupported record version %d", ErrCorruptIndex, record[4])
	}
	if record[6] != 0 || record[7] != 0 {
		return Chunk{}, fmt.Errorf("%w: reserved record bits are set", ErrCorruptIndex)
	}
	checksum := binary.LittleEndian.Uint32(record[48:52])
	if checksum != crc32.Checksum(record[:48], indexChecksumTab) {
		return Chunk{}, fmt.Errorf("%w: record checksum mismatch", ErrCorruptIndex)
	}

	stream := Stream(record[5])
	if err := validateStream(stream); err != nil {
		return Chunk{}, fmt.Errorf("%w: invalid stream: %w", ErrCorruptIndex, err)
	}
	length := binary.LittleEndian.Uint64(record[24:32])
	if length == 0 || length > MaxChunkSize {
		return Chunk{}, fmt.Errorf("%w: invalid chunk length %d", ErrCorruptIndex, length)
	}
	var seconds int64
	if _, err := binary.Decode(record[32:40], binary.LittleEndian, &seconds); err != nil {
		return Chunk{}, fmt.Errorf("%w: decode timestamp seconds: %w", ErrCorruptIndex, err)
	}
	var nanosecond int64
	if _, err := binary.Decode(record[40:48], binary.LittleEndian, &nanosecond); err != nil {
		return Chunk{}, fmt.Errorf("%w: decode timestamp nanoseconds: %w", ErrCorruptIndex, err)
	}
	if nanosecond < 0 || nanosecond >= int64(time.Second) {
		return Chunk{}, fmt.Errorf("%w: invalid timestamp nanoseconds %d", ErrCorruptIndex, nanosecond)
	}

	return Chunk{
		Sequence:   binary.LittleEndian.Uint64(record[8:16]),
		Stream:     stream,
		Offset:     binary.LittleEndian.Uint64(record[16:24]),
		Length:     length,
		ObservedAt: time.Unix(seconds, nanosecond).UTC(),
	}, nil
}

type indexState struct {
	status         IndexStatus
	nextSequence   uint64
	stdoutExpected uint64
	stderrExpected uint64
}

func scanIndex(reader io.Reader, visit func(Chunk) error) (IndexStatus, error) {
	state := indexState{nextSequence: 1}
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

	expectedOffset := &state.stdoutExpected
	if chunk.Stream == Stderr {
		expectedOffset = &state.stderrExpected
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
	if chunk.Stream == Stdout {
		state.status.IndexedStdoutBytes = *expectedOffset
	} else {
		state.status.IndexedStderrBytes = *expectedOffset
	}

	return nil
}
