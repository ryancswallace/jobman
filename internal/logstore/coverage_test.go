package logstore

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStreamAndPathHelpers(t *testing.T) {
	t.Parallel()

	if Stdout.String() != "stdout" || Stderr.String() != "stderr" || Stream(99).String() != "unknown" {
		t.Fatal("Stream.String() returned an unexpected name")
	}
	if !isWithin("/tmp/root", "/tmp/root/child") || isWithin("/tmp/root", "/tmp/root") || isWithin("/tmp/root", "/tmp/other") {
		t.Fatal("isWithin() returned an unexpected result")
	}
	if value, err := boundedIntToUint64(42); err != nil || value != 42 {
		t.Fatalf("boundedIntToUint64(42) = (%d, %v)", value, err)
	}
	if _, err := boundedIntToUint64(-1); err == nil {
		t.Fatal("boundedIntToUint64(-1) error = nil")
	}
	if _, err := boundedIntToUint64(MaxChunkSize + 1); err == nil {
		t.Fatal("boundedIntToUint64(too large) error = nil")
	}
	if value, err := boundedUint64ToInt(42); err != nil || value != 42 {
		t.Fatalf("boundedUint64ToInt(42) = (%d, %v)", value, err)
	}
	if _, err := boundedUint64ToInt(MaxChunkSize + 1); err == nil {
		t.Fatal("boundedUint64ToInt(too large) error = nil")
	}
	if value, err := nonnegativeInt64ToUint64(42); err != nil || value != 42 {
		t.Fatalf("nonnegativeInt64ToUint64(42) = (%d, %v)", value, err)
	}
	if _, err := nonnegativeInt64ToUint64(-1); err == nil {
		t.Fatal("nonnegativeInt64ToUint64(-1) error = nil")
	}
	if value, err := uint64ToInt64(42); err != nil || value != 42 {
		t.Fatalf("uint64ToInt64(42) = (%d, %v)", value, err)
	}
	if _, err := uint64ToInt64(math.MaxUint64); err == nil {
		t.Fatal("uint64ToInt64(MaxUint64) error = nil")
	}
}

func TestReaderPathsAndStreamValidation(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	run, err := CreateRun(stateDir, testJobID, 1)
	if err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	paths := run.Paths()
	if _, err := run.Append(Stdout, nil, time.Time{}); err != nil {
		t.Fatalf("Append(empty) error = %v", err)
	}
	if err := run.Sync(); err != nil {
		t.Fatalf("Sync() error = %v", err)
	}
	if err := run.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := run.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if err := run.Sync(); !errors.Is(err, ErrClosed) {
		t.Fatalf("Sync() error = %v, want ErrClosed", err)
	}

	reader, err := OpenRun(stateDir, testJobID, 1)
	if err != nil {
		t.Fatalf("OpenRun() error = %v", err)
	}
	if reader.Paths() != paths {
		t.Fatalf("Paths() = %+v, want %+v", reader.Paths(), paths)
	}
	if reader.streamBasePath(Stdout) != paths.Stdout || reader.streamBasePath(Stderr) != paths.Stderr {
		t.Fatal("streamBasePath() returned an unexpected path")
	}
	if _, err := reader.OpenStream(Stream(99)); !errors.Is(err, ErrInvalidStream) {
		t.Fatalf("OpenStream(invalid) error = %v", err)
	}
	if _, err := reader.StreamSize(Stream(99)); !errors.Is(err, ErrInvalidStream) {
		t.Fatalf("StreamSize(invalid) error = %v", err)
	}
	if _, err := reader.CopyStream(io.Discard, Stream(99)); !errors.Is(err, ErrInvalidStream) {
		t.Fatalf("CopyStream(invalid) error = %v", err)
	}
}

func TestIndexRecordValidationBranches(t *testing.T) {
	t.Parallel()

	base := Chunk{Sequence: 1, Stream: Stdout, Offset: 0, Length: 2, ObservedAt: time.Unix(5, 6).UTC()}
	record, err := encodeRecord(base)
	if err != nil {
		t.Fatalf("encodeRecord() error = %v", err)
	}
	decoded, err := decodeRecord(record)
	if err != nil || decoded != base {
		t.Fatalf("decodeRecord() = (%+v, %v), want %+v", decoded, err, base)
	}

	for name, mutate := range map[string]func(*[recordSize]byte){
		"magic":    func(record *[recordSize]byte) { record[0] = 0 },
		"checksum": func(record *[recordSize]byte) { record[51] ^= 1 },
		"version":  func(record *[recordSize]byte) { record[4] = 99; fixRecordChecksum(record) },
		"reserved": func(record *[recordSize]byte) { record[6] = 1; fixRecordChecksum(record) },
		"stream":   func(record *[recordSize]byte) { record[5] = 99; fixRecordChecksum(record) },
		"length-zero": func(record *[recordSize]byte) {
			binary.LittleEndian.PutUint64(record[24:32], 0)
			fixRecordChecksum(record)
		},
		"length-large": func(record *[recordSize]byte) {
			binary.LittleEndian.PutUint64(record[24:32], MaxChunkSize+1)
			fixRecordChecksum(record)
		},
		"nanosecond": func(record *[recordSize]byte) {
			binary.LittleEndian.PutUint64(record[40:48], uint64(time.Second))
			fixRecordChecksum(record)
		},
	} {
		t.Run(name, func(t *testing.T) {
			bad := record
			mutate(&bad)
			if _, decodeErr := decodeRecord(bad); !errors.Is(decodeErr, ErrCorruptIndex) {
				t.Fatalf("decodeRecord() error = %v, want ErrCorruptIndex", decodeErr)
			}
		})
	}

	for _, chunk := range []Chunk{
		{Sequence: 0, Stream: Stdout, Length: 1},
		{Sequence: 1, Stream: Stream(99), Length: 1},
		{Sequence: 1, Stream: Stdout, Length: 0},
		{Sequence: 1, Stream: Stdout, Length: MaxChunkSize + 1},
	} {
		if _, encodeErr := encodeRecord(chunk); encodeErr == nil {
			t.Fatalf("encodeRecord(%+v) error = nil", chunk)
		}
	}
}

func TestIndexStateRejectsDiscontinuities(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		state indexState
		chunk Chunk
	}{
		{name: "sequence", state: indexState{nextSequence: 2}, chunk: Chunk{Sequence: 1, Stream: Stdout, Length: 1}},
		{name: "offset", state: indexState{nextSequence: 1, expected: [3]uint64{0, 2}}, chunk: Chunk{Sequence: 1, Stream: Stdout, Length: 1}},
		{name: "offset overflow", state: indexState{nextSequence: 1, expected: [3]uint64{0, math.MaxUint64}}, chunk: Chunk{Sequence: 1, Stream: Stdout, Offset: math.MaxUint64, Length: 1}},
		{name: "segmented then legacy", state: indexState{nextSequence: 1, segmented: true}, chunk: Chunk{Sequence: 1, Stream: Stdout, Length: 1}},
		{name: "legacy then segmented", state: indexState{nextSequence: 1, legacy: true}, chunk: Chunk{Sequence: 1, Stream: Stdout, Segment: 1, Length: 1}},
		{name: "starts late", state: indexState{nextSequence: 1}, chunk: Chunk{Sequence: 1, Stream: Stdout, Segment: 2, Length: 1}},
		{name: "segment skips", state: indexState{nextSequence: 1, segmented: true, segments: [3]uint16{0, 1}}, chunk: Chunk{Sequence: 1, Stream: Stdout, Segment: 3, Length: 1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.state.accept(test.chunk); !errors.Is(err, ErrCorruptIndex) {
				t.Fatalf("accept() error = %v, want ErrCorruptIndex", err)
			}
		})
	}
	visitorErr := errors.New("visit failed")
	record, err := encodeRecord(Chunk{Sequence: 1, Stream: Stdout, Length: 1, ObservedAt: time.Unix(1, 0)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := scanIndex(bytes.NewReader(record[:]), func(Chunk) error { return visitorErr }); !errors.Is(err, visitorErr) {
		t.Fatalf("scanIndex() error = %v, want visitor error", err)
	}
}

func TestReaderDetectsMissingAndChangedStreamFiles(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	run, err := CreateRun(stateDir, testJobID, 1)
	if err != nil {
		t.Fatal(err)
	}
	appendBytes(t, run, Stdout, []byte("data"), time.Unix(1, 0))
	paths := run.Paths()
	if err := run.Close(); err != nil {
		t.Fatal(err)
	}
	reader, err := OpenRun(stateDir, testJobID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(paths.Stdout); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.StreamSize(Stdout); err == nil {
		t.Fatal("StreamSize() error = nil after removing stream")
	}
	if _, _, err := reader.CopyCombined(io.Discard); err == nil {
		t.Fatal("CopyCombined() error = nil after removing stream")
	}
}

func TestCleanupAndFollowValidationBranches(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := CleanupRun(ctx, t.TempDir(), testJobID, 1, func(context.Context) (bool, error) { return true, nil }); err == nil {
		t.Fatal("CleanupRun(canceled) error = nil")
	}
	if err := requireCleanupEligibility(t.Context(), func(context.Context) (bool, error) {
		return false, errors.New("lookup failed")
	}); err == nil {
		t.Fatal("requireCleanupEligibility(error) = nil")
	}
	if _, _, err := cleanupSource(filepath.Join(t.TempDir(), "missing"), filepath.Join(t.TempDir(), "also-missing")); err == nil {
		t.Fatal("cleanupSource(missing) error = nil")
	}

	copyErr := errors.New("copy failed")
	if _, err := follow(t.Context(), FollowOptions{Complete: func(context.Context) (bool, error) {
		return false, errors.New("complete failed")
	}}, func() (int64, error) { return 1, nil }); err == nil {
		t.Fatal("follow(complete error) error = nil")
	}
	if count, err := follow(t.Context(), FollowOptions{}, func() (int64, error) { return 2, copyErr }); count != 2 || !errors.Is(err, copyErr) {
		t.Fatalf("follow(copy error) = (%d, %v)", count, err)
	}
}

func TestWriteAllHandlesShortAndPartialWriters(t *testing.T) {
	t.Parallel()

	short := &scriptedWriter{counts: []int{1, 0}}
	if count, err := writeAll(short, []byte("ab")); count != 1 || !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("writeAll(short) = (%d, %v)", count, err)
	}
	wantErr := errors.New("write failed")
	failing := &scriptedWriter{counts: []int{1}, err: wantErr}
	if count, err := writeAll(failing, []byte("ab")); count != 1 || !errors.Is(err, wantErr) {
		t.Fatalf("writeAll(failing) = (%d, %v)", count, err)
	}
}

func TestFilesystemRollbackAndValidationBranches(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	nested := filepath.Join(root, "private", "nested")
	if err := ensurePrivateDirectory(nested); err != nil {
		t.Fatalf("ensurePrivateDirectory() error = %v", err)
	}
	unsafeDirectory := filepath.Join(root, "unsafe")
	if err := os.Mkdir(unsafeDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ensurePrivateDirectory(unsafeDirectory); err == nil {
		t.Fatal("ensurePrivateDirectory(unsafe mode) error = nil")
	}
	notDirectory := filepath.Join(root, "file")
	if err := os.WriteFile(notDirectory, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensurePrivateDirectory(notDirectory); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("ensurePrivateDirectory(file) error = %v", err)
	}
	if _, err := openPrivateRegularFile(filepath.Join(root, "missing")); err == nil {
		t.Fatal("openPrivateRegularFile(missing) error = nil")
	}
	unsafeFile := filepath.Join(root, "unsafe-file")
	if err := os.WriteFile(unsafeFile, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := openPrivateRegularFile(unsafeFile); err == nil {
		t.Fatal("openPrivateRegularFile(unsafe mode) error = nil")
	}
	symlink := filepath.Join(root, "link")
	if err := os.Symlink(notDirectory, symlink); err != nil {
		t.Fatal(err)
	}
	if _, err := openPrivateRegularFile(symlink); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("openPrivateRegularFile(symlink) error = %v", err)
	}

	runDirectory := filepath.Join(root, "rollback")
	if err := os.Mkdir(runDirectory, directoryMode); err != nil {
		t.Fatal(err)
	}
	paths := Paths{
		Directory: runDirectory, Stdout: filepath.Join(runDirectory, stdoutFilename),
		Stderr: filepath.Join(runDirectory, stderrFilename), Index: filepath.Join(runDirectory, indexFilename),
		Active: filepath.Join(runDirectory, activeFilename),
	}
	for _, path := range []string{paths.Stdout, paths.Stderr, paths.Index, paths.Active} {
		if err := os.WriteFile(path, nil, fileMode); err != nil {
			t.Fatal(err)
		}
	}
	if err := rollbackRunDirectory(paths); err != nil {
		t.Fatalf("rollbackRunDirectory() error = %v", err)
	}
	if _, err := os.Stat(runDirectory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rollback directory stat error = %v", err)
	}
}

func TestAbortCreateAndFileHelpers(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	active := filepath.Join(root, activeFilename)
	if err := os.WriteFile(active, nil, fileMode); err != nil {
		t.Fatal(err)
	}
	stdout, err := os.CreateTemp(root, "stdout")
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := os.CreateTemp(root, "stderr")
	if err != nil {
		t.Fatal(err)
	}
	index, err := os.CreateTemp(root, "index")
	if err != nil {
		t.Fatal(err)
	}
	run := &Run{stdout: stdout, stderr: stderr, index: index, paths: Paths{Active: active}}
	if err := run.abortCreate(); err != nil {
		t.Fatalf("abortCreate() error = %v", err)
	}
	if err := syncFile("nil", nil); err != nil || closeFile("nil", nil) != nil {
		t.Fatal("nil file helpers returned an error")
	}
	if err := removeFile("missing", filepath.Join(root, "missing")); err != nil {
		t.Fatalf("removeFile(missing) error = %v", err)
	}
}

func TestReaderDetectsShortRawFileAndSegmentGap(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	run, err := CreateRunWithOptions(stateDir, testJobID, 1, RunOptions{
		Rotation: RotationPolicy{SegmentBytes: 2, MaxSegmentsPerStream: 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	appendBytes(t, run, Stdout, []byte("abcd"), time.Unix(1, 0))
	paths := run.Paths()
	if err := run.Close(); err != nil {
		t.Fatal(err)
	}
	reader, err := OpenRun(stateDir, testJobID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(segmentPath(paths, Stdout, 2), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.ScanIndex(nil); !errors.Is(err, ErrCorruptIndex) {
		t.Fatalf("ScanIndex(short raw stream) error = %v", err)
	}
	if err := os.Remove(segmentPath(paths, Stdout, 2)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(segmentPath(paths, Stdout, 3), nil, fileMode); err != nil && !errors.Is(err, os.ErrExist) {
		t.Fatal(err)
	}
	if _, err := reader.StreamSize(Stdout); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("StreamSize(segment gap) error = %v", err)
	}
}

func TestCopyChunkAndIncrementalFollowCorruption(t *testing.T) {
	t.Parallel()

	file, err := os.CreateTemp(t.TempDir(), "source")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.WriteString("x"); err != nil {
		t.Fatal(err)
	}
	if _, err := copyChunk(io.Discard, file, Chunk{Sequence: 1, Stream: Stdout, Offset: math.MaxUint64, Length: 1}); !errors.Is(err, ErrCorruptIndex) {
		t.Fatalf("copyChunk(large offset) error = %v", err)
	}
	if _, err := copyChunk(io.Discard, file, Chunk{Sequence: 1, Stream: Stdout, Length: math.MaxUint64}); !errors.Is(err, ErrCorruptIndex) {
		t.Fatalf("copyChunk(large length) error = %v", err)
	}
	if _, err := copyChunk(io.Discard, file, Chunk{Sequence: 1, Stream: Stdout, Length: 2}); !errors.Is(err, ErrCorruptIndex) {
		t.Fatalf("copyChunk(short source) error = %v", err)
	}

	stateDir := filepath.Join(t.TempDir(), "state")
	run, err := CreateRun(stateDir, testJobID, 1)
	if err != nil {
		t.Fatal(err)
	}
	appendBytes(t, run, Stdout, []byte("x"), time.Unix(1, 0))
	paths := run.Paths()
	if err := run.Close(); err != nil {
		t.Fatal(err)
	}
	reader, err := OpenRun(stateDir, testJobID, 1)
	if err != nil {
		t.Fatal(err)
	}
	cursor := &indexFollowCursor{state: indexState{nextSequence: 1}}
	if err := reader.scanIndexGrowth(cursor, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(paths.Index, 0); err != nil {
		t.Fatal(err)
	}
	if err := reader.scanIndexGrowth(cursor, nil); !errors.Is(err, ErrCorruptIndex) {
		t.Fatalf("scanIndexGrowth(shrunk) error = %v", err)
	}
}

func TestCleanupClaimSafetyBranches(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	runDirectory := filepath.Join(root, "run")
	tombstone := runDirectory + ".deleting"
	if err := os.Mkdir(runDirectory, directoryMode); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(tombstone, directoryMode); err != nil {
		t.Fatal(err)
	}
	if _, _, err := cleanupSource(runDirectory, tombstone); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("cleanupSource(both) error = %v", err)
	}
	if _, err := moveCleanupDirectory(runDirectory, tombstone, true); err != nil {
		t.Fatalf("moveCleanupDirectory(already moved) error = %v", err)
	}
	if _, err := moveCleanupDirectory(runDirectory, tombstone, false); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("moveCleanupDirectory(existing tombstone) error = %v", err)
	}
	if _, _, err := inspectCleanupDirectory(filepath.Join(root, "missing"), false); err == nil {
		t.Fatal("inspectCleanupDirectory(missing) error = nil")
	}
	regular := filepath.Join(root, "regular")
	if err := os.WriteFile(regular, nil, fileMode); err != nil {
		t.Fatal(err)
	}
	if _, _, err := inspectCleanupDirectory(regular, false); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("inspectCleanupDirectory(file) error = %v", err)
	}

	entryPath := filepath.Join(root, stdoutFilename)
	if err := os.WriteFile(entryPath, []byte("old"), fileMode); err != nil {
		t.Fatal(err)
	}
	before, err := os.Lstat(entryPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(entryPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(entryPath, directoryMode); err != nil {
		t.Fatal(err)
	}
	if err := removeCleanupEntry(root, cleanupEntry{name: stdoutFilename, info: before}); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("removeCleanupEntry(changed) error = %v", err)
	}
}

func TestReaderPropagatesDestinationAndIndexReplacementErrors(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	run, err := CreateRun(stateDir, testJobID, 1)
	if err != nil {
		t.Fatal(err)
	}
	appendBytes(t, run, Stdout, []byte("payload"), time.Unix(1, 0))
	paths := run.Paths()
	if err := run.Close(); err != nil {
		t.Fatal(err)
	}
	reader, err := OpenRun(stateDir, testJobID, 1)
	if err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("destination failed")
	if _, err := reader.CopyStream(&alwaysFailWriter{err: wantErr}, Stdout); !errors.Is(err, wantErr) {
		t.Fatalf("CopyStream(destination failure) error = %v", err)
	}
	if _, _, err := reader.CopyCombined(&alwaysFailWriter{err: wantErr}); !errors.Is(err, wantErr) {
		t.Fatalf("CopyCombined(destination failure) error = %v", err)
	}

	cursor := &indexFollowCursor{state: indexState{nextSequence: 1}}
	if err := reader.scanIndexGrowth(cursor, nil); err != nil {
		t.Fatal(err)
	}
	replacement := paths.Index + ".replacement"
	data, err := os.ReadFile(paths.Index)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(replacement, data, fileMode); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(replacement, paths.Index); err != nil {
		t.Fatal(err)
	}
	if err := reader.scanIndexGrowth(cursor, nil); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("scanIndexGrowth(replaced) error = %v", err)
	}
}

type alwaysFailWriter struct{ err error }

func (writer *alwaysFailWriter) Write([]byte) (int, error) { return 0, writer.err }

func TestReleaseAbandonedAndStreamCursorEdges(t *testing.T) {
	t.Parallel()

	stateDir, paths := closedRunFixture(t)
	if err := ReleaseAbandonedRun(t.Context(), stateDir, testJobID, 1, nil); !errors.Is(err, ErrCleanupIneligible) {
		t.Fatalf("ReleaseAbandonedRun(nil) error = %v", err)
	}
	if err := ReleaseAbandonedRun(t.Context(), stateDir, testJobID, 1, alwaysEligible); err != nil {
		t.Fatalf("ReleaseAbandonedRun(no marker) error = %v", err)
	}
	if err := os.Symlink(paths.Stdout, paths.Active); err != nil {
		t.Fatal(err)
	}
	if err := ReleaseAbandonedRun(t.Context(), stateDir, testJobID, 1, alwaysEligible); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("ReleaseAbandonedRun(symlink marker) error = %v", err)
	}
	if err := os.Remove(paths.Active); err != nil {
		t.Fatal(err)
	}

	reader, err := OpenRun(stateDir, testJobID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.copyStreamGrowth(io.Discard, Stdout, &streamCursor{segment: 2}); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("copyStreamGrowth(disappeared) error = %v", err)
	}
	cursor := &streamCursor{segment: 1, offset: 100}
	var output bytes.Buffer
	if _, err := reader.copyStreamGrowth(&output, Stdout, cursor); err != nil || output.String() != "data" {
		t.Fatalf("copyStreamGrowth(truncated cursor) = (%q, %v)", output.String(), err)
	}
}

func TestCreateFilesFailureStagesAndRetentionOverflow(t *testing.T) {
	t.Parallel()

	for _, blocked := range []string{stdoutFilename, stderrFilename, indexFilename, activeFilename} {
		t.Run(blocked, func(t *testing.T) {
			root := t.TempDir()
			paths := Paths{
				Directory: root, Stdout: filepath.Join(root, stdoutFilename), Stderr: filepath.Join(root, stderrFilename),
				Index: filepath.Join(root, indexFilename), Active: filepath.Join(root, activeFilename),
			}
			if err := os.WriteFile(filepath.Join(root, blocked), nil, fileMode); err != nil {
				t.Fatal(err)
			}
			run := &Run{paths: paths}
			if err := run.createFiles(); err == nil {
				t.Fatal("createFiles() error = nil")
			}
			_ = run.abortCreate()
		})
	}

	now := time.Now().UTC()
	overflow := []RetentionCandidate{
		{JobID: testJobID, RunNumber: 1, CompletedAt: now, Bytes: math.MaxUint64},
		{JobID: testJobID, RunNumber: 2, CompletedAt: now, Bytes: 1},
	}
	policy := RetentionPolicy{
		MaxAge: UnlimitedRetentionAge(), MaxJobs: UnlimitedRetentionLimit(),
		MaxRunsPerJob: UnlimitedRetentionLimit(), MaxBytesPerJob: RetentionLimit{Maximum: 1},
		MaxTotalBytes: UnlimitedRetentionLimit(),
	}
	if _, err := PlanRetention(now, overflow, policy); err == nil {
		t.Fatal("PlanRetention(per-job overflow) error = nil")
	}
	overflow[1].JobID = "019c5f8b-7c8a-7000-8000-000000000002"
	policy.MaxBytesPerJob = UnlimitedRetentionLimit()
	policy.MaxTotalBytes = RetentionLimit{Maximum: 1}
	if _, err := PlanRetention(now, overflow, policy); err == nil {
		t.Fatal("PlanRetention(total overflow) error = nil")
	}

	sortable := []RetentionCandidate{
		{JobID: testJobID, RunNumber: 2, CompletedAt: now, Active: true},
		{JobID: "019c5f8b-7c8a-7000-8000-000000000002", RunNumber: 1, CompletedAt: now},
		{JobID: testJobID, RunNumber: 1, CompletedAt: now},
	}
	sortRetentionOldestFirst(sortable)
	if sortable[0].JobID != testJobID || sortable[0].RunNumber != 1 || !sortable[2].Active {
		t.Fatalf("sortRetentionOldestFirst() = %+v", sortable)
	}
}

func TestAppendPropagatesRawAndIndexFailures(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	rawPath := filepath.Join(root, "raw")
	if err := os.WriteFile(rawPath, nil, fileMode); err != nil {
		t.Fatal(err)
	}
	raw, err := os.Open(rawPath)
	if err != nil {
		t.Fatal(err)
	}
	index, err := os.CreateTemp(root, "index")
	if err != nil {
		t.Fatal(err)
	}
	run := &Run{stdout: raw, stderr: raw, index: index, nextSequence: 1}
	if _, err := run.Append(Stdout, []byte("x"), time.Now()); err == nil {
		t.Fatal("Append(read-only raw file) error = nil")
	}
	if _, err := run.Append(Stdout, []byte("x"), time.Now()); err == nil {
		t.Fatal("Append(after write error) error = nil")
	}
	_ = raw.Close()
	_ = index.Close()

	raw, err = os.OpenFile(rawPath, os.O_WRONLY, fileMode)
	if err != nil {
		t.Fatal(err)
	}
	closedIndex, err := os.CreateTemp(root, "closed-index")
	if err != nil {
		t.Fatal(err)
	}
	_ = closedIndex.Close()
	run = &Run{stdout: raw, stderr: raw, index: closedIndex, nextSequence: 1}
	if _, err := run.Append(Stdout, []byte("x"), time.Now()); err == nil {
		t.Fatal("Append(closed index) error = nil")
	}
	_ = raw.Close()
}

func TestPublicCleanupFollowAndOpenValidation(t *testing.T) {
	t.Parallel()

	if _, err := CleanupRun(t.Context(), t.TempDir(), testJobID, 1, nil); !errors.Is(err, ErrCleanupIneligible) {
		t.Fatalf("CleanupRun(nil eligibility) error = %v", err)
	}
	if _, err := CleanupRun(t.Context(), "", testJobID, 1, alwaysEligible); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("CleanupRun(empty state directory) error = %v", err)
	}
	if err := ReleaseAbandonedRun(t.Context(), "", testJobID, 1, alwaysEligible); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("ReleaseAbandonedRun(empty state directory) error = %v", err)
	}
	if _, err := OpenRun("", testJobID, 1); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("OpenRun(empty state directory) error = %v", err)
	}
	if _, err := OpenRun(t.TempDir(), testJobID, 1); err == nil {
		t.Fatal("OpenRun(missing directories) error = nil")
	}

	stateDir := filepath.Join(t.TempDir(), "state")
	run, err := CreateRun(stateDir, testJobID, 1)
	if err != nil {
		t.Fatal(err)
	}
	appendBytes(t, run, Stdout, []byte("data"), time.Unix(1, 0))
	if err := ReleaseAbandonedRun(t.Context(), stateDir, testJobID, 1, func(context.Context) (bool, error) {
		return false, nil
	}); !errors.Is(err, ErrCleanupIneligible) {
		t.Fatalf("ReleaseAbandonedRun(ineligible) error = %v", err)
	}
	wantErr := errors.New("eligibility failed")
	if err := ReleaseAbandonedRun(t.Context(), stateDir, testJobID, 1, func(context.Context) (bool, error) {
		return false, wantErr
	}); !errors.Is(err, wantErr) {
		t.Fatalf("ReleaseAbandonedRun(eligibility error) error = %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := ReleaseAbandonedRun(ctx, stateDir, testJobID, 1, alwaysEligible); !errors.Is(err, context.Canceled) {
		t.Fatalf("ReleaseAbandonedRun(canceled) error = %v", err)
	}
	if err := run.Close(); err != nil {
		t.Fatal(err)
	}

	reader, err := OpenRun(stateDir, testJobID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.FollowStream(t.Context(), io.Discard, Stream(99), FollowOptions{}); !errors.Is(err, ErrInvalidStream) {
		t.Fatalf("FollowStream(invalid stream) error = %v", err)
	}
	if _, err := reader.FollowCombined(t.Context(), io.Discard, FollowOptions{PollInterval: -time.Second}); err == nil {
		t.Fatal("FollowCombined(negative poll interval) error = nil")
	}
	if _, err := reader.copyStreamGrowth(&alwaysFailWriter{err: wantErr}, Stdout, &streamCursor{segment: 1}); !errors.Is(err, wantErr) {
		t.Fatalf("copyStreamGrowth(destination failure) error = %v", err)
	}

	calls := 0
	if _, err := follow(t.Context(), FollowOptions{Complete: func(context.Context) (bool, error) {
		return true, nil
	}}, func() (int64, error) {
		calls++
		if calls == 2 {
			return 2, wantErr
		}
		return 1, nil
	}); !errors.Is(err, wantErr) {
		t.Fatalf("follow(final copy failure) error = %v", err)
	}
}

func TestAdditionalFilesystemIndexAndRetentionEdges(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	stateFile := filepath.Join(root, "state-file")
	if err := os.WriteFile(stateFile, nil, fileMode); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenRun(stateFile, testJobID, 1); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("OpenRun(file state root) error = %v", err)
	}
	if err := ensurePrivateDirectory(filepath.Join(stateFile, "child")); err == nil {
		t.Fatal("ensurePrivateDirectory(beneath file) error = nil")
	}
	directory := filepath.Join(root, "directory")
	if err := os.Mkdir(directory, directoryMode); err != nil {
		t.Fatal(err)
	}
	directoryLink := filepath.Join(root, "directory-link")
	if err := os.Symlink(directory, directoryLink); err != nil {
		t.Fatal(err)
	}
	if err := inspectPrivateDirectory(directoryLink); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("inspectPrivateDirectory(symlink) error = %v", err)
	}
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, fileMode); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(file, filepath.Join(root, "hard-link")); err != nil {
		t.Fatal(err)
	}
	if _, err := openPrivateRegularFile(file); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("openPrivateRegularFile(hard link) error = %v", err)
	}

	readErr := errors.New("read failed")
	if _, err := scanIndexFrom(&failingReader{err: readErr}, &indexState{nextSequence: 1}, nil); !errors.Is(err, readErr) {
		t.Fatalf("scanIndexFrom(read failure) error = %v", err)
	}
	state := indexState{
		status:       IndexStatus{IndexedStdoutBytes: math.MaxUint64},
		nextSequence: 1,
	}
	if err := state.accept(Chunk{Sequence: 1, Stream: Stdout, Length: 1}); !errors.Is(err, ErrCorruptIndex) {
		t.Fatalf("accept(indexed byte overflow) error = %v", err)
	}
	if err := waitForPoll(t.Context(), 0); err != nil {
		t.Fatalf("waitForPoll(ready timer) error = %v", err)
	}
	for _, name := range []string{"stdout.not-a-number.log", "stdout.000001.log", "stdout.000002.LOG"} {
		if _, ok := parseSegmentFilename("stdout", name); ok {
			t.Fatalf("parseSegmentFilename(%q) matched", name)
		}
	}

	now := time.Now().UTC()
	basePolicy := RetentionPolicy{
		MaxAge: UnlimitedRetentionAge(), MaxJobs: UnlimitedRetentionLimit(),
		MaxRunsPerJob: UnlimitedRetentionLimit(), MaxBytesPerJob: UnlimitedRetentionLimit(),
		MaxTotalBytes: UnlimitedRetentionLimit(),
	}
	badAgePolicy := basePolicy
	badAgePolicy.MaxAge.Maximum = time.Second
	if _, err := PlanRetention(now, nil, badAgePolicy); err == nil {
		t.Fatal("PlanRetention(unlimited age with maximum) error = nil")
	}
	for _, candidate := range []RetentionCandidate{
		{JobID: testJobID, RunNumber: 0, CompletedAt: now},
		{JobID: testJobID, RunNumber: 1},
	} {
		if _, err := PlanRetention(now, []RetentionCandidate{candidate}, basePolicy); err == nil {
			t.Fatalf("PlanRetention(%+v) error = nil", candidate)
		}
	}
}

type failingReader struct{ err error }

func (reader *failingReader) Read([]byte) (int, error) { return 0, reader.err }

type scriptedWriter struct {
	counts []int
	err    error
}

func (writer *scriptedWriter) Write(data []byte) (int, error) {
	if len(writer.counts) == 0 {
		return len(data), nil
	}
	count := writer.counts[0]
	writer.counts = writer.counts[1:]
	return count, writer.err
}

func fixRecordChecksum(record *[recordSize]byte) {
	binary.LittleEndian.PutUint32(record[48:52], crc32.Checksum(record[:48], indexChecksumTab))
}
