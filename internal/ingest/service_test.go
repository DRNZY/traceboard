package ingest

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"traceboard/internal/event"
	"traceboard/internal/redact"
	"traceboard/internal/store"
)

type recordingStore struct {
	events []event.Event
	result storeResult
	err    error
}

type storeResult struct {
	accepted    int
	duplicate   int
	quarantined int
}

func (recorder *recordingStore) InsertEvents(_ context.Context, events ...event.Event) (store.InsertResult, error) {
	recorder.events = append(recorder.events, events...)
	if recorder.err != nil {
		return store.InsertResult{}, recorder.err
	}
	return store.InsertResult{Accepted: recorder.result.accepted, Duplicate: recorder.result.duplicate, Quarantined: recorder.result.quarantined}, nil
}

func newService(t *testing.T, mode event.CaptureMode) (*Service, *recordingStore) {
	t.Helper()
	redactor, err := redact.New(redact.Options{})
	if err != nil {
		t.Fatalf("redactor: %v", err)
	}
	recorder := &recordingStore{}
	service := NewService(recorder, redactor, DefaultLimits())
	service.SetCaptureMode("opencode", mode)
	return service, recorder
}

func baseEvent() event.Event {
	return event.Event{
		SchemaVersion: 1,
		Source:        "opencode",
		SourceVersion: "1.0.0",
		RunID:         "run_1",
		OccurredAt:    time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC),
		Type:          event.TypeToolStarted,
		Status:        event.StatusStarted,
		Attributes:    map[string]any{},
	}
}

func TestIngestDerivesStableSourceEventIDs(t *testing.T) {
	service, recorder := newService(t, event.CaptureMetadata)
	input := baseEvent()
	input.Raw = map[string]any{"call": "abc"}

	first := service.Ingest(context.Background(), event.Batch{Events: []event.Event{input}})
	second := service.Ingest(context.Background(), event.Batch{Events: []event.Event{input}})
	if first.Dropped != 0 || second.Dropped != 0 {
		t.Fatalf("results = %+v / %+v", first, second)
	}
	if len(recorder.events) != 2 {
		t.Fatalf("recorded %d events", len(recorder.events))
	}
	if recorder.events[0].SourceEventID == "" || recorder.events[0].SourceEventID != recorder.events[1].SourceEventID {
		t.Fatalf("derived source event ids differ: %q vs %q", recorder.events[0].SourceEventID, recorder.events[1].SourceEventID)
	}
	if recorder.events[0].EventID == recorder.events[0].SourceEventID {
		t.Fatal("event_id and source_event_id must be distinct identifiers")
	}
}

func TestIngestDerivationChangesWithPayload(t *testing.T) {
	service, recorder := newService(t, event.CaptureMetadata)
	first := baseEvent()
	first.Raw = map[string]any{"call": "abc"}
	second := baseEvent()
	second.Raw = map[string]any{"call": "xyz"}
	service.Ingest(context.Background(), event.Batch{Events: []event.Event{first, second}})
	if recorder.events[0].SourceEventID == recorder.events[1].SourceEventID {
		t.Fatal("different payloads must derive different identifiers")
	}
}

func TestIngestKeepsSuppliedIdentifiers(t *testing.T) {
	service, recorder := newService(t, event.CaptureMetadata)
	input := baseEvent()
	input.EventID = "evt_fixed"
	input.SourceEventID = "src_fixed"
	service.Ingest(context.Background(), event.Batch{Events: []event.Event{input}})
	if recorder.events[0].EventID != "evt_fixed" || recorder.events[0].SourceEventID != "src_fixed" {
		t.Fatalf("identifiers = %+v", recorder.events[0])
	}
}

func TestIngestWithholdsContentInMetadataMode(t *testing.T) {
	service, recorder := newService(t, event.CaptureMetadata)
	input := baseEvent()
	input.Content = map[string]any{"text": "a private prompt"}

	service.Ingest(context.Background(), event.Batch{Events: []event.Event{input}})
	stored := recorder.events[0]
	if stored.Content != nil {
		t.Fatalf("metadata mode kept content: %+v", stored.Content)
	}
	if stored.Capture["content_withheld"] != true {
		t.Fatalf("capture did not record the withholding: %+v", stored.Capture)
	}
	fields, ok := stored.Capture["content_fields"].([]string)
	if !ok || len(fields) != 1 || fields[0] != "text" {
		t.Fatalf("content_fields = %+v", stored.Capture["content_fields"])
	}
}

func TestIngestKeepsContentInDetailedMode(t *testing.T) {
	service, recorder := newService(t, event.CaptureDetailed)
	input := baseEvent()
	input.Content = map[string]any{"text": "a captured prompt"}
	service.Ingest(context.Background(), event.Batch{Events: []event.Event{input}})
	if recorder.events[0].Content["text"] != "a captured prompt" {
		t.Fatalf("content = %+v", recorder.events[0].Content)
	}
}

func TestIngestDropsEverythingInOffMode(t *testing.T) {
	service, recorder := newService(t, event.CaptureOff)
	input := baseEvent()
	input.Content = map[string]any{"text": "never stored"}
	result := service.Ingest(context.Background(), event.Batch{Events: []event.Event{input}})
	if result.Dropped != 1 {
		t.Fatalf("result = %+v", result)
	}
	if len(recorder.events) != 0 {
		t.Fatalf("off mode still recorded %d events", len(recorder.events))
	}
}

func TestIngestRedactsBeforeStore(t *testing.T) {
	service, recorder := newService(t, event.CaptureDetailed)
	input := baseEvent()
	input.Content = map[string]any{"text": "Authorization: Bearer abcdefghijklmnopqrstuvwxyz"}
	input.Attributes = map[string]any{"nested": map[string]any{"key": "sk-abcdefghijklmnopqrstuvwx"}}
	input.Raw = map[string]any{"raw": "postgres://user:hunter2pass@db.internal:5432/app"}

	service.Ingest(context.Background(), event.Batch{Events: []event.Event{input}})
	encoded, err := json.Marshal(recorder.events[0])
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	text := string(encoded)
	for _, secret := range []string{"abcdefghijklmnopqrstuvwxyz", "sk-abcdefghijklmnopqrstuvwx", "hunter2pass"} {
		if strings.Contains(text, secret) {
			t.Fatalf("secret %q survived redaction: %s", secret, text)
		}
	}
	if !strings.Contains(text, "[REDACTED:") {
		t.Fatalf("no redaction marker was recorded: %s", text)
	}
}

func TestIngestWithholdsRawOutsideDetailedMode(t *testing.T) {
	for _, mode := range []event.CaptureMode{event.CaptureMetadata} {
		service, recorder := newService(t, mode)
		input := baseEvent()
		input.Raw = map[string]any{"anything": "value"}
		service.Ingest(context.Background(), event.Batch{Events: []event.Event{input}})
		if recorder.events[0].Raw != nil {
			t.Fatalf("%s mode kept a raw payload", mode)
		}
		if recorder.events[0].Capture["raw_withheld"] != true {
			t.Fatalf("%s mode did not record the withholding: %+v", mode, recorder.events[0].Capture)
		}
	}
}

func TestIngestTruncatesOversizedRawPayload(t *testing.T) {
	service, recorder := newService(t, event.CaptureDetailed)
	input := baseEvent()
	input.Raw = map[string]any{"blob": strings.Repeat("a", MaxRawPayloadBytes+1024)}
	service.Ingest(context.Background(), event.Batch{Events: []event.Event{input}})
	stored := recorder.events[0]
	if stored.Capture["raw_truncated"] != true {
		t.Fatalf("capture did not record truncation: %+v", stored.Capture)
	}
	encoded, _ := json.Marshal(stored.Raw)
	if len(encoded) > MaxRawPayloadBytes {
		t.Fatalf("raw payload of %d bytes exceeded the limit", len(encoded))
	}
}

func TestIngestEnforcesTheBatchCeiling(t *testing.T) {
	service, recorder := newService(t, event.CaptureMetadata)
	batch := make([]event.Event, 0, MaxEventsPerBatch+10)
	for index := 0; index < MaxEventsPerBatch+10; index++ {
		input := baseEvent()
		input.EventID = "evt_" + strings.Repeat("x", index%5) + time.Duration(index).String()
		input.SourceEventID = "src_" + time.Duration(index).String()
		batch = append(batch, input)
	}
	result := service.Ingest(context.Background(), event.Batch{Events: batch})
	if len(recorder.events) != MaxEventsPerBatch {
		t.Fatalf("recorded %d events, want the %d ceiling", len(recorder.events), MaxEventsPerBatch)
	}
	if result.Dropped != 10 {
		t.Fatalf("dropped = %d, want 10", result.Dropped)
	}
}

func TestIngestNormalizesUnknownTypesToExtensions(t *testing.T) {
	service, recorder := newService(t, event.CaptureMetadata)
	input := baseEvent()
	input.Type = "vendor.specific.event"
	input.Status = event.StatusFailed
	service.Ingest(context.Background(), event.Batch{Events: []event.Event{input}})
	stored := recorder.events[0]
	if stored.Type != event.TypeSourceExtension {
		t.Fatalf("type = %q, want a source extension", stored.Type)
	}
	if stored.Attributes["source_type"] != "vendor.specific.event" {
		t.Fatalf("source_type = %+v", stored.Attributes["source_type"])
	}
	if stored.Status == event.StatusFailed {
		t.Fatal("an unknown benign event must not be recorded as an error")
	}
}

func TestIngestKeepsErrorRecordedForSourceReportedErrors(t *testing.T) {
	service, recorder := newService(t, event.CaptureMetadata)
	input := baseEvent()
	input.Type = event.TypeErrorRecorded
	input.Status = event.StatusFailed
	service.Ingest(context.Background(), event.Batch{Events: []event.Event{input}})
	if recorder.events[0].Type != event.TypeErrorRecorded {
		t.Fatalf("type = %q", recorder.events[0].Type)
	}
	if recorder.events[0].Status != event.StatusFailed {
		t.Fatalf("status = %q", recorder.events[0].Status)
	}
}

func TestIngestForwardsUnsupportedSchemaVersionsForQuarantine(t *testing.T) {
	service, recorder := newService(t, event.CaptureMetadata)
	input := baseEvent()
	input.SchemaVersion = 99
	service.Ingest(context.Background(), event.Batch{Events: []event.Event{input}})
	if len(recorder.events) != 1 {
		t.Fatalf("an unsupported schema version must reach the store to be quarantined, got %d events", len(recorder.events))
	}
	if recorder.events[0].SchemaVersion != 99 {
		t.Fatalf("schema version = %d, want the original 99", recorder.events[0].SchemaVersion)
	}
}

func TestIngestOverwritesTheClaimedCaptureMode(t *testing.T) {
	service, recorder := newService(t, event.CaptureMetadata)
	input := baseEvent()
	input.Capture = map[string]any{"mode": "detailed"}
	input.Content = map[string]any{"text": "a prompt"}
	service.Ingest(context.Background(), event.Batch{Events: []event.Event{input}})
	if recorder.events[0].Capture["mode"] != string(event.CaptureMetadata) {
		t.Fatalf("capture mode = %+v, want the configured mode", recorder.events[0].Capture["mode"])
	}
	if recorder.events[0].Content != nil {
		t.Fatal("a source must not be able to upgrade its own capture mode")
	}
}

func TestIngestReportsCommittedRuns(t *testing.T) {
	service, _ := newService(t, event.CaptureMetadata)
	touched := map[string]int{}
	service.SetOnAccepted(func(runID string) { touched[runID]++ })
	service.Ingest(context.Background(), event.Batch{Events: []event.Event{baseEvent()}})
	if touched["run_1"] != 1 {
		t.Fatalf("onAccepted calls = %v", touched)
	}
}

func TestIngestReportsStoreFailureAsQuarantine(t *testing.T) {
	service, recorder := newService(t, event.CaptureMetadata)
	recorder.err = errStoreFailed
	result := service.Ingest(context.Background(), event.Batch{Events: []event.Event{baseEvent()}})
	if result.Quarantined != 1 || result.Accepted != 0 {
		t.Fatalf("result = %+v", result)
	}
}

var errStoreFailed = errStore("the store is unavailable")

type errStore string

func (e errStore) Error() string { return string(e) }
