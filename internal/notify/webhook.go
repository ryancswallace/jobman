package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/textproto"
	"net/url"
	"strings"
	"time"
)

const (
	defaultSignatureHeader = "X-Jobman-Signature"
	maximumRedirects       = 10
)

// Webhook delivers JSON events with HTTP POST. HTTPS, no redirects, and no
// local, link-local, or private destinations are the secure defaults.
type Webhook struct {
	Transport           http.RoundTripper
	Headers             map[string]string
	NameValue           string
	URL                 string
	SignatureHeader     string
	SignatureSecret     []byte
	Timeout             time.Duration
	ResponseLimit       int64
	AllowInsecureHTTP   bool
	AllowPrivateNetwork bool
	FollowRedirects     bool
}

// Name returns the configured notifier identity.
func (webhook Webhook) Name() string {
	return webhook.NameValue
}

// Deliver posts one event and returns a bounded response body.
func (webhook Webhook) Deliver(parent context.Context, event Event) (Result, error) {
	payload, err := marshalEvent(event)
	if err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(webhook.NameValue) == "" {
		return Result{}, errors.New("webhook notifier name is required")
	}
	endpoint, err := webhook.endpoint()
	if err != nil {
		return Result{}, err
	}
	limit, err := byteLimit(webhook.ResponseLimit)
	if err != nil {
		return Result{}, err
	}
	ctx, cancel, err := withTimeout(parent, webhook.Timeout)
	if err != nil {
		return Result{}, err
	}
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), bytes.NewReader(payload))
	if err != nil {
		return Result{}, deliveryError(ErrorInvalid, false)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", "jobman-notifier/1")
	request.Header.Set("Idempotency-Key", event.ID)
	request.Header.Set("X-Jobman-Event-Id", event.ID)
	if headerErr := webhook.applyHeaders(request, payload); headerErr != nil {
		return Result{}, headerErr
	}

	client := webhook.client(endpoint)
	response, err := client.Do(request)
	if err != nil {
		return Result{}, classifyContext(ctx, ErrorTransport, true)
	}
	body, truncated, readErr := readBounded(response.Body, limit)
	closeErr := response.Body.Close()
	result := Result{ResponseBody: body, StatusCode: response.StatusCode, Truncated: truncated}
	if readErr != nil || closeErr != nil {
		return result, classifyContext(ctx, ErrorTransport, true)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return result, deliveryError(ErrorRejected, retryableHTTPStatus(response.StatusCode))
	}

	return result, nil
}

func (webhook Webhook) endpoint() (*url.URL, error) {
	value := strings.TrimSpace(webhook.URL)
	if !strings.Contains(value, "://") {
		value = "https://" + value
	}
	endpoint, err := url.Parse(value)
	if err != nil || endpoint.Host == "" || endpoint.User != nil || endpoint.Fragment != "" {
		return nil, errors.New("webhook notifier URL is invalid")
	}
	if endpoint.Scheme != "https" && (endpoint.Scheme != "http" || !webhook.AllowInsecureHTTP) {
		return nil, errors.New("webhook notifier URL must use HTTPS")
	}
	if !webhook.AllowPrivateNetwork && privateHost(endpoint.Hostname()) {
		return nil, errors.New("webhook notifier destination is not permitted")
	}

	return endpoint, nil
}

func (webhook Webhook) applyHeaders(request *http.Request, payload []byte) error {
	for name, value := range webhook.Headers {
		canonical := textproto.CanonicalMIMEHeaderKey(name)
		if canonical == "" || strings.ContainsAny(value, "\r\n\x00") {
			return errors.New("webhook notifier header is invalid")
		}
		switch canonical {
		case "Content-Length", "Host", "Idempotency-Key", "X-Jobman-Event-Id":
			return errors.New("webhook notifier header is reserved")
		default:
			request.Header.Set(canonical, value)
		}
	}
	if len(webhook.SignatureSecret) == 0 {
		return nil
	}
	header := webhook.SignatureHeader
	if header == "" {
		header = defaultSignatureHeader
	}
	canonical := textproto.CanonicalMIMEHeaderKey(header)
	if canonical == "" || canonical == "Content-Length" || canonical == "Host" {
		return errors.New("webhook notifier signature header is invalid")
	}
	digest := hmac.New(sha256.New, webhook.SignatureSecret)
	_, _ = digest.Write(payload)
	request.Header.Set(canonical, "sha256="+hex.EncodeToString(digest.Sum(nil)))

	return nil
}

func (webhook Webhook) client(origin *url.URL) *http.Client {
	transport := webhook.Transport
	if transport == nil {
		transport = secureHTTPTransport(webhook.AllowPrivateNetwork)
	}
	client := &http.Client{Transport: transport}
	client.CheckRedirect = func(request *http.Request, prior []*http.Request) error {
		if !webhook.FollowRedirects {
			return http.ErrUseLastResponse
		}
		if len(prior) >= maximumRedirects {
			return errors.New("webhook redirect limit reached")
		}
		if request.URL.Scheme != origin.Scheme || !strings.EqualFold(request.URL.Host, origin.Host) {
			return errors.New("webhook cross-origin redirect rejected")
		}

		return nil
	}

	return client
}

func secureHTTPTransport(allowPrivate bool) *http.Transport {
	defaultTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		defaultTransport = &http.Transport{}
	}
	transport := defaultTransport.Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, errors.New("dial webhook destination")
		}
		addresses, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		if err != nil {
			return nil, errors.New("resolve webhook destination")
		}
		dialer := &net.Dialer{}
		for _, address := range addresses {
			if !allowPrivate && privateAddress(address) {
				return nil, errors.New("webhook destination is not permitted")
			}
		}
		for _, destination := range addresses {
			connection, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(destination.String(), port))
			if dialErr == nil {
				return connection, nil
			}
		}

		return nil, errors.New("connect to webhook destination")
	}

	return transport
}

func privateHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	address, err := netip.ParseAddr(host)

	return err == nil && privateAddress(address)
}

func privateAddress(address netip.Addr) bool {
	return !address.IsValid() ||
		address.IsUnspecified() ||
		address.IsLoopback() ||
		address.IsPrivate() ||
		address.IsLinkLocalUnicast() ||
		address.IsLinkLocalMulticast() ||
		address.IsMulticast()
}

func readBounded(reader io.Reader, limit int64) (data []byte, truncated bool, returned error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, false, errors.New("read notification response")
	}
	truncated = int64(len(data)) > limit
	if truncated {
		data = data[:limit]
	}

	return data, truncated, nil
}

func retryableHTTPStatus(status int) bool {
	return status == http.StatusRequestTimeout ||
		status == http.StatusTooEarly ||
		status == http.StatusTooManyRequests ||
		status >= http.StatusInternalServerError
}
