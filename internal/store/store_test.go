package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"traceboard/internal/event"
)

func TestInsertEventsIsIdempotentBySourceEvent(t *testing.T) {
	store := openMigratedStore(t)
	defer store.Close()

	input := storeTestEvent("evt_1", "source_1", "run_1", "run.started", event.StatusStarted, "2026-09-25T10:00:00Z")
	first, err := store.InsertEvents(context.Background(), input)
	if err != nil {
		t.Fatalf("insert first event: %v", err)
	}
	second, err := store.InsertEvents(context.Background(), input)
	if err != nil {
		t.Fatalf("insert duplicate event: %v", err)
	}
	if first != (InsertResult{Accepted: 1}) {
		t.Fatalf("first result = %+v, want one accepted", first)
	}
	if second != (InsertResult{Duplicate: 1}) {
		t.Fatalf("second result = %+v, want one duplicate", second)
	}

	var eventCount int
	var sequence int64
	if err := store.db.QueryRow("SELECT COUNT(*), MIN(sequence) FROM events").Scan(&eventCount, &sequence); err != nil {
		t.Fatalf("query event count: %v", err)
	}
	if eventCount != 1 || sequence != 1 {
		t.Fatalf("event count/sequence = %d/%d, want 1/1", eventCount, sequence)
	}
	var ftsCount int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM events_fts").Scan(&ftsCount); err != nil {
		t.Fatalf("query FTS count: %v", err)
	}
	if ftsCount != 1 {
		t.Fatalf("FTS count = %d, want 1", ftsCount)
	}
}

func TestInsertEventsEnforcesUniqueRunSequence(t *testing.T) {
	store := openMigratedStore(t)
	defer store.Close()

	first := storeTestEvent("evt_1", "source_1", "run_1", "run.started", event.StatusStarted, "2026-09-25T10:00:00Z")
	if _, err := store.InsertEvents(context.Background(), first); err != nil {
		t.Fatalf("insert first event: %v", err)
	}

	_, err := store.db.Exec(`
		INSERT INTO events (
			event_id, source_event_id, source, source_version, run_id, sequence,
			occurred_at, received_at, type, status, capture, attributes, search_text
		) VALUES (?, ?, ?, '', ?, 1, ?, ?, ?, ?, '{}', '{}', 'duplicate sequence')`,
		"evt_2", "source_2", first.Source, first.RunID, first.OccurredAt.UnixNano(), time.Now().UnixNano(), "tool.completed", event.StatusCompleted,
	)
	if err == nil || !strings.Contains(err.Error(), "UNIQUE constraint failed") {
		t.Fatalf("expected run sequence uniqueness error, got %v", err)
	}
}

func TestInsertEventsRollsBackEventStepSummaryAndFTS(t *testing.T) {
	store := openMigratedStore(t)
	defer store.Close()

	if _, err := store.db.Exec(`
		CREATE TRIGGER reject_interrupted_event
		BEFORE INSERT ON events
		WHEN new.source_event_id = 'source_reject'
		BEGIN
			SELECT RAISE(ABORT, 'forced transaction interruption');
		END`); err != nil {
		t.Fatalf("create rejection trigger: %v", err)
	}

	first := storeTestEvent("evt_1", "source_1", "run_atomic", "tool.started", event.StatusStarted, "2026-09-25T10:00:00Z")
	first.StepID = "step_1"
	second := storeTestEvent("evt_2", "source_reject", "run_atomic", "tool.completed", event.StatusCompleted, "2026-09-25T10:00:01Z")
	second.StepID = "step_1"

	if _, err := store.InsertEvents(context.Background(), first, second); err == nil {
		t.Fatal("expected interrupted transaction to fail")
	}

	for table, want := range map[string]int{
		"sources":    0,
		"runs":       0,
		"steps":      0,
		"events":     0,
		"events_fts": 0,
	} {
		var count int
		if err := store.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
			t.Fatalf("query %s count: %v", table, err)
		}
		if count != want {
			t.Fatalf("%s count = %d, want %d", table, count, want)
		}
	}
}

func TestInsertEventsUpdatesRunSummaryFromKnownEventTypes(t *testing.T) {
	store := openMigratedStore(t)
	defer store.Close()

	startedAt := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	endedAt := startedAt.Add(2 * time.Second)
	events := []event.Event{
		storeTestEvent("evt_1", "source_1", "run_1", "run.started", event.StatusStarted, startedAt.Format(time.RFC3339)),
		storeTestEvent("evt_2", "source_2", "run_1", "prompt.received", event.StatusCompleted, startedAt.Add(time.Second).Format(time.RFC3339)),
		storeTestEvent("evt_3", "source_3", "run_1", "run.completed", event.StatusCompleted, endedAt.Format(time.RFC3339)),
	}

	result, err := store.InsertEvents(context.Background(), events...)
	if err != nil {
		t.Fatalf("insert events: %v", err)
	}
	if result != (InsertResult{Accepted: 3}) {
		t.Fatalf("result = %+v, want three accepted", result)
	}

	run, err := store.GetRun(context.Background(), "run_1")
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if run.Status != event.StatusCompleted || run.EventCount != 3 || run.ErrorCount != 0 || run.NextSequence != 4 {
		t.Fatalf("run summary = %+v", run)
	}
	if run.StartedAt == nil || !run.StartedAt.Equal(startedAt) {
		t.Fatalf("started at = %v, want %v", run.StartedAt, startedAt)
	}
	if run.EndedAt == nil || !run.EndedAt.Equal(endedAt) {
		t.Fatalf("ended at = %v, want %v", run.EndedAt, endedAt)
	}
}

func TestInsertEventsQuarantinesInvalidEventWithRedactedReason(t *testing.T) {
	store := openMigratedStore(t)
	defer store.Close()

	const secret = "sk-abcdefghijklmnopqrst"
	input := storeTestEvent("evt_invalid", "source_invalid", "run_invalid", "custom.event", event.StatusStarted, "2026-09-25T10:00:00Z")
	input.Capture = map[string]any{"mode": secret}
	input.Raw = map[string]any{"credential": secret}

	result, err := store.InsertEvents(context.Background(), input)
	if err != nil {
		t.Fatalf("quarantine invalid event: %v", err)
	}
	if result != (InsertResult{Quarantined: 1}) {
		t.Fatalf("result = %+v, want one quarantined", result)
	}

	var payload string
	var reason string
	if err := store.db.QueryRow("SELECT payload, reason FROM quarantine").Scan(&payload, &reason); err != nil {
		t.Fatalf("query quarantine: %v", err)
	}
	if strings.Contains(payload, secret) || strings.Contains(reason, secret) {
		t.Fatalf("secret remained in quarantine payload/reason: %s / %s", payload, reason)
	}
	if !strings.Contains(reason, "[REDACTED:api_key]") {
		t.Fatalf("reason = %q, want redacted validation reason", reason)
	}
}

func TestInsertEventsDoesNotInferUnknownRunData(t *testing.T) {
	store := openMigratedStore(t)
	defer store.Close()

	input := storeTestEvent("evt_1", "source_1", "run_unknown", "custom.event", event.StatusUnknown, "2026-09-25T10:00:00Z")
	input.Attributes = map[string]any{"unrelated": "value"}
	if _, err := store.InsertEvents(context.Background(), input); err != nil {
		t.Fatalf("insert extension event: %v", err)
	}

	run, err := store.GetRun(context.Background(), "run_unknown")
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if run.Status != event.StatusUnknown || run.StartedAt != nil || run.EndedAt != nil || run.ProjectID != nil || run.Title != nil {
		t.Fatalf("unknown run data was inferred: %+v", run)
	}
}

func mustParseTimestamp(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return parsed
}

func storeTestEvent(eventID, sourceEventID, runID, eventType string, status event.Status, occurredAt string) event.Event {
	return event.Event{
		SchemaVersion: 1,
		EventID:       eventID,
		SourceEventID: sourceEventID,
		Source:        "opencode",
		SourceVersion: "1.0.0",
		RunID:         runID,
		OccurredAt:    mustParseTimestamp(occurredAt),
		Type:          eventType,
		Status:        status,
		Capture:       map[string]any{"mode": "metadata"},
		Attributes:    map[string]any{},
	}
}
