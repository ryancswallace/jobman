package logstore

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path/filepath"
)

const (
	cleanupSummaryFilename = ".cleanup-summary"
	cleanupSummarySize     = 24
)

var cleanupSummaryMagic = [4]byte{'J', 'M', 'C', 1}

// CleanupEligibility rechecks durable metadata before filesystem mutation. It
// should return true only for a completed, transactionally claimed cleanup.
type CleanupEligibility func(context.Context) (bool, error)

// CleanupResult reports the private filesystem content removed for one run.
type CleanupResult struct {
	Bytes uint64
	Files uint64
}

// CleanupRun atomically moves an eligible, inactive run out of its canonical
// location and removes only recognized private regular files. Unknown entries,
// links, active ownership, and containment failures stop cleanup fail-closed.
// An interrupted cleanup can be resumed by calling CleanupRun again.
func CleanupRun(
	ctx context.Context,
	stateDir string,
	jobID string,
	runNumber uint64,
	eligible CleanupEligibility,
) (CleanupResult, error) {
	if eligible == nil {
		return CleanupResult{}, ErrCleanupIneligible
	}
	paths, parentDirs, pathErr := pathsForRun(stateDir, jobID, runNumber)
	if pathErr != nil {
		return CleanupResult{}, pathErr
	}
	for _, dir := range parentDirs {
		if directoryErr := inspectPrivateDirectory(dir); directoryErr != nil {
			return CleanupResult{}, directoryErr
		}
	}
	if eligibilityErr := requireCleanupEligibility(ctx, eligible); eligibilityErr != nil {
		return CleanupResult{}, eligibilityErr
	}

	claim, claimErr := claimCleanup(ctx, paths.Directory, eligible)
	if claimErr != nil {
		return CleanupResult{}, claimErr
	}

	return removeCleanupClaim(claim)
}

// FinalizeCleanupRun removes the empty durable cleanup claim after its pruning
// result has committed to the metadata store. It is idempotent so a later
// cleanup can finish the handoff after a client crash.
func FinalizeCleanupRun(stateDir, jobID string, runNumber uint64) error {
	paths, parentDirs, pathErr := pathsForRun(stateDir, jobID, runNumber)
	if pathErr != nil {
		return pathErr
	}
	for _, dir := range parentDirs {
		if directoryErr := inspectPrivateDirectory(dir); directoryErr != nil {
			return directoryErr
		}
	}
	tombstone := paths.Directory + ".deleting"
	if _, err := os.Lstat(tombstone); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("inspect cleanup tombstone %q: %w", tombstone, err)
	}
	if _, entries, err := inspectCleanupDirectory(tombstone, true); err != nil {
		return err
	} else if len(entries) != 0 {
		return fmt.Errorf("%w: cleanup tombstone %q still contains log files", ErrUnsafePath, tombstone)
	}
	summaryPath := filepath.Join(tombstone, cleanupSummaryFilename)
	if _, err := os.Lstat(summaryPath); err == nil {
		if _, readErr := readCleanupSummary(summaryPath); readErr != nil {
			return readErr
		}
		if removeErr := removeFile("cleanup summary", summaryPath); removeErr != nil {
			return removeErr
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect cleanup summary %q: %w", summaryPath, err)
	}
	if err := os.Remove(tombstone); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove empty cleanup tombstone %q: %w", tombstone, err)
	}

	return nil
}

// ReleaseAbandonedRun removes a conservative ownership marker only after an
// external metadata check proves that no supervisor or target owns the run.
// Normal capture calls never need this; Run.Close releases ownership itself.
func ReleaseAbandonedRun(
	ctx context.Context,
	stateDir string,
	jobID string,
	runNumber uint64,
	inactive CleanupEligibility,
) error {
	if inactive == nil {
		return ErrCleanupIneligible
	}
	paths, parentDirs, pathErr := pathsForRun(stateDir, jobID, runNumber)
	if pathErr != nil {
		return pathErr
	}
	for _, dir := range append(parentDirs, paths.Directory) {
		if directoryErr := inspectPrivateDirectory(dir); directoryErr != nil {
			return directoryErr
		}
	}
	marker, openErr := openPrivateRegularFile(paths.Active)
	if openErr != nil {
		if errors.Is(openErr, os.ErrNotExist) {
			return nil
		}

		return openErr
	}
	before, statErr := marker.Stat()
	closeErr := marker.Close()
	if markerErr := errors.Join(statErr, closeErr); markerErr != nil {
		return fmt.Errorf("inspect active log marker: %w", markerErr)
	}
	if eligibilityErr := requireCleanupEligibility(ctx, inactive); eligibilityErr != nil {
		return eligibilityErr
	}
	current, currentErr := os.Lstat(paths.Active)
	if currentErr != nil {
		return fmt.Errorf("reinspect active log marker: %w", currentErr)
	}
	if !os.SameFile(before, current) || !current.Mode().IsRegular() {
		return fmt.Errorf("%w: active log marker changed before release", ErrUnsafePath)
	}

	return removeFile("abandoned active log marker", paths.Active)
}

type cleanupEntry struct {
	info os.FileInfo
	name string
}

type cleanupClaim struct {
	path    string
	entries []cleanupEntry
	result  CleanupResult
}

func claimCleanup(
	ctx context.Context,
	runDirectory string,
	eligible CleanupEligibility,
) (cleanupClaim, error) {
	tombstone := runDirectory + ".deleting"
	source, alreadyMoved, sourceErr := cleanupSource(runDirectory, tombstone)
	if sourceErr != nil {
		return cleanupClaim{}, sourceErr
	}
	before, entries, inspectErr := inspectCleanupDirectory(source, alreadyMoved)
	if inspectErr != nil {
		return cleanupClaim{}, inspectErr
	}
	if identityErr := primeCleanupIdentities(before, entries); identityErr != nil {
		return cleanupClaim{}, identityErr
	}
	if activeErr := rejectActiveMarker(source); activeErr != nil {
		return cleanupClaim{}, activeErr
	}
	if eligibilityErr := requireCleanupEligibility(ctx, eligible); eligibilityErr != nil {
		return cleanupClaim{}, eligibilityErr
	}

	claimedPath, moveErr := moveCleanupDirectory(source, tombstone, alreadyMoved)
	if moveErr != nil {
		return cleanupClaim{}, moveErr
	}
	after, statErr := os.Lstat(claimedPath)
	if statErr != nil {
		return cleanupClaim{}, fmt.Errorf("inspect claimed cleanup directory %q: %w", claimedPath, statErr)
	}
	if !os.SameFile(before, after) || !after.IsDir() || after.Mode()&os.ModeSymlink != 0 {
		return cleanupClaim{}, fmt.Errorf("%w: run log directory changed during cleanup", ErrUnsafePath)
	}
	result, summaryErr := ensureCleanupSummary(claimedPath, entries)
	if summaryErr != nil {
		return cleanupClaim{}, summaryErr
	}

	return cleanupClaim{path: claimedPath, entries: entries, result: result}, nil
}

func moveCleanupDirectory(source, tombstone string, alreadyMoved bool) (string, error) {
	if alreadyMoved {
		return source, nil
	}
	_, statErr := os.Lstat(tombstone)
	if statErr == nil {
		return "", fmt.Errorf("%w: cleanup tombstone %q already exists", ErrUnsafePath, tombstone)
	}
	if !errors.Is(statErr, os.ErrNotExist) {
		return "", fmt.Errorf("inspect cleanup tombstone %q: %w", tombstone, statErr)
	}
	if activeErr := rejectActiveMarker(source); activeErr != nil {
		return "", activeErr
	}
	if renameErr := os.Rename(source, tombstone); renameErr != nil {
		return "", fmt.Errorf("claim run logs for cleanup: %w", renameErr)
	}

	return tombstone, nil
}

func removeCleanupClaim(claim cleanupClaim) (CleanupResult, error) {
	for _, entry := range claim.entries {
		removeErr := removeCleanupEntry(claim.path, entry)
		if removeErr != nil {
			return CleanupResult{}, removeErr
		}
	}

	return claim.result, nil
}

func ensureCleanupSummary(directory string, entries []cleanupEntry) (CleanupResult, error) {
	path := filepath.Join(directory, cleanupSummaryFilename)
	if _, err := os.Lstat(path); err == nil {
		result, readErr := readCleanupSummary(path)
		if readErr == nil {
			return result, nil
		}
		// Removal never begins until a complete summary has been synced and
		// closed. A partial summary with original entries still present is
		// therefore a recoverable process-crash boundary.
		if len(entries) == 0 {
			return CleanupResult{}, readErr
		}
		if removeErr := removeFile("incomplete cleanup summary", path); removeErr != nil {
			return CleanupResult{}, errors.Join(readErr, removeErr)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return CleanupResult{}, fmt.Errorf("inspect cleanup summary %q: %w", path, err)
	}
	result := CleanupResult{}
	for _, entry := range entries {
		size, err := nonnegativeInt64ToUint64(entry.info.Size())
		if err != nil {
			return CleanupResult{}, err
		}
		if result.Bytes > ^uint64(0)-size {
			return CleanupResult{}, errors.New("cleaned log byte count overflows")
		}
		result.Bytes += size
		result.Files++
	}
	encoded := encodeCleanupSummary(result)
	file, err := createPrivateFile(path)
	if err != nil {
		return CleanupResult{}, err
	}
	_, writeErr := writeAll(file, encoded[:])
	syncErr := file.Sync()
	closeErr := file.Close()
	if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
		return CleanupResult{}, errors.Join(fmt.Errorf("persist cleanup summary: %w", err), os.Remove(path))
	}

	return result, nil
}

func encodeCleanupSummary(result CleanupResult) [cleanupSummarySize]byte {
	var encoded [cleanupSummarySize]byte
	copy(encoded[:4], cleanupSummaryMagic[:])
	binary.LittleEndian.PutUint64(encoded[4:12], result.Files)
	binary.LittleEndian.PutUint64(encoded[12:20], result.Bytes)
	binary.LittleEndian.PutUint32(encoded[20:24], crc32.Checksum(encoded[:20], indexChecksumTab))

	return encoded
}

func readCleanupSummary(path string) (CleanupResult, error) {
	file, err := openPrivateRegularFile(path)
	if err != nil {
		return CleanupResult{}, err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, cleanupSummarySize+1))
	closeErr := file.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return CleanupResult{}, fmt.Errorf("read cleanup summary: %w", err)
	}
	if len(data) != cleanupSummarySize || !bytes.Equal(data[:4], cleanupSummaryMagic[:]) ||
		binary.LittleEndian.Uint32(data[20:24]) != crc32.Checksum(data[:20], indexChecksumTab) {
		return CleanupResult{}, fmt.Errorf("%w: invalid cleanup summary %q", ErrUnsafePath, path)
	}

	return CleanupResult{
		Files: binary.LittleEndian.Uint64(data[4:12]),
		Bytes: binary.LittleEndian.Uint64(data[12:20]),
	}, nil
}

func removeCleanupEntry(directory string, entry cleanupEntry) error {
	path := filepath.Join(directory, entry.name)
	current, statErr := os.Lstat(path)
	if statErr != nil {
		return fmt.Errorf("reinspect cleanup file %q: %w", path, statErr)
	}
	if !os.SameFile(entry.info, current) || !current.Mode().IsRegular() || current.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: cleanup file %q changed before removal", ErrUnsafePath, path)
	}
	if linkErr := validateSingleLink(path, current); linkErr != nil {
		return linkErr
	}
	if removeErr := os.Remove(path); removeErr != nil {
		return fmt.Errorf("remove run log file %q: %w", path, removeErr)
	}

	return nil
}

func cleanupSource(runDirectory, tombstone string) (source string, alreadyMoved bool, err error) {
	_, runErr := os.Lstat(runDirectory)
	_, tombstoneErr := os.Lstat(tombstone)
	if runErr == nil && tombstoneErr == nil {
		return "", false, fmt.Errorf("%w: run directory and cleanup tombstone both exist", ErrUnsafePath)
	}
	if runErr == nil {
		return runDirectory, false, nil
	}
	if !errors.Is(runErr, os.ErrNotExist) {
		return "", false, fmt.Errorf("inspect run log directory %q: %w", runDirectory, runErr)
	}
	if tombstoneErr == nil {
		return tombstone, true, nil
	}
	if !errors.Is(tombstoneErr, os.ErrNotExist) {
		return "", false, fmt.Errorf("inspect cleanup tombstone %q: %w", tombstone, tombstoneErr)
	}

	return "", false, fmt.Errorf("inspect run log directory %q: %w", runDirectory, os.ErrNotExist)
}

func inspectCleanupDirectory(path string, allowSummary bool) (os.FileInfo, []cleanupEntry, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, nil, fmt.Errorf("inspect cleanup directory %q: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, nil, fmt.Errorf("%w: %q is not a real directory", ErrUnsafePath, path)
	}
	if modeErr := validatePrivateMode(path, info, directoryMode); modeErr != nil {
		return nil, nil, modeErr
	}

	directoryEntries, err := os.ReadDir(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read cleanup directory %q: %w", path, err)
	}
	entries := make([]cleanupEntry, 0, len(directoryEntries))
	for _, entry := range directoryEntries {
		if entry.Name() == cleanupSummaryFilename && allowSummary {
			continue
		}
		if !isRecognizedLogFilename(entry.Name()) {
			return nil, nil, fmt.Errorf("%w: unrecognized run log entry %q", ErrUnsafePath, entry.Name())
		}
		entryPath := filepath.Join(path, entry.Name())
		entryInfo, entryStatErr := os.Lstat(entryPath)
		if entryStatErr != nil {
			return nil, nil, fmt.Errorf("inspect cleanup file %q: %w", entryPath, entryStatErr)
		}
		if entryInfo.Mode()&os.ModeSymlink != 0 || !entryInfo.Mode().IsRegular() {
			return nil, nil, fmt.Errorf("%w: cleanup entry %q is not a regular file", ErrUnsafePath, entryPath)
		}
		if modeErr := validatePrivateMode(entryPath, entryInfo, fileMode); modeErr != nil {
			return nil, nil, modeErr
		}
		if linkErr := validateSingleLink(entryPath, entryInfo); linkErr != nil {
			return nil, nil, linkErr
		}
		entries = append(entries, cleanupEntry{name: entry.Name(), info: entryInfo})
	}

	return info, entries, nil
}

func isRecognizedLogFilename(name string) bool {
	if name == stdoutFilename || name == stderrFilename || name == indexFilename || name == activeFilename {
		return true
	}
	_, stdoutSegment := parseSegmentFilename(Stdout.String(), name)
	_, stderrSegment := parseSegmentFilename(Stderr.String(), name)

	return stdoutSegment || stderrSegment
}

func rejectActiveMarker(directory string) error {
	marker := filepath.Join(directory, activeFilename)
	_, err := os.Lstat(marker)
	if err == nil {
		return ErrActiveRun
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect active log marker %q: %w", marker, err)
	}

	return nil
}

func requireCleanupEligibility(ctx context.Context, eligible CleanupEligibility) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	allowed, err := eligible(ctx)
	if err != nil {
		return fmt.Errorf("check run log cleanup eligibility: %w", err)
	}
	if !allowed {
		return ErrCleanupIneligible
	}

	return nil
}
