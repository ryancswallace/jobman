package logstore

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestFollowStreamDrainsBytesWrittenAcrossRotationAtCompletion(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	run, err := CreateRunWithOptions(stateDir, testJobID, 1, RunOptions{
		Rotation: RotationPolicy{SegmentBytes: 3, MaxSegmentsPerStream: 3},
	})
	if err != nil {
		t.Fatalf("CreateRunWithOptions() error = %v", err)
	}
	appendBytes(t, run, Stdout, []byte("abc"), time.Unix(1, 0))
	reader, err := OpenRun(stateDir, testJobID, 1)
	if err != nil {
		t.Fatalf("OpenRun() error = %v", err)
	}

	completeCalls := 0
	var output bytes.Buffer
	written, err := reader.FollowStream(t.Context(), &output, Stdout, FollowOptions{
		Complete: func(context.Context) (bool, error) {
			completeCalls++
			appendBytes(t, run, Stdout, []byte("more"), time.Unix(2, 0))
			if closeErr := run.Close(); closeErr != nil {
				t.Fatalf("Close() error = %v", closeErr)
			}

			return true, nil
		},
	})
	if err != nil {
		t.Fatalf("FollowStream() error = %v", err)
	}
	expected := "abc" + "more"
	if completeCalls != 1 || written != 7 || output.String() != expected {
		t.Errorf("FollowStream() = calls %d, written %d, output %q", completeCalls, written, output.String())
	}
}

func TestFollowCombinedDrainsFinalIndexedChunk(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	run, err := CreateRun(stateDir, testJobID, 1)
	if err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	appendBytes(t, run, Stdout, []byte("out"), time.Unix(1, 0))
	reader, err := OpenRun(stateDir, testJobID, 1)
	if err != nil {
		t.Fatalf("OpenRun() error = %v", err)
	}

	var output bytes.Buffer
	written, err := reader.FollowCombined(t.Context(), &output, FollowOptions{
		Complete: func(context.Context) (bool, error) {
			appendBytes(t, run, Stderr, []byte("err"), time.Unix(2, 0))
			if closeErr := run.Close(); closeErr != nil {
				t.Fatalf("Close() error = %v", closeErr)
			}

			return true, nil
		},
	})
	if err != nil {
		t.Fatalf("FollowCombined() error = %v", err)
	}
	expected := "out" + "err"
	if written != 6 || output.String() != expected {
		t.Errorf("FollowCombined() = (%d, %q), want (6, %q)", written, output.String(), expected)
	}
}

func TestFollowCancellationIsPromptAndPreservesCopiedPrefix(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	run, err := CreateRun(stateDir, testJobID, 1)
	if err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	t.Cleanup(func() { _ = run.Close() })
	appendBytes(t, run, Stdout, []byte("prefix"), time.Unix(1, 0))
	reader, err := OpenRun(stateDir, testJobID, 1)
	if err != nil {
		t.Fatalf("OpenRun() error = %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	var output bytes.Buffer
	written, err := reader.FollowStream(ctx, &output, Stdout, FollowOptions{
		PollInterval: time.Hour,
		Complete: func(context.Context) (bool, error) {
			cancel()

			return false, nil
		},
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("FollowStream() error = %v, want context.Canceled", err)
	}
	if written != 6 || output.String() != "prefix" {
		t.Errorf("FollowStream() = (%d, %q), want copied prefix", written, output.String())
	}
}

func TestFollowRejectsNegativePollingInterval(t *testing.T) {
	t.Parallel()

	reader := &Reader{}
	_, err := reader.FollowCombined(t.Context(), &bytes.Buffer{}, FollowOptions{PollInterval: -1})
	if err == nil {
		t.Fatal("FollowCombined() error = nil, want invalid interval")
	}
}

func TestIncrementalIndexFollowVisitsEachChunkOnce(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join(t.TempDir(), "state")
	run, err := CreateRun(stateDir, testJobID, 1)
	if err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	t.Cleanup(func() { _ = run.Close() })
	reader, err := OpenRun(stateDir, testJobID, 1)
	if err != nil {
		t.Fatalf("OpenRun() error = %v", err)
	}
	cursor := indexFollowCursor{state: indexState{nextSequence: 1}}
	var sequences []uint64
	visit := func(chunk Chunk) error {
		sequences = append(sequences, chunk.Sequence)

		return nil
	}

	appendBytes(t, run, Stdout, []byte("one"), time.Unix(1, 0))
	if scanErr := reader.scanIndexGrowth(&cursor, visit); scanErr != nil {
		t.Fatalf("first scanIndexGrowth() error = %v", scanErr)
	}
	appendBytes(t, run, Stderr, []byte("two"), time.Unix(2, 0))
	if scanErr := reader.scanIndexGrowth(&cursor, visit); scanErr != nil {
		t.Fatalf("second scanIndexGrowth() error = %v", scanErr)
	}
	if scanErr := reader.scanIndexGrowth(&cursor, visit); scanErr != nil {
		t.Fatalf("idle scanIndexGrowth() error = %v", scanErr)
	}
	if !reflect.DeepEqual(sequences, []uint64{1, 2}) {
		t.Errorf("incremental sequences = %v, want [1 2]", sequences)
	}
}
