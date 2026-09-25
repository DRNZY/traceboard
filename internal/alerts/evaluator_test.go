package alerts

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"traceboard/internal/event"
	"traceboard/internal/store"
)

type recordingNotifier struct {
	calls []store.Alert
}

func (notifier *recordingNotifier) Notify(_ context.Context, alert store.Alert) error {
	notifier.calls = append(notifier.calls, alert)
	return nil
}

func newTestEvaluator(t *testing.T, config Config) (*Evaluator, *store.Store, *recordingNotifier) {
	t.Helper()
	directory := t.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		t.Fatalf("secure temp directory: %v", err)
	}
	database, err := store.Open(filepath.Join(directory, "traceboard.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := database.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	notifier := &recordingNotifier{}
	return NewEvaluator(database, config, notifier), database, notifier
}

var seedCounter int

func seedRun(t *testing.T, database *store.Store, runID, eventType string, status event.Status, at time.Time) {
	t.Helper()
	seedCounter++
	identifier := runID + "-" + strconv.Itoa(seedCounter)
	if _, err := database.InsertEvents(context.Background(), event.Event{
		SchemaVersion: event.SchemaVersion1,
		EventID:       identifier,
		SourceEventID: identifier,
		Source:        "opencode",
		SourceVersion: "test",
		RunID:         runID,
		OccurredAt:    at,
		Type:          eventType,
		Status:        status,
		Capture:       map[string]any{"mode": "metadata"},
		Attributes:    map[string]any{"title": "a captured run title"},
	}); err != nil {
		t.Fatalf("seed run: %v", err)
	}
}

func TestFailedRunAlertsImmediately(t *testing.T) {
	evaluator, database, notifier := newTestEvaluator(t, DefaultConfig())
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	seedRun(t, database, "run_failed", event.TypeRunFailed, event.StatusFailed, now)

	if err := evaluator.Evaluate(context.Background(), now); err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	alerts, err := database.ListAlerts(context.Background(), store.AlertFilter{State: "open"})
	if err != nil {
		t.Fatalf("list alerts: %v", err)
	}
	if len(alerts) != 1 || alerts[0].Type != TypeRunFailed {
		t.Fatalf("alerts = %+v", alerts)
	}
	if len(notifier.calls) != 1 {
		t.Fatalf("desktop notifications = %d", len(notifier.calls))
	}
}

func TestAlertCreationIsIdempotent(t *testing.T) {
	evaluator, database, notifier := newTestEvaluator(t, DefaultConfig())
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	seedRun(t, database, "run_failed", event.TypeRunFailed, event.StatusFailed, now)

	for round := 0; round < 3; round++ {
		if err := evaluator.Evaluate(context.Background(), now); err != nil {
			t.Fatalf("evaluate %d: %v", round, err)
		}
	}
	alerts, _ := database.ListAlerts(context.Background(), store.AlertFilter{})
	if len(alerts) != 1 {
		t.Fatalf("repeated evaluation created %d alerts", len(alerts))
	}
	if len(notifier.calls) != 1 {
		t.Fatalf("repeated evaluation sent %d desktop notifications", len(notifier.calls))
	}
}

func TestActiveRunAlertsAfterTheStallThreshold(t *testing.T) {
	evaluator, database, _ := newTestEvaluator(t, DefaultConfig())
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	seedRun(t, database, "run_active", event.TypeRunStarted, event.StatusStarted, now.Add(-20*time.Minute))

	if err := evaluator.Evaluate(context.Background(), now); err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	alerts, _ := database.ListAlerts(context.Background(), store.AlertFilter{State: "open"})
	if len(alerts) != 1 || alerts[0].Type != TypeRunStalled {
		t.Fatalf("a 20 minute old active run produced %+v", alerts)
	}
}

func TestFreshActiveRunDoesNotAlert(t *testing.T) {
	evaluator, database, _ := newTestEvaluator(t, DefaultConfig())
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	seedRun(t, database, "run_fresh", event.TypeRunStarted, event.StatusStarted, now.Add(-2*time.Minute))

	if err := evaluator.Evaluate(context.Background(), now); err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	alerts, _ := database.ListAlerts(context.Background(), store.AlertFilter{})
	if len(alerts) != 0 {
		t.Fatalf("a fresh active run produced %+v", alerts)
	}
}

func TestStallAlertResolvesWhenTheRunFinishes(t *testing.T) {
	evaluator, database, _ := newTestEvaluator(t, DefaultConfig())
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	seedRun(t, database, "run_recover", event.TypeRunStarted, event.StatusStarted, now.Add(-30*time.Minute))
	if err := evaluator.Evaluate(context.Background(), now); err != nil {
		t.Fatalf("first evaluate: %v", err)
	}
	seedRun(t, database, "run_recover", event.TypeRunCompleted, event.StatusCompleted, now)
	if err := evaluator.Evaluate(context.Background(), now); err != nil {
		t.Fatalf("second evaluate: %v", err)
	}
	open, _ := database.ListAlerts(context.Background(), store.AlertFilter{State: "open"})
	if len(open) != 0 {
		t.Fatalf("a completed run left %+v open", open)
	}
	resolved, _ := database.ListAlerts(context.Background(), store.AlertFilter{State: "resolved"})
	if len(resolved) != 1 {
		t.Fatalf("resolved alerts = %+v", resolved)
	}
}

func TestSourceAlertsAfterThreeMissedHeartbeats(t *testing.T) {
	config := DefaultConfig()
	config.Notifiable = false
	evaluator, database, _ := newTestEvaluator(t, config)
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	if err := database.RecordHeartbeat(context.Background(), "opencode", now.Add(-91*time.Second)); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}

	if err := evaluator.Evaluate(context.Background(), now); err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	alerts, _ := database.ListAlerts(context.Background(), store.AlertFilter{State: "open"})
	if len(alerts) != 1 || alerts[0].Type != TypeSourceOffline {
		t.Fatalf("alerts = %+v", alerts)
	}
}

func TestFreshHeartbeatDoesNotAlert(t *testing.T) {
	config := DefaultConfig()
	config.Notifiable = false
	evaluator, database, _ := newTestEvaluator(t, config)
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	if err := database.RecordHeartbeat(context.Background(), "opencode", now.Add(-10*time.Second)); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if err := evaluator.Evaluate(context.Background(), now); err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	alerts, _ := database.ListAlerts(context.Background(), store.AlertFilter{})
	if len(alerts) != 0 {
		t.Fatalf("a live source produced %+v", alerts)
	}
}

func TestSourceAlertResolvesWhenHeartbeatsResume(t *testing.T) {
	config := DefaultConfig()
	config.Notifiable = false
	evaluator, database, _ := newTestEvaluator(t, config)
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	_ = database.RecordHeartbeat(context.Background(), "opencode", now.Add(-5*time.Minute))
	if err := evaluator.Evaluate(context.Background(), now); err != nil {
		t.Fatalf("first evaluate: %v", err)
	}
	_ = database.RecordHeartbeat(context.Background(), "opencode", now)
	if err := evaluator.Evaluate(context.Background(), now); err != nil {
		t.Fatalf("second evaluate: %v", err)
	}
	open, _ := database.ListAlerts(context.Background(), store.AlertFilter{State: "open"})
	if len(open) != 0 {
		t.Fatalf("a recovered source left %+v open", open)
	}
}

func TestOffSourcesAreNotMonitored(t *testing.T) {
	config := DefaultConfig()
	config.Notifiable = false
	evaluator, database, _ := newTestEvaluator(t, config)
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	if err := database.SetSourceCaptureMode(context.Background(), "claude-code", event.CaptureOff); err != nil {
		t.Fatalf("capture mode: %v", err)
	}
	_ = database.RecordHeartbeat(context.Background(), "claude-code", now.Add(-time.Hour))
	if err := evaluator.Evaluate(context.Background(), now); err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	alerts, _ := database.ListAlerts(context.Background(), store.AlertFilter{})
	if len(alerts) != 0 {
		t.Fatalf("a source with capture off produced %+v", alerts)
	}
}

func TestAcknowledgedAlertIsNotNotifiedAgain(t *testing.T) {
	evaluator, database, notifier := newTestEvaluator(t, DefaultConfig())
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	seedRun(t, database, "run_ack", event.TypeRunFailed, event.StatusFailed, now)
	if err := evaluator.Evaluate(context.Background(), now); err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	alerts, _ := database.ListAlerts(context.Background(), store.AlertFilter{State: "open"})
	if _, err := database.AcknowledgeAlert(context.Background(), alerts[0].ID); err != nil {
		t.Fatalf("acknowledge: %v", err)
	}
	if err := evaluator.Evaluate(context.Background(), now); err != nil {
		t.Fatalf("second evaluate: %v", err)
	}
	if len(notifier.calls) != 1 {
		t.Fatalf("an acknowledged alert produced %d notifications", len(notifier.calls))
	}
}
