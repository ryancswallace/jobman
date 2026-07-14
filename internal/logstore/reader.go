package logstore

import (
	"errors"
	"fmt"
	"io"
	"os"
)

// Reader opens raw streams and the chunk index for one existing run.
type Reader struct {
	paths Paths
}

// OpenRun validates and opens the path metadata for an existing run.
func OpenRun(stateDir, jobID string, runNumber uint64) (*Reader, error) {
	paths, parentDirs, err := pathsForRun(stateDir, jobID, runNumber)
	if err != nil {
		return nil, err
	}
	for _, dir := range append(parentDirs, paths.Directory) {
		if err := inspectPrivateDirectory(dir); err != nil {
			return nil, err
		}
	}

	for _, path := range []string{paths.Stdout, paths.Stderr, paths.Index} {
		file, err := openPrivateRegularFile(path)
		if err != nil {
			return nil, err
		}
		if err := file.Close(); err != nil {
			return nil, fmt.Errorf("close validated log file %q: %w", path, err)
		}
	}

	return &Reader{paths: paths}, nil
}

// Paths returns the canonical path set for this reader.
func (reader *Reader) Paths() Paths {
	return reader.paths
}

// OpenStream opens one authoritative raw stream at its beginning.
func (reader *Reader) OpenStream(stream Stream) (io.ReadCloser, error) {
	path, err := reader.streamPath(stream)
	if err != nil {
		return nil, err
	}

	return openPrivateRegularFile(path)
}

// CopyStream copies one authoritative raw stream without text conversion.
func (reader *Reader) CopyStream(destination io.Writer, stream Stream) (int64, error) {
	source, err := reader.OpenStream(stream)
	if err != nil {
		return 0, err
	}

	written, copyErr := io.Copy(destination, source)
	closeErr := source.Close()
	if copyErr != nil {
		copyErr = fmt.Errorf("copy %s log: %w", stream, copyErr)
	}
	if closeErr != nil {
		closeErr = fmt.Errorf("close %s log: %w", stream, closeErr)
	}

	return written, errors.Join(copyErr, closeErr)
}

// ScanIndex validates every complete index record and visits its valid prefix.
// A partial final record is reported through IndexStatus and is not an error.
func (reader *Reader) ScanIndex(visit func(Chunk) error) (IndexStatus, error) {
	index, err := openPrivateRegularFile(reader.paths.Index)
	if err != nil {
		return IndexStatus{}, err
	}

	status, scanErr := scanIndex(index, visit)
	closeErr := index.Close()
	if scanErr != nil {
		return status, errors.Join(scanErr, closeErr)
	}
	if closeErr != nil {
		return status, fmt.Errorf("close log chunk index: %w", closeErr)
	}

	if err := reader.addRawTailStatus(&status); err != nil {
		return status, err
	}

	return status, nil
}

// CopyCombined validates the complete index snapshot before copying chunks in
// their observed order. Raw bytes beyond the valid index prefix are reported
// as unindexed and are intentionally omitted because their ordering is unknown.
func (reader *Reader) CopyCombined(destination io.Writer) (int64, IndexStatus, error) {
	status, err := reader.ScanIndex(nil)
	if err != nil {
		return 0, status, err
	}

	stdout, err := openPrivateRegularFile(reader.paths.Stdout)
	if err != nil {
		return 0, status, err
	}
	stderr, err := openPrivateRegularFile(reader.paths.Stderr)
	if err != nil {
		return 0, status, errors.Join(err, stdout.Close())
	}
	index, err := openPrivateRegularFile(reader.paths.Index)
	if err != nil {
		return 0, status, errors.Join(err, stdout.Close(), stderr.Close())
	}

	written, copyErr := copyIndexedPrefix(destination, stdout, stderr, index, status.Records)
	closeErr := errors.Join(stdout.Close(), stderr.Close(), index.Close())
	if copyErr != nil {
		copyErr = fmt.Errorf("copy combined log: %w", copyErr)
	}
	if closeErr != nil {
		closeErr = fmt.Errorf("close combined log files: %w", closeErr)
	}

	return written, status, errors.Join(copyErr, closeErr)
}

func copyIndexedPrefix(
	destination io.Writer,
	stdout *os.File,
	stderr *os.File,
	index *os.File,
	records uint64,
) (int64, error) {
	var written int64
	for range records {
		var encoded [recordSize]byte
		if _, err := io.ReadFull(index, encoded[:]); err != nil {
			return written, fmt.Errorf("reread validated log chunk index: %w", err)
		}
		chunk, err := decodeRecord(encoded)
		if err != nil {
			return written, err
		}

		source := stdout
		if chunk.Stream == Stderr {
			source = stderr
		}
		offset, err := uint64ToInt64(chunk.Offset)
		if err != nil {
			return written, fmt.Errorf("%w: chunk %d offset: %w", ErrCorruptIndex, chunk.Sequence, err)
		}
		length, err := uint64ToInt64(chunk.Length)
		if err != nil {
			return written, fmt.Errorf("%w: chunk %d length: %w", ErrCorruptIndex, chunk.Sequence, err)
		}
		section := io.NewSectionReader(source, offset, length)
		count, err := io.Copy(destination, section)
		written += count
		if err != nil {
			return written, fmt.Errorf("copy %s chunk %d: %w", chunk.Stream, chunk.Sequence, err)
		}
		if count != length {
			return written, fmt.Errorf(
				"%w: %s chunk %d refers to %d bytes but only %d exist",
				ErrCorruptIndex,
				chunk.Stream,
				chunk.Sequence,
				chunk.Length,
				count,
			)
		}
	}

	return written, nil
}

func (reader *Reader) addRawTailStatus(status *IndexStatus) error {
	stdoutSize, err := privateRegularFileSize(reader.paths.Stdout)
	if err != nil {
		return err
	}
	stderrSize, err := privateRegularFileSize(reader.paths.Stderr)
	if err != nil {
		return err
	}

	if stdoutSize < status.IndexedStdoutBytes {
		return fmt.Errorf(
			"%w: stdout index covers %d bytes but file has %d",
			ErrCorruptIndex,
			status.IndexedStdoutBytes,
			stdoutSize,
		)
	}
	if stderrSize < status.IndexedStderrBytes {
		return fmt.Errorf(
			"%w: stderr index covers %d bytes but file has %d",
			ErrCorruptIndex,
			status.IndexedStderrBytes,
			stderrSize,
		)
	}

	status.UnindexedStdoutBytes = stdoutSize - status.IndexedStdoutBytes
	status.UnindexedStderrBytes = stderrSize - status.IndexedStderrBytes

	return nil
}

func (reader *Reader) streamPath(stream Stream) (string, error) {
	if err := validateStream(stream); err != nil {
		return "", err
	}
	if stream == Stdout {
		return reader.paths.Stdout, nil
	}

	return reader.paths.Stderr, nil
}

func inspectPrivateDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect private directory %q: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("%w: %q is not a real directory", ErrUnsafePath, path)
	}

	return validatePrivateMode(path, info, directoryMode)
}

func openPrivateRegularFile(path string) (*os.File, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect private log file %q: %w", path, err)
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: %q is not a regular file", ErrUnsafePath, path)
	}
	if modeErr := validatePrivateMode(path, before, fileMode); modeErr != nil {
		return nil, modeErr
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open private log file %q: %w", path, err)
	}
	after, err := file.Stat()
	if err != nil {
		_ = file.Close()

		return nil, fmt.Errorf("inspect opened log file %q: %w", path, err)
	}
	if !os.SameFile(before, after) || !after.Mode().IsRegular() {
		_ = file.Close()

		return nil, fmt.Errorf("%w: log file %q changed while opening", ErrUnsafePath, path)
	}

	return file, nil
}

func privateRegularFileSize(path string) (uint64, error) {
	file, err := openPrivateRegularFile(path)
	if err != nil {
		return 0, err
	}
	info, statErr := file.Stat()
	closeErr := file.Close()
	if statErr != nil {
		return 0, errors.Join(fmt.Errorf("stat private log file %q: %w", path, statErr), closeErr)
	}
	size, conversionErr := nonnegativeInt64ToUint64(info.Size())
	if conversionErr != nil {
		return 0, errors.Join(
			fmt.Errorf("%w: invalid file size for %q: %w", ErrUnsafePath, path, conversionErr),
			closeErr,
		)
	}
	if closeErr != nil {
		return 0, fmt.Errorf("close private log file %q: %w", path, closeErr)
	}

	return size, nil
}
