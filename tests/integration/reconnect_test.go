//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"traceboard/internal/event"
	"traceboard/internal/ingest"
	"traceboard/internal/redact"
	"traceboard/internal/store"
)

// An adapter whose collector is unavailable must buffer locally, lose nothing,
// and replay in order without duplicating an event the collector already stored.
func TestSpoolDrainsAfterACollectorRestartWithoutDuplicates(t *testing.T) {
	instance := newHarness(t, "spool")
	redactor, err := redact.New(redact.Options{})
	if err != nil {
		t.Fatalf("redactor: %v", err)
	}
	spool, err := ingest.NewSpool(spoolDirectory(t), redactor, ingest.DefaultSpoolLimitBytes, ingest.DefaultSpoolAge)
	if err != nil {
		t.Fatalf("spool: %v", err)
	}

	started := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	batch := []event.Event{
		integrationEvent(1, "run_spooled", event.TypeRunStarted, event.StatusStarted, started),
		integrationEvent(2, "run_spooled", event.TypeToolFailed, event.StatusFailed, started.Add(time.Second)),
		integrationEvent(3, "run_spooled", event.TypeRunFailed, event.StatusFailed, started.Add(2*time.Second)),
	}
	encoded := make([]json.RawMessage, 0, len(batch))
	for _, item := range batch {
		raw, marshalErr := json.Marshal(item)
		if marshalErr != nil {
			t.Fatalf("marshal: %v", marshalErr)
		}
		encoded = append(encoded, raw)
	}
	if err := spool.Append("opencode", encoded...); err != nil {
		t.Fatalf("spool append: %v", err)
	}

	state, err := spool.State("opencode")
	if err != nil {
		t.Fatalf("spool state: %v", err)
	}
	if state.Events != 3 {
		t.Fatalf("spooled events = %d, want 3", state.Events)
	}

	// The collector restarts, drains the spool, and a second drain is a no-op.
	first := drainInto(t, spool, instance)
	if first.Drained != 3 {
		t.Fatalf("first drain = %+v", first)
	}
	second := drainInto(t, spool, instance)
	if second.Drained != 0 || second.Remaining != 0 {
		t.Fatalf("a second drain must be empty, got %+v", second)
	}

	cookie := instance.sessionCookie()
	status, body := instance.get(cookie, "/api/v1/runs/run_spooled/events")
	if status != http.StatusOK {
		t.Fatalf("events = %d", status)
	}
	var page store.EventPage
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(page.Events) != 3 {
		t.Fatalf("replayed events = %d, want 3", len(page.Events))
	}
	for index, want := range []string{event.TypeRunStarted, event.TypeToolFailed, event.TypeRunFailed} {
		if page.Events[index].Type != want {
			t.Fatalf("replay order at %d = %s, want %s", index, page.Events[index].Type, want)
		}
		if page.Events[index].Sequence != int64(index+1) {
			t.Fatalf("replay sequence at %d = %d", index, page.Events[index].Sequence)
		}
	}
}

// A client that reconnects learns which runs changed instead of refetching
// everything, and the changed list is bounded to what actually moved.
func TestChangedRunSnapshotServesAReconnectingClient(t *testing.T) {
	instance := newHarness(t, "changed")
	cookie := instance.sessionCookie()
	started := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)

	instance.ingest(
		integrationEvent(1, "run_old", event.TypeRunCompleted, event.StatusCompleted, started.Add(-72*time.Hour)),
		integrationEvent(1, "run_new", event.TypeRunCompleted, event.StatusCompleted, started),
	)

	status, body := instance.get(cookie, "/api/v1/runs/changed?since=2026-09-25T00:00:00Z")
	if status != http.StatusOK {
		t.Fatalf("changed runs = %d", status)
	}
	var page store.RunPage
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(page.Runs) != 1 || page.Runs[0].ID != "run_new" {
		t.Fatalf("changed runs = %+v", page.Runs)
	}
}

// An event range read reports exactly what is missing, so the dashboard refuses
// to claim a complete timeline.
func TestEventRangeReadReportsTheMissingTail(t *testing.T) {
	instance := newHarness(t, "range")
	cookie := instance.sessionCookie()
	started := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	for index := 1; index <= 5; index++ {
		instance.ingest(integrationEvent(index, "run_range", event.TypeToolStarted, event.StatusStarted,
			started.Add(time.Duration(index)*time.Second)))
	}

	status, body := instance.get(cookie, "/api/v1/runs/run_range/events?after=1&limit=2")
	if status != http.StatusOK {
		t.Fatalf("events = %d", status)
	}
	var page store.EventPage
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(page.Events) != 2 {
		t.Fatalf("page = %d events, want 2", len(page.Events))
	}
	if !page.HasMore {
		t.Fatal("a truncated page must report that more remain")
	}
	if page.NextSequence != 3 {
		t.Fatalf("resume cursor = %d, want 3", page.NextSequence)
	}
	if page.NextSequence >= page.RunSequence {
		t.Fatal("a truncated page must leave the cursor behind the run head")
	}
}

// Replaying a batch the store already holds changes nothing.
func TestDuplicateDeliveryAcrossARestartIsIdempotent(t *testing.T) {
	instance := newHarness(t, "idempotent")
	cookie := instance.sessionCookie()
	started := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	batch := []event.Event{
		integrationEvent(1, "run_dup", event.TypeRunStarted, event.StatusStarted, started),
		integrationEvent(2, "run_dup", event.TypeRunCompleted, event.StatusCompleted, started.Add(time.Second)),
	}

	first := instance.ingest(batch...)
	if first.Accepted != 2 {
		t.Fatalf("first ingest = %+v", first)
	}
	second := instance.ingest(batch...)
	if second.Accepted != 0 || second.Duplicate != 2 {
		t.Fatalf("replayed ingest = %+v", second)
	}

	status, body := instance.get(cookie, "/api/v1/runs/run_dup")
	if status != http.StatusOK {
		t.Fatalf("run detail = %d", status)
	}
	var detail struct {
		Run store.Run `json:"run"`
	}
	if err := json.Unmarshal(body, &detail); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if detail.Run.EventCount != 2 {
		t.Fatalf("event count = %d, want 2", detail.Run.EventCount)
	}
}

func drainInto(t *testing.T, spool *ingest.Spool, instance *harness) ingest.DrainResult {
	t.Helper()
	result, err := spool.Drain(context.Background(), func(payload json.RawMessage) error {
		var batch event.Batch
		if err := json.Unmarshal(payload, &batch); err != nil {
			return err
		}
		if len(batch.Events) == 0 {
			// A spooled entry may be a single envelope rather than a batch.
			var single event.Event
			if err := json.Unmarshal(payload, &single); err != nil {
				return err
			}
			batch.Events = []event.Event{single}
		}
		ingested := instance.ingest(batch.Events...)
		if ingested.Accepted == 0 && ingested.Quarantined > 0 {
			return errSpoolRejected
		}
		return nil
	})
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	return result
}

var errSpoolRejected = errors.New("spooled payload was rejected")

func spoolDirectory(t *testing.T) string {
	t.Helper()
	directory := filepath.Join(t.TempDir(), "spool")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("secure spool directory: %v", err)
	}
	return directory
}
