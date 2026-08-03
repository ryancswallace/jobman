package notify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
	"strconv"
	"strings"
	"time"
)

// SMTPMode identifies how the SMTP connection is protected.
type SMTPMode string

// Supported SMTP connection modes.
const (
	SMTPStartTLS SMTPMode = "starttls"
	SMTPImplicit SMTPMode = "implicit"
	SMTPPlain    SMTPMode = "plain"
)

// SMTPRequest contains one fully constructed message and resolved in-memory
// credentials. Implementations must never include these fields in errors.
type SMTPRequest struct {
	Message    []byte
	Password   []byte
	Recipients []string
	Address    string
	ServerName string
	Username   string
	Sender     string
	Mode       SMTPMode
}

// SMTPTransport sends a prepared RFC 5322 message.
type SMTPTransport interface {
	Send(context.Context, SMTPRequest) error
}

// SMTPResponseError classifies a secret-free SMTP transport failure.
type SMTPResponseError struct {
	Permanent bool
}

// Error returns a stable description without the server response.
func (*SMTPResponseError) Error() string {
	return "smtp delivery failed"
}

// NetSMTPTransport sends SMTP messages using the standard library.
type NetSMTPTransport struct{}

// Send connects, negotiates the configured TLS mode, authenticates, and sends
// one message while respecting the context deadline.
func (NetSMTPTransport) Send(ctx context.Context, request SMTPRequest) error {
	return sendSMTP(ctx, request, connectSMTP)
}

func sendSMTP(
	ctx context.Context,
	request SMTPRequest,
	connect func(context.Context, SMTPRequest) (*smtp.Client, error),
) error {
	client, err := connect(ctx, request)
	if err != nil {
		return smtpResponseError(err)
	}
	closed := false
	defer func() {
		if !closed {
			_ = client.Close()
		}
	}()
	if err := sendSMTPMessage(client, request); err != nil {
		return smtpResponseError(err)
	}
	quitErr := client.Quit()
	closed = true
	if quitErr != nil {
		return smtpResponseError(quitErr)
	}

	return nil
}

func connectSMTP(ctx context.Context, request SMTPRequest) (*smtp.Client, error) {
	return connectSMTPWithDial(ctx, request, dialSMTP)
}

func connectSMTPWithDial(
	ctx context.Context,
	request SMTPRequest,
	dial func(context.Context, string, SMTPMode, *tls.Config) (net.Conn, error),
) (*smtp.Client, error) {
	host, _, err := net.SplitHostPort(request.Address)
	if err != nil {
		return nil, err
	}
	serverName := request.ServerName
	if serverName == "" {
		serverName = host
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: serverName}
	connection, err := dial(ctx, request.Address, request.Mode, tlsConfig)
	if err != nil {
		return nil, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		if deadlineErr := connection.SetDeadline(deadline); deadlineErr != nil {
			_ = connection.Close()

			return nil, deadlineErr
		}
	}

	client, err := smtp.NewClient(connection, host)
	if err != nil {
		_ = connection.Close()

		return nil, err
	}
	if request.Mode == SMTPStartTLS {
		if tlsErr := client.StartTLS(tlsConfig); tlsErr != nil {
			_ = client.Close()

			return nil, tlsErr
		}
	}
	if request.Username != "" {
		authentication := smtp.PlainAuth("", request.Username, string(request.Password), serverName)
		if authErr := client.Auth(authentication); authErr != nil {
			_ = client.Close()

			return nil, authErr
		}
	}

	return client, nil
}

func sendSMTPMessage(client *smtp.Client, request SMTPRequest) error {
	if err := client.Mail(request.Sender); err != nil {
		return err
	}
	for _, recipient := range request.Recipients {
		if err := client.Rcpt(recipient); err != nil {
			return err
		}
	}
	writer, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := writer.Write(request.Message); err != nil {
		_ = writer.Close()

		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}

	return nil
}

func dialSMTP(ctx context.Context, address string, mode SMTPMode, tlsConfig *tls.Config) (net.Conn, error) {
	switch mode {
	case SMTPImplicit:
		dialer := &tls.Dialer{NetDialer: &net.Dialer{}, Config: tlsConfig}

		return dialer.DialContext(ctx, "tcp", address)
	case SMTPStartTLS, SMTPPlain:
		return (&net.Dialer{}).DialContext(ctx, "tcp", address)
	default:
		return nil, errors.New("unsupported SMTP mode")
	}
}

func smtpResponseError(err error) error {
	var protocolError *textproto.Error
	permanent := errors.As(err, &protocolError) && protocolError.Code >= 500 && protocolError.Code < 600

	return &SMTPResponseError{Permanent: permanent}
}

// SMTP delivers an event as an email containing the same JSON representation
// used by command and webhook notifiers.
type SMTP struct {
	Transport     SMTPTransport
	NameValue     string
	Address       string
	ServerName    string
	Username      string
	Password      []byte
	From          string
	To            []string
	SubjectPrefix string
	Mode          SMTPMode
	Timeout       time.Duration
	MessageLimit  int64
	AllowPlain    bool
}

// Name returns the configured notifier identity.
func (notifier SMTP) Name() string {
	return notifier.NameValue
}

// Deliver builds and sends one bounded email.
func (notifier SMTP) Deliver(parent context.Context, event Event) (Result, error) {
	_, err := marshalEvent(event)
	if err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(notifier.NameValue) == "" {
		return Result{}, errors.New("smtp notifier name is required")
	}
	request, messageID, err := notifier.request(event)
	if err != nil {
		return Result{}, err
	}
	ctx, cancel, err := withTimeout(parent, notifier.Timeout)
	if err != nil {
		return Result{}, err
	}
	defer cancel()
	transport := notifier.Transport
	if transport == nil {
		transport = NetSMTPTransport{}
	}
	if err := transport.Send(ctx, request); err != nil {
		var responseError *SMTPResponseError
		if errors.As(err, &responseError) {
			return Result{MessageID: messageID}, classifyContext(ctx, ErrorRejected, !responseError.Permanent)
		}

		return Result{MessageID: messageID}, classifyContext(ctx, ErrorTransport, true)
	}

	return Result{MessageID: messageID}, nil
}

func (notifier SMTP) request(event Event) (SMTPRequest, string, error) {
	mode, err := notifier.connectionMode()
	if err != nil {
		return SMTPRequest{}, "", err
	}
	if notifier.Username == "" && len(notifier.Password) != 0 {
		return SMTPRequest{}, "", errors.New("smtp notifier password requires a username")
	}
	from, recipients, toHeaders, err := notifier.envelope()
	if err != nil {
		return SMTPRequest{}, "", err
	}
	prefix := notifier.SubjectPrefix
	if prefix == "" {
		prefix = "Jobman"
	}
	if strings.ContainsAny(prefix, "\r\n\x00") {
		return SMTPRequest{}, "", errors.New("smtp notifier subject prefix is invalid")
	}
	digest := sha256.Sum256([]byte(event.ID))
	messageID := hex.EncodeToString(digest[:16]) + "@jobman.local"
	message := buildMessage(from.String(), toHeaders, prefix, event, messageID)
	limit, err := byteLimit(notifier.MessageLimit)
	if err != nil {
		return SMTPRequest{}, "", err
	}
	if int64(len(message)) > limit {
		return SMTPRequest{}, "", errors.New("smtp notification message exceeds configured limit")
	}

	return SMTPRequest{
		Address:    notifier.Address,
		ServerName: notifier.ServerName,
		Username:   notifier.Username,
		Password:   bytes.Clone(notifier.Password),
		Sender:     from.Address,
		Recipients: recipients,
		Message:    message,
		Mode:       mode,
	}, messageID, nil
}

func (notifier SMTP) connectionMode() (SMTPMode, error) {
	if _, _, err := net.SplitHostPort(notifier.Address); err != nil {
		return "", errors.New("smtp notifier address must include a host and port")
	}
	mode := notifier.Mode
	if mode == "" {
		mode = SMTPStartTLS
	}
	switch mode {
	case SMTPStartTLS, SMTPImplicit:
		return mode, nil
	case SMTPPlain:
		if notifier.AllowPlain {
			return mode, nil
		}
	}

	return "", errors.New("smtp notifier requires TLS")
}

func (notifier SMTP) envelope() (
	from *mail.Address,
	recipients []string,
	toHeaders []string,
	returned error,
) {
	from, err := mail.ParseAddress(notifier.From)
	if err != nil {
		return nil, nil, nil, errors.New("smtp notifier sender is invalid")
	}
	if len(notifier.To) == 0 {
		return nil, nil, nil, errors.New("smtp notifier requires at least one recipient")
	}
	recipients = make([]string, 0, len(notifier.To))
	toHeaders = make([]string, 0, len(notifier.To))
	for _, value := range notifier.To {
		recipient, parseErr := mail.ParseAddress(value)
		if parseErr != nil {
			return nil, nil, nil, errors.New("smtp notifier recipient is invalid")
		}
		recipients = append(recipients, recipient.Address)
		toHeaders = append(toHeaders, recipient.String())
	}

	return from, recipients, toHeaders, nil
}

func buildMessage(from string, to []string, prefix string, event Event, messageID string) []byte {
	var message bytes.Buffer
	fmt.Fprintf(&message, "From: %s\r\n", from)
	fmt.Fprintf(&message, "To: %s\r\n", strings.Join(to, ", "))
	fmt.Fprintf(&message, "Date: %s\r\n", event.OccurredAt.UTC().Format(time.RFC1123Z))
	fmt.Fprintf(&message, "Message-ID: <%s>\r\n", messageID)
	fmt.Fprintf(&message, "Subject: %s\r\n", encodeSubject(smtpSubject(prefix, event)))
	message.WriteString("MIME-Version: 1.0\r\n")
	message.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	message.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	writeBase64Lines(&message, smtpBody(event))

	return message.Bytes()
}

func smtpSubject(prefix string, event Event) string {
	identity := shortNotificationID(event.JobID)
	if summary, found := event.Summary(); found && summary.JobName != "" {
		identity = truncateSubjectValue(summary.JobName, 64) + " [" + identity + "]"
	}

	return strings.TrimSpace(prefix) + ": " + notificationSubjectStatus(event.Type) + ": " + identity
}

func notificationSubjectStatus(eventType EventType) string {
	switch eventType {
	case EventJobSucceeded, EventRunSucceeded:
		return "succeeded"
	case EventJobFailed, EventRunFailed:
		return "failed"
	case EventJobTimedOut, EventRunTimedOut:
		return "timed out"
	case EventJobCancelled, EventRunCancelled:
		return "cancelled" //nolint:misspell // Event vocabulary uses this spelling.
	case EventJobLost, EventRunLost:
		return "lost"
	case EventJobAborted:
		return "aborted"
	case EventSubmissionFailed:
		return "submission failed"
	case EventRetryScheduled:
		return "retry scheduled"
	case EventJobStarted, EventRunStarted:
		return "started"
	default:
		return string(eventType)
	}
}

func smtpBody(event Event) []byte {
	var body bytes.Buffer
	summary, _ := event.Summary()
	writeSMTPField(&body, "Job", summary.JobName)
	writeSMTPField(&body, "Job ID", event.JobID)
	writeSMTPField(&body, "Event", string(event.Type))
	writeSMTPField(&body, "Event ID", event.ID)
	writeSMTPField(&body, "Outcome", summary.JobOutcome)
	writeSMTPTime(&body, "Occurred", &event.OccurredAt)
	writeSMTPTime(&body, "Submitted", summary.SubmittedAt)
	writeSMTPTime(&body, "Started", summary.StartedAt)
	writeSMTPTime(&body, "Completed", summary.CompletedAt)
	writeSMTPDuration(&body, "Duration", summary.StartedAt, summary.CompletedAt)
	if summary.RunNumber > 0 {
		writeSMTPField(&body, "Run", strconv.FormatUint(summary.RunNumber, 10))
	}
	runID := summary.RunID
	if runID == "" {
		runID = event.RunID
	}
	writeSMTPField(&body, "Run ID", runID)
	writeSMTPField(&body, "Run outcome", summary.RunOutcome)
	writeSMTPTime(&body, "Run started", summary.RunStartedAt)
	writeSMTPTime(&body, "Run completed", summary.RunCompletedAt)
	writeSMTPDuration(&body, "Run duration", summary.RunStartedAt, summary.RunCompletedAt)
	if summary.ExitCode != nil {
		writeSMTPField(&body, "Exit code", strconv.Itoa(*summary.ExitCode))
	}
	if summary.RunCount != nil && summary.SuccessCount != nil && summary.FailureCount != nil {
		writeSMTPField(&body, "Runs", fmt.Sprintf(
			"%d total, %d succeeded, %d failed",
			*summary.RunCount,
			*summary.SuccessCount,
			*summary.FailureCount,
		))
	}
	writeSMTPField(&body, "Diagnostic", summary.DiagnosticCode)
	writeSMTPTime(&body, "Next run", summary.NextRunAt)
	writeSMTPField(&body, "Reason", summary.Reason)
	body.WriteString("\r\nInspect:\r\n  jobman show ")
	body.WriteString(event.JobID)
	body.WriteString("\r\n")
	if summary.RunNumber > 0 || event.RunID != "" {
		body.WriteString("  jobman logs ")
		body.WriteString(event.JobID)
		body.WriteString("\r\n")
	}

	return body.Bytes()
}

func writeSMTPField(body *bytes.Buffer, label, value string) {
	if value == "" {
		return
	}
	fmt.Fprintf(body, "%-14s %s\r\n", label+":", value)
}

func writeSMTPTime(body *bytes.Buffer, label string, value *time.Time) {
	if value == nil || value.IsZero() {
		return
	}
	writeSMTPField(body, label, value.UTC().Format(time.RFC3339Nano))
}

func writeSMTPDuration(body *bytes.Buffer, label string, start, end *time.Time) {
	if start == nil || end == nil || end.Before(*start) {
		return
	}
	writeSMTPField(body, label, end.Sub(*start).String())
}

func shortNotificationID(value string) string {
	const shortLength = 8
	if len(value) <= shortLength {
		return value
	}

	return value[:shortLength]
}

func truncateSubjectValue(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}

	return string(runes[:limit-1]) + "…"
}

func encodeSubject(value string) string {
	for _, character := range value {
		if character > 0x7f {
			return mime.QEncoding.Encode("utf-8", value)
		}
	}

	return value
}

func writeBase64Lines(writer *bytes.Buffer, payload []byte) {
	encoded := base64.StdEncoding.EncodeToString(payload)
	for len(encoded) > 76 {
		writer.WriteString(encoded[:76])
		writer.WriteString("\r\n")
		encoded = encoded[76:]
	}
	writer.WriteString(encoded)
	writer.WriteString("\r\n")
}
