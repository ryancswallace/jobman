package logstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"
)

const (
	// DefaultFollowPollInterval bounds fallback polling without busy-waiting.
	DefaultFollowPollInterval = 100 * time.Millisecond
)

// FollowOptions controls active or completed log following. Complete must only
// return true after the capture writer has finalized. A nil Complete function
// follows until the context is canceled.
type FollowOptions struct {
	PollInterval time.Duration
	Complete     func(context.Context) (bool, error)
}

// FollowStream copies current and newly-appended raw bytes across rotations.
// It returns context cancellation as an error so callers can distinguish an
// interrupted follow from normal completion.
func (reader *Reader) FollowStream(
	ctx context.Context,
	destination io.Writer,
	stream Stream,
	options FollowOptions,
) (int64, error) {
	if err := validateStream(stream); err != nil {
		return 0, err
	}
	cursor := streamCursor{segment: 1}

	return follow(ctx, options, func() (int64, error) {
		return reader.copyStreamGrowth(destination, stream, &cursor)
	})
}

// FollowCombined copies indexed chunks in observed order as they become
// durable. Unindexed raw tails remain available through FollowStream and are
// never assigned an invented cross-stream order.
func (reader *Reader) FollowCombined(
	ctx context.Context,
	destination io.Writer,
	options FollowOptions,
) (int64, error) {
	cursor := indexFollowCursor{state: indexState{nextSequence: 1}}

	return follow(ctx, options, func() (int64, error) {
		var chunks []Chunk
		if scanErr := reader.scanIndexGrowth(&cursor, func(chunk Chunk) error {
			chunks = append(chunks, chunk)
			return nil
		}); scanErr != nil {
			return 0, scanErr
		}

		written, _, copyErr := reader.copyChunks(destination, chunks)

		return written, copyErr
	})
}

type indexFollowCursor struct {
	identity os.FileInfo
	state    indexState
}

func (reader *Reader) scanIndexGrowth(cursor *indexFollowCursor, visit func(Chunk) error) error {
	index, err := openPrivateRegularFile(reader.paths.Index)
	if err != nil {
		return err
	}
	info, statErr := index.Stat()
	if statErr != nil {
		return errors.Join(fmt.Errorf("stat followed log chunk index: %w", statErr), index.Close())
	}
	if cursor.identity == nil {
		cursor.identity = info
	} else if !os.SameFile(cursor.identity, info) {
		return errors.Join(
			fmt.Errorf("%w: log chunk index changed while following", ErrUnsafePath),
			index.Close(),
		)
	}
	validOffset, conversionErr := uint64ToInt64(cursor.state.status.ValidIndexBytes)
	if conversionErr != nil {
		return errors.Join(conversionErr, index.Close())
	}
	if info.Size() < validOffset {
		return errors.Join(
			fmt.Errorf(
				"%w: index shrank from %d to %d bytes while following",
				ErrCorruptIndex,
				validOffset,
				info.Size(),
			),
			index.Close(),
		)
	}
	if _, seekErr := index.Seek(validOffset, io.SeekStart); seekErr != nil {
		return errors.Join(fmt.Errorf("seek followed log chunk index: %w", seekErr), index.Close())
	}
	_, scanErr := scanIndexFrom(index, &cursor.state, visit)
	closeErr := index.Close()

	return errors.Join(scanErr, closeErr)
}

func follow(
	ctx context.Context,
	options FollowOptions,
	copyGrowth func() (int64, error),
) (int64, error) {
	interval, intervalErr := followPollInterval(options.PollInterval)
	if intervalErr != nil {
		return 0, intervalErr
	}

	var total int64
	for {
		if contextErr := ctx.Err(); contextErr != nil {
			return total, contextErr
		}
		written, copyErr := copyGrowth()
		total += written
		if copyErr != nil {
			return total, copyErr
		}

		complete := false
		if options.Complete != nil {
			var completeErr error
			complete, completeErr = options.Complete(ctx)
			if completeErr != nil {
				return total, fmt.Errorf("check log capture completion: %w", completeErr)
			}
		}
		if complete {
			// Completion promises that the writer is closed, so a final fresh
			// snapshot drains bytes or records that raced with the prior read.
			written, copyErr = copyGrowth()
			total += written
			if copyErr != nil {
				return total, copyErr
			}

			return total, nil
		}

		if waitErr := waitForPoll(ctx, interval); waitErr != nil {
			return total, waitErr
		}
	}
}

func followPollInterval(configured time.Duration) (time.Duration, error) {
	if configured < 0 {
		return 0, errors.New("log follow poll interval must not be negative")
	}
	if configured == 0 {
		return DefaultFollowPollInterval, nil
	}

	return configured, nil
}

func waitForPoll(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type streamCursor struct {
	segment uint16
	offset  uint64
}

func (reader *Reader) copyStreamGrowth(
	destination io.Writer,
	stream Stream,
	cursor *streamCursor,
) (int64, error) {
	segments, err := reader.streamSegments(stream)
	if err != nil {
		return 0, err
	}
	if int(cursor.segment) > len(segments) {
		return 0, fmt.Errorf("%w: followed %s segment %d disappeared", ErrUnsafePath, stream, cursor.segment)
	}

	var written int64
	for index := int(cursor.segment) - 1; index < len(segments); index++ {
		segment := segments[index]
		count, copyErr := copyStreamSegment(destination, stream, segment, cursor)
		written += count
		if copyErr != nil {
			return written, copyErr
		}
		if index+1 < len(segments) {
			cursor.segment = segments[index+1].number
			cursor.offset = 0
		}
	}

	return written, nil
}

func copyStreamSegment(
	destination io.Writer,
	stream Stream,
	segment streamSegment,
	cursor *streamCursor,
) (int64, error) {
	file, err := openPrivateRegularFile(segment.path)
	if err != nil {
		return 0, err
	}
	info, statErr := file.Stat()
	if statErr != nil {
		_ = file.Close()

		return 0, fmt.Errorf("stat %s segment %d: %w", stream, segment.number, statErr)
	}
	size, conversionErr := nonnegativeInt64ToUint64(info.Size())
	if conversionErr != nil {
		_ = file.Close()

		return 0, conversionErr
	}
	if segment.number == cursor.segment && size < cursor.offset {
		// A Jobman-owned truncation starts a fresh byte range in the same
		// segment. This can duplicate a prefix but never drops new raw bytes.
		cursor.offset = 0
	}
	start := uint64(0)
	if segment.number == cursor.segment {
		start = cursor.offset
	}
	chunk := Chunk{
		Sequence: 1,
		Stream:   stream,
		Offset:   start,
		Length:   size - start,
	}
	count, copyErr := copyChunk(destination, file, chunk)
	countUint64, conversionErr := nonnegativeInt64ToUint64(count)
	if conversionErr == nil {
		cursor.segment = segment.number
		cursor.offset = start + countUint64
	}
	closeErr := file.Close()
	if copyErr != nil {
		copyErr = fmt.Errorf("follow %s segment %d: %w", stream, segment.number, copyErr)
	}
	if closeErr != nil {
		closeErr = fmt.Errorf("close followed %s segment %d: %w", stream, segment.number, closeErr)
	}

	return count, errors.Join(copyErr, conversionErr, closeErr)
}

func (reader *Reader) copyChunks(
	destination io.Writer,
	chunks []Chunk,
) (written int64, copied uint64, err error) {
	sources := make(map[segmentKey]*os.File)
	defer func() {
		err = errors.Join(err, closeIndexedSources(sources))
	}()

	for _, chunk := range chunks {
		source, openErr := reader.indexedSource(sources, chunk)
		if openErr != nil {
			return written, copied, openErr
		}
		count, copyErr := copyChunk(destination, source, chunk)
		written += count
		if copyErr != nil {
			return written, copied, copyErr
		}
		copied++
	}

	return written, copied, nil
}
