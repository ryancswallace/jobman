package logstore

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestReaderPropagatesDisappearingFilesAndDirectories(t *testing.T) {
	t.Parallel()

	t.Run("stream", func(t *testing.T) {
		t.Parallel()
		reader := closedRunReader(t)
		if err := os.Remove(reader.paths.Stdout); err != nil {
			t.Fatal(err)
		}
		if _, err := reader.CopyStream(io.Discard, Stdout); err == nil {
			t.Fatal("CopyStream(missing stream) error = nil")
		}
	})

	t.Run("index", func(t *testing.T) {
		t.Parallel()
		reader := closedRunReader(t)
		if err := os.Remove(reader.paths.Index); err != nil {
			t.Fatal(err)
		}
		if _, err := reader.ScanIndex(nil); err == nil {
			t.Fatal("ScanIndex(missing index) error = nil")
		}
	})

	t.Run("stderr tail", func(t *testing.T) {
		t.Parallel()
		reader := closedRunReader(t)
		if err := os.Remove(reader.paths.Stderr); err != nil {
			t.Fatal(err)
		}
		if err := reader.addRawTailStatus(&IndexStatus{}); err == nil {
			t.Fatal("addRawTailStatus(missing stderr) error = nil")
		}
	})

	t.Run("directory", func(t *testing.T) {
		t.Parallel()
		reader := closedRunReader(t)
		if err := os.RemoveAll(reader.paths.Directory); err != nil {
			t.Fatal(err)
		}
		if _, err := reader.streamSegments(Stdout); err == nil {
			t.Fatal("streamSegments(missing directory) error = nil")
		}
	})
}

func TestIndexedCopyFailureBoundaries(t *testing.T) {
	t.Parallel()

	reader := closedRunReader(t)
	missing := Chunk{Sequence: 1, Stream: Stdout, Segment: 2, Length: 1, ObservedAt: time.Now().UTC()}
	if _, err := reader.indexedSource(make(map[segmentKey]*os.File), missing); err == nil {
		t.Fatal("indexedSource(missing segment) error = nil")
	}

	truncated := filepath.Join(t.TempDir(), "truncated.idx")
	if err := os.WriteFile(truncated, []byte{1}, fileMode); err != nil {
		t.Fatal(err)
	}
	index, err := os.Open(truncated)
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	if _, err := reader.copyIndexedPrefix(io.Discard, index, 1, 0); err == nil {
		t.Fatal("copyIndexedPrefix(truncated index) error = nil")
	}

	record, err := encodeRecord(Chunk{
		Sequence: 1, Stream: Stdout, Length: 1, ObservedAt: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	skipped := filepath.Join(t.TempDir(), "skipped.idx")
	if err := os.WriteFile(skipped, record[:], fileMode); err != nil {
		t.Fatal(err)
	}
	index, err = os.Open(skipped)
	if err != nil {
		t.Fatal(err)
	}
	defer index.Close()
	if _, err := reader.copyIndexedPrefix(io.Discard, index, 1, 1); err != nil {
		t.Fatalf("copyIndexedPrefix(skipped record) error = %v", err)
	}
}

func TestFileAndIndexHelperFailureEdges(t *testing.T) {
	t.Parallel()

	closed, err := os.CreateTemp(t.TempDir(), "closed")
	if err != nil {
		t.Fatal(err)
	}
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	if err := syncFile("closed", closed); err == nil {
		t.Fatal("syncFile(closed) error = nil")
	}
	if err := closeFile("closed", closed); err == nil {
		t.Fatal("closeFile(closed) error = nil")
	}

	directory := t.TempDir()
	paths := Paths{Directory: directory}
	if err := os.WriteFile(filepath.Join(directory, "unexpected"), nil, fileMode); err != nil {
		t.Fatal(err)
	}
	if err := rollbackRunDirectory(paths); err == nil {
		t.Fatal("rollbackRunDirectory(nonempty) error = nil")
	}

	var record [recordSize]byte
	record[4] = indexVersion2
	if _, err := decodeSegment(record); err == nil {
		t.Fatal("decodeSegment(version 2 without segment) error = nil")
	}
}

func closedRunReader(t *testing.T) *Reader {
	t.Helper()
	stateDir := filepath.Join(t.TempDir(), "state")
	run, err := CreateRun(stateDir, testJobID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := run.Close(); err != nil {
		t.Fatal(err)
	}
	reader, err := OpenRun(stateDir, testJobID, 1)
	if err != nil {
		t.Fatal(err)
	}

	return reader
}
