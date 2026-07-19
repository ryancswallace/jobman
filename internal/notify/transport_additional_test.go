package notify

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

type deadlineFailureConn struct{ net.Conn }

func (connection deadlineFailureConn) SetDeadline(time.Time) error {
	return errors.New("deadline failed")
}

func TestSMTPConnectionFailureBoundaries(t *testing.T) {
	t.Parallel()

	request := SMTPRequest{Address: "smtp.example.test:25", Mode: SMTPPlain}
	if _, err := connectSMTPWithDial(t.Context(), request, func(
		context.Context, string, SMTPMode, *tls.Config,
	) (net.Conn, error) {
		return nil, errors.New("dial failed")
	}); err == nil {
		t.Fatal("connectSMTPWithDial(dial failure) error = nil")
	}

	deadlineContext, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	client, server := net.Pipe()
	if _, err := connectSMTPWithDial(deadlineContext, request, func(
		context.Context, string, SMTPMode, *tls.Config,
	) (net.Conn, error) {
		return deadlineFailureConn{client}, nil
	}); err == nil {
		t.Fatal("connectSMTPWithDial(deadline failure) error = nil")
	}
	_ = server.Close()

	client, server = net.Pipe()
	go func() {
		_, _ = server.Write([]byte("not an SMTP greeting\r\n"))
		_ = server.Close()
	}()
	if _, err := connectSMTPWithDial(t.Context(), request, func(
		context.Context, string, SMTPMode, *tls.Config,
	) (net.Conn, error) {
		return client, nil
	}); err == nil {
		t.Fatal("connectSMTPWithDial(invalid greeting) error = nil")
	}
}

func TestSMTPNegotiationFailureBoundaries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		request SMTPRequest
		stage   string
	}{
		{stage: "starttls", request: SMTPRequest{Address: "smtp.example.test:25", Mode: SMTPStartTLS}},
		{stage: "auth", request: SMTPRequest{
			Address: "smtp.example.test:25", Mode: SMTPPlain, Username: "jobman", Password: []byte("secret"),
		}},
	}
	for _, test := range tests {
		t.Run(test.stage, func(t *testing.T) {
			t.Parallel()
			client, server := net.Pipe()
			go serveSMTPNegotiationFailure(server, test.stage)
			if _, err := connectSMTPWithDial(t.Context(), test.request, func(
				context.Context, string, SMTPMode, *tls.Config,
			) (net.Conn, error) {
				return client, nil
			}); err == nil {
				t.Fatalf("connectSMTPWithDial(%s failure) error = nil", test.stage)
			}
		})
	}
}

func serveSMTPNegotiationFailure(connection net.Conn, stage string) {
	defer connection.Close()
	reader := bufio.NewReader(connection)
	_, _ = connection.Write([]byte("220 smtp.example.test ready\r\n"))
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		switch {
		case strings.HasPrefix(line, "EHLO "):
			if stage == "starttls" {
				_, _ = connection.Write([]byte("250-smtp.example.test\r\n250 STARTTLS\r\n"))
			} else {
				_, _ = connection.Write([]byte("250-smtp.example.test\r\n250 AUTH PLAIN\r\n"))
			}
		case line == "STARTTLS\r\n":
			_, _ = connection.Write([]byte("454 TLS unavailable\r\n"))
			return
		case strings.HasPrefix(line, "AUTH "):
			_, _ = connection.Write([]byte("535 authentication rejected\r\n"))
		case line == "QUIT\r\n":
			_, _ = connection.Write([]byte("221 goodbye\r\n"))
			return
		}
	}
}

func TestSMTPDialModeBranches(t *testing.T) {
	t.Parallel()

	for _, mode := range []SMTPMode{SMTPPlain, SMTPImplicit} {
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		_, err := dialSMTP(ctx, "127.0.0.1:1", mode, &tls.Config{MinVersion: tls.VersionTLS12})
		cancel()
		if err == nil {
			t.Fatalf("dialSMTP(%s) unexpectedly connected", mode)
		}
	}
}

func TestNotifierInvalidEventAndLimitBranches(t *testing.T) {
	t.Parallel()

	invalid := testEvent()
	invalid.ID = ""
	if _, err := (SMTP{NameValue: "mail"}).Deliver(t.Context(), invalid); err == nil {
		t.Fatal("SMTP.Deliver(invalid event) error = nil")
	}
	if _, err := (Webhook{NameValue: "hook"}).Deliver(t.Context(), invalid); err == nil {
		t.Fatal("Webhook.Deliver(invalid event) error = nil")
	}
	baseSMTP := SMTP{
		NameValue: "mail", Address: "smtp.example.test:25", From: "jobman@example.test",
		To: []string{"ops@example.test"}, MessageLimit: -1,
	}
	if _, err := baseSMTP.Deliver(t.Context(), testEvent()); err == nil {
		t.Fatal("SMTP.Deliver(negative limit) error = nil")
	}
	if _, err := (Webhook{
		NameValue: "hook", URL: "https://example.test", ResponseLimit: -1,
	}).Deliver(t.Context(), testEvent()); err == nil {
		t.Fatal("Webhook.Deliver(negative limit) error = nil")
	}
}

func TestNotifierRemainingValidationBoundaries(t *testing.T) {
	t.Parallel()

	oversized := testEvent()
	oversized.Detail = map[string]any{"payload": strings.Repeat("x", int(maximumByteLimit))}
	if _, err := marshalEvent(oversized); err == nil {
		t.Fatal("marshalEvent() accepted an oversized payload")
	}
	deadline, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	if err := classifyContext(deadline, ErrorTransport, false); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("classifyContext(deadline) = %v", err)
	}

	if _, err := (Command{NameValue: "", Executable: "/bin/true"}).Deliver(t.Context(), testEvent()); err == nil {
		t.Fatal("Command.Deliver() accepted a blank name")
	}
	if _, err := (Command{NameValue: "command", Executable: "/bin/true", Timeout: -time.Second}).Deliver(t.Context(), testEvent()); err == nil {
		t.Fatal("Command.Deliver() accepted a negative timeout")
	}
	if _, err := (Webhook{URL: "https://example.test"}).Deliver(t.Context(), testEvent()); err == nil {
		t.Fatal("Webhook.Deliver() accepted a blank name")
	}
	if _, err := (Webhook{NameValue: "hook", URL: "https://example.test", Timeout: -time.Second}).Deliver(t.Context(), testEvent()); err == nil {
		t.Fatal("Webhook.Deliver() accepted a negative timeout")
	}
	request, err := http.NewRequest(http.MethodPost, "https://example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := (Webhook{SignatureSecret: []byte("secret"), SignatureHeader: "Host"}).applyHeaders(request, nil); err == nil {
		t.Fatal("applyHeaders() accepted a reserved signature header")
	}

	if _, err := (SMTP{
		NameValue: "mail", Address: "smtp.example.test:25", From: "jobman@example.test",
		To: []string{"ops@example.test"}, Mode: SMTPStartTLS, Timeout: -time.Second,
	}).Deliver(t.Context(), testEvent()); err == nil {
		t.Fatal("SMTP.Deliver() accepted a negative timeout")
	}
}
