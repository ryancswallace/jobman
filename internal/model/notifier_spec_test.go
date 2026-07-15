package model

import (
	"bytes"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestJobSpecNotificationDefinitionsCanonicalRoundTrip(t *testing.T) {
	t.Parallel()

	password := SecretReference{Provider: "env", Name: "JOBMAN_SMTP_PASSWORD"}
	signing := SecretReference{Provider: "file", Name: "/run/secrets/webhook-signing-key"}
	definitions := []NotifierDefinition{
		{
			Name: "hook", Kind: NotifierCommand, Timeout: 3 * time.Second,
			Retry: NotifierRetryPolicy{MaxAttempts: 2, Delay: time.Second, MaxDelay: 4 * time.Second},
			Command: &CommandNotifierDefinition{
				Executable: "/usr/local/bin/job-event", Arguments: []string{"--json"},
				WorkingDirectory: "/var/empty", Environment: map[string]string{"MODE": "production"},
				SecretEnvironment: map[string]SecretReference{
					"TOKEN": {Provider: "env", Name: "JOBMAN_HOOK_TOKEN"},
				},
				OutputLimit: 4096,
			},
		},
		{
			Name: "webhook", Kind: NotifierWebhook, Timeout: 5 * time.Second,
			Retry: NotifierRetryPolicy{MaxAttempts: 3, Delay: time.Second, MaxDelay: 8 * time.Second},
			Webhook: &WebhookNotifierDefinition{
				URL: "https://events.example.test/jobman", Headers: map[string]string{"X-Environment": "production"},
				SecretHeaders: map[string]SecretReference{
					"Authorization": {Provider: "env", Name: "JOBMAN_WEBHOOK_AUTH"},
				},
				SigningSecret: &signing, SignatureHeader: "X-Jobman-Signature", ResponseLimit: 8192,
			},
		},
		{
			Name: "mail", Kind: NotifierSMTP, Timeout: 10 * time.Second,
			Retry: NotifierRetryPolicy{MaxAttempts: 1},
			SMTP: &SMTPNotifierDefinition{
				Address: "smtp.example.test:587", Username: "jobman", PasswordSecret: &password,
				From: "Jobman <jobman@example.test>", To: []string{"ops@example.test"},
				SubjectPrefix: "Jobman", Mode: "starttls", MessageLimit: 65536,
			},
		},
	}
	policy := DefaultExecutionPolicy()
	policy.NotifierDefinitions = definitions
	policy.Notifications = []NotificationSubscription{
		{Notifier: "hook", Events: []string{"job_failed"}},
		{Notifier: "webhook", Events: []string{"retry_scheduled", "job_succeeded"}},
		{Notifier: "mail", Events: []string{"job_timed_out"}},
	}
	specification, err := NewJobSpec(JobSpecInput{
		Executable: "/bin/echo", Arguments: []string{"test"}, WorkingDirectory: filepath.Clean(t.TempDir()),
		ExecutionPolicy: policy,
	})
	if err != nil {
		t.Fatalf("NewJobSpec() error = %v", err)
	}

	// Mutating caller-owned definitions and getter results must not affect the
	// immutable specification.
	definitions[0].Command.Environment["MODE"] = "mutated"
	definitions[1].Webhook.SigningSecret.Name = "mutated"
	returned := specification.ExecutionPolicy()
	returned.NotifierDefinitions[2].SMTP.To[0] = "mutated@example.test"
	if got := specification.ExecutionPolicy().NotifierDefinitions; got[0].Command.Environment["MODE"] != "production" ||
		got[1].Webhook.SigningSecret.Name != "/run/secrets/webhook-signing-key" || got[2].SMTP.To[0] != "ops@example.test" {
		t.Fatal("notification definition was mutated through caller-owned data")
	}

	encoded, err := specification.CanonicalJSON()
	if err != nil {
		t.Fatalf("CanonicalJSON() error = %v", err)
	}
	if bytes.Contains(encoded, []byte("top-secret")) || !bytes.Contains(encoded, []byte(`"signing_secret":{"provider":"file","name":"/run/secrets/webhook-signing-key"}`)) {
		t.Fatalf("canonical notification credential representation is unsafe or incomplete: %s", encoded)
	}
	parsed, err := ParseJobSpecJSON(encoded)
	if err != nil {
		t.Fatalf("ParseJobSpecJSON() error = %v", err)
	}
	reencoded, err := parsed.CanonicalJSON()
	if err != nil {
		t.Fatalf("parsed CanonicalJSON() error = %v", err)
	}
	if !bytes.Equal(reencoded, encoded) {
		t.Fatalf("canonical round trip changed JSON\nfirst:  %s\nsecond: %s", encoded, reencoded)
	}

	withUnknownField := bytes.Replace(encoded, []byte(`"response_limit":8192`), []byte(`"response_limit":8192,"credential":"secret"`), 1)
	if _, err := ParseJobSpecJSON(withUnknownField); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("ParseJobSpecJSON(unknown notifier field) error = %v", err)
	}
}

func TestParseJobSpecJSONAcceptsSchemaV2WithoutNotifierDefinitions(t *testing.T) {
	t.Parallel()

	policy := DefaultExecutionPolicy()
	policy.Notifications = []NotificationSubscription{{Notifier: "legacy", Events: []string{"job_failed"}}}
	specification, err := NewJobSpec(JobSpecInput{
		Executable: "/bin/echo", WorkingDirectory: filepath.Clean(t.TempDir()), ExecutionPolicy: policy,
	})
	if err != nil {
		t.Fatalf("NewJobSpec() error = %v", err)
	}
	encoded, err := specification.CanonicalJSON()
	if err != nil {
		t.Fatalf("CanonicalJSON() error = %v", err)
	}
	legacy := bytes.Replace(encoded, []byte(`,"notifier_definitions":[]`), nil, 1)
	if bytes.Equal(legacy, encoded) {
		t.Fatalf("test fixture did not remove notifier_definitions: %s", encoded)
	}
	parsed, err := ParseJobSpecJSON(legacy)
	if err != nil {
		t.Fatalf("ParseJobSpecJSON(older schema v2) error = %v", err)
	}
	got := parsed.ExecutionPolicy()
	if len(got.NotifierDefinitions) != 0 || !reflect.DeepEqual(got.Notifications, policy.Notifications) {
		t.Fatalf("parsed legacy notifications = %#v / %#v", got.NotifierDefinitions, got.Notifications)
	}
}

func TestNotifierDefinitionRejectsUnsafeOrInconsistentConfiguration(t *testing.T) {
	t.Parallel()

	valid := NotifierDefinition{
		Name: "webhook", Kind: NotifierWebhook, Timeout: time.Second,
		Retry:   NotifierRetryPolicy{MaxAttempts: 1},
		Webhook: &WebhookNotifierDefinition{URL: "https://example.test/events"},
	}
	tests := map[string]func(*NotifierDefinition){
		"literal credential header": func(definition *NotifierDefinition) {
			definition.Webhook.Headers = map[string]string{"X-Api-Key": "literal-secret"}
		},
		"invalid secret reference": func(definition *NotifierDefinition) {
			definition.Webhook.SecretHeaders = map[string]SecretReference{"Authorization": {Provider: "", Name: "token"}}
		},
		"mismatched union": func(definition *NotifierDefinition) {
			definition.Command = &CommandNotifierDefinition{Executable: "/bin/true"}
		},
		"unbounded attempts": func(definition *NotifierDefinition) {
			definition.Retry.MaxAttempts = 0
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			definition := valid
			webhook := *valid.Webhook
			definition.Webhook = &webhook
			mutate(&definition)
			if err := definition.Validate(); err == nil {
				t.Fatal("Validate() error = nil")
			}
		})
	}
}

func TestExecutionPolicyRejectsUnknownNotificationReferences(t *testing.T) {
	t.Parallel()

	policy := DefaultExecutionPolicy()
	policy.NotifierDefinitions = []NotifierDefinition{{
		Name: "known", Kind: NotifierWebhook, Timeout: time.Second,
		Retry:   NotifierRetryPolicy{MaxAttempts: 1},
		Webhook: &WebhookNotifierDefinition{URL: "https://example.test/events"},
	}}
	policy.Notifications = []NotificationSubscription{{Notifier: "missing", Events: []string{"job_failed"}}}
	if err := policy.Validate(StdinNull); err == nil {
		t.Fatal("Validate() error = nil")
	}
}

func TestNotifierDefinitionValidationEdges(t *testing.T) {
	t.Parallel()

	commandDefinition := func() NotifierDefinition {
		return NotifierDefinition{
			Name: "command", Kind: NotifierCommand, Timeout: time.Second,
			Retry:   NotifierRetryPolicy{MaxAttempts: 1},
			Command: &CommandNotifierDefinition{Executable: "/bin/true"},
		}
	}
	for _, mutate := range []func(*NotifierDefinition){
		func(value *NotifierDefinition) { value.Name = " bad " },
		func(value *NotifierDefinition) { value.Timeout = 0 },
		func(value *NotifierDefinition) { value.Retry.MaxAttempts = 101 },
		func(value *NotifierDefinition) { value.Retry.Delay = -1 },
		func(value *NotifierDefinition) { value.Command = nil },
		func(value *NotifierDefinition) { value.Webhook = &WebhookNotifierDefinition{} },
		func(value *NotifierDefinition) { value.Kind = NotifierKind("unknown") },
		func(value *NotifierDefinition) { value.Command.Executable = "relative" },
		func(value *NotifierDefinition) { value.Command.WorkingDirectory = "relative" },
		func(value *NotifierDefinition) { value.Command.Arguments = []string{"bad\x00argument"} },
		func(value *NotifierDefinition) { value.Command.Environment = map[string]string{"BAD=NAME": "x"} },
		func(value *NotifierDefinition) {
			value.Command.SecretEnvironment = map[string]SecretReference{"BAD=NAME": {Provider: "env", Name: "TOKEN"}}
		},
		func(value *NotifierDefinition) {
			value.Command.Environment = map[string]string{"TOKEN": "literal"}
			value.Command.SecretEnvironment = map[string]SecretReference{"TOKEN": {Provider: "env", Name: "TOKEN"}}
		},
		func(value *NotifierDefinition) { value.Command.OutputLimit = maximumNotifierPayloadBytes + 1 },
	} {
		definition := commandDefinition()
		mutate(&definition)
		if err := definition.Validate(); err == nil {
			t.Fatal("Validate(invalid command definition) error = nil")
		}
	}
}

func TestWebhookDefinitionValidationEdges(t *testing.T) {
	t.Parallel()

	webhookDefinition := func() NotifierDefinition {
		return NotifierDefinition{
			Name: "webhook", Kind: NotifierWebhook, Timeout: time.Second,
			Retry:   NotifierRetryPolicy{MaxAttempts: 1},
			Webhook: &WebhookNotifierDefinition{URL: "https://example.test/events"},
		}
	}
	for _, mutate := range []func(*WebhookNotifierDefinition){
		func(value *WebhookNotifierDefinition) { value.URL = "invalid" },
		func(value *WebhookNotifierDefinition) { value.URL = "http://example.test" },
		func(value *WebhookNotifierDefinition) { value.Headers = map[string]string{"Bad Header": "x"} },
		func(value *WebhookNotifierDefinition) { value.Headers = map[string]string{"Authorization": "literal"} },
		func(value *WebhookNotifierDefinition) { value.Headers = map[string]string{"Host": "example.test"} },
		func(value *WebhookNotifierDefinition) {
			value.Headers = map[string]string{"X-Test": "a", "x-test": "b"}
		},
		func(value *WebhookNotifierDefinition) {
			value.SecretHeaders = map[string]SecretReference{"Bad Header": {Provider: "env", Name: "TOKEN"}}
		},
		func(value *WebhookNotifierDefinition) {
			value.SecretHeaders = map[string]SecretReference{"Host": {Provider: "env", Name: "TOKEN"}}
		},
		func(value *WebhookNotifierDefinition) {
			value.Headers = map[string]string{"X-Test": "literal"}
			value.SecretHeaders = map[string]SecretReference{"x-test": {Provider: "env", Name: "TOKEN"}}
		},
		func(value *WebhookNotifierDefinition) {
			value.SecretHeaders = map[string]SecretReference{
				"X-Test": {Provider: "env", Name: "TOKEN"}, "x-test": {Provider: "env", Name: "TOKEN2"},
			}
		},
		func(value *WebhookNotifierDefinition) {
			value.SigningSecret = &SecretReference{Provider: "unknown", Name: "key"}
		},
		func(value *WebhookNotifierDefinition) { value.SignatureHeader = "Bad Header" },
		func(value *WebhookNotifierDefinition) {
			value.Headers = map[string]string{"X-Signature": "literal"}
			value.SignatureHeader = "x-signature"
		},
		func(value *WebhookNotifierDefinition) {
			value.SecretHeaders = map[string]SecretReference{"X-Signature": {Provider: "env", Name: "TOKEN"}}
			value.SignatureHeader = "x-signature"
		},
		func(value *WebhookNotifierDefinition) { value.ResponseLimit = -1 },
	} {
		definition := webhookDefinition()
		mutate(definition.Webhook)
		if err := definition.Validate(); err == nil {
			t.Fatal("Validate(invalid webhook definition) error = nil")
		}
	}
	definition := webhookDefinition()
	definition.Webhook.URL = "http://example.test/events"
	definition.Webhook.AllowInsecureHTTP = true
	if err := definition.Validate(); err != nil {
		t.Fatalf("Validate(explicit HTTP) error = %v", err)
	}
}

func TestSMTPDefinitionValidationEdges(t *testing.T) {
	t.Parallel()

	smtpDefinition := func() NotifierDefinition {
		return NotifierDefinition{
			Name: "mail", Kind: NotifierSMTP, Timeout: time.Second,
			Retry: NotifierRetryPolicy{MaxAttempts: 1},
			SMTP: &SMTPNotifierDefinition{
				Address: "smtp.example.test:587", From: "jobman@example.test",
				To: []string{"ops@example.test"}, Mode: "starttls",
			},
		}
	}
	for _, mutate := range []func(*SMTPNotifierDefinition){
		func(value *SMTPNotifierDefinition) { value.Address = "invalid" },
		func(value *SMTPNotifierDefinition) { value.ServerName = " bad " },
		func(value *SMTPNotifierDefinition) { value.Mode = "plain" },
		func(value *SMTPNotifierDefinition) { value.From = "invalid" },
		func(value *SMTPNotifierDefinition) { value.To = nil },
		func(value *SMTPNotifierDefinition) { value.To = []string{"invalid"} },
		func(value *SMTPNotifierDefinition) { value.SubjectPrefix = "bad\r\nsubject" },
		func(value *SMTPNotifierDefinition) { value.Username = "user" },
		func(value *SMTPNotifierDefinition) {
			value.Username = "user"
			value.PasswordSecret = &SecretReference{Provider: "unknown", Name: "PASSWORD"}
		},
		func(value *SMTPNotifierDefinition) { value.MessageLimit = -1 },
	} {
		definition := smtpDefinition()
		mutate(definition.SMTP)
		if err := definition.Validate(); err == nil {
			t.Fatal("Validate(invalid SMTP definition) error = nil")
		}
	}
}

func TestNotificationSubscriptionValidationEdges(t *testing.T) {
	t.Parallel()

	definition := NotifierDefinition{
		Name: "hook", Kind: NotifierWebhook, Timeout: time.Second,
		Retry:   NotifierRetryPolicy{MaxAttempts: 1},
		Webhook: &WebhookNotifierDefinition{URL: "https://example.test/events"},
	}
	if err := validateNotifierDefinitions([]NotifierDefinition{definition, definition}, nil); err == nil {
		t.Fatal("duplicate notifier definitions error = nil")
	}
	for _, subscription := range []NotificationSubscription{
		{},
		{Notifier: "hook"},
		{Notifier: "hook", Events: []string{"unknown"}},
		{Notifier: "hook", Events: []string{"job_failed", "job_failed"}},
		{Notifier: "missing", Events: []string{"job_failed"}},
	} {
		if err := validateNotifierDefinitions([]NotifierDefinition{definition}, []NotificationSubscription{subscription}); err == nil {
			t.Fatalf("validateNotifierDefinitions(%#v) error = nil", subscription)
		}
	}
	if !ValidNotificationEvent("job_succeeded") || ValidNotificationEvent("unknown") {
		t.Fatal("ValidNotificationEvent() vocabulary mismatch")
	}
}
