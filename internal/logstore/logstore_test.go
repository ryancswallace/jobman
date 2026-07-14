package logstore

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

const testJobID = "019c5f8b-7c8a-7000-8000-000000000001"

func TestRunCapturesRawStreamsAndObservedOrder(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	run, createErr := CreateRun(stateDir, testJobID, 1)
	if createErr != nil {
		t.Fatalf("CreateRun() error = %v", createErr)
	}

	firstTime := time.Date(2026, time.July, 14, 12, 30, 0, 123, time.UTC)
	secondTime := firstTime.Add(time.Millisecond)
	thirdTime := secondTime.Add(time.Millisecond)
	stdoutFirst := []byte{0x00, 0xff, 'a', '\n'}
	stderrData := []byte("error without newline")
	stdoutSecond := []byte{'z', 0x00}

	appendBytes(t, run, Stdout, stdoutFirst, firstTime)
	appendBytes(t, run, Stderr, stderrData, secondTime)
	appendBytes(t, run, Stdout, stdoutSecond, thirdTime)
	if closeErr := run.Close(); closeErr != nil {
		t.Fatalf("Close() error = %v", closeErr)
	}

	reader, openErr := OpenRun(stateDir, testJobID, 1)
	if openErr != nil {
		t.Fatalf("OpenRun() error = %v", openErr)
	}

	assertStreamEquals(t, reader, Stdout, append(bytes.Clone(stdoutFirst), stdoutSecond...))
	assertStreamEquals(t, reader, Stderr, stderrData)

	var combined bytes.Buffer
	written, status, copyErr := reader.CopyCombined(&combined)
	if copyErr != nil {
		t.Fatalf("CopyCombined() error = %v", copyErr)
	}
	wantCombined := append(bytes.Clone(stdoutFirst), stderrData...)
	wantCombined = append(wantCombined, stdoutSecond...)
	if !bytes.Equal(combined.Bytes(), wantCombined) {
		t.Errorf("CopyCombined() = %v, want %v", combined.Bytes(), wantCombined)
	}
	if written != int64(len(wantCombined)) {
		t.Errorf("CopyCombined() count = %d, want %d", written, len(wantCombined))
	}
	if status.TornTail || status.Records != 3 ||
		status.IndexedStdoutBytes != uint64(len(stdoutFirst)+len(stdoutSecond)) ||
		status.IndexedStderrBytes != uint64(len(stderrData)) {
		t.Errorf("CopyCombined() status = %+v", status)
	}

	var chunks []Chunk
	indexStatus, scanErr := reader.ScanIndex(func(chunk Chunk) error {
		chunks = append(chunks, chunk)

		return nil
	})
	if scanErr != nil {
		t.Fatalf("ScanIndex() error = %v", scanErr)
	}
	wantChunks := []Chunk{
		{Sequence: 1, Stream: Stdout, Offset: 0, Length: uint64(len(stdoutFirst)), ObservedAt: firstTime},
		{Sequence: 2, Stream: Stderr, Offset: 0, Length: uint64(len(stderrData)), ObservedAt: secondTime},
		{Sequence: 3, Stream: Stdout, Offset: uint64(len(stdoutFirst)), Length: uint64(len(stdoutSecond)), ObservedAt: thirdTime},
	}
	if !reflect.DeepEqual(chunks, wantChunks) {
		t.Errorf("ScanIndex() chunks = %#v, want %#v", chunks, wantChunks)
	}
	if indexStatus != status {
		t.Errorf("ScanIndex() status = %+v, want %+v", indexStatus, status)
	}
}

func TestWriterPreservesBinaryBytes(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	run, createErr := CreateRun(stateDir, testJobID, 1)
	if createErr != nil {
		t.Fatalf("CreateRun() error = %v", createErr)
	}
	writer, writerErr := run.Writer(Stdout)
	if writerErr != nil {
		t.Fatalf("Writer() error = %v", writerErr)
	}
	data := []byte{0x00, 0x01, 0xfe, 0xff, '\r', '\n'}
	count, writeErr := writer.Write(data)
	if writeErr != nil {
		t.Fatalf("Write() error = %v", writeErr)
	}
	if count != len(data) {
		t.Fatalf("Write() count = %d, want %d", count, len(data))
	}
	if closeErr := run.Close(); closeErr != nil {
		t.Fatalf("Close() error = %v", closeErr)
	}

	reader, openErr := OpenRun(stateDir, testJobID, 1)
	if openErr != nil {
		t.Fatalf("OpenRun() error = %v", openErr)
	}
	assertStreamEquals(t, reader, Stdout, data)
}

func TestIndexReaderToleratesTornFinalRecord(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	run, createErr := CreateRun(stateDir, testJobID, 1)
	if createErr != nil {
		t.Fatalf("CreateRun() error = %v", createErr)
	}
	appendBytes(t, run, Stdout, []byte("complete"), time.Unix(1, 0))
	paths := run.Paths()
	if closeErr := run.Close(); closeErr != nil {
		t.Fatalf("Close() error = %v", closeErr)
	}

	appendFile(t, paths.Index, []byte("partial"))
	reader, openErr := OpenRun(stateDir, testJobID, 1)
	if openErr != nil {
		t.Fatalf("OpenRun() error = %v", openErr)
	}
	status, scanErr := reader.ScanIndex(nil)
	if scanErr != nil {
		t.Fatalf("ScanIndex() error = %v", scanErr)
	}
	if !status.TornTail || status.TornBytes != len("partial") || status.Records != 1 {
		t.Errorf("ScanIndex() status = %+v", status)
	}

	var combined bytes.Buffer
	_, combinedStatus, copyErr := reader.CopyCombined(&combined)
	if copyErr != nil {
		t.Fatalf("CopyCombined() error = %v", copyErr)
	}
	if combined.String() != "complete" {
		t.Errorf("CopyCombined() = %q, want %q", combined.String(), "complete")
	}
	if combinedStatus != status {
		t.Errorf("CopyCombined() status = %+v, want %+v", combinedStatus, status)
	}
}

func TestIndexReaderRejectsChecksumCorruptionBeforeCombinedOutput(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	run, createErr := CreateRun(stateDir, testJobID, 1)
	if createErr != nil {
		t.Fatalf("CreateRun() error = %v", createErr)
	}
	appendBytes(t, run, Stdout, []byte("secret bytes"), time.Unix(1, 0))
	paths := run.Paths()
	if closeErr := run.Close(); closeErr != nil {
		t.Fatalf("Close() error = %v", closeErr)
	}

	corruptByte(t, paths.Index, recordSize-1)
	reader, openErr := OpenRun(stateDir, testJobID, 1)
	if openErr != nil {
		t.Fatalf("OpenRun() error = %v", openErr)
	}
	if _, scanErr := reader.ScanIndex(nil); !errors.Is(scanErr, ErrCorruptIndex) {
		t.Fatalf("ScanIndex() error = %v, want ErrCorruptIndex", scanErr)
	}

	var combined bytes.Buffer
	_, _, copyErr := reader.CopyCombined(&combined)
	if !errors.Is(copyErr, ErrCorruptIndex) {
		t.Fatalf("CopyCombined() error = %v, want ErrCorruptIndex", copyErr)
	}
	if combined.Len() != 0 {
		t.Errorf("CopyCombined() wrote %d bytes before rejecting corrupt index", combined.Len())
	}
}

func TestUnindexedRawTailRemainsAvailableOnlyInIndividualStream(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	run, createErr := CreateRun(stateDir, testJobID, 1)
	if createErr != nil {
		t.Fatalf("CreateRun() error = %v", createErr)
	}
	appendBytes(t, run, Stdout, []byte("indexed"), time.Unix(1, 0))
	paths := run.Paths()
	if closeErr := run.Close(); closeErr != nil {
		t.Fatalf("Close() error = %v", closeErr)
	}
	appendFile(t, paths.Stdout, []byte("-unknown-order"))

	reader, openErr := OpenRun(stateDir, testJobID, 1)
	if openErr != nil {
		t.Fatalf("OpenRun() error = %v", openErr)
	}
	assertStreamEquals(t, reader, Stdout, []byte("indexed-unknown-order"))

	var combined bytes.Buffer
	_, status, copyErr := reader.CopyCombined(&combined)
	if copyErr != nil {
		t.Fatalf("CopyCombined() error = %v", copyErr)
	}
	if combined.String() != "indexed" {
		t.Errorf("CopyCombined() = %q, want indexed prefix", combined.String())
	}
	if status.UnindexedStdoutBytes != uint64(len("-unknown-order")) {
		t.Errorf("CopyCombined() status = %+v", status)
	}
}

func TestConcurrentStreamAppendsHaveContiguousIndex(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	run, createErr := CreateRun(stateDir, testJobID, 1)
	if createErr != nil {
		t.Fatalf("CreateRun() error = %v", createErr)
	}

	const writesPerStream = 12
	start := make(chan struct{})
	var wait sync.WaitGroup
	errorsByStream := make(chan error, 2)
	for _, stream := range []Stream{Stdout, Stderr} {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			for range writesPerStream {
				if _, appendErr := run.Append(stream, []byte{byte(stream)}, time.Unix(1, 0)); appendErr != nil {
					errorsByStream <- appendErr

					return
				}
			}
		}()
	}
	close(start)
	wait.Wait()
	close(errorsByStream)
	for err := range errorsByStream {
		t.Errorf("Append() error = %v", err)
	}
	if closeErr := run.Close(); closeErr != nil {
		t.Fatalf("Close() error = %v", closeErr)
	}

	reader, openErr := OpenRun(stateDir, testJobID, 1)
	if openErr != nil {
		t.Fatalf("OpenRun() error = %v", openErr)
	}
	status, scanErr := reader.ScanIndex(nil)
	if scanErr != nil {
		t.Fatalf("ScanIndex() error = %v", scanErr)
	}
	if status.Records != 2*writesPerStream ||
		status.IndexedStdoutBytes != writesPerStream ||
		status.IndexedStderrBytes != writesPerStream {
		t.Errorf("ScanIndex() status = %+v", status)
	}
}

func TestCreateRunRejectsUnsafeOrDuplicatePaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		jobID     string
		runNumber uint64
	}{
		{name: "empty ID", jobID: "", runNumber: 1},
		{name: "parent traversal", jobID: "../outside", runNumber: 1},
		{name: "slash", jobID: "a/b", runNumber: 1},
		{name: "backslash", jobID: `a\b`, runNumber: 1},
		{name: "uppercase", jobID: "ABC", runNumber: 1},
		{name: "zero run", jobID: testJobID, runNumber: 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := CreateRun(filepath.Join(t.TempDir(), "state"), test.jobID, test.runNumber)
			if !errors.Is(err, ErrUnsafePath) {
				t.Fatalf("CreateRun() error = %v, want ErrUnsafePath", err)
			}
		})
	}

	stateDir := filepath.Join(t.TempDir(), "state")
	run, createErr := CreateRun(stateDir, testJobID, 1)
	if createErr != nil {
		t.Fatalf("first CreateRun() error = %v", createErr)
	}
	if closeErr := run.Close(); closeErr != nil {
		t.Fatalf("Close() error = %v", closeErr)
	}
	if _, duplicateErr := CreateRun(stateDir, testJobID, 1); duplicateErr == nil {
		t.Fatal("second CreateRun() error = nil, want duplicate-directory error")
	}
}

func TestInvalidStreamAndClosedCapture(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	run, createErr := CreateRun(stateDir, testJobID, 1)
	if createErr != nil {
		t.Fatalf("CreateRun() error = %v", createErr)
	}
	if _, appendErr := run.Append(0, []byte("x"), time.Unix(1, 0)); !errors.Is(appendErr, ErrInvalidStream) {
		t.Errorf("Append(invalid) error = %v, want ErrInvalidStream", appendErr)
	}
	if _, writerErr := run.Writer(0); !errors.Is(writerErr, ErrInvalidStream) {
		t.Errorf("Writer(invalid) error = %v, want ErrInvalidStream", writerErr)
	}
	if closeErr := run.Close(); closeErr != nil {
		t.Fatalf("Close() error = %v", closeErr)
	}
	if _, appendErr := run.Append(Stdout, []byte("x"), time.Unix(1, 0)); !errors.Is(appendErr, ErrClosed) {
		t.Errorf("Append(closed) error = %v, want ErrClosed", appendErr)
	}
	if syncErr := run.Sync(); !errors.Is(syncErr, ErrClosed) {
		t.Errorf("Sync(closed) error = %v, want ErrClosed", syncErr)
	}
}

func appendBytes(t *testing.T, run *Run, stream Stream, data []byte, observedAt time.Time) {
	t.Helper()

	written, err := run.Append(stream, data, observedAt)
	if err != nil {
		t.Fatalf("Append(%s) error = %v", stream, err)
	}
	if written != len(data) {
		t.Fatalf("Append(%s) count = %d, want %d", stream, written, len(data))
	}
}

func assertStreamEquals(t *testing.T, reader *Reader, stream Stream, want []byte) {
	t.Helper()

	var output bytes.Buffer
	written, err := reader.CopyStream(&output, stream)
	if err != nil {
		t.Fatalf("CopyStream(%s) error = %v", stream, err)
	}
	if written != int64(len(want)) {
		t.Errorf("CopyStream(%s) count = %d, want %d", stream, written, len(want))
	}
	if !bytes.Equal(output.Bytes(), want) {
		t.Errorf("CopyStream(%s) = %v, want %v", stream, output.Bytes(), want)
	}
}

func appendFile(t *testing.T, path string, data []byte) {
	t.Helper()

	file, openErr := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if openErr != nil {
		t.Fatalf("open %q for append: %v", path, openErr)
	}
	if _, writeErr := file.Write(data); writeErr != nil {
		_ = file.Close()
		t.Fatalf("append %q: %v", path, writeErr)
	}
	if closeErr := file.Close(); closeErr != nil {
		t.Fatalf("close %q: %v", path, closeErr)
	}
}

func corruptByte(t *testing.T, path string, offset int64) {
	t.Helper()

	file, openErr := os.OpenFile(path, os.O_RDWR, 0)
	if openErr != nil {
		t.Fatalf("open %q for corruption: %v", path, openErr)
	}
	var original [1]byte
	if _, readErr := file.ReadAt(original[:], offset); readErr != nil {
		_ = file.Close()
		t.Fatalf("read %q at %d: %v", path, offset, readErr)
	}
	original[0] ^= 0xff
	if _, writeErr := file.WriteAt(original[:], offset); writeErr != nil {
		_ = file.Close()
		t.Fatalf("write %q at %d: %v", path, offset, writeErr)
	}
	if closeErr := file.Close(); closeErr != nil {
		t.Fatalf("close %q: %v", path, closeErr)
	}
}

func TestOpenStream(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	run, createErr := CreateRun(stateDir, testJobID, 1)
	if createErr != nil {
		t.Fatalf("CreateRun() error = %v", createErr)
	}
	appendBytes(t, run, Stderr, []byte("stderr"), time.Unix(1, 0))
	if closeErr := run.Close(); closeErr != nil {
		t.Fatalf("Close() error = %v", closeErr)
	}
	reader, openErr := OpenRun(stateDir, testJobID, 1)
	if openErr != nil {
		t.Fatalf("OpenRun() error = %v", openErr)
	}
	source, streamErr := reader.OpenStream(Stderr)
	if streamErr != nil {
		t.Fatalf("OpenStream() error = %v", streamErr)
	}
	data, readErr := io.ReadAll(source)
	closeErr := source.Close()
	if streamReadErr := errors.Join(readErr, closeErr); streamReadErr != nil {
		t.Fatalf("read stream: %v", streamReadErr)
	}
	if string(data) != "stderr" {
		t.Errorf("OpenStream() data = %q, want stderr", data)
	}
}
