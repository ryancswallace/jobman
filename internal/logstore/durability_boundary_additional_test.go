package logstore

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReaderRejectsPostValidationCorruption(t *testing.T) {
	t.Parallel()

	t.Run("invalid full index record", func(t *testing.T) {
		t.Parallel()

		reader := closedRunReader(t)
		path := filepath.Join(t.TempDir(), "invalid.idx")
		if err := os.WriteFile(path, make([]byte, recordSize), fileMode); err != nil {
			t.Fatal(err)
		}
		index, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer index.Close()
		if _, err := reader.copyIndexedPrefix(io.Discard, index, 1, 0); err == nil {
			t.Fatal("copyIndexedPrefix(invalid record) error = nil")
		}
	})

	t.Run("stderr index exceeds stream", func(t *testing.T) {
		t.Parallel()

		reader := closedRunReader(t)
		status := IndexStatus{IndexedStderrBytes: 1}
		if err := reader.addRawTailStatus(&status); err == nil {
			t.Fatal("addRawTailStatus(oversized stderr index) error = nil")
		}
	})

	t.Run("stream entry becomes directory", func(t *testing.T) {
		t.Parallel()

		directory := t.TempDir()
		if err := os.Mkdir(filepath.Join(directory, stdoutFilename), directoryMode); err != nil {
			t.Fatal(err)
		}
		reader := &Reader{paths: Paths{Directory: directory, Stdout: filepath.Join(directory, stdoutFilename)}}
		if _, err := reader.StreamSize(Stdout); err == nil {
			t.Fatal("StreamSize(directory segment) error = nil")
		}
		if _, err := reader.CopyStream(io.Discard, Stdout); err == nil {
			t.Fatal("CopyStream(directory segment) error = nil")
		}
	})

	t.Run("stream paths", func(t *testing.T) {
		t.Parallel()

		reader := &Reader{paths: Paths{Stdout: "stdout-path", Stderr: "stderr-path"}}
		if path, err := reader.streamPath(Stdout); err != nil || path != "stdout-path" {
			t.Fatalf("streamPath(stdout) = (%q, %v)", path, err)
		}
		if path, err := reader.streamPath(Stderr); err != nil || path != "stderr-path" {
			t.Fatalf("streamPath(stderr) = (%q, %v)", path, err)
		}
	})

	t.Run("indexed segment disappears", func(t *testing.T) {
		t.Parallel()

		reader := closedRunReader(t)
		record, err := encodeRecord(Chunk{
			Sequence: 1, Stream: Stdout, Segment: 2, Length: 1, ObservedAt: time.Now().UTC(),
		})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "missing-segment.idx")
		if writeErr := os.WriteFile(path, record[:], fileMode); writeErr != nil {
			t.Fatal(writeErr)
		}
		index, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer index.Close()
		if _, err := reader.copyIndexedPrefix(io.Discard, index, 1, 0); err == nil {
			t.Fatal("copyIndexedPrefix(missing segment) error = nil")
		}
	})

	t.Run("indexed source close", func(t *testing.T) {
		t.Parallel()

		file, err := os.CreateTemp(t.TempDir(), "closed-segment")
		if err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		if err := closeIndexedSources(map[segmentKey]*os.File{{stream: Stdout, segment: 1}: file}); err == nil {
			t.Fatal("closeIndexedSources(already closed) error = nil")
		}
	})
}

func TestCapturePropagatesDurabilityBoundaryFailures(t *testing.T) {
	t.Parallel()

	t.Run("raw stream sync", func(t *testing.T) {
		t.Parallel()

		run := newBoundaryRun(t, RunOptions{})
		if err := run.stdout.Close(); err != nil {
			t.Fatal(err)
		}
		reader, writer, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })
		run.stdout = writer
		if _, err := run.Append(Stdout, []byte("data"), time.Now().UTC()); err == nil {
			t.Fatal("Append(pipe-backed stream) error = nil")
		}
	})

	t.Run("record encoding", func(t *testing.T) {
		t.Parallel()

		run := newBoundaryRun(t, RunOptions{})
		run.nextSequence = 0
		if _, err := run.Append(Stdout, []byte("data"), time.Now().UTC()); err == nil {
			t.Fatal("Append(invalid next sequence) error = nil")
		}
	})

	t.Run("index sync", func(t *testing.T) {
		t.Parallel()

		run := newBoundaryRun(t, RunOptions{})
		if err := run.index.Close(); err != nil {
			t.Fatal(err)
		}
		reader, writer, err := os.Pipe()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = reader.Close(); _ = writer.Close() })
		run.index = writer
		if _, err := run.Append(Stdout, []byte("data"), time.Now().UTC()); err == nil {
			t.Fatal("Append(pipe-backed index) error = nil")
		}
	})

	t.Run("next segment already exists", func(t *testing.T) {
		t.Parallel()

		run := newBoundaryRun(t, RunOptions{Rotation: RotationPolicy{SegmentBytes: 1}})
		if _, err := run.Append(Stdout, []byte("a"), time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		next := segmentPath(run.paths, Stdout, 2)
		if err := os.WriteFile(next, nil, fileMode); err != nil {
			t.Fatal(err)
		}
		if _, err := run.Append(Stdout, []byte("b"), time.Now().UTC()); err == nil {
			t.Fatal("Append(existing next segment) error = nil")
		}
	})

	t.Run("full segment sync", func(t *testing.T) {
		t.Parallel()

		run := newBoundaryRun(t, RunOptions{Rotation: RotationPolicy{SegmentBytes: 1}})
		if _, err := run.Append(Stdout, []byte("a"), time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		if err := run.stdout.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := run.Append(Stdout, []byte("b"), time.Now().UTC()); err == nil {
			t.Fatal("Append(closed full segment) error = nil")
		}
	})
}

func newBoundaryRun(t *testing.T, options RunOptions) *Run {
	t.Helper()
	run, err := CreateRunWithOptions(filepath.Join(t.TempDir(), "state"), testJobID, 1, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = run.Close() })

	return run
}
