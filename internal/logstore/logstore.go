// Package logstore captures and reads the raw output streams of a managed run.
package logstore

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	directoryMode = 0o700
	fileMode      = 0o600

	stdoutFilename = "stdout.log"
	stderrFilename = "stderr.log"
	indexFilename  = "chunks.idx"
)

var (
	// ErrClosed indicates that an operation requires an open run capture.
	ErrClosed = errors.New("log capture is closed")
	// ErrInvalidStream indicates that a stream is neither stdout nor stderr.
	ErrInvalidStream = errors.New("invalid log stream")
	// ErrUnsafePath indicates that a log path could escape or traverse the state root.
	ErrUnsafePath = errors.New("unsafe log path")
	// ErrCorruptIndex indicates that a complete chunk-index record is invalid.
	ErrCorruptIndex = errors.New("corrupt log chunk index")
)

// Stream identifies one raw target output stream.
type Stream uint8

const (
	// Stdout identifies the target's standard output.
	Stdout Stream = 1
	// Stderr identifies the target's standard error.
	Stderr Stream = 2
)

// String returns the stable display name of a stream.
func (stream Stream) String() string {
	switch stream {
	case Stdout:
		return "stdout"
	case Stderr:
		return "stderr"
	default:
		return "unknown"
	}
}

// Paths contains the canonical files belonging to one run.
type Paths struct {
	Directory string
	Stdout    string
	Stderr    string
	Index     string
}

// Run serializes capture of stdout, stderr, and their observed chunk order.
// It is safe for concurrent use by one stdout writer and one stderr writer.
type Run struct {
	mu sync.Mutex

	paths  Paths
	stdout *os.File
	stderr *os.File
	index  *os.File

	nextSequence uint64
	stdoutOffset uint64
	stderrOffset uint64
	writeErr     error
	closed       bool
}

// CreateRun exclusively creates a private log directory for a positive run number.
func CreateRun(stateDir, jobID string, runNumber uint64) (*Run, error) {
	paths, parentDirs, err := pathsForRun(stateDir, jobID, runNumber)
	if err != nil {
		return nil, err
	}

	for _, dir := range parentDirs {
		if err := ensurePrivateDirectory(dir); err != nil {
			return nil, err
		}
	}

	if err := os.Mkdir(paths.Directory, directoryMode); err != nil {
		return nil, fmt.Errorf("create run log directory %q: %w", paths.Directory, err)
	}

	run := &Run{
		paths:        paths,
		nextSequence: 1,
	}
	if err := run.createFiles(); err != nil {
		return nil, errors.Join(err, run.abortCreate(), rollbackRunDirectory(paths))
	}

	return run, nil
}

func (run *Run) abortCreate() error {
	return errors.Join(
		closeFile("incomplete stdout log", run.stdout),
		closeFile("incomplete stderr log", run.stderr),
		closeFile("incomplete log chunk index", run.index),
	)
}

func (run *Run) createFiles() error {
	var err error
	run.stdout, err = createPrivateFile(run.paths.Stdout)
	if err != nil {
		return err
	}

	run.stderr, err = createPrivateFile(run.paths.Stderr)
	if err != nil {
		return err
	}

	run.index, err = createPrivateFile(run.paths.Index)
	if err != nil {
		return err
	}

	return nil
}

// Paths returns the canonical path set for this run.
func (run *Run) Paths() Paths {
	return run.paths
}

// Append writes bytes to their raw stream before recording their observed order.
// A non-nil error after a positive byte count means those raw bytes may be an
// unindexed tail. Further appends are rejected so a torn index cannot acquire a
// valid-looking suffix.
func (run *Run) Append(stream Stream, data []byte, observedAt time.Time) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	if err := validateStream(stream); err != nil {
		return 0, err
	}

	run.mu.Lock()
	defer run.mu.Unlock()

	if run.closed {
		return 0, ErrClosed
	}
	if run.writeErr != nil {
		return 0, run.writeErr
	}

	written := 0
	for len(data) > 0 {
		chunkLength := min(len(data), MaxChunkSize)
		count, err := run.appendChunk(stream, data[:chunkLength], observedAt)
		written += count
		data = data[count:]
		if err != nil {
			run.writeErr = err

			return written, err
		}
	}

	return written, nil
}

func (run *Run) appendChunk(stream Stream, data []byte, observedAt time.Time) (int, error) {
	file, offset := run.streamFileAndOffset(stream)
	written, err := writeAll(file, data)
	writtenLength, conversionErr := boundedIntToUint64(written)
	if conversionErr != nil {
		return written, conversionErr
	}
	if written > 0 {
		run.advanceOffset(stream, writtenLength)
	}
	if err != nil {
		return written, fmt.Errorf("append %s log: %w", stream, err)
	}
	if syncErr := file.Sync(); syncErr != nil {
		return written, fmt.Errorf("sync %s log: %w", stream, syncErr)
	}
	record := Chunk{
		Sequence:   run.nextSequence,
		Stream:     stream,
		Offset:     offset,
		Length:     writtenLength,
		ObservedAt: observedAt,
	}
	encoded, err := encodeRecord(record)
	if err != nil {
		return written, err
	}
	if _, err := writeAll(run.index, encoded[:]); err != nil {
		return written, fmt.Errorf("append log chunk index: %w", err)
	}
	if err := run.index.Sync(); err != nil {
		return written, fmt.Errorf("sync log chunk index: %w", err)
	}

	run.nextSequence++

	return written, nil
}

func (run *Run) streamFileAndOffset(stream Stream) (file *os.File, offset uint64) {
	if stream == Stdout {
		return run.stdout, run.stdoutOffset
	}

	return run.stderr, run.stderrOffset
}

func (run *Run) advanceOffset(stream Stream, length uint64) {
	if stream == Stdout {
		run.stdoutOffset += length

		return
	}

	run.stderrOffset += length
}

// Writer returns an io.Writer that timestamps each observed write with time.Now.
func (run *Run) Writer(stream Stream) (io.Writer, error) {
	if err := validateStream(stream); err != nil {
		return nil, err
	}

	return &streamWriter{run: run, stream: stream, now: time.Now}, nil
}

type streamWriter struct {
	run    *Run
	stream Stream
	now    func() time.Time
}

func (writer *streamWriter) Write(data []byte) (int, error) {
	return writer.run.Append(writer.stream, data, writer.now())
}

// Sync flushes both raw streams and the chunk index.
func (run *Run) Sync() error {
	run.mu.Lock()
	defer run.mu.Unlock()

	if run.closed {
		return ErrClosed
	}

	return errors.Join(
		syncFile("stdout log", run.stdout),
		syncFile("stderr log", run.stderr),
		syncFile("log chunk index", run.index),
	)
}

// Close flushes and closes all files. It is safe to call more than once.
func (run *Run) Close() error {
	run.mu.Lock()
	defer run.mu.Unlock()

	if run.closed {
		return nil
	}
	run.closed = true

	return errors.Join(
		run.writeErr,
		syncFile("stdout log", run.stdout),
		syncFile("stderr log", run.stderr),
		syncFile("log chunk index", run.index),
		closeFile("stdout log", run.stdout),
		closeFile("stderr log", run.stderr),
		closeFile("log chunk index", run.index),
	)
}

func validateStream(stream Stream) error {
	if stream != Stdout && stream != Stderr {
		return fmt.Errorf("%w: %d", ErrInvalidStream, stream)
	}

	return nil
}

func pathsForRun(stateDir, jobID string, runNumber uint64) (Paths, []string, error) {
	if stateDir == "" {
		return Paths{}, nil, fmt.Errorf("%w: state directory is empty", ErrUnsafePath)
	}
	if err := validateJobID(jobID); err != nil {
		return Paths{}, nil, err
	}
	if runNumber == 0 {
		return Paths{}, nil, fmt.Errorf("%w: run number must be positive", ErrUnsafePath)
	}

	stateRoot, err := filepath.Abs(stateDir)
	if err != nil {
		return Paths{}, nil, fmt.Errorf("resolve state directory %q: %w", stateDir, err)
	}
	stateRoot = filepath.Clean(stateRoot)
	logsDir := filepath.Join(stateRoot, "logs")
	jobDir := filepath.Join(logsDir, jobID)
	runDir := filepath.Join(jobDir, strconv.FormatUint(runNumber, 10))
	if !isWithin(stateRoot, runDir) {
		return Paths{}, nil, fmt.Errorf("%w: derived run directory escapes state root", ErrUnsafePath)
	}

	return Paths{
		Directory: runDir,
		Stdout:    filepath.Join(runDir, stdoutFilename),
		Stderr:    filepath.Join(runDir, stderrFilename),
		Index:     filepath.Join(runDir, indexFilename),
	}, []string{stateRoot, logsDir, jobDir}, nil
}

func validateJobID(jobID string) error {
	if jobID == "" {
		return fmt.Errorf("%w: job ID is empty", ErrUnsafePath)
	}
	for _, char := range jobID {
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '-' {
			continue
		}

		return fmt.Errorf("%w: job ID contains an invalid character", ErrUnsafePath)
	}

	return nil
}

func isWithin(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}

	return relative != ".." && relative != "." && relative != "" &&
		!filepath.IsAbs(relative) &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func ensurePrivateDirectory(path string) error {
	err := os.Mkdir(path, directoryMode)
	if err != nil && !errors.Is(err, os.ErrExist) {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("create private directory %q: %w", path, err)
		}
		if mkdirErr := os.MkdirAll(path, directoryMode); mkdirErr != nil {
			return fmt.Errorf("create private directory %q: %w", path, mkdirErr)
		}
	}

	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect private directory %q: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("%w: %q is not a real directory", ErrUnsafePath, path)
	}
	if err := validatePrivateMode(path, info, directoryMode); err != nil {
		return err
	}

	return nil
}

func boundedIntToUint64(value int) (uint64, error) {
	if value < 0 || value > MaxChunkSize {
		return 0, fmt.Errorf("log chunk length %d is outside 0..%d", value, MaxChunkSize)
	}

	return uint64(value), nil
}

func nonnegativeInt64ToUint64(value int64) (uint64, error) {
	if value < 0 {
		return 0, fmt.Errorf("negative file size %d", value)
	}

	return uint64(value), nil
}

func uint64ToInt64(value uint64) (int64, error) {
	if value > ^uint64(0)>>1 {
		return 0, fmt.Errorf("value %d exceeds the supported file offset", value)
	}

	return int64(value), nil
}

func createPrivateFile(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, fileMode)
	if err != nil {
		return nil, fmt.Errorf("create private log file %q: %w", path, err)
	}

	return file, nil
}

func rollbackRunDirectory(paths Paths) error {
	var errs []error
	for _, path := range []string{paths.Stdout, paths.Stderr, paths.Index} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, fmt.Errorf("remove incomplete log file %q: %w", path, err))
		}
	}
	if err := os.Remove(paths.Directory); err != nil && !errors.Is(err, os.ErrNotExist) {
		errs = append(errs, fmt.Errorf("remove incomplete run log directory %q: %w", paths.Directory, err))
	}

	return errors.Join(errs...)
}

func writeAll(writer io.Writer, data []byte) (int, error) {
	total := 0
	for len(data) > 0 {
		count, err := writer.Write(data)
		total += count
		data = data[count:]
		if err != nil {
			return total, err
		}
		if count == 0 {
			return total, io.ErrShortWrite
		}
	}

	return total, nil
}

func syncFile(name string, file *os.File) error {
	if file == nil {
		return nil
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync %s: %w", name, err)
	}

	return nil
}

func closeFile(name string, file *os.File) error {
	if file == nil {
		return nil
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close %s: %w", name, err)
	}

	return nil
}
