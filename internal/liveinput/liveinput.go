// Package liveinput implements bounded, authenticated-by-filesystem local IPC
// for writing to the standard input of an active detached target.
package liveinput

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"sync"
	"time"
)

const (
	// MaxPayload bounds one client request so a local peer cannot make the
	// supervisor allocate unbounded memory.
	MaxPayload           = 1 << 20
	runIDSize            = 36
	headerSize           = 1 + runIDSize + 4
	ackSize              = 9
	opData               = byte(1)
	opEOF                = byte(2)
	defaultClientTimeout = 30 * time.Second
)

// Stable live-input errors.
var (
	ErrUnsupported = errors.New("live input is unsupported on this platform")
	ErrNoTarget    = errors.New("job has no active input target")
	ErrEOF         = errors.New("job input is closed")
	ErrTooLarge    = errors.New("live input request exceeds maximum payload")
	ErrRunChanged  = errors.New("active live-input run changed")
)

// Result reports exactly how many payload bytes the supervisor accepted.
type Result struct {
	Delivered uint64
	EOF       bool
}

// Broker owns the private endpoint and serializes writes to the current target.
type Broker struct {
	listener net.Listener
	path     string

	mu     sync.Mutex
	target io.WriteCloser
	runID  string
	eof    bool
	closed bool

	connectionMu sync.Mutex
	connection   net.Conn
}

// BeginRun resets per-run EOF state before attaching a new target pipe.
func (broker *Broker) BeginRun(runID string) error {
	if len(runID) != runIDSize {
		return errors.New("begin live-input run: invalid run ID")
	}
	broker.mu.Lock()
	defer broker.mu.Unlock()
	if broker.closed {
		return net.ErrClosed
	}
	if broker.target != nil {
		return errors.New("begin live-input run: target is already active")
	}
	broker.eof = false
	broker.runID = runID

	return nil
}

// Listen creates a user-private endpoint. Its parent is created with 0700 and
// the endpoint itself is restricted to the current user on platforms that
// expose filesystem-backed local sockets.
func Listen(path string) (*Broker, error) {
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, errors.New("listen for live input: path must be clean and absolute")
	}
	listener, err := listenLocal(path)
	if err != nil {
		return nil, err
	}

	return &Broker{listener: listener, path: path}, nil
}

// Path returns the client endpoint.
func (broker *Broker) Path() string {
	return broker.path
}

// Attach selects the pipe for the current invocation. BeginRun must be called
// first so EOF is scoped to one run.
func (broker *Broker) Attach(target io.WriteCloser) error {
	if target == nil {
		return errors.New("attach live input: target is nil")
	}
	broker.mu.Lock()
	defer broker.mu.Unlock()
	if broker.closed {
		return net.ErrClosed
	}
	if broker.target != nil {
		return errors.New("attach live input: target is already active")
	}
	if broker.eof {
		return errors.Join(ErrEOF, target.Close())
	}
	broker.target = target

	return nil
}

// Detach closes and clears the current pipe. It is idempotent.
func (broker *Broker) Detach() error {
	broker.mu.Lock()
	defer broker.mu.Unlock()
	if broker.target == nil {
		return nil
	}
	err := broker.target.Close()
	broker.target = nil

	return err
}

// Serve accepts requests until ctx is canceled or Close is called.
func (broker *Broker) Serve(ctx context.Context) error {
	stop := context.AfterFunc(ctx, broker.closeTransport)
	defer stop()
	for {
		connection, err := broker.listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}

			return fmt.Errorf("accept live input: %w", err)
		}
		// Handle accepted clients in listener order. The per-connection deadline
		// bounds a malformed local peer without allowing goroutine scheduling to
		// reorder writes from concurrent clients.
		broker.setConnection(connection)
		_ = broker.handle(connection) //nolint:errcheck // A request-scoped failure must not stop input for later clients.
		broker.clearConnection(connection)
	}
}

func (broker *Broker) handle(connection net.Conn) (returned error) {
	defer func() { returned = errors.Join(returned, connection.Close()) }()
	if err := connection.SetDeadline(time.Now().Add(defaultClientTimeout)); err != nil {
		return fmt.Errorf("set live-input server deadline: %w", err)
	}
	var header [headerSize]byte
	if _, err := io.ReadFull(connection, header[:]); err != nil {
		return err
	}
	runID := string(header[1 : 1+runIDSize])
	length := int64(binary.BigEndian.Uint32(header[1+runIDSize:]))
	if length > MaxPayload || header[0] == opEOF && length != 0 {
		return writeAck(connection, 3, 0)
	}
	payload := make([]byte, length)
	if _, err := io.ReadFull(connection, payload); err != nil {
		return err
	}

	return broker.handlePayload(connection, header[0], runID, payload)
}

func (broker *Broker) handlePayload(connection net.Conn, operation byte, runID string, payload []byte) error {
	broker.mu.Lock()
	defer broker.mu.Unlock()
	if runID != broker.runID {
		return writeAck(connection, 4, 0)
	}
	switch operation {
	case opData:
		if broker.eof {
			return writeAck(connection, 2, 0)
		}
		if broker.target == nil {
			return writeAck(connection, 1, 0)
		}
		written, err := broker.target.Write(payload)
		if written < 0 || written > len(payload) {
			return errors.Join(err, writeAck(connection, 3, 0))
		}
		// A target cannot accept more than the MaxPayload-sized slice supplied
		// above, so the count is representable in the unsigned wire field.
		delivered := uint64(written)
		if ackErr := writeAck(connection, 0, delivered); ackErr != nil {
			return errors.Join(err, ackErr)
		}

		return err
	case opEOF:
		if broker.target == nil {
			return writeAck(connection, 1, 0)
		}
		broker.eof = true
		var closeErr error
		if broker.target != nil {
			closeErr = broker.target.Close()
			broker.target = nil
		}
		return errors.Join(closeErr, writeAck(connection, 0, 0))
	default:
		return writeAck(connection, 3, 0)
	}
}

// Close releases the endpoint and attached target.
func (broker *Broker) Close() error {
	broker.mu.Lock()
	if broker.closed {
		broker.mu.Unlock()
		return nil
	}
	broker.closed = true
	target := broker.target
	broker.target = nil
	broker.mu.Unlock()

	err := broker.listener.Close()
	if errors.Is(err, net.ErrClosed) {
		err = nil
	}
	broker.closeConnection()
	if target != nil {
		err = errors.Join(err, target.Close())
	}
	err = errors.Join(err, removeLocal(broker.path))

	return err
}

func (broker *Broker) closeTransport() {
	_ = broker.listener.Close()
	broker.closeConnection()
}

func (broker *Broker) setConnection(connection net.Conn) {
	broker.connectionMu.Lock()
	broker.connection = connection
	broker.connectionMu.Unlock()
}

func (broker *Broker) clearConnection(connection net.Conn) {
	broker.connectionMu.Lock()
	if broker.connection == connection {
		broker.connection = nil
	}
	broker.connectionMu.Unlock()
}

func (broker *Broker) closeConnection() {
	broker.connectionMu.Lock()
	connection := broker.connection
	broker.connectionMu.Unlock()
	if connection != nil {
		_ = connection.Close()
	}
}

// Send delivers one bounded payload or a durable EOF request.
func Send(ctx context.Context, endpoint, expectedRunID string, source io.Reader, sendEOF bool) (Result, error) {
	return sendWithDialer(ctx, endpoint, expectedRunID, source, sendEOF, dialLocal)
}

func sendWithDialer(
	ctx context.Context,
	endpoint string,
	expectedRunID string,
	source io.Reader,
	sendEOF bool,
	dial func(context.Context, string) (net.Conn, error),
) (Result, error) {
	if len(expectedRunID) != runIDSize {
		return Result{}, errors.New("send live input: invalid expected run ID")
	}
	if _, bounded := ctx.Deadline(); !bounded {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultClientTimeout)
		defer cancel()
	}
	payload, err := readPayload(source, sendEOF)
	if err != nil {
		return Result{}, err
	}
	connection, err := dial(ctx, endpoint)
	if err != nil {
		return Result{}, err
	}
	defer connection.Close()
	if deadline, ok := ctx.Deadline(); ok {
		if err := connection.SetDeadline(deadline); err != nil {
			return Result{}, fmt.Errorf("set live-input client deadline: %w", err)
		}
	}
	if err := writeRequest(connection, expectedRunID, payload, sendEOF); err != nil {
		return Result{}, err
	}
	var ack [ackSize]byte
	if _, err := io.ReadFull(connection, ack[:]); err != nil {
		return Result{}, fmt.Errorf("read live-input acknowledgement: %w", err)
	}
	delivered := binary.BigEndian.Uint64(ack[1:])
	if delivered > MaxPayload {
		return Result{}, errors.New("supervisor returned an invalid live-input byte count")
	}
	payloadLength := uint64(len(payload))

	return resultFromAck(ack[0], delivered, payloadLength, sendEOF)
}

func readPayload(source io.Reader, sendEOF bool) ([]byte, error) {
	if sendEOF {
		return nil, nil
	}
	if source == nil {
		return nil, errors.New("read live input: source is nil")
	}
	payload, err := io.ReadAll(io.LimitReader(source, MaxPayload+1))
	if err != nil {
		return nil, fmt.Errorf("read live input: %w", err)
	}
	if len(payload) > MaxPayload {
		return nil, ErrTooLarge
	}

	return payload, nil
}

func writeRequest(connection net.Conn, expectedRunID string, payload []byte, sendEOF bool) error {
	operation := opData
	if sendEOF {
		operation = opEOF
	}
	if len(payload) > MaxPayload {
		return ErrTooLarge
	}
	if len(expectedRunID) != runIDSize {
		return errors.New("write live-input request: invalid expected run ID")
	}
	header := [headerSize]byte{operation}
	copy(header[1:1+runIDSize], expectedRunID)
	// The checks above prove that the payload length is representable on the
	// fixed-width local protocol wire.
	binary.BigEndian.PutUint32(header[1+runIDSize:], uint32(len(payload))) //nolint:gosec // Bounds checked immediately above.
	if _, err := connection.Write(header[:]); err != nil {
		return fmt.Errorf("write live-input header: %w", err)
	}
	if len(payload) > 0 {
		if _, err := connection.Write(payload); err != nil {
			return fmt.Errorf("write live-input payload: %w", err)
		}
	}

	return nil
}

func resultFromAck(status byte, delivered, payloadLength uint64, sendEOF bool) (Result, error) {
	switch status {
	case 0:
		result := Result{Delivered: delivered, EOF: sendEOF}
		if !sendEOF && delivered != payloadLength {
			return result, io.ErrShortWrite
		}
		return result, nil
	case 1:
		return Result{Delivered: delivered}, ErrNoTarget
	case 2:
		return Result{Delivered: delivered, EOF: true}, ErrEOF
	case 4:
		return Result{Delivered: delivered}, ErrRunChanged
	default:
		return Result{Delivered: delivered}, errors.New("supervisor rejected live-input request")
	}
}

func writeAck(writer io.Writer, status byte, delivered uint64) error {
	var ack [ackSize]byte
	ack[0] = status
	binary.BigEndian.PutUint64(ack[1:], delivered)
	_, err := writer.Write(ack[:])

	return err
}

// NewEndpoint returns the conventional private endpoint for one job.
func NewEndpoint(stateDir, jobID string) string {
	return filepath.Join(stateDir, "input", jobID+".sock")
}
