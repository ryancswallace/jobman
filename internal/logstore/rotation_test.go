package logstore

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestRotatedRunPreservesRawStreamsAndCombinedOrder(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	run, err := CreateRunWithOptions(stateDir, testJobID, 1, RunOptions{
		Rotation: RotationPolicy{SegmentBytes: 4, MaxSegmentsPerStream: 3},
	})
	if err != nil {
		t.Fatalf("CreateRunWithOptions() error = %v", err)
	}
	if run.IndexVersion() != IndexVersionSegmented {
		t.Fatalf("IndexVersion() = %d, want %d", run.IndexVersion(), IndexVersionSegmented)
	}
	if _, statErr := os.Stat(run.Paths().Active); statErr != nil {
		t.Fatalf("active marker while capture is open: %v", statErr)
	}

	observed := time.Date(2026, time.July, 14, 13, 0, 0, 0, time.UTC)
	appendBytes(t, run, Stdout, []byte("abcdefghij"), observed)
	appendBytes(t, run, Stderr, []byte("ERR"), observed.Add(time.Millisecond))
	paths := run.Paths()
	if closeErr := run.Close(); closeErr != nil {
		t.Fatalf("Close() error = %v", closeErr)
	}
	if _, statErr := os.Stat(paths.Active); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("active marker after Close() error = %v, want not exist", statErr)
	}

	assertFileSize(t, paths.Stdout, 4)
	assertFileSize(t, segmentPath(paths, Stdout, 2), 4)
	assertFileSize(t, segmentPath(paths, Stdout, 3), 2)

	reader, err := OpenRun(stateDir, testJobID, 1)
	if err != nil {
		t.Fatalf("OpenRun() error = %v", err)
	}
	assertStreamEquals(t, reader, Stdout, []byte("abcdefghij"))
	assertStreamEquals(t, reader, Stderr, []byte("ERR"))
	stdoutSize, sizeErr := reader.StreamSize(Stdout)
	if sizeErr != nil || stdoutSize != 10 {
		t.Errorf("StreamSize(stdout) = (%d, %v), want (10, nil)", stdoutSize, sizeErr)
	}

	var chunks []Chunk
	status, err := reader.ScanIndex(func(chunk Chunk) error {
		chunks = append(chunks, chunk)

		return nil
	})
	if err != nil {
		t.Fatalf("ScanIndex() error = %v", err)
	}
	wantChunks := []Chunk{
		{Sequence: 1, Stream: Stdout, Segment: 1, Offset: 0, Length: 4, ObservedAt: observed},
		{Sequence: 2, Stream: Stdout, Segment: 2, Offset: 0, Length: 4, ObservedAt: observed},
		{Sequence: 3, Stream: Stdout, Segment: 3, Offset: 0, Length: 2, ObservedAt: observed},
		{Sequence: 4, Stream: Stderr, Segment: 1, Offset: 0, Length: 3, ObservedAt: observed.Add(time.Millisecond)},
	}
	if !reflect.DeepEqual(chunks, wantChunks) {
		t.Errorf("ScanIndex() chunks = %#v, want %#v", chunks, wantChunks)
	}
	if status.IndexedStdoutBytes != 10 || status.IndexedStderrBytes != 3 {
		t.Errorf("ScanIndex() status = %+v", status)
	}

	var combined bytes.Buffer
	written, _, err := reader.CopyCombined(&combined)
	if err != nil {
		t.Fatalf("CopyCombined() error = %v", err)
	}
	if written != 13 || combined.String() != "abcdefghijERR" {
		t.Errorf("CopyCombined() = (%d, %q), want (13, %q)", written, combined.String(), "abcdefghijERR")
	}
}

func TestRotationStopsAtConfiguredSegmentLimit(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	run, err := CreateRunWithOptions(stateDir, testJobID, 1, RunOptions{
		Rotation: RotationPolicy{SegmentBytes: 2, MaxSegmentsPerStream: 2},
	})
	if err != nil {
		t.Fatalf("CreateRunWithOptions() error = %v", err)
	}
	written, appendErr := run.Append(Stdout, []byte("abcde"), time.Unix(1, 0))
	if written != 4 || !errors.Is(appendErr, ErrSegmentLimit) {
		t.Fatalf("Append() = (%d, %v), want (4, ErrSegmentLimit)", written, appendErr)
	}
	if closeErr := run.Close(); !errors.Is(closeErr, ErrSegmentLimit) {
		t.Fatalf("Close() error = %v, want ErrSegmentLimit", closeErr)
	}
	if _, statErr := os.Stat(run.Paths().Active); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("active marker after failed capture Close() error = %v", statErr)
	}

	reader, err := OpenRun(stateDir, testJobID, 1)
	if err != nil {
		t.Fatalf("OpenRun() error = %v", err)
	}
	assertStreamEquals(t, reader, Stdout, []byte("abcd"))
}

func TestRotationPolicyAndSegmentContinuityValidation(t *testing.T) {
	t.Parallel()

	if _, err := CreateRunWithOptions(filepath.Join(t.TempDir(), "state"), testJobID, 1, RunOptions{
		Rotation: RotationPolicy{MaxSegmentsPerStream: 2},
	}); err == nil {
		t.Fatal("CreateRunWithOptions() error = nil, want invalid rotation policy")
	}

	data := indexFixture(t,
		Chunk{Sequence: 1, Stream: Stdout, Segment: 1, Offset: 0, Length: 1, ObservedAt: time.Unix(1, 0)},
		Chunk{Sequence: 2, Stream: Stdout, Segment: 3, Offset: 0, Length: 1, ObservedAt: time.Unix(2, 0)},
	)
	if _, err := scanIndex(bytes.NewReader(data), nil); !errors.Is(err, ErrCorruptIndex) {
		t.Fatalf("scanIndex() error = %v, want ErrCorruptIndex", err)
	}

	mixed := indexFixture(t,
		Chunk{Sequence: 1, Stream: Stdout, Offset: 0, Length: 1, ObservedAt: time.Unix(1, 0)},
		Chunk{Sequence: 2, Stream: Stdout, Segment: 1, Offset: 1, Length: 1, ObservedAt: time.Unix(2, 0)},
	)
	if _, err := scanIndex(bytes.NewReader(mixed), nil); !errors.Is(err, ErrCorruptIndex) {
		t.Fatalf("scanIndex(mixed) error = %v, want ErrCorruptIndex", err)
	}
}

func TestCreateRunPreservesUnsegmentedIndexVersion(t *testing.T) {
	t.Parallel()

	run, err := CreateRun(filepath.Join(t.TempDir(), "state"), testJobID, 1)
	if err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	defer func() {
		if closeErr := run.Close(); closeErr != nil {
			t.Errorf("Close() error = %v", closeErr)
		}
	}()
	if run.IndexVersion() != IndexVersionUnsegmented {
		t.Fatalf("IndexVersion() = %d, want %d", run.IndexVersion(), IndexVersionUnsegmented)
	}
}

func TestVersion2IndexToleratesTornFinalRecord(t *testing.T) {
	t.Parallel()

	complete := indexFixture(t,
		Chunk{Sequence: 1, Stream: Stdout, Segment: 1, Offset: 0, Length: 1, ObservedAt: time.Unix(1, 0)},
	)
	data := append(bytes.Clone(complete), []byte("partial")...)
	status, err := scanIndex(bytes.NewReader(data), nil)
	if err != nil {
		t.Fatalf("scanIndex() error = %v", err)
	}
	if status.Records != 1 || !status.TornTail || status.TornBytes != len("partial") {
		t.Errorf("scanIndex() status = %+v", status)
	}
}

func TestConcurrentRotatedAppendsKeepContiguousIndex(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	run, err := CreateRunWithOptions(stateDir, testJobID, 1, RunOptions{
		Rotation: RotationPolicy{SegmentBytes: 5, MaxSegmentsPerStream: 4},
	})
	if err != nil {
		t.Fatalf("CreateRunWithOptions() error = %v", err)
	}

	const writesPerStream = 20
	start := make(chan struct{})
	errorsByStream := make(chan error, 2)
	var wait sync.WaitGroup
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
	for appendErr := range errorsByStream {
		t.Errorf("Append() error = %v", appendErr)
	}
	if closeErr := run.Close(); closeErr != nil {
		t.Fatalf("Close() error = %v", closeErr)
	}

	reader, err := OpenRun(stateDir, testJobID, 1)
	if err != nil {
		t.Fatalf("OpenRun() error = %v", err)
	}
	status, err := reader.ScanIndex(nil)
	if err != nil {
		t.Fatalf("ScanIndex() error = %v", err)
	}
	if status.Records != 2*writesPerStream ||
		status.IndexedStdoutBytes != writesPerStream ||
		status.IndexedStderrBytes != writesPerStream {
		t.Errorf("ScanIndex() status = %+v", status)
	}
}

func assertFileSize(t *testing.T, path string, want int64) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %q: %v", path, err)
	}
	if info.Size() != want {
		t.Errorf("size of %q = %d, want %d", path, info.Size(), want)
	}
}
