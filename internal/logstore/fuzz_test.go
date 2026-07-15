package logstore

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const (
	maxFuzzIndexBytes = 32 * recordSize
	maxFuzzStreamSize = 64 * 1024
)

func FuzzScanIndexSafety(fuzz *testing.F) {
	valid := indexFixture(fuzz,
		Chunk{Sequence: 1, Stream: Stdout, Offset: 0, Length: 3, ObservedAt: time.Unix(1, 2)},
		Chunk{Sequence: 2, Stream: Stderr, Offset: 0, Length: 3, ObservedAt: time.Unix(3, 4)},
	)
	torn := append(bytes.Clone(valid), []byte("torn")...)
	corrupt := bytes.Clone(valid)
	corrupt[recordSize-1] ^= 0xff
	segmented := indexFixture(fuzz,
		Chunk{Sequence: 1, Stream: Stdout, Segment: 1, Offset: 0, Length: 3, ObservedAt: time.Unix(1, 2)},
		Chunk{Sequence: 2, Stream: Stdout, Segment: 2, Offset: 0, Length: 3, ObservedAt: time.Unix(3, 4)},
	)
	for _, seed := range [][]byte{{}, valid, torn, corrupt, segmented, []byte("short record")} {
		fuzz.Add(seed)
	}

	fuzz.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxFuzzIndexBytes {
			return
		}

		var visited []Chunk
		status, err := scanIndex(bytes.NewReader(data), func(chunk Chunk) error {
			visited = append(visited, chunk)

			return nil
		})
		if status.Records != uint64(len(visited)) {
			t.Fatalf("ScanIndex() records = %d, visited %d chunks", status.Records, len(visited))
		}
		if status.ValidIndexBytes != status.Records*recordSize {
			t.Fatalf("ScanIndex() valid bytes = %d, want %d", status.ValidIndexBytes, status.Records*recordSize)
		}
		if status.ValidIndexBytes > uint64(len(data)) {
			t.Fatalf("ScanIndex() valid bytes = %d, input length %d", status.ValidIndexBytes, len(data))
		}
		if err != nil {
			if !errors.Is(err, ErrCorruptIndex) {
				t.Fatalf("ScanIndex() error = %v, want ErrCorruptIndex", err)
			}
			if status.TornTail || status.TornBytes != 0 {
				t.Fatalf("corrupt complete record reported as torn tail: %+v", status)
			}

			return
		}

		tornBytes, conversionErr := boundedIntToUint64(status.TornBytes)
		if conversionErr != nil {
			t.Fatalf("convert torn-byte count: %v", conversionErr)
		}
		accounted := status.ValidIndexBytes + tornBytes
		if accounted != uint64(len(data)) {
			t.Fatalf("ScanIndex() accounted for %d bytes, input has %d", accounted, len(data))
		}
		if status.TornTail != (status.TornBytes != 0) {
			t.Fatalf("ScanIndex() has inconsistent torn-tail status: %+v", status)
		}
	})
}

func FuzzReaderIndexSafety(fuzz *testing.F) {
	stdoutRecord := indexFixture(fuzz,
		Chunk{Sequence: 1, Stream: Stdout, Offset: 0, Length: 3, ObservedAt: time.Unix(1, 2)},
	)
	twoStreams := indexFixture(fuzz,
		Chunk{Sequence: 1, Stream: Stdout, Offset: 0, Length: 3, ObservedAt: time.Unix(1, 2)},
		Chunk{Sequence: 2, Stream: Stderr, Offset: 0, Length: 3, ObservedAt: time.Unix(3, 4)},
	)
	torn := append(bytes.Clone(stdoutRecord), []byte("partial")...)
	corrupt := bytes.Clone(stdoutRecord)
	corrupt[recordSize-1] ^= 0xff
	fuzz.Add([]byte{}, []byte{}, []byte{})
	fuzz.Add(stdoutRecord, []byte("out"), []byte{})
	fuzz.Add(twoStreams, []byte("out"), []byte("err"))
	fuzz.Add(torn, []byte("out-tail"), []byte{})
	fuzz.Add(corrupt, []byte("out"), []byte{})
	fuzz.Add(stdoutRecord, []byte("x"), []byte{})

	fuzz.Fuzz(func(t *testing.T, indexData, stdoutData, stderrData []byte) {
		if len(indexData) > maxFuzzIndexBytes ||
			len(stdoutData) > maxFuzzStreamSize ||
			len(stderrData) > maxFuzzStreamSize {
			return
		}

		stateDir := filepath.Join(t.TempDir(), "state")
		run, err := CreateRun(stateDir, testJobID, 1)
		if err != nil {
			t.Fatalf("CreateRun() error = %v", err)
		}
		paths := run.Paths()
		if closeErr := run.Close(); closeErr != nil {
			t.Fatalf("Close() error = %v", closeErr)
		}
		writeFuzzFixture(t, paths.Index, indexData)
		writeFuzzFixture(t, paths.Stdout, stdoutData)
		writeFuzzFixture(t, paths.Stderr, stderrData)

		reader, err := OpenRun(stateDir, testJobID, 1)
		if err != nil {
			t.Fatalf("OpenRun() error = %v", err)
		}
		var chunks []Chunk
		status, scanErr := reader.ScanIndex(func(chunk Chunk) error {
			chunks = append(chunks, chunk)

			return nil
		})

		var combined bytes.Buffer
		written, combinedStatus, combinedErr := reader.CopyCombined(&combined)
		if written != int64(combined.Len()) {
			t.Fatalf("CopyCombined() wrote %d bytes but destination contains %d", written, combined.Len())
		}
		if scanErr != nil {
			if !errors.Is(scanErr, ErrCorruptIndex) {
				t.Fatalf("ScanIndex() error = %v, want ErrCorruptIndex", scanErr)
			}
			if !errors.Is(combinedErr, ErrCorruptIndex) {
				t.Fatalf("CopyCombined() error = %v, want ErrCorruptIndex", combinedErr)
			}
			if combined.Len() != 0 {
				t.Fatalf("CopyCombined() emitted %d bytes before validating corrupt input", combined.Len())
			}

			return
		}

		if combinedErr != nil {
			t.Fatalf("CopyCombined() error = %v after successful ScanIndex()", combinedErr)
		}
		if combinedStatus != status {
			t.Fatalf("CopyCombined() status = %+v, want %+v", combinedStatus, status)
		}
		if status.IndexedStdoutBytes+status.UnindexedStdoutBytes != uint64(len(stdoutData)) ||
			status.IndexedStderrBytes+status.UnindexedStderrBytes != uint64(len(stderrData)) {
			t.Fatalf("ScanIndex() raw-byte accounting is inconsistent: %+v", status)
		}

		var expected bytes.Buffer
		for _, chunk := range chunks {
			source := stdoutData
			if chunk.Stream == Stderr {
				source = stderrData
			}
			offset, conversionErr := uint64ToInt64(chunk.Offset)
			if conversionErr != nil {
				t.Fatalf("convert accepted chunk offset: %v", conversionErr)
			}
			length, conversionErr := uint64ToInt64(chunk.Length)
			if conversionErr != nil {
				t.Fatalf("convert accepted chunk length: %v", conversionErr)
			}
			if _, copyErr := io.Copy(&expected, io.NewSectionReader(bytes.NewReader(source), offset, length)); copyErr != nil {
				t.Fatalf("construct expected combined log: %v", copyErr)
			}
		}
		if !bytes.Equal(combined.Bytes(), expected.Bytes()) {
			t.Fatalf("CopyCombined() = %v, want %v", combined.Bytes(), expected.Bytes())
		}
	})
}

func indexFixture(tb testing.TB, chunks ...Chunk) []byte {
	tb.Helper()

	result := make([]byte, 0, len(chunks)*recordSize)
	for _, chunk := range chunks {
		record, err := encodeRecord(chunk)
		if err != nil {
			tb.Fatalf("encode index fixture: %v", err)
		}
		result = append(result, record[:]...)
	}

	return result
}

func writeFuzzFixture(t *testing.T, path string, data []byte) {
	t.Helper()

	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write fuzz fixture %q: %v", path, err)
	}
}
