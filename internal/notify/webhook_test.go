package notify

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (function roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestWebhookDeliversSignedJSONAndBoundsResponse(t *testing.T) {
	t.Parallel()

	secret := []byte("resolved-secret")
	var received *http.Request
	var requestBody []byte
	notifier := Webhook{
		NameValue:       "deploy",
		URL:             "hooks.example.test/jobman",
		SignatureSecret: secret,
		Headers:         map[string]string{"X-Deployment": "production"},
		ResponseLimit:   4,
		Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
			received = request.Clone(request.Context())
			var err error
			requestBody, err = io.ReadAll(request.Body)
			if err != nil {
				t.Fatalf("read request: %v", err)
			}

			return &http.Response{
				StatusCode: http.StatusAccepted,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("abcdef")),
				Request:    request,
			}, nil
		}),
	}
	result, err := notifier.Deliver(t.Context(), testEvent())
	if err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}
	if received.URL.String() != "https://hooks.example.test/jobman" {
		t.Fatalf("URL = %q", received.URL)
	}
	if received.Method != http.MethodPost || received.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("request = %s, Content-Type %q", received.Method, received.Header.Get("Content-Type"))
	}
	if received.Header.Get("Idempotency-Key") != "evt_01" || received.Header.Get("X-Deployment") != "production" {
		t.Fatalf("headers = %#v", received.Header)
	}
	digest := hmac.New(sha256.New, secret)
	_, _ = digest.Write(requestBody)
	wantSignature := "sha256=" + hex.EncodeToString(digest.Sum(nil))
	if received.Header.Get(defaultSignatureHeader) != wantSignature {
		t.Fatalf("signature = %q, want %q", received.Header.Get(defaultSignatureHeader), wantSignature)
	}
	if result.StatusCode != http.StatusAccepted || string(result.ResponseBody) != "abcd" || !result.Truncated {
		t.Fatalf("result = %#v", result)
	}
}

func TestWebhookSecureURLDefaults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		webhook Webhook
		name    string
	}{
		{name: "HTTP", webhook: Webhook{NameValue: "x", URL: "http://example.test/hook"}},
		{name: "loopback", webhook: Webhook{NameValue: "x", URL: "https://127.0.0.1/hook"}},
		{name: "private", webhook: Webhook{NameValue: "x", URL: "https://10.1.2.3/hook"}},
		{name: "localhost", webhook: Webhook{NameValue: "x", URL: "https://notify.localhost/hook"}},
		{name: "userinfo", webhook: Webhook{NameValue: "x", URL: "https://user:password@example.test/hook"}}, //nolint:gosec // Deliberately invalid credential-bearing URL fixture.
		{name: "fragment", webhook: Webhook{NameValue: "x", URL: "https://example.test/hook#secret"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := test.webhook.Deliver(t.Context(), testEvent()); err == nil {
				t.Fatal("Deliver() error = nil")
			}
		})
	}
}

func TestWebhookExplicitlyAllowsHTTPAndPrivateDestination(t *testing.T) {
	t.Parallel()

	notifier := Webhook{
		NameValue:           "local",
		URL:                 "http://127.0.0.1/hook",
		AllowInsecureHTTP:   true,
		AllowPrivateNetwork: true,
		Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusNoContent,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    request,
			}, nil
		}),
	}
	if _, err := notifier.Deliver(t.Context(), testEvent()); err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}
}

func TestWebhookStatusRetryClassification(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status    int
		retryable bool
	}{
		{status: http.StatusBadRequest, retryable: false},
		{status: http.StatusRequestTimeout, retryable: true},
		{status: http.StatusTooManyRequests, retryable: true},
		{status: http.StatusBadGateway, retryable: true},
	}
	for _, test := range tests {
		t.Run(http.StatusText(test.status), func(t *testing.T) {
			t.Parallel()
			notifier := Webhook{
				NameValue: "hook",
				URL:       "https://example.test/hook",
				Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: test.status,
						Header:     make(http.Header),
						Body:       io.NopCloser(strings.NewReader("server detail")),
						Request:    request,
					}, nil
				}),
			}
			result, err := notifier.Deliver(t.Context(), testEvent())
			if err == nil {
				t.Fatal("Deliver() error = nil")
			}
			if result.StatusCode != test.status || IsRetryable(err) != test.retryable {
				t.Fatalf("Deliver() = (%#v, %v), retryable want %t", result, err, test.retryable)
			}
			if strings.Contains(err.Error(), "server detail") {
				t.Fatalf("Deliver() error = %q, contains response", err)
			}
		})
	}
}

func TestWebhookHidesTransportError(t *testing.T) {
	t.Parallel()

	notifier := Webhook{
		NameValue: "hook",
		URL:       "https://example.test/hook?token=top-secret",
		Transport: roundTripperFunc(func(_ *http.Request) (*http.Response, error) {
			return nil, errors.New("token=top-secret")
		}),
	}
	_, err := notifier.Deliver(t.Context(), testEvent())
	if err == nil {
		t.Fatal("Deliver() error = nil")
	}
	if strings.Contains(err.Error(), "top-secret") || !IsRetryable(err) {
		t.Fatalf("Deliver() error = %q", err)
	}
}

func TestWebhookRejectsReservedAndInjectedHeaders(t *testing.T) {
	t.Parallel()

	tests := []map[string]string{
		{"Host": "example.test"},
		{"Authorization\r\nInjected": "value"},
		{"X-Test": "value\r\nInjected: yes"},
	}
	for _, headers := range tests {
		notifier := Webhook{NameValue: "hook", URL: "https://example.test", Headers: headers}
		if _, err := notifier.Deliver(t.Context(), testEvent()); err == nil {
			t.Fatalf("Deliver() with headers %#v returned nil error", headers)
		}
	}
}

func TestWebhookCrossOriginRedirectIsRejected(t *testing.T) {
	t.Parallel()

	notifier := Webhook{
		NameValue:       "hook",
		URL:             "https://example.test/start",
		FollowRedirects: true,
		Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusTemporaryRedirect,
				Header:     http.Header{"Location": []string{"https://elsewhere.test/hook"}},
				Body:       io.NopCloser(strings.NewReader("")),
				Request:    request,
			}, nil
		}),
	}
	_, err := notifier.Deliver(t.Context(), testEvent())
	if err == nil {
		t.Fatal("Deliver() error = nil")
	}
	if !IsRetryable(err) {
		t.Fatalf("Deliver() error = %v, want retryable transport error", err)
	}
}

func TestPrivateAddress(t *testing.T) {
	t.Parallel()

	if !privateHost("[::1]") && !privateHost("::1") {
		t.Fatal("privateHost(loopback) = false")
	}
	if privateHost("203.0.113.10") {
		t.Fatal("privateHost(documentation address) = true")
	}
}

func TestReadBoundedHidesReaderError(t *testing.T) {
	t.Parallel()

	_, _, err := readBounded(errorReader{}, 10)
	if err == nil || strings.Contains(err.Error(), "top-secret") {
		t.Fatalf("readBounded() error = %v", err)
	}
}

func TestWebhookNameRedirectAndTransportPolicies(t *testing.T) {
	t.Parallel()

	webhook := Webhook{NameValue: "hook"}
	if webhook.Name() != "hook" {
		t.Fatalf("Name() = %q", webhook.Name())
	}
	origin, parseErr := url.Parse("https://example.test/hook")
	if parseErr != nil {
		t.Fatalf("parse origin: %v", parseErr)
	}
	request := &http.Request{URL: origin}
	if err := webhook.client(origin).CheckRedirect(request, nil); !errors.Is(err, http.ErrUseLastResponse) {
		t.Fatalf("disabled redirect error = %v", err)
	}
	webhook.FollowRedirects = true
	client := webhook.client(origin)
	prior := make([]*http.Request, maximumRedirects)
	if err := client.CheckRedirect(request, prior); err == nil {
		t.Fatal("redirect limit error = nil")
	}
	crossOrigin, crossParseErr := url.Parse("https://other.example.test/hook")
	if crossParseErr != nil {
		t.Fatalf("parse cross-origin URL: %v", crossParseErr)
	}
	if err := client.CheckRedirect(&http.Request{URL: crossOrigin}, nil); err == nil {
		t.Fatal("cross-origin redirect error = nil")
	}
	if err := client.CheckRedirect(request, nil); err != nil {
		t.Fatalf("same-origin redirect error = %v", err)
	}

	transport := secureHTTPTransport(false)
	if _, err := transport.DialContext(t.Context(), "tcp", "invalid-address"); err == nil {
		t.Fatal("DialContext(invalid address) error = nil")
	}
	if _, err := transport.DialContext(t.Context(), "tcp", "localhost:1"); err == nil {
		t.Fatal("DialContext(private address) error = nil")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
	defer cancel()
	if _, err := secureHTTPTransport(true).DialContext(ctx, "tcp", "localhost:1"); err == nil {
		t.Fatal("DialContext(unreachable allowed address) error = nil")
	}
}

func TestWebhookResponseReadAndCloseFailures(t *testing.T) {
	t.Parallel()

	for _, body := range []io.ReadCloser{
		readCloseError{readErr: errors.New("read")},
		readCloseError{reader: strings.NewReader("body"), closeErr: errors.New("close")},
	} {
		notifier := Webhook{
			NameValue: "hook",
			URL:       "https://example.test/hook",
			Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusOK, Body: body, Request: request}, nil
			}),
		}
		result, err := notifier.Deliver(t.Context(), testEvent())
		if err == nil || !IsRetryable(err) {
			t.Fatalf("Deliver() = %#v, %v; want retryable error", result, err)
		}
	}
}

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) {
	return 0, errors.New("top-secret")
}

type readCloseError struct {
	reader   io.Reader
	readErr  error
	closeErr error
}

func (body readCloseError) Read(payload []byte) (int, error) {
	if body.readErr != nil {
		return 0, body.readErr
	}

	return body.reader.Read(payload)
}

func (body readCloseError) Close() error { return body.closeErr }
