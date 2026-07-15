package model

import (
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
)

const maximumNotifierPayloadBytes = int64(1024 * 1024)

// NotifierKind identifies one persisted notification delivery mechanism.
type NotifierKind string

// Supported notifier kinds.
const (
	NotifierCommand NotifierKind = "command"
	NotifierWebhook NotifierKind = "http"
	NotifierSMTP    NotifierKind = "smtp"
)

// NotifierRetryPolicy bounds at-least-once delivery work.
type NotifierRetryPolicy struct {
	MaxAttempts int
	Delay       time.Duration
	MaxDelay    time.Duration
}

// CommandNotifierDefinition is a direct, shell-free command hook. Environment
// contains only non-secret values; secret values are represented exclusively
// by SecretEnvironment references and resolved in the supervisor.
type CommandNotifierDefinition struct {
	Executable        string
	Arguments         []string
	WorkingDirectory  string
	Environment       map[string]string
	SecretEnvironment map[string]SecretReference
	OutputLimit       int64
}

// WebhookNotifierDefinition is a persisted HTTP destination. Sensitive header
// values and the optional signing key are references, never credential bytes.
type WebhookNotifierDefinition struct {
	URL                 string
	Headers             map[string]string
	SecretHeaders       map[string]SecretReference
	SigningSecret       *SecretReference
	SignatureHeader     string
	ResponseLimit       int64
	AllowInsecureHTTP   bool
	AllowPrivateNetwork bool
	FollowRedirects     bool
}

// SMTPNotifierDefinition is a persisted email destination. PasswordSecret is
// resolved immediately before delivery and is never serialized as a value.
type SMTPNotifierDefinition struct {
	Address        string
	ServerName     string
	Username       string
	PasswordSecret *SecretReference
	From           string
	To             []string
	SubjectPrefix  string
	Mode           string
	MessageLimit   int64
}

// NotifierDefinition is one named, immutable notifier configuration. Exactly
// one kind-specific definition must be present.
type NotifierDefinition struct {
	Name    string
	Kind    NotifierKind
	Timeout time.Duration
	Retry   NotifierRetryPolicy
	Command *CommandNotifierDefinition
	Webhook *WebhookNotifierDefinition
	SMTP    *SMTPNotifierDefinition
}

// Validate checks a notifier definition without resolving or exposing any
// credential value.
func (definition NotifierDefinition) Validate() error {
	if err := definition.validateEnvelope(); err != nil {
		return err
	}
	if configuredNotifierKinds(definition) != 1 {
		return invalid("notifier definition", "must contain exactly one command, HTTP, or SMTP configuration")
	}

	return definition.validateKind()
}

func (definition NotifierDefinition) validateEnvelope() error {
	if definition.Name == "" || strings.TrimSpace(definition.Name) != definition.Name || containsControl(definition.Name) {
		return invalid("notifier name", "must be nonempty, trimmed, and contain no controls")
	}
	if definition.Timeout <= 0 {
		return invalid("notifier timeout", "must be positive")
	}
	if definition.Retry.MaxAttempts < 1 || definition.Retry.MaxAttempts > 100 {
		return invalid("notifier retry attempts", "must be between 1 and 100")
	}
	if definition.Retry.Delay < 0 || definition.Retry.MaxDelay < 0 ||
		definition.Retry.MaxDelay != 0 && definition.Retry.MaxDelay < definition.Retry.Delay {
		return invalid("notifier retry delay", "must be nonnegative and no greater than the maximum")
	}

	return nil
}

func configuredNotifierKinds(definition NotifierDefinition) int {
	configured := 0
	if definition.Command != nil {
		configured++
	}
	if definition.Webhook != nil {
		configured++
	}
	if definition.SMTP != nil {
		configured++
	}

	return configured
}

func (definition NotifierDefinition) validateKind() error {
	switch definition.Kind {
	case NotifierCommand:
		if definition.Command == nil {
			return invalid("command notifier", "configuration is missing")
		}
		return definition.Command.validate()
	case NotifierWebhook:
		if definition.Webhook == nil {
			return invalid("HTTP notifier", "configuration is missing")
		}
		return definition.Webhook.validate()
	case NotifierSMTP:
		if definition.SMTP == nil {
			return invalid("SMTP notifier", "configuration is missing")
		}
		return definition.SMTP.validate()
	default:
		return invalid("notifier kind", "must be command, http, or smtp")
	}
}

func (definition CommandNotifierDefinition) validate() error {
	if !cleanAbsolutePath(definition.Executable) {
		return invalid("command notifier executable", "must be a clean absolute path")
	}
	if definition.WorkingDirectory != "" && !cleanAbsolutePath(definition.WorkingDirectory) {
		return invalid("command notifier working directory", "must be a clean absolute path")
	}
	for _, argument := range definition.Arguments {
		if strings.ContainsRune(argument, '\x00') {
			return invalid("command notifier argument", "must not contain NUL")
		}
	}
	if err := validateEnvironment(definition.Environment, nil); err != nil {
		return fmt.Errorf("validate command notifier environment: %w", err)
	}
	if err := validateSecretReferences("command notifier secret environment", definition.SecretEnvironment); err != nil {
		return err
	}
	for name := range definition.SecretEnvironment {
		if _, exists := definition.Environment[name]; exists {
			return invalid("command notifier environment", fmt.Sprintf("%q is both literal and secret-referenced", name))
		}
	}

	return validateNotifierByteLimit("command notifier output limit", definition.OutputLimit)
}

func (definition WebhookNotifierDefinition) validate() error {
	if err := definition.validateEndpoint(); err != nil {
		return err
	}
	literalHeaders, err := validateLiteralWebhookHeaders(definition.Headers)
	if err != nil {
		return err
	}
	secretHeaders, err := validateSecretWebhookHeaders(definition.SecretHeaders, literalHeaders)
	if err != nil {
		return err
	}
	if definition.SigningSecret != nil && !definition.SigningSecret.valid() {
		return invalid("HTTP notifier signing secret", "reference is invalid")
	}
	if err := validateWebhookSignatureHeader(definition.SignatureHeader, literalHeaders, secretHeaders); err != nil {
		return err
	}

	return validateNotifierByteLimit("HTTP notifier response limit", definition.ResponseLimit)
}

func (definition WebhookNotifierDefinition) validateEndpoint() error {
	endpoint, err := url.Parse(strings.TrimSpace(definition.URL))
	if err != nil || endpoint.Host == "" || endpoint.User != nil || endpoint.Fragment != "" || definition.URL != strings.TrimSpace(definition.URL) {
		return invalid("HTTP notifier URL", "is invalid")
	}
	if endpoint.Scheme != "https" && (endpoint.Scheme != "http" || !definition.AllowInsecureHTTP) {
		return invalid("HTTP notifier URL", "must use HTTPS unless insecure HTTP is explicitly allowed")
	}

	return nil
}

func validateLiteralWebhookHeaders(headers map[string]string) (map[string]struct{}, error) {
	literalHeaders := make(map[string]struct{}, len(headers))
	for name, value := range headers {
		if !validHTTPHeaderName(name) || strings.ContainsAny(value, "\r\n\x00") {
			return nil, invalid("HTTP notifier header", "name or value is invalid")
		}
		if sensitiveHTTPHeader(name) {
			return nil, invalid("HTTP notifier header", fmt.Sprintf("%q must use a secret reference", name))
		}
		canonical := strings.ToLower(name)
		if reservedHTTPHeader(canonical) {
			return nil, invalid("HTTP notifier header", fmt.Sprintf("%q is reserved", name))
		}
		if _, duplicate := literalHeaders[canonical]; duplicate {
			return nil, invalid("HTTP notifier header", "must not be duplicated case-insensitively")
		}
		literalHeaders[canonical] = struct{}{}
	}

	return literalHeaders, nil
}

func validateSecretWebhookHeaders(
	headers map[string]SecretReference,
	literalHeaders map[string]struct{},
) (map[string]struct{}, error) {
	secretHeaders := make(map[string]struct{}, len(headers))
	for name, reference := range headers {
		if !validHTTPHeaderName(name) || !reference.valid() {
			return nil, invalid("HTTP notifier secret header", "name or reference is invalid")
		}
		canonical := strings.ToLower(name)
		if reservedHTTPHeader(canonical) {
			return nil, invalid("HTTP notifier secret header", fmt.Sprintf("%q is reserved", name))
		}
		if _, exists := literalHeaders[canonical]; exists {
			return nil, invalid("HTTP notifier header", fmt.Sprintf("%q is both literal and secret-referenced", name))
		}
		if _, duplicate := secretHeaders[canonical]; duplicate {
			return nil, invalid("HTTP notifier secret header", "must not be duplicated case-insensitively")
		}
		secretHeaders[canonical] = struct{}{}
	}

	return secretHeaders, nil
}

func validateWebhookSignatureHeader(
	header string,
	literalHeaders, secretHeaders map[string]struct{},
) error {
	if header == "" {
		return nil
	}
	if !validHTTPHeaderName(header) || reservedHTTPHeader(strings.ToLower(header)) {
		return invalid("HTTP notifier signature header", "is invalid or reserved")
	}
	canonical := strings.ToLower(header)
	if _, exists := literalHeaders[canonical]; exists {
		return invalid("HTTP notifier signature header", "conflicts with a literal header")
	}
	if _, exists := secretHeaders[canonical]; exists {
		return invalid("HTTP notifier signature header", "conflicts with a secret header")
	}

	return nil
}

func (definition SMTPNotifierDefinition) validate() error {
	if err := definition.validateConnection(); err != nil {
		return err
	}
	if err := definition.validateAddresses(); err != nil {
		return err
	}
	if strings.ContainsAny(definition.SubjectPrefix, "\r\n\x00") {
		return invalid("SMTP notifier subject prefix", "is invalid")
	}
	if err := definition.validateCredentials(); err != nil {
		return err
	}

	return validateNotifierByteLimit("SMTP notifier message limit", definition.MessageLimit)
}

func (definition SMTPNotifierDefinition) validateConnection() error {
	host, portText, err := net.SplitHostPort(definition.Address)
	port, portErr := strconv.ParseUint(portText, 10, 16)
	if err != nil || host == "" || portErr != nil || port == 0 {
		return invalid("SMTP notifier address", "must contain a host and port")
	}
	if definition.ServerName != "" && (strings.TrimSpace(definition.ServerName) != definition.ServerName || containsControl(definition.ServerName)) {
		return invalid("SMTP notifier server name", "is invalid")
	}
	if definition.Mode != "starttls" && definition.Mode != "implicit" {
		return invalid("SMTP notifier mode", "must be starttls or implicit")
	}

	return nil
}

func (definition SMTPNotifierDefinition) validateAddresses() error {
	if _, err := mail.ParseAddress(definition.From); err != nil {
		return invalid("SMTP notifier sender", "is invalid")
	}
	if len(definition.To) == 0 {
		return invalid("SMTP notifier recipients", "must not be empty")
	}
	for _, recipient := range definition.To {
		if _, err := mail.ParseAddress(recipient); err != nil {
			return invalid("SMTP notifier recipient", "is invalid")
		}
	}

	return nil
}

func (definition SMTPNotifierDefinition) validateCredentials() error {
	if (definition.Username == "") != (definition.PasswordSecret == nil) {
		return invalid("SMTP notifier credentials", "username and password reference must be configured together")
	}
	if definition.PasswordSecret != nil && !definition.PasswordSecret.valid() {
		return invalid("SMTP notifier password", "reference is invalid")
	}

	return nil
}

func validateNotifierDefinitions(definitions []NotifierDefinition, subscriptions []NotificationSubscription) error {
	byName, err := validateNotifierDefinitionSet(definitions)
	if err != nil {
		return err
	}
	for _, subscription := range subscriptions {
		if err := validateNotificationSubscription(subscription, byName, len(definitions) != 0); err != nil {
			return err
		}
	}

	return nil
}

func validateNotifierDefinitionSet(definitions []NotifierDefinition) (map[string]struct{}, error) {
	byName := make(map[string]struct{}, len(definitions))
	for _, definition := range definitions {
		if err := definition.Validate(); err != nil {
			return nil, err
		}
		if _, exists := byName[definition.Name]; exists {
			return nil, invalid("notifier definition", "must not be duplicated")
		}
		byName[definition.Name] = struct{}{}
	}

	return byName, nil
}

func validateNotificationSubscription(
	subscription NotificationSubscription,
	definitions map[string]struct{},
	requireDefinition bool,
) error {
	if subscription.Notifier == "" || strings.TrimSpace(subscription.Notifier) != subscription.Notifier || containsControl(subscription.Notifier) {
		return invalid("notification subscription", "requires a valid notifier name")
	}
	if len(subscription.Events) == 0 {
		return invalid("notification subscription", "requires at least one event")
	}
	seenEvents := make(map[string]struct{}, len(subscription.Events))
	for _, event := range subscription.Events {
		if !knownNotificationEvent(event) {
			return invalid("notification event", fmt.Sprintf("%q is unknown", event))
		}
		if _, exists := seenEvents[event]; exists {
			return invalid("notification event", "must not be duplicated")
		}
		seenEvents[event] = struct{}{}
	}
	// Definition-less subscriptions are accepted for compatibility with job
	// specification schema v2 data written before definitions were embedded.
	if requireDefinition {
		if _, exists := definitions[subscription.Notifier]; !exists {
			return invalid("notification subscription", fmt.Sprintf("references unknown notifier %q", subscription.Notifier))
		}
	}

	return nil
}

func cloneNotifierDefinitions(source []NotifierDefinition) []NotifierDefinition {
	result := slices.Clone(source)
	if result == nil {
		return []NotifierDefinition{}
	}
	for index := range result {
		result[index].Command = cloneCommandNotifier(source[index].Command)
		result[index].Webhook = cloneWebhookNotifier(source[index].Webhook)
		result[index].SMTP = cloneSMTPNotifier(source[index].SMTP)
	}

	return result
}

func cloneCommandNotifier(source *CommandNotifierDefinition) *CommandNotifierDefinition {
	if source == nil {
		return nil
	}
	result := *source
	result.Arguments = slices.Clone(source.Arguments)
	if result.Arguments == nil {
		result.Arguments = []string{}
	}
	result.Environment = cloneStringMap(source.Environment)
	if result.Environment == nil {
		result.Environment = map[string]string{}
	}
	result.SecretEnvironment = cloneSecretReferences(source.SecretEnvironment)

	return &result
}

func cloneWebhookNotifier(source *WebhookNotifierDefinition) *WebhookNotifierDefinition {
	if source == nil {
		return nil
	}
	result := *source
	result.Headers = cloneStringMap(source.Headers)
	if result.Headers == nil {
		result.Headers = map[string]string{}
	}
	result.SecretHeaders = cloneSecretReferences(source.SecretHeaders)
	if source.SigningSecret != nil {
		reference := *source.SigningSecret
		result.SigningSecret = &reference
	}

	return &result
}

func cloneSMTPNotifier(source *SMTPNotifierDefinition) *SMTPNotifierDefinition {
	if source == nil {
		return nil
	}
	result := *source
	result.To = slices.Clone(source.To)
	if result.To == nil {
		result.To = []string{}
	}
	if source.PasswordSecret != nil {
		reference := *source.PasswordSecret
		result.PasswordSecret = &reference
	}

	return &result
}

func cloneSecretReferences(source map[string]SecretReference) map[string]SecretReference {
	result := make(map[string]SecretReference, len(source))
	for name, reference := range source {
		result[name] = reference
	}

	return result
}

func validateSecretReferences(field string, references map[string]SecretReference) error {
	for name, reference := range references {
		if !validEnvironmentName(name) || !reference.valid() {
			return invalid(field, "contains an invalid name or reference")
		}
	}

	return nil
}

func (reference SecretReference) valid() bool {
	switch reference.Provider {
	case "env":
		return validEnvironmentName(reference.Name)
	case "file":
		return cleanAbsolutePath(reference.Name)
	default:
		return false
	}
}

func cleanAbsolutePath(value string) bool {
	return value != "" && filepath.IsAbs(value) && filepath.Clean(value) == value && !strings.ContainsRune(value, '\x00')
}

func validateNotifierByteLimit(field string, value int64) error {
	if value < 0 || value > maximumNotifierPayloadBytes {
		return invalid(field, fmt.Sprintf("must be between zero and %d bytes", maximumNotifierPayloadBytes))
	}

	return nil
}

func validHTTPHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, character := range name {
		if character > unicode.MaxASCII {
			return false
		}
		if !httpTokenCharacter(character) {
			return false
		}
	}

	return true
}

func httpTokenCharacter(character rune) bool {
	if character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' ||
		character >= '0' && character <= '9' {
		return true
	}

	return strings.ContainsRune("!#$%&'*+-.^_`|~", character)
}

func sensitiveHTTPHeader(name string) bool {
	switch strings.ToLower(name) {
	case "authorization", "cookie", "proxy-authorization", "set-cookie", "x-api-key":
		return true
	default:
		return false
	}
}

func reservedHTTPHeader(canonical string) bool {
	switch canonical {
	case "content-length", "host", "idempotency-key", "x-jobman-event-id":
		return true
	default:
		return false
	}
}

// ValidNotificationEvent reports whether event belongs to the stable v1
// notification event vocabulary.
func ValidNotificationEvent(event string) bool {
	//nolint:misspell // The v1 event vocabulary deliberately preserves the externally documented "cancelled" spelling.
	switch event {
	case "job_started", "run_started", "run_succeeded", "run_failed", "run_timed_out",
		"run_cancelled", "run_lost", "retry_scheduled", "job_succeeded", "job_failed",
		"job_timed_out", "job_cancelled", "job_aborted", "job_lost", "job_submission_failed":
		return true
	default:
		return false
	}
}

func knownNotificationEvent(event string) bool {
	return ValidNotificationEvent(event)
}
