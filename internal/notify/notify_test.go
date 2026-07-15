package notify

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func testEvent() Event {
	return Event{
		SchemaVersion: EventSchemaVersion,
		ID:            "evt_01",
		Type:          EventRunSucceeded,
		OccurredAt:    time.Date(2026, time.July, 15, 12, 30, 0, 0, time.UTC),
		JobID:         "job_01",
		RunID:         "run_01",
		Detail:        map[string]any{"attempt": float64(1)},
	}
}

func TestEventValidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		mutate func(*Event)
		name   string
	}{
		{name: "schema", mutate: func(event *Event) { event.SchemaVersion = 2 }},
		{name: "event ID", mutate: func(event *Event) { event.ID = "" }},
		{name: "job ID", mutate: func(event *Event) { event.JobID = "" }},
		{name: "event type", mutate: func(event *Event) { event.Type = "surprise" }},
		{name: "occurrence", mutate: func(event *Event) { event.OccurredAt = time.Time{} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			event := testEvent()
			test.mutate(&event)
			if err := event.Validate(); err == nil {
				t.Fatal("Validate() error = nil, want validation error")
			}
		})
	}
}

func TestMarshalEventProducesVersionedJSON(t *testing.T) {
	t.Parallel()

	payload, err := marshalEvent(testEvent())
	if err != nil {
		t.Fatalf("marshalEvent() error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(payload, &decoded); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if decoded["schema_version"] != float64(EventSchemaVersion) {
		t.Fatalf("schema_version = %v, want %d", decoded["schema_version"], EventSchemaVersion)
	}
	if decoded["id"] != "evt_01" || decoded["type"] != "run_succeeded" {
		t.Fatalf("event identity = %#v", decoded)
	}
}

func TestMarshalEventDoesNotExposeJSONEncodingFailure(t *testing.T) {
	t.Parallel()

	event := testEvent()
	event.Detail = map[string]any{"secret": func() {}}
	_, err := marshalEvent(event)
	if err == nil {
		t.Fatal("marshalEvent() error = nil")
	}
	if strings.Contains(err.Error(), "func") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("marshalEvent() error = %q, contains payload detail", err)
	}
}

func TestDeliveryErrorIsSecretFree(t *testing.T) {
	t.Parallel()

	err := deliveryError(ErrorTransport, true)
	if strings.Contains(err.Error(), "password") {
		t.Fatalf("error = %q, contains credential", err)
	}
	if !IsRetryable(err) {
		t.Fatal("IsRetryable() = false, want true")
	}
	if IsRetryable(errors.New("arbitrary")) {
		t.Fatal("IsRetryable() = true for unclassified error")
	}
}

func TestDeliveryErrorPreservesContextClassification(t *testing.T) {
	t.Parallel()

	if !errors.Is(deliveryError(ErrorCanceled, false), context.Canceled) {
		t.Fatal("canceled delivery error does not match context.Canceled")
	}
	if !errors.Is(deliveryError(ErrorTimeout, true), context.DeadlineExceeded) {
		t.Fatal("timeout delivery error does not match context.DeadlineExceeded")
	}
}

func TestBoundedBuffer(t *testing.T) {
	t.Parallel()

	buffer := newBoundedBuffer(4)
	if written, err := buffer.Write([]byte("abcd")); err != nil || written != 4 {
		t.Fatalf("Write() = (%d, %v), want (4, nil)", written, err)
	}
	if buffer.truncated {
		t.Fatal("exact-limit write marked truncated")
	}
	if written, err := buffer.Write([]byte("ef")); err != nil || written != 2 {
		t.Fatalf("Write() = (%d, %v), want (2, nil)", written, err)
	}
	if !buffer.truncated {
		t.Fatal("overflowing write not marked truncated")
	}
	if got := string(buffer.Bytes()); got != "abcd" {
		t.Fatalf("Bytes() = %q, want %q", got, "abcd")
	}
}

func TestWithTimeoutRejectsNegativeDuration(t *testing.T) {
	t.Parallel()

	ctx, cancel, err := withTimeout(t.Context(), -time.Second)
	if err == nil || ctx != nil || cancel != nil {
		t.Fatalf("withTimeout() = (%v, %v, %v), want nil values and error", ctx, cancel, err)
	}
}

func TestNormalizeDeliveryErrorUsesSafeClassification(t *testing.T) {
	t.Parallel()

	err := normalizeDeliveryError(t.Context(), errors.New("password=top-secret"))
	if strings.Contains(err.Error(), "top-secret") {
		t.Fatalf("normalizeDeliveryError() = %q, contains secret", err)
	}
	var classified *DeliveryError
	if !errors.As(err, &classified) || classified.Kind != ErrorInternal {
		t.Fatalf("normalizeDeliveryError() = %#v, want internal DeliveryError", err)
	}
}
