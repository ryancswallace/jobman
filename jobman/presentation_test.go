package jobman

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ryancswallace/jobman/internal/model"
	"github.com/ryancswallace/jobman/internal/store"
)

func TestPresentLogsDoesNotExposePrunedPathsAsAvailable(t *testing.T) {
	t.Parallel()

	prunedAt := time.Date(2026, time.July, 15, 9, 30, 0, 0, time.UTC)
	root := filepath.Join(t.TempDir(), "logs")
	logs := model.LogMetadata{
		StdoutPath:      filepath.Join(root, "stdout.log"),
		StderrPath:      filepath.Join(root, "stderr.log"),
		IndexPath:       filepath.Join(root, "chunks.idx"),
		IndexVersion:    model.LogIndexVersion,
		StdoutSize:      12,
		StderrSize:      7,
		Integrity:       model.LogIntegrityValid,
		RecordingHealth: model.RecordingHealthy,
		PrunedAt:        &prunedAt,
		PrunedFiles:     3,
		PrunedBytes:     42,
	}
	presented := presentLogs(logs)
	if presented.Available || presented.StdoutPath != "" || presented.StderrPath != "" || presented.IndexPath != "" {
		t.Fatalf("presentLogs() = %+v, want unavailable logs without filesystem paths", presented)
	}
	encoded, err := json.Marshal(presented)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	text := string(encoded)
	if !strings.Contains(text, `"available":false`) || !strings.Contains(text, `"pruned_at":`) {
		t.Fatalf("pruned log JSON = %s, want explicit availability and prune time", text)
	}
	if strings.Contains(text, "stdout_path") || strings.Contains(text, "stderr_path") || strings.Contains(text, "index_path") {
		t.Fatalf("pruned log JSON = %s, unexpectedly exposes removed paths", text)
	}
	if got := formatLogAvailability(false, &prunedAt); got != "pruned "+prunedAt.Format(timeFormat) {
		t.Errorf("formatLogAvailability() = %q", got)
	}
}

func TestPresentLogsIncludesAvailablePaths(t *testing.T) {
	t.Parallel()

	root := filepath.Join(t.TempDir(), "logs")
	logs := model.LogMetadata{
		StdoutPath:      filepath.Join(root, "stdout.log"),
		StderrPath:      filepath.Join(root, "stderr.log"),
		IndexPath:       filepath.Join(root, "chunks.idx"),
		IndexVersion:    model.LogIndexVersion,
		Integrity:       model.LogIntegrityValid,
		RecordingHealth: model.RecordingHealthy,
	}
	presented := presentLogs(logs)
	if !presented.Available || presented.StdoutPath != logs.StdoutPath || presented.PrunedAt != nil {
		t.Fatalf("presentLogs() = %+v, want available paths", presented)
	}
}

func TestShowSummarizesAdmissionAndPendingNotificationWork(t *testing.T) {
	t.Parallel()

	if got := formatAdmission(nil); got != "none" {
		t.Fatalf("formatAdmission(nil) = %q", got)
	}
	releasedAt := time.Date(2026, time.July, 14, 12, 0, 0, 0, time.UTC)
	admission := &store.Admission{Pool: "network", Slots: 2, ReleasedAt: &releasedAt}
	if got := formatAdmission(admission); got != "released, pool network, 2 slot(s)" {
		t.Fatalf("formatAdmission() = %q", got)
	}
	deliveries := []store.NotificationDelivery{
		{Status: store.NotificationDeliveryPending},
		{Status: store.NotificationDeliveryDelivering},
		{Status: store.NotificationDeliverySucceeded},
		{Status: store.NotificationDeliveryFailed},
	}
	if got := pendingNotificationDeliveries(deliveries); got != 2 {
		t.Fatalf("pendingNotificationDeliveries() = %d, want 2", got)
	}
}

func TestPolicyPresentationUsesStableJSONAndOmitsDeliveryClaimToken(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.July, 14, 14, 0, 0, 0, time.UTC)
	runtimeJSON, err := json.Marshal(presentRuntime(store.JobRuntime{
		Revision: 2, RunCount: 1, TotalPaused: 3 * time.Second,
		InputEndpoint: "/private/input.sock",
	}))
	if err != nil {
		t.Fatalf("marshal runtime presentation: %v", err)
	}
	runtimeText := string(runtimeJSON)
	if !strings.Contains(runtimeText, `"run_count":1`) ||
		!strings.Contains(runtimeText, `"total_paused":"3s"`) ||
		strings.Contains(runtimeText, "RunCount") {
		t.Fatalf("runtime JSON = %s", runtimeText)
	}

	deliveryJSON, err := json.Marshal(presentNotificationDeliveries([]store.NotificationDelivery{{
		JobID:        model.JobID("01980f4c-0000-7000-8000-000000000001"),
		EventID:      model.EventID("01980f4c-0000-7000-8000-000000000002"),
		ClaimToken:   model.EventID("01980f4c-0000-7000-8000-000000000003"),
		NotifierName: "audit", EventType: "job_started",
		Status:     store.NotificationDeliveryDelivering,
		OccurredAt: now, CreatedAt: now, MaxAttempts: 2,
	}}))
	if err != nil {
		t.Fatalf("marshal notification delivery presentation: %v", err)
	}
	deliveryText := string(deliveryJSON)
	if !strings.Contains(deliveryText, `"notifier":"audit"`) ||
		strings.Contains(deliveryText, "claim_token") || strings.Contains(deliveryText, "000000000003") {
		t.Fatalf("notification delivery JSON = %s", deliveryText)
	}
}
