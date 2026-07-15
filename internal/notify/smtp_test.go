package notify

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"testing"
	"time"
)

type smtpTransportFunc func(context.Context, SMTPRequest) error

func (function smtpTransportFunc) Send(ctx context.Context, request SMTPRequest) error {
	return function(ctx, request)
}

func TestSMTPBuildsVersionedMessageAndPassesResolvedCredentialOnlyToTransport(t *testing.T) {
	t.Parallel()

	password := []byte("top-secret")
	var received SMTPRequest
	notifier := SMTP{
		NameValue:     "operations",
		Address:       "smtp.example.test:587",
		Username:      "jobman",
		Password:      password,
		From:          "Jobman <jobman@example.test>",
		To:            []string{"Operator <operator@example.test>"},
		SubjectPrefix: "Production Jobman",
		Mode:          SMTPStartTLS,
		Transport: smtpTransportFunc(func(_ context.Context, request SMTPRequest) error {
			received = request

			return nil
		}),
	}
	result, err := notifier.Deliver(t.Context(), testEvent())
	if err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}
	if result.MessageID == "" || result.MessageID != strings.Trim(result.MessageID, "<>") {
		t.Fatalf("MessageID = %q", result.MessageID)
	}
	if received.Address != notifier.Address || received.Username != notifier.Username {
		t.Fatalf("SMTP request destination = %#v", received)
	}
	if !bytes.Equal(received.Password, password) {
		t.Fatal("SMTP request password differs from resolved value")
	}
	password[0] = 'X'
	if string(received.Password) != "top-secret" {
		t.Fatal("SMTP request did not clone credential")
	}
	if received.Sender != "jobman@example.test" || len(received.Recipients) != 1 || received.Recipients[0] != "operator@example.test" {
		t.Fatalf("SMTP envelope = %q -> %#v", received.Sender, received.Recipients)
	}
	message := string(received.Message)
	if strings.Contains(message, "top-secret") || strings.Contains(message, notifier.Address) {
		t.Fatalf("SMTP message contains credential or server: %q", message)
	}
	if !strings.Contains(message, "Subject: Production Jobman: run_succeeded\r\n") ||
		!strings.Contains(message, "Message-ID: <"+result.MessageID+">\r\n") {
		t.Fatalf("SMTP message headers = %q", message)
	}
	_, body, found := strings.Cut(message, "\r\n\r\n")
	if !found {
		t.Fatalf("SMTP message has no body: %q", body)
	}
	encoded := strings.NewReplacer("\r", "", "\n", "").Replace(body)
	payload, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if !strings.Contains(string(payload), `"schema_version":1`) || !strings.Contains(string(payload), `"id":"evt_01"`) {
		t.Fatalf("decoded SMTP payload = %q", payload)
	}
}

func TestSMTPDefaultsToStartTLS(t *testing.T) {
	t.Parallel()

	var mode SMTPMode
	notifier := SMTP{
		NameValue: "mail",
		Address:   "smtp.example.test:587",
		From:      "jobman@example.test",
		To:        []string{"operator@example.test"},
		Transport: smtpTransportFunc(func(_ context.Context, request SMTPRequest) error {
			mode = request.Mode

			return nil
		}),
	}
	if _, err := notifier.Deliver(t.Context(), testEvent()); err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}
	if mode != SMTPStartTLS {
		t.Fatalf("SMTP mode = %q, want %q", mode, SMTPStartTLS)
	}
}

func TestSMTPRequiresSecureTransportUnlessExplicitlyAllowed(t *testing.T) {
	t.Parallel()

	base := SMTP{
		NameValue: "mail",
		Address:   "smtp.example.test:25",
		From:      "jobman@example.test",
		To:        []string{"operator@example.test"},
		Mode:      SMTPPlain,
		Transport: smtpTransportFunc(func(_ context.Context, _ SMTPRequest) error { return nil }),
	}
	if _, err := base.Deliver(t.Context(), testEvent()); err == nil {
		t.Fatal("Deliver() accepted plaintext SMTP without opt-in")
	}
	base.AllowPlain = true
	if _, err := base.Deliver(t.Context(), testEvent()); err != nil {
		t.Fatalf("Deliver() with explicit plaintext opt-in error = %v", err)
	}
}

func TestSMTPClassifiesPermanentAndTransientFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		transportError error
		retryable      bool
		name           string
	}{
		{name: "permanent", transportError: &SMTPResponseError{Permanent: true}, retryable: false},
		{name: "transient", transportError: &SMTPResponseError{Permanent: false}, retryable: true},
		{name: "unknown", transportError: errors.New("password=top-secret"), retryable: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			notifier := SMTP{
				NameValue: "mail",
				Address:   "smtp.example.test:587",
				From:      "jobman@example.test",
				To:        []string{"operator@example.test"},
				Transport: smtpTransportFunc(func(_ context.Context, _ SMTPRequest) error {
					return test.transportError
				}),
			}
			_, err := notifier.Deliver(t.Context(), testEvent())
			if err == nil {
				t.Fatal("Deliver() error = nil")
			}
			if IsRetryable(err) != test.retryable || strings.Contains(err.Error(), "top-secret") {
				t.Fatalf("Deliver() error = %q, retryable want %t", err, test.retryable)
			}
		})
	}
}

func TestSMTPValidatesEnvelopeHeadersAndSize(t *testing.T) {
	t.Parallel()

	base := SMTP{
		NameValue: "mail",
		Address:   "smtp.example.test:587",
		From:      "jobman@example.test",
		To:        []string{"operator@example.test"},
		Transport: smtpTransportFunc(func(_ context.Context, _ SMTPRequest) error { return nil }),
	}
	tests := []struct {
		mutate func(*SMTP)
		name   string
	}{
		{name: "address", mutate: func(notifier *SMTP) { notifier.Address = "smtp.example.test" }},
		{name: "sender", mutate: func(notifier *SMTP) { notifier.From = "not an address" }},
		{name: "recipient", mutate: func(notifier *SMTP) { notifier.To = []string{"not an address"} }},
		{name: "subject injection", mutate: func(notifier *SMTP) { notifier.SubjectPrefix = "alert\r\nBcc: attacker@example.test" }},
		{name: "message size", mutate: func(notifier *SMTP) { notifier.MessageLimit = 1 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			notifier := base
			test.mutate(&notifier)
			if _, err := notifier.Deliver(t.Context(), testEvent()); err == nil {
				t.Fatal("Deliver() error = nil")
			}
		})
	}
}

func TestSMTPResponseErrorHidesServerText(t *testing.T) {
	t.Parallel()

	err := smtpResponseError(errors.New("535 password top-secret rejected"))
	if strings.Contains(err.Error(), "top-secret") {
		t.Fatalf("smtpResponseError() = %q", err)
	}
}

func TestNetSMTPTransportPlainConversation(t *testing.T) {
	t.Parallel()

	clientConnection, serverConnection := net.Pipe()
	serverDone := make(chan error, 1)
	go func() { serverDone <- serveSMTPConversation(serverConnection) }()
	t.Cleanup(func() { _ = serverConnection.Close() })

	request := SMTPRequest{
		Address:    "smtp.example.test:25",
		Sender:     "jobman@example.test",
		Recipients: []string{"operator@example.test"},
		Message:    []byte("Subject: test\r\n\r\npayload\r\n"),
		Mode:       SMTPPlain,
	}
	connector := func(ctx context.Context, got SMTPRequest) (*smtp.Client, error) {
		return connectSMTPWithDial(ctx, got, func(
			context.Context, string, SMTPMode, *tls.Config,
		) (net.Conn, error) {
			return clientConnection, nil
		})
	}
	if err := sendSMTP(t.Context(), request, connector); err != nil {
		t.Fatalf("sendSMTP() error = %v", err)
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("SMTP server error = %v", err)
	}

	if err := (NetSMTPTransport{}).Send(t.Context(), SMTPRequest{Address: "invalid"}); err == nil {
		t.Fatal("NetSMTPTransport.Send(invalid address) error = nil")
	}
	connectFailure := errors.New("connect failed")
	if err := sendSMTP(t.Context(), request, func(context.Context, SMTPRequest) (*smtp.Client, error) {
		return nil, connectFailure
	}); err == nil {
		t.Fatal("sendSMTP(connect failure) error = nil")
	}
	if _, err := dialSMTP(t.Context(), "unused", SMTPMode("unknown"), &tls.Config{}); err == nil {
		t.Fatal("dialSMTP(unknown mode) error = nil")
	}
}

func TestSMTPNamesAndValidationEdges(t *testing.T) {
	t.Parallel()

	if got := (SMTP{NameValue: "mail"}).Name(); got != "mail" {
		t.Fatalf("SMTP.Name() = %q", got)
	}
	base := SMTP{
		NameValue: "mail",
		Address:   "smtp.example.test:587",
		From:      "jobman@example.test",
		To:        []string{"operator@example.test"},
		Transport: smtpTransportFunc(func(context.Context, SMTPRequest) error { return nil }),
	}
	for _, mutate := range []func(*SMTP){
		func(value *SMTP) { value.NameValue = " " },
		func(value *SMTP) { value.To = nil },
		func(value *SMTP) { value.Password = []byte("secret") },
		func(value *SMTP) { value.Mode = SMTPMode("unknown") },
		func(value *SMTP) { value.Timeout = -time.Second },
	} {
		notifier := base
		mutate(&notifier)
		if _, err := notifier.Deliver(t.Context(), testEvent()); err == nil {
			t.Fatal("SMTP.Deliver(invalid configuration) error = nil")
		}
	}
}

func TestNetSMTPTransportProtocolFailures(t *testing.T) {
	t.Parallel()

	for _, stage := range []string{"MAIL", "RCPT", "DATA", "QUIT"} {
		t.Run(stage, func(t *testing.T) {
			t.Parallel()
			clientConnection, serverConnection := net.Pipe()
			serverDone := make(chan error, 1)
			go func() { serverDone <- serveSMTPConversationAt(serverConnection, stage) }()
			t.Cleanup(func() { _ = serverConnection.Close() })
			request := SMTPRequest{
				Address:    "smtp.example.test:25",
				Sender:     "jobman@example.test",
				Recipients: []string{"operator@example.test"},
				Message:    []byte("Subject: test\r\n\r\npayload\r\n"),
				Mode:       SMTPPlain,
			}
			connector := func(ctx context.Context, got SMTPRequest) (*smtp.Client, error) {
				return connectSMTPWithDial(ctx, got, func(
					context.Context, string, SMTPMode, *tls.Config,
				) (net.Conn, error) {
					return clientConnection, nil
				})
			}
			if err := sendSMTP(t.Context(), request, connector); err == nil {
				t.Fatalf("sendSMTP(%s rejection) error = nil", stage)
			}
			if err := <-serverDone; err != nil {
				t.Fatalf("SMTP server error = %v", err)
			}
		})
	}
}

func serveSMTPConversation(connection net.Conn) error {
	return serveSMTPConversationAt(connection, "")
}

func serveSMTPConversationAt(connection net.Conn, reject string) error {
	defer connection.Close()
	reader := bufio.NewReader(connection)
	writer := bufio.NewWriter(connection)
	respond := func(response string) error {
		if _, err := writer.WriteString(response); err != nil {
			return err
		}

		return writer.Flush()
	}
	if err := respond("220 smtp.example.test ready\r\n"); err != nil {
		return err
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		switch {
		case strings.HasPrefix(line, "EHLO "):
			if err := respond("250 smtp.example.test\r\n"); err != nil {
				return err
			}
		case strings.HasPrefix(line, "MAIL FROM:"):
			if reject == "MAIL" {
				return respond("550 sender rejected\r\n")
			}
			if err := respond("250 sender accepted\r\n"); err != nil {
				return err
			}
		case strings.HasPrefix(line, "RCPT TO:"):
			if reject == "RCPT" {
				return respond("550 recipient rejected\r\n")
			}
			if err := respond("250 recipient accepted\r\n"); err != nil {
				return err
			}
		case line == "DATA\r\n":
			if reject == "DATA" {
				return respond("550 data rejected\r\n")
			}
			if err := respond("354 send message\r\n"); err != nil {
				return err
			}
			for {
				dataLine, readErr := reader.ReadString('\n')
				if readErr != nil {
					return readErr
				}
				if dataLine == ".\r\n" {
					break
				}
			}
			if err := respond("250 queued\r\n"); err != nil {
				return err
			}
		case line == "QUIT\r\n":
			if reject == "QUIT" {
				return respond("550 quit rejected\r\n")
			}
			return respond("221 goodbye\r\n")
		default:
			return fmt.Errorf("unexpected SMTP command %q", line)
		}
	}
}
