package notify

import (
	"bytes"
	"errors"
)

type boundedBuffer struct {
	buffer    bytes.Buffer
	limit     int64
	truncated bool
}

func newBoundedBuffer(limit int64) *boundedBuffer {
	return &boundedBuffer{limit: limit}
}

func (buffer *boundedBuffer) Write(data []byte) (int, error) {
	remaining := buffer.limit - int64(buffer.buffer.Len())
	if remaining > 0 {
		kept := min(int64(len(data)), remaining)
		if kept > 0 {
			if _, err := buffer.buffer.Write(data[:kept]); err != nil {
				return 0, errors.New("capture notification output")
			}
		}
	}
	if int64(len(data)) > max(remaining, 0) {
		buffer.truncated = true
	}

	// Report the complete write so the child or transport is never blocked by
	// the capture limit.
	return len(data), nil
}

func (buffer *boundedBuffer) Bytes() []byte {
	return bytes.Clone(buffer.buffer.Bytes())
}
