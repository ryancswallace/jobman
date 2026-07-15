package logstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

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
	before, entries, inspectErr := inspectCleanupDirectory(source)
	if inspectErr != nil {
		return cleanupClaim{}, inspectErr
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

	return cleanupClaim{path: claimedPath, entries: entries}, nil
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
	result := CleanupResult{}
	for _, entry := range claim.entries {
		size, removeErr := removeCleanupEntry(claim.path, entry)
		if removeErr != nil {
			return result, removeErr
		}
		if result.Bytes > ^uint64(0)-size {
			return result, errors.New("cleaned log byte count overflows")
		}
		result.Bytes += size
		result.Files++
	}
	if removeErr := os.Remove(claim.path); removeErr != nil {
		return result, fmt.Errorf("remove empty run log directory %q: %w", claim.path, removeErr)
	}

	return result, nil
}

func removeCleanupEntry(directory string, entry cleanupEntry) (uint64, error) {
	path := filepath.Join(directory, entry.name)
	current, statErr := os.Lstat(path)
	if statErr != nil {
		return 0, fmt.Errorf("reinspect cleanup file %q: %w", path, statErr)
	}
	if !os.SameFile(entry.info, current) || !current.Mode().IsRegular() || current.Mode()&os.ModeSymlink != 0 {
		return 0, fmt.Errorf("%w: cleanup file %q changed before removal", ErrUnsafePath, path)
	}
	if linkErr := validateSingleLink(path, current); linkErr != nil {
		return 0, linkErr
	}
	size, conversionErr := nonnegativeInt64ToUint64(current.Size())
	if conversionErr != nil {
		return 0, conversionErr
	}
	if removeErr := os.Remove(path); removeErr != nil {
		return 0, fmt.Errorf("remove run log file %q: %w", path, removeErr)
	}

	return size, nil
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

func inspectCleanupDirectory(path string) (os.FileInfo, []cleanupEntry, error) {
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
