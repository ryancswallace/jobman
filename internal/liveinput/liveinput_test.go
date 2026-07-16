//go:build !windows

package liveinput

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

const testRunID = "018f0000-0000-7000-8000-000000000001"

func TestBrokerDeliversBytesAndEOF(t *testing.T) {
	t.Parallel()

	broker, listenErr := Listen(NewEndpoint(t.TempDir(), "deliver"))
	if listenErr != nil {
		if errors.Is(listenErr, syscall.EPERM) {
			t.Skipf("local sockets are blocked by the test sandbox: %v", listenErr)
		}
		t.Fatalf("Listen() error = %v", listenErr)
	}
	t.Cleanup(func() {
		if closeErr := broker.Close(); closeErr != nil {
			t.Errorf("Close() error = %v", closeErr)
		}
	})
	information, err := os.Stat(broker.Path())
	if err != nil {
		t.Fatalf("stat private endpoint: %v", err)
	}
	if information.Mode().Perm()&0o077 != 0 {
		t.Fatalf("endpoint permissions = %#o, grant group/other access", information.Mode().Perm())
	}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	served := make(chan error, 1)
	go func() { served <- broker.Serve(ctx) }()
	target := &memoryWriteCloser{}
	if beginErr := broker.BeginRun(testRunID); beginErr != nil {
		t.Fatalf("BeginRun() error = %v", beginErr)
	}
	if attachErr := broker.Attach(target); attachErr != nil {
		t.Fatalf("Attach() error = %v", attachErr)
	}
	result, err := Send(t.Context(), broker.Path(), testRunID, bytes.NewBufferString("hello\x00world"), false)
	if err != nil {
		t.Fatalf("Send(data) error = %v", err)
	}
	if result.Delivered != 11 || target.String() != "hello\x00world" {
		t.Fatalf("delivery = %#v, target = %q", result, target.String())
	}
	if _, err := Send(t.Context(), broker.Path(), testRunID, bytes.NewReader(nil), true); err != nil {
		t.Fatalf("Send(EOF) error = %v", err)
	}
	if !target.closed {
		t.Fatal("EOF did not close target")
	}
	if _, err := Send(t.Context(), broker.Path(), testRunID, bytes.NewBufferString("late"), false); !errors.Is(err, ErrEOF) {
		t.Fatalf("Send(after EOF) error = %v, want ErrEOF", err)
	}
	secondRun := "018f0000-0000-7000-8000-000000000002"
	if err := broker.BeginRun(secondRun); err != nil {
		t.Fatalf("BeginRun(second) error = %v", err)
	}
	secondTarget := &memoryWriteCloser{}
	if err := broker.Attach(secondTarget); err != nil {
		t.Fatalf("Attach(second) error = %v", err)
	}
	if _, err := Send(
		t.Context(), broker.Path(), secondRun, bytes.NewBufferString("second"), false,
	); err != nil {
		t.Fatalf("Send(second run) error = %v", err)
	}
	if secondTarget.String() != "second" || secondTarget.closed {
		t.Fatalf("second run target = %q, closed=%t", secondTarget.String(), secondTarget.closed)
	}
	cancel()
	select {
	case err := <-served:
		if err != nil {
			t.Fatalf("Serve() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Serve() did not stop")
	}
}

func TestSendRejectsOversizedPayload(t *testing.T) {
	t.Parallel()

	_, err := Send(t.Context(), "unused", testRunID, io.LimitReader(zeroReader{}, MaxPayload+1), false)
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Send() error = %v, want ErrTooLarge", err)
	}
}

func TestWriteEOFRequestDoesNotWriteEmptyPayload(t *testing.T) {
	t.Parallel()

	client, server := net.Pipe()
	t.Cleanup(func() {
		_ = client.Close()
		_ = server.Close()
	})
	read := make(chan error, 1)
	go func() {
		var header [headerSize]byte
		_, err := io.ReadFull(server, header[:])
		read <- err
	}()
	connection := rejectEmptyWrites{Conn: client}
	if err := writeRequest(connection, testRunID, nil, true); err != nil {
		t.Fatalf("writeRequest(EOF) error = %v", err)
	}
	if err := <-read; err != nil {
		t.Fatalf("read EOF request header: %v", err)
	}
}

func TestBrokerRejectsRequestForDifferentRunBeforeWriting(t *testing.T) {
	t.Parallel()

	broker, listenErr := Listen(NewEndpoint(t.TempDir(), "different-run"))
	if listenErr != nil {
		if errors.Is(listenErr, syscall.EPERM) {
			t.Skipf("local sockets are blocked by the test sandbox: %v", listenErr)
		}
		t.Fatalf("Listen() error = %v", listenErr)
	}
	t.Cleanup(func() { _ = broker.Close() })
	ctx, cancel := context.WithCancel(t.Context())
	serveBroker(ctx, t, broker, cancel)
	if err := broker.BeginRun(testRunID); err != nil {
		t.Fatalf("BeginRun() error = %v", err)
	}
	target := &memoryWriteCloser{}
	if err := broker.Attach(target); err != nil {
		t.Fatalf("Attach() error = %v", err)
	}
	otherRun := "018f0000-0000-7000-8000-000000000002"
	if _, err := Send(
		t.Context(), broker.Path(), otherRun, bytes.NewBufferString("wrong-run"), false,
	); !errors.Is(err, ErrRunChanged) {
		t.Fatalf("Send(wrong run) error = %v, want ErrRunChanged", err)
	}
	if target.Len() != 0 {
		t.Fatalf("wrong-run request wrote %q", target.String())
	}
}

func TestBrokerReportsPartialTargetWrite(t *testing.T) {
	t.Parallel()

	broker, listenErr := Listen(NewEndpoint(t.TempDir(), "partial-write"))
	if listenErr != nil {
		if errors.Is(listenErr, syscall.EPERM) {
			t.Skipf("local sockets are blocked by the test sandbox: %v", listenErr)
		}
		t.Fatalf("Listen() error = %v", listenErr)
	}
	t.Cleanup(func() { _ = broker.Close() })
	ctx, cancel := context.WithCancel(t.Context())
	serveBroker(ctx, t, broker, cancel)
	if err := broker.BeginRun(testRunID); err != nil {
		t.Fatalf("BeginRun() error = %v", err)
	}
	if err := broker.Attach(&shortWriteCloser{limit: 2}); err != nil {
		t.Fatalf("Attach() error = %v", err)
	}
	result, err := Send(
		t.Context(), broker.Path(), testRunID, bytes.NewBufferString("payload"), false,
	)
	if !errors.Is(err, io.ErrShortWrite) || result.Delivered != 2 {
		t.Fatalf("Send(partial) = %#v, %v; want 2 bytes and io.ErrShortWrite", result, err)
	}
}

func TestBrokerStateAndProtocolValidation(t *testing.T) {
	t.Parallel()

	listener := &stubListener{}
	endpoint := filepath.Join(t.TempDir(), "missing.sock")
	broker := &Broker{listener: listener, path: endpoint}
	if broker.Path() != endpoint {
		t.Fatalf("Path() = %q, want %q", broker.Path(), endpoint)
	}
	if err := broker.BeginRun("short"); err == nil {
		t.Fatal("BeginRun(short) error = nil")
	}
	if err := broker.BeginRun(testRunID); err != nil {
		t.Fatalf("BeginRun() error = %v", err)
	}
	if err := broker.Attach(nil); err == nil {
		t.Fatal("Attach(nil) error = nil")
	}
	target := &memoryWriteCloser{}
	if err := broker.Attach(target); err != nil {
		t.Fatalf("Attach() error = %v", err)
	}
	if err := broker.BeginRun(testRunID); err == nil {
		t.Fatal("BeginRun(active target) error = nil")
	}
	if err := broker.Attach(&memoryWriteCloser{}); err == nil {
		t.Fatal("Attach(second target) error = nil")
	}
	if err := broker.Detach(); err != nil {
		t.Fatalf("Detach() error = %v", err)
	}
	if !target.closed {
		t.Fatal("Detach() did not close target")
	}
	if err := broker.Detach(); err != nil {
		t.Fatalf("repeated Detach() error = %v", err)
	}
	if err := broker.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if !listener.closed {
		t.Fatal("Close() did not close listener")
	}
	if err := broker.BeginRun(testRunID); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("BeginRun(closed) error = %v, want net.ErrClosed", err)
	}
	if err := broker.Attach(&memoryWriteCloser{}); !errors.Is(err, net.ErrClosed) {
		t.Fatalf("Attach(closed) error = %v, want net.ErrClosed", err)
	}
	if err := broker.Close(); err != nil {
		t.Fatalf("repeated Close() error = %v", err)
	}

	closedByEOF := &memoryWriteCloser{}
	eofBroker := &Broker{listener: &stubListener{}, eof: true}
	if err := eofBroker.Attach(closedByEOF); !errors.Is(err, ErrEOF) {
		t.Fatalf("Attach(after EOF) error = %v, want ErrEOF", err)
	}
	if !closedByEOF.closed {
		t.Fatal("Attach(after EOF) did not close rejected target")
	}
}

func TestBrokerHandlePayloadStatuses(t *testing.T) {
	t.Parallel()

	newBroker := func(target io.WriteCloser) *Broker {
		return &Broker{runID: testRunID, target: target}
	}
	tests := []struct {
		name      string
		broker    *Broker
		operation byte
		runID     string
		payload   []byte
		status    byte
		delivered uint64
	}{
		{name: "run changed", broker: newBroker(&memoryWriteCloser{}), operation: opData, runID: "018f0000-0000-7000-8000-000000000002", status: 4},
		{name: "no target", broker: newBroker(nil), operation: opData, runID: testRunID, status: 1},
		{name: "already eof", broker: &Broker{runID: testRunID, eof: true}, operation: opData, runID: testRunID, status: 2},
		{name: "data", broker: newBroker(&memoryWriteCloser{}), operation: opData, runID: testRunID, payload: []byte("abc"), status: 0, delivered: 3},
		{name: "eof no target", broker: newBroker(nil), operation: opEOF, runID: testRunID, status: 1},
		{name: "eof", broker: newBroker(&memoryWriteCloser{}), operation: opEOF, runID: testRunID, status: 0},
		{name: "unknown", broker: newBroker(&memoryWriteCloser{}), operation: 99, runID: testRunID, status: 3},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			connection := &recordConn{}
			if err := test.broker.handlePayload(connection, test.operation, test.runID, test.payload); err != nil {
				t.Fatalf("handlePayload() error = %v", err)
			}
			assertAck(t, connection.written.Bytes(), test.status, test.delivered)
		})
	}

	invalidWriter := &invalidCountWriteCloser{count: 2}
	connection := &recordConn{}
	if err := newBroker(invalidWriter).handlePayload(connection, opData, testRunID, []byte("x")); err != nil {
		t.Fatalf("handlePayload(invalid count) error = %v", err)
	}
	assertAck(t, connection.written.Bytes(), 3, 0)

	writeFailure := errors.New("ack failed")
	connection = &recordConn{writeErr: writeFailure}
	if err := newBroker(&memoryWriteCloser{}).handlePayload(
		connection, opData, testRunID, []byte("x"),
	); !errors.Is(err, writeFailure) {
		t.Fatalf("handlePayload(ack failure) error = %v, want write failure", err)
	}
}

func TestBrokerHandleMalformedRequests(t *testing.T) {
	t.Parallel()

	header := func(operation byte, length uint32) []byte {
		value := make([]byte, headerSize)
		value[0] = operation
		copy(value[1:1+runIDSize], testRunID)
		binary.BigEndian.PutUint32(value[1+runIDSize:], length)

		return value
	}
	tests := []struct {
		name        string
		request     []byte
		deadlineErr error
		wantStatus  *byte
		wantErr     bool
	}{
		{name: "deadline", deadlineErr: errors.New("deadline"), wantErr: true},
		{name: "short header", request: []byte{opData}, wantErr: true},
		{name: "oversize", request: header(opData, MaxPayload+1), wantStatus: bytePointer(3)},
		{name: "eof payload", request: header(opEOF, 1), wantStatus: bytePointer(3)},
		{name: "short payload", request: header(opData, 2), wantErr: true},
		{name: "unknown operation", request: header(99, 0), wantStatus: bytePointer(3)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			connection := &recordConn{
				reader:      bytes.NewReader(test.request),
				deadlineErr: test.deadlineErr,
			}
			broker := &Broker{runID: testRunID}
			err := broker.handle(connection)
			if (err != nil) != test.wantErr {
				t.Fatalf("handle() error = %v, wantErr %t", err, test.wantErr)
			}
			if test.wantStatus != nil {
				assertAck(t, connection.written.Bytes(), *test.wantStatus, 0)
			}
			if !connection.closed {
				t.Fatal("handle() did not close connection")
			}
		})
	}
}

func TestLiveInputWireHelpers(t *testing.T) {
	t.Parallel()

	if _, err := readPayload(nil, false); err == nil {
		t.Fatal("readPayload(nil) error = nil")
	}
	readFailure := errors.New("read failed")
	if _, err := readPayload(errorReader{err: readFailure}, false); !errors.Is(err, readFailure) {
		t.Fatalf("readPayload(error) = %v, want read failure", err)
	}
	if payload, err := readPayload(errorReader{err: readFailure}, true); err != nil || payload != nil {
		t.Fatalf("readPayload(EOF) = %v, %v; want nil, nil", payload, err)
	}

	if err := writeRequest(&recordConn{}, "short", nil, false); err == nil {
		t.Fatal("writeRequest(short run ID) error = nil")
	}
	if err := writeRequest(&recordConn{}, testRunID, make([]byte, MaxPayload+1), false); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("writeRequest(oversize) error = %v, want ErrTooLarge", err)
	}
	headerFailure := errors.New("header failed")
	if err := writeRequest(&recordConn{writeErr: headerFailure}, testRunID, nil, false); !errors.Is(err, headerFailure) {
		t.Fatalf("writeRequest(header failure) error = %v", err)
	}
	payloadFailure := errors.New("payload failed")
	connection := &recordConn{failWrite: 2, writeErr: payloadFailure}
	if err := writeRequest(connection, testRunID, []byte("payload"), false); !errors.Is(err, payloadFailure) {
		t.Fatalf("writeRequest(payload failure) error = %v", err)
	}

	for _, test := range []struct {
		status    byte
		delivered uint64
		length    uint64
		eof       bool
		wantErr   error
		wantEOF   bool
	}{
		{status: 0, delivered: 2, length: 2},
		{status: 0, delivered: 1, length: 2, wantErr: io.ErrShortWrite},
		{status: 0, eof: true, wantEOF: true},
		{status: 1, wantErr: ErrNoTarget},
		{status: 2, wantErr: ErrEOF, wantEOF: true},
		{status: 4, wantErr: ErrRunChanged},
		{status: 9, wantErr: errors.New("rejected")},
	} {
		result, err := resultFromAck(test.status, test.delivered, test.length, test.eof)
		if test.wantErr == nil && err != nil || test.wantErr != nil && err == nil {
			t.Errorf("resultFromAck(%d) error = %v, wantErr %v", test.status, err, test.wantErr)
		}
		if result.Delivered != test.delivered || result.EOF != test.wantEOF {
			t.Errorf("resultFromAck(%d) = %#v", test.status, result)
		}
	}

	ackFailure := errors.New("ack failed")
	if err := writeAck(errorWriter{err: ackFailure}, 0, 1); !errors.Is(err, ackFailure) {
		t.Fatalf("writeAck() error = %v, want ack failure", err)
	}
	if got := NewEndpoint("/state", "job"); got != filepath.Clean("/state/input/job.sock") {
		t.Fatalf("NewEndpoint() = %q", got)
	}
	longEndpoint := NewEndpoint(filepath.Join(string(filepath.Separator), strings.Repeat("long-state-directory", 8)), "job")
	if len(longEndpoint) >= portableUnixSocketPathLimit {
		t.Fatalf("NewEndpoint(long state path) length = %d, endpoint %q", len(longEndpoint), longEndpoint)
	}
	if _, err := Send(t.Context(), "unused", "short", bytes.NewReader(nil), false); err == nil {
		t.Fatal("Send(short run ID) error = nil")
	}
	if _, err := Listen(""); err == nil {
		t.Fatal("Listen(empty) error = nil")
	}
}

func TestSendProtocolExchange(t *testing.T) {
	t.Parallel()

	ack := func(status byte, delivered uint64) []byte {
		payload := make([]byte, ackSize)
		payload[0] = status
		binary.BigEndian.PutUint64(payload[1:], delivered)

		return payload
	}
	dial := func(connection net.Conn, err error) func(context.Context, string) (net.Conn, error) {
		return func(context.Context, string) (net.Conn, error) { return connection, err }
	}

	connection := &recordConn{reader: bytes.NewReader(ack(0, 3))}
	result, sendErr := sendWithDialer(
		t.Context(), "endpoint", testRunID, bytes.NewBufferString("abc"), false, dial(connection, nil),
	)
	if sendErr != nil || result.Delivered != 3 || result.EOF {
		t.Fatalf("send() = %#v, %v", result, sendErr)
	}
	if connection.written.Len() != headerSize+3 || !connection.closed {
		t.Fatalf("request bytes/closed = %d/%t", connection.written.Len(), connection.closed)
	}

	dialFailure := errors.New("dial failed")
	if _, err := sendWithDialer(
		t.Context(), "endpoint", testRunID, bytes.NewReader(nil), false, dial(nil, dialFailure),
	); !errors.Is(err, dialFailure) {
		t.Fatalf("send(dial failure) error = %v", err)
	}
	deadlineFailure := errors.New("deadline failed")
	if _, err := sendWithDialer(
		t.Context(), "endpoint", testRunID, bytes.NewReader(nil), false,
		dial(&recordConn{deadlineErr: deadlineFailure}, nil),
	); !errors.Is(err, deadlineFailure) {
		t.Fatalf("send(deadline failure) error = %v", err)
	}
	writeFailure := errors.New("write failed")
	if _, err := sendWithDialer(
		t.Context(), "endpoint", testRunID, bytes.NewReader(nil), false,
		dial(&recordConn{writeErr: writeFailure}, nil),
	); !errors.Is(err, writeFailure) {
		t.Fatalf("send(write failure) error = %v", err)
	}
	if _, err := sendWithDialer(
		t.Context(), "endpoint", testRunID, bytes.NewReader(nil), false,
		dial(&recordConn{reader: bytes.NewReader(nil)}, nil),
	); err == nil {
		t.Fatal("send(short acknowledgement) error = nil")
	}
	if _, err := sendWithDialer(
		t.Context(), "endpoint", testRunID, bytes.NewReader(nil), false,
		dial(&recordConn{reader: bytes.NewReader(ack(0, MaxPayload+1))}, nil),
	); err == nil {
		t.Fatal("send(invalid delivered count) error = nil")
	}
	result, sendErr = sendWithDialer(
		t.Context(), "endpoint", testRunID, bytes.NewReader(nil), true,
		dial(&recordConn{reader: bytes.NewReader(ack(0, 0))}, nil),
	)
	if sendErr != nil || !result.EOF {
		t.Fatalf("send(EOF) = %#v, %v", result, sendErr)
	}
}

func TestBrokerServeAndConnectionBookkeeping(t *testing.T) {
	t.Parallel()

	request := make([]byte, headerSize)
	request[0] = 99
	copy(request[1:1+runIDSize], testRunID)
	connection := &recordConn{reader: bytes.NewReader(request)}
	listener := &sequenceListener{
		connections: []net.Conn{connection},
		err:         errors.New("accept failed"),
	}
	broker := &Broker{listener: listener, runID: testRunID}
	if err := broker.Serve(t.Context()); err == nil {
		t.Fatal("Serve(accept failure) error = nil")
	}
	assertAck(t, connection.written.Bytes(), 3, 0)

	broker.setConnection(connection)
	broker.clearConnection(&recordConn{})
	broker.closeConnection()
	broker.clearConnection(connection)
	broker.closeConnection()
	broker.closeTransport()

	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	closedListener := &stubListener{}
	broker = &Broker{listener: closedListener}
	if err := broker.Serve(canceled); err != nil {
		t.Fatalf("Serve(canceled) error = %v", err)
	}
}

func TestBrokerSerializesConcurrentClientsInAcceptOrder(t *testing.T) {
	t.Parallel()

	broker, listenErr := Listen(NewEndpoint(t.TempDir(), "serialized-clients"))
	if listenErr != nil {
		if errors.Is(listenErr, syscall.EPERM) {
			t.Skipf("local sockets are blocked by the test sandbox: %v", listenErr)
		}
		t.Fatalf("Listen() error = %v", listenErr)
	}
	t.Cleanup(func() { _ = broker.Close() })
	ctx, cancel := context.WithCancel(t.Context())
	serveBroker(ctx, t, broker, cancel)
	if err := broker.BeginRun(testRunID); err != nil {
		t.Fatalf("BeginRun() error = %v", err)
	}
	target := &memoryWriteCloser{}
	if err := broker.Attach(target); err != nil {
		t.Fatalf("Attach() error = %v", err)
	}
	first, err := dialLocal(t.Context(), broker.Path())
	if err != nil {
		t.Fatalf("dial first client: %v", err)
	}
	defer first.Close()
	second, err := dialLocal(t.Context(), broker.Path())
	if err != nil {
		t.Fatalf("dial second client: %v", err)
	}
	defer second.Close()
	if err := writeRequest(second, testRunID, []byte("2"), false); err != nil {
		t.Fatalf("write second request first: %v", err)
	}
	if err := writeRequest(first, testRunID, []byte("1"), false); err != nil {
		t.Fatalf("write first request: %v", err)
	}
	for index, connection := range []net.Conn{first, second} {
		var ack [ackSize]byte
		if _, err := io.ReadFull(connection, ack[:]); err != nil {
			t.Fatalf("read client %d acknowledgement: %v", index+1, err)
		}
		if ack[0] != 0 {
			t.Fatalf("client %d acknowledgement status = %d", index+1, ack[0])
		}
	}
	if target.String() != "12" {
		t.Fatalf("serialized target = %q, want accept order 12", target.String())
	}
}

func serveBroker(
	ctx context.Context,
	t *testing.T,
	broker *Broker,
	cancel context.CancelFunc,
) {
	t.Helper()
	served := make(chan error, 1)
	go func() { served <- broker.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-served:
			if err != nil {
				t.Errorf("Serve() error = %v", err)
			}
		case <-time.After(time.Second):
			t.Error("Serve() did not stop")
		}
	})
}

type rejectEmptyWrites struct {
	net.Conn
}

func (connection rejectEmptyWrites) Write(payload []byte) (int, error) {
	if len(payload) == 0 {
		return 0, errors.New("unexpected empty write")
	}

	return connection.Conn.Write(payload)
}

type memoryWriteCloser struct {
	bytes.Buffer
	closed bool
}

type shortWriteCloser struct {
	limit  int
	closed bool
}

func (writer *shortWriteCloser) Write(payload []byte) (int, error) {
	return min(writer.limit, len(payload)), io.ErrShortWrite
}

func (writer *shortWriteCloser) Close() error {
	writer.closed = true

	return nil
}

func (writer *memoryWriteCloser) Close() error {
	writer.closed = true
	return nil
}

type zeroReader struct{}

func (zeroReader) Read(buffer []byte) (int, error) {
	for index := range buffer {
		buffer[index] = 0
	}
	return len(buffer), nil
}

type stubListener struct {
	closed bool
}

type sequenceListener struct {
	connections []net.Conn
	err         error
}

func (listener *sequenceListener) Accept() (net.Conn, error) {
	if len(listener.connections) == 0 {
		return nil, listener.err
	}
	connection := listener.connections[0]
	listener.connections = listener.connections[1:]

	return connection, nil
}

func (*sequenceListener) Close() error   { return nil }
func (*sequenceListener) Addr() net.Addr { return stubAddr("sequence") }

func (*stubListener) Accept() (net.Conn, error) { return nil, net.ErrClosed }
func (listener *stubListener) Close() error {
	listener.closed = true

	return nil
}
func (*stubListener) Addr() net.Addr { return stubAddr("listener") }

type stubAddr string

func (address stubAddr) Network() string { return string(address) }
func (address stubAddr) String() string  { return string(address) }

type recordConn struct {
	reader      io.Reader
	written     bytes.Buffer
	deadlineErr error
	writeErr    error
	failWrite   int
	writes      int
	closed      bool
}

func (connection *recordConn) Read(payload []byte) (int, error) {
	if connection.reader == nil {
		return 0, io.EOF
	}

	return connection.reader.Read(payload)
}

func (connection *recordConn) Write(payload []byte) (int, error) {
	connection.writes++
	if connection.writeErr != nil && (connection.failWrite == 0 || connection.writes == connection.failWrite) {
		return 0, connection.writeErr
	}

	return connection.written.Write(payload)
}

func (connection *recordConn) Close() error {
	connection.closed = true

	return nil
}

func (*recordConn) LocalAddr() net.Addr                    { return stubAddr("local") }
func (*recordConn) RemoteAddr() net.Addr                   { return stubAddr("remote") }
func (connection *recordConn) SetDeadline(time.Time) error { return connection.deadlineErr }
func (*recordConn) SetReadDeadline(time.Time) error        { return nil }
func (*recordConn) SetWriteDeadline(time.Time) error       { return nil }

type invalidCountWriteCloser struct {
	count int
}

func (writer *invalidCountWriteCloser) Write([]byte) (int, error) { return writer.count, nil }
func (*invalidCountWriteCloser) Close() error                     { return nil }

type errorReader struct{ err error }

func (reader errorReader) Read([]byte) (int, error) { return 0, reader.err }

type errorWriter struct{ err error }

func (writer errorWriter) Write([]byte) (int, error) { return 0, writer.err }

func assertAck(t *testing.T, payload []byte, status byte, delivered uint64) {
	t.Helper()
	if len(payload) != ackSize {
		t.Fatalf("ack length = %d, want %d (%x)", len(payload), ackSize, payload)
	}
	if payload[0] != status || binary.BigEndian.Uint64(payload[1:]) != delivered {
		t.Fatalf("ack = %x, want status %d delivered %d", payload, status, delivered)
	}
}

func bytePointer(value byte) *byte { return &value }
